package ui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/network"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/storage"
	"github.com/nemo/Tsumugi/internal/telegram"
	"github.com/nemo/Tsumugi/internal/version"
)

type App struct {
	cfg              config.Config
	db               *storage.DB
	events           <-chan telegram.Event
	commands         chan<- telegram.Command
	app              *tview.Application
	root             *tview.Flex
	folders          *tview.List
	chats            *tview.List
	messages         *MessageViewport
	composer         *tview.InputField
	footer           *tview.TextView
	statusBar        *tview.Flex
	statusConn       *tview.TextView
	statusForeground *tview.TextView
	statusBackground *tview.TextView
	proxy            *tview.Modal
	pages            *tview.Pages
	theme            Theme
	settings         settings.Settings

	connectedAs         string
	connStatusMsg       i18n.Msg
	foregroundStatusMsg i18n.Msg
	foregroundStatusAt  time.Time
	backgroundState     *telegram.BackgroundState
	lastStatusMsg       i18n.Msg
	currentChat         string
	currentTitle        string
	currentFolder       int
	currentBroadcast    bool
	currentGroupRead    bool
	allChats            []telegram.Chat
	allFolders          []telegram.Folder
	replyTarget         *telegram.Message
	// chatsHighlightPeer tracks keyboard highlight in the chat list (not only Enter-selected chat).
	chatsHighlightPeer string
	// folderBrowseSnapID is the folder list item ID under the cursor before a folder list rebuild (setFolders).
	folderBrowseSnapID *int
	// chatsListRestoring is true while refreshChats is rebuilding items; SetChangedFunc must not clobber highlight.
	chatsListRestoring bool
	// Message actions modal (non-nil while detail/form overlay is open).
	msgActionDetail  *tview.TextView
	msgActionPreview *tview.TextView
	msgActionForm    *tview.Form
	msgActionSeq     int
	settingsOverlay  *settingsOverlay
	lastChatRefresh  time.Time
	gapFillQueued    map[string]struct{}
	control          chan<- ControlEvent
	onboardingActive bool
}

func New(cfg config.Config, db *storage.DB, events <-chan telegram.Event, commands chan<- telegram.Command, control chan<- ControlEvent) *App {
	theme := DefaultTheme()
	ApplyTheme(theme)

	tui := &App{
		cfg:      cfg,
		db:       db,
		events:   events,
		commands: commands,
		control:  control,
		settings: settings.Load(context.Background(), db),
		app:      tview.NewApplication(),
		folders: tview.NewList().
			ShowSecondaryText(false).
			SetSelectedBackgroundColor(tcell.ColorDarkSlateGray).
			SetSelectedTextColor(tcell.ColorWhite),
		chats: tview.NewList().
			ShowSecondaryText(true).
			SetSelectedBackgroundColor(tcell.ColorDarkSlateGray).
			SetSelectedTextColor(tcell.ColorWhite),
		messages:         NewMessageViewport(),
		composer:         tview.NewInputField().SetLabel("> "),
		footer:           tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		statusConn:       tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		statusForeground: tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		statusBackground: tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		theme:            theme,
	}
	tui.messages.SetInlineAnim(tui.settings.InlineAnim)
	tui.messages.SetLayoutMode(render.ParseLayoutMode(tui.settings.OutgoingLayout))
	tui.build()
	tui.applyMainLocale()
	return tui
}

func (a *App) Run(ctx context.Context) error {
	if a.cfg.NeedsOnboarding() {
		a.showOnboarding()
	}
	go a.consumeEvents(ctx)
	go func() {
		<-ctx.Done()
		a.app.Stop()
	}()
	go a.runInlineAnimTicker(ctx)
	go a.runMemoryDiagnostics(ctx)
	return a.app.Run()
}

func (a *App) runInlineAnimTicker(ctx context.Context) {
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if a.messages.LayoutRelayoutBusy() {
				continue
			}
			if a.messages.AdvanceGIF() {
				a.app.QueueUpdateDraw(func() {})
			}
		}
	}
}

func (a *App) build() {
	a.app.EnableMouse(true)
	a.folders.SetBorder(true)
	a.chats.SetBorder(true)
	a.messages.SetBorder(true)
	a.composer.SetBorder(true)
	a.statusConn.SetWrap(false)
	a.statusForeground.SetWrap(false)
	a.statusBackground.SetWrap(false)
	a.statusBar = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.statusConn, 0, 2, false).
		AddItem(a.statusForeground, 0, 4, false).
		AddItem(a.statusBackground, 0, 2, false)
	a.statusBar.SetBorder(true)

	a.folders.AddItem("All", "", 0, func() {
		a.currentFolder = 0
		a.refreshChats()
	})
	a.chats.AddItem("Tsumugi", i18n.T(i18n.KeyUIWaitingConnection), 0, func() {
		a.currentChat = "welcome"
		a.currentTitle = "Tsumugi"
		a.commands <- telegram.Command{Kind: telegram.CommandFocusChat, PeerKey: ""}
		a.applyMessagesPaneTitle()
		a.messages.SetText(i18n.T(i18n.KeyUIWelcomeConnect))
	})
	a.messages.SetText(i18n.T(i18n.KeyUIWelcomeHint))
	a.messages.SetActionFunc(a.showMessageActions)
	a.messages.SetOnReachOlder(a.onReachOlderMessages)
	a.footer.SetText(render.Footer(string(a.cfg.AuthMode), version.String(), a.cfg.Proxy))

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.messages, 0, 1, false).
		AddItem(a.composer, 3, 0, false).
		AddItem(a.statusBar, 1, 0, false).
		AddItem(a.footer, 1, 0, false)

	a.root = tview.NewFlex().
		AddItem(a.folders, 16, 0, false).
		AddItem(a.chats, 34, 0, true).
		AddItem(right, 0, 1, false)

	a.chats.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if a.chatsListRestoring {
			return
		}
		vis := a.visibleChatsForFolder()
		if index >= 0 && index < len(vis) {
			a.chatsHighlightPeer = vis[index].ID
		}
	})

	a.composer.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			text := strings.TrimSpace(a.composer.GetText())
			if text != "" {
				replyToID := 0
				if a.replyTarget != nil {
					replyToID, _ = strconv.Atoi(a.replyTarget.ID)
				}
				a.commands <- telegram.Command{Kind: telegram.CommandSendText, PeerKey: a.currentChat, Text: text, ReplyToID: replyToID}
				a.composer.SetText("")
				a.clearReplyTarget()
				a.setStatusMsg(i18n.KeyStatusSending)
			}
			a.updateFocusStyle()
		}
	})

	a.app.SetRoot(a.root, true)
	a.app.SetInputCapture(a.capture)
	a.updateFocusStyle()
	a.refreshStatusBar()
}

func (a *App) capture(event *tcell.EventKey) *tcell.EventKey {
	if a.settingsOverlay != nil {
		return a.captureSettings(event)
	}
	focus := a.app.GetFocus()
	if a.focusedOverlayPrimitiveOwnsNavigation(focus, event) {
		return event
	}
	switch event.Key() {
	case tcell.KeyCtrlC:
		a.app.Stop()
		return nil
	case tcell.KeyUp:
		if focus == a.messages {
			a.messages.SelectDelta(-1)
			a.onMessageSelectionChanged()
			return nil
		}
	case tcell.KeyDown:
		if focus == a.messages {
			a.messages.SelectDelta(1)
			a.onMessageSelectionChanged()
			return nil
		}
	case tcell.KeyEnter:
		if focus == a.messages {
			a.showMessageActions()
			return nil
		}
	case tcell.KeyTAB:
		if a.msgActionForm != nil {
			f := a.app.GetFocus()
			if f != a.msgActionDetail && f != a.msgActionPreview {
				return event
			}
			switch f {
			case a.msgActionDetail:
				if a.msgActionPreview != nil {
					a.app.SetFocus(a.msgActionPreview)
				} else {
					a.app.SetFocus(a.msgActionForm)
				}
			case a.msgActionPreview:
				a.app.SetFocus(a.msgActionForm)
			default:
				a.app.SetFocus(a.msgActionDetail)
			}
			return nil
		}
		a.switchFocus()
		return nil
	case tcell.KeyBacktab:
		if a.msgActionForm != nil {
			f := a.app.GetFocus()
			switch {
			case a.msgActionForm.HasFocus():
				if a.msgActionPreview != nil {
					a.app.SetFocus(a.msgActionPreview)
				} else {
					a.app.SetFocus(a.msgActionDetail)
				}
			case f == a.msgActionPreview:
				a.app.SetFocus(a.msgActionDetail)
			case f == a.msgActionDetail:
				a.app.SetFocus(a.msgActionForm)
			default:
				return event
			}
			return nil
		}
	case tcell.KeyEsc:
		if a.msgActionForm != nil {
			a.restoreMessageFocus()
			return nil
		}
		a.app.SetRoot(a.root, true)
		if a.currentChat != "" {
			a.app.SetFocus(a.messages)
		} else {
			a.app.SetFocus(a.chats)
		}
		a.updateFocusStyle()
		return nil
	}

	if _, typing := focus.(*tview.InputField); typing {
		return event
	}
	if focus == a.msgActionForm {
		return event
	}
	if _, form := focus.(*tview.Form); form {
		return event
	}
	if focus != a.folders && focus != a.chats && focus != a.messages && focus != a.composer {
		return event
	}

	switch event.Rune() {
	case 'j':
		if focus == a.messages {
			a.messages.SelectDelta(1)
			a.onMessageSelectionChanged()
			return nil
		}
	case 'k':
		if focus == a.messages {
			a.messages.SelectDelta(-1)
			a.onMessageSelectionChanged()
			return nil
		}
	case 'q':
		a.app.Stop()
		return nil
	case 'i':
		a.app.SetFocus(a.composer)
		a.updateFocusStyle()
		return nil
	case 'P', 'p':
		a.showProxySettings()
		return nil
	case '?', '？':
		a.showSettings()
		return nil
	case 'D':
		msg, ok := a.selectedMessageRow()
		if !ok || msg.Media.Kind == "" {
			a.setStatusMsg(i18n.KeyStatusSelectedNoMedia)
		} else if msg.Media.LocalPath == "" {
			a.commands <- telegram.Command{Kind: telegram.CommandDownloadMedia, PeerKey: a.currentChat, Media: msg.Media}
			a.setStatusMsg(i18n.KeyStatusDownloadingMedia)
		} else {
			a.setStatusMsg(i18n.KeyStatusMediaCachedAt, msg.Media.LocalPath)
		}
		return nil
	case 'O':
		msg, ok := a.selectedMessageRow()
		if !ok || msg.Media.LocalPath == "" {
			a.setStatusMsg(i18n.KeyStatusNoCachedPreview)
			return nil
		}
		if err := media.OpenPath(msg.Media.LocalPath); err != nil {
			a.setStatusError(err)
		} else {
			a.setStatusMsg(i18n.KeyStatusOpenedExternally)
		}
		return nil
	case 'L', 'l':
		if focus == a.messages {
			a.toggleOutgoingLayout()
			return nil
		}
	case 'R', 'r':
		if focus == a.messages {
			a.showReactionPanel()
			return nil
		}
	case '/':
		a.showSearch()
		return nil
	case 'n', 'N':
		a.setStatusMsg(i18n.KeyStatusSearchNavHint)
		return nil
	}
	return event
}

func (a *App) focusedOverlayPrimitiveOwnsNavigation(focus tview.Primitive, event *tcell.EventKey) bool {
	if !nativeNavigationKey(event) || a.isMainShellFocus(focus) {
		return false
	}
	if a.msgActionForm != nil {
		if focus == a.msgActionDetail || focus == a.msgActionPreview {
			return false
		}
		if a.msgActionForm.HasFocus() && event.Key() == tcell.KeyBacktab {
			return false
		}
	}
	switch focus.(type) {
	case *tview.Form, *tview.InputField, *tview.DropDown, *tview.Checkbox, *tview.Button, *tview.List, *tview.Modal, *tview.TextView:
		return true
	default:
		return false
	}
}

func nativeNavigationKey(event *tcell.EventKey) bool {
	switch event.Key() {
	case tcell.KeyTAB, tcell.KeyBacktab, tcell.KeyLeft, tcell.KeyRight, tcell.KeyEnter, tcell.KeyUp, tcell.KeyDown:
		return true
	default:
		return false
	}
}

func (a *App) isMainShellFocus(focus tview.Primitive) bool {
	return focus == a.folders || focus == a.chats || focus == a.messages || focus == a.composer
}

func (a *App) consumeEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-a.events:
			if !ok {
				return
			}
			a.app.QueueUpdateDraw(func() {
				a.applyEvent(event)
			})
		}
	}
}

func (a *App) applyEvent(event telegram.Event) {
	if event.Error != nil {
		a.setStatusError(event.Error)
	}
	if event.Background != nil {
		a.setBackgroundState(event.Background)
	}
	if !event.StatusMsg.IsZero() {
		a.setStatusFrom(event.StatusMsg)
		switch event.StatusMsg.Key {
		case i18n.KeyStatusGapFilled:
			a.pruneGapFillQueue()
			a.applyMessagesPaneTitle()
		case i18n.KeyStatusLoadingOlderMessages, i18n.KeyStatusFetchingOlderNetwork,
			i18n.KeyStatusOlderMessagesLoaded, i18n.KeyStatusNoOlderMessages,
			i18n.KeyStatusOlderCacheOnlyGapFilled,
			i18n.KeyStatusLoadingMediaPreviews, i18n.KeyStatusMediaPreviewsReady:
			a.applyMessagesPaneTitle()
		}
	}
	messageEventForCurrentChat := event.PeerKey == "" || event.PeerKey == a.currentChat
	if messageEventForCurrentChat {
		a.messages.RemoveIDs(event.RemoveMessageIDs)
	}
	switch event.Kind {
	case telegram.EventConnected:
		a.connectedAs = event.Self.Name
		if !event.StatusMsg.IsZero() {
			a.setStatusFrom(event.StatusMsg)
		}
		a.messages.SetText(a.connectedMessage())
		a.refreshStatusBar()
	case telegram.EventChats:
		if len(event.Folders) > 0 {
			a.setFolders(event.Folders)
		}
		if len(event.Chats) > 0 {
			a.setChats(event.Chats)
		}
	case telegram.EventMessages:
		if !messageEventForCurrentChat {
			return
		}
		if event.Append {
			for _, message := range event.Messages {
				a.appendMessage(message)
			}
		} else if event.Patch {
			a.patchMessages(event.Messages)
		} else if event.Merge {
			a.mergeMessages(event.Messages, event.PreserveViewport)
		} else {
			a.setMessages(event.Messages, event.PreserveViewport)
		}
	case telegram.EventReadOutbox:
		if event.PeerKey == a.currentChat && strings.HasPrefix(event.PeerKey, "user:") {
			a.messages.ApplyReadOutboxMaxID(event.ReadOutboxMaxID, true)
		}
	case telegram.EventAuthPrompt:
		if event.Auth != nil {
			a.showAuthPrompt(event.Auth)
		}
	}
}

func (a *App) setChats(chats []telegram.Chat) {
	if a.chats.GetItemCount() > 0 {
		idx := a.chats.GetCurrentItem()
		oldVisible := a.visibleChatsForFolder()
		vi := a.chatsVisibleIndexFromListIndex(idx)
		if vi >= 0 && vi < len(oldVisible) {
			a.chatsHighlightPeer = oldVisible[vi].ID
		}
	}
	a.allChats = append([]telegram.Chat(nil), chats...)
	if a.currentChat != "" {
		if idx := chatIndexByID(a.allChats, a.currentChat); idx >= 0 {
			if title := strings.TrimSpace(a.allChats[idx].Title); title != "" {
				a.currentTitle = title
			}
		}
	}
	if time.Since(a.lastChatRefresh) < 750*time.Millisecond {
		return
	}
	a.lastChatRefresh = time.Now()
	a.refreshFolders()
	a.refreshChats()
}

// chatsVisibleIndexFromListIndex maps a List index to the index in visibleChatsForFolder().
// The synthetic "Tsumugi / Waiting…" row is not part of allChats; return -1 when the highlight is on it.
func (a *App) chatsVisibleIndexFromListIndex(listIdx int) int {
	if listIdx < 0 {
		return -1
	}
	if a.chats.GetItemCount() == 0 {
		return -1
	}
	main0, sec0 := a.chats.GetItemText(0)
	if main0 == "Tsumugi" && (strings.Contains(sec0, "Waiting for Telegram") || sec0 == i18n.T(i18n.KeyUIWaitingConnection)) {
		if listIdx == 0 {
			return -1
		}
		return listIdx - 1
	}
	return listIdx
}

func (a *App) visibleChatsForFolder() []telegram.Chat {
	folder, _ := a.folderByID(a.currentFolder)
	var out []telegram.Chat
	for _, chat := range a.allChats {
		if a.chatInCurrentFolder(chat) {
			out = append(out, telegram.ChatForFolderDisplay(chat, folder))
		}
	}
	telegram.SortChatsForFolder(out, folder)
	return out
}

func chatIndexByID(chats []telegram.Chat, id string) int {
	for i, c := range chats {
		if c.ID == id {
			return i
		}
	}
	return -1
}

const (
	defaultChatListInnerWidth    = 32
	chatListRightEdgeSafetyCells = 1
	// New tview boxes start at 15x10 before layout; use the chat column fallback until Draw sets the real rect.
	tviewDefaultBoxWidth  = 15
	tviewDefaultBoxHeight = 10
)

func (a *App) chatListRowWidth() int {
	width := defaultChatListInnerWidth
	if a != nil && a.chats != nil {
		_, _, outerWidth, outerHeight := a.chats.GetRect()
		_, _, innerWidth, _ := a.chats.GetInnerRect()
		if innerWidth > 0 && (outerWidth != tviewDefaultBoxWidth || outerHeight != tviewDefaultBoxHeight) {
			width = innerWidth
		}
	}
	if width > chatListRightEdgeSafetyCells {
		width -= chatListRightEdgeSafetyCells
	}
	if width < 1 {
		return 1
	}
	return width
}

func (a *App) refreshChats() {
	visible := a.visibleChatsForFolder()
	prevVisibleIdx := -1
	if a.chats.GetItemCount() > 0 {
		prevVisibleIdx = a.chatsVisibleIndexFromListIndex(a.chats.GetCurrentItem())
	}
	a.chatsListRestoring = true
	defer func() { a.chatsListRestoring = false }()

	a.chats.Clear()
	rowWidth := a.chatListRowWidth()
	for _, chat := range visible {
		display := localizedChatDisplay(chat)
		peerID := chat.ID
		title := chat.Title
		a.chats.AddItem(render.ChatRow(display, rowWidth), render.Truncate(display.Subtitle, rowWidth), 0, func() {
			a.currentChat = peerID
			a.resetGapFillQueue()
			a.currentTitle = title
			a.currentBroadcast = telegram.ChatIsBroadcast(chat)
			a.currentGroupRead = telegram.ChatSupportsGroupReadMarks(chat)
			a.messages.SetBroadcastChannel(a.currentBroadcast)
			a.messages.SetGroupReadMarks(a.currentGroupRead)
			a.chatsHighlightPeer = peerID
			a.clearReplyTarget()
			a.setMessages(nil, false)
			a.commands <- telegram.Command{Kind: telegram.CommandFocusChat, PeerKey: peerID}
			a.setStatusMsg(i18n.KeyStatusLoadingHistory)
			a.applyMessagesPaneTitle()
			a.refreshStatusBar()
			a.app.SetFocus(a.messages)
			a.updateFocusStyle()
			a.commands <- telegram.Command{Kind: telegram.CommandOpenChat, PeerKey: peerID}
		})
	}
	if len(visible) == 0 {
		return
	}
	if a.chatsHighlightPeer != "" {
		if idx := chatIndexByID(visible, a.chatsHighlightPeer); idx >= 0 {
			a.chats.SetCurrentItem(idx)
			return
		}
	}
	if a.currentChat != "" && a.currentChat != "welcome" {
		if idx := chatIndexByID(visible, a.currentChat); idx >= 0 {
			a.chats.SetCurrentItem(idx)
			return
		}
	}
	if prevVisibleIdx >= 0 && prevVisibleIdx < len(visible) {
		a.chats.SetCurrentItem(prevVisibleIdx)
		return
	}
	a.chats.SetCurrentItem(0)
}

func (a *App) setFolders(folders []telegram.Folder) {
	a.folderBrowseSnapID = nil
	if a.folders.GetItemCount() > 0 && len(a.allFolders) > 0 {
		idx := a.folders.GetCurrentItem()
		if idx >= 0 && idx < len(a.allFolders) {
			id := a.allFolders[idx].ID
			a.folderBrowseSnapID = &id
		}
	}
	a.allFolders = append([]telegram.Folder(nil), folders...)
	a.refreshFolders()
}

func (a *App) refreshFolders() {
	prevIdx := -1
	if a.folders.GetItemCount() > 0 {
		prevIdx = a.folders.GetCurrentItem()
	}
	a.folders.Clear()
	if len(a.allFolders) == 0 {
		a.allFolders = []telegram.Folder{{ID: 0, Title: "All", Kind: "all"}}
	}
	for _, folder := range a.allFolders {
		f := folder
		a.folders.AddItem(a.folderTitle(f), "", 0, func() {
			a.currentFolder = f.ID
			a.refreshChats()
			a.setStatusMsg(i18n.KeyStatusFolder, f.Title)
		})
	}
	n := len(a.allFolders)
	if a.folderBrowseSnapID != nil {
		if i := folderIndexByID(a.allFolders, *a.folderBrowseSnapID); i >= 0 {
			a.folders.SetCurrentItem(i)
			a.folderBrowseSnapID = nil
			return
		}
		a.folderBrowseSnapID = nil
	}
	if prevIdx >= 0 && prevIdx < n {
		a.folders.SetCurrentItem(prevIdx)
		return
	}
	idx := folderIndexByID(a.allFolders, a.currentFolder)
	if idx < 0 {
		a.currentFolder = 0
		idx = folderIndexByID(a.allFolders, 0)
		if idx < 0 {
			idx = 0
		}
	}
	a.folders.SetCurrentItem(idx)
}

func folderIndexByID(folders []telegram.Folder, id int) int {
	for i, f := range folders {
		if f.ID == id {
			return i
		}
	}
	return -1
}

func (a *App) folderTitle(folder telegram.Folder) string {
	title := i18n.FolderTitle(folder.Title)
	if folder.ID == 0 && (folder.Kind == "all" || folder.Kind == "telegram") {
		return title
	}
	unread := 0
	for _, chat := range a.allChats {
		if a.chatInFolder(chat, folder) {
			unread += chat.Unread
		}
	}
	if unread > 0 {
		return fmt.Sprintf("%s (%d)", title, unread)
	}
	return title
}

func (a *App) chatInCurrentFolder(chat telegram.Chat) bool {
	folder, ok := a.folderByID(a.currentFolder)
	if !ok {
		return a.currentFolder == 0
	}
	return a.chatInFolder(chat, folder)
}

func (a *App) folderByID(id int) (telegram.Folder, bool) {
	if id == 0 {
		return telegram.Folder{ID: 0, Title: "All", Kind: "all"}, true
	}
	for _, folder := range a.allFolders {
		if folder.ID == id {
			return folder, true
		}
	}
	return telegram.Folder{}, false
}

func (a *App) chatInFolder(chat telegram.Chat, folder telegram.Folder) bool {
	folderID := folder.ID
	switch {
	case folderID == 0:
		return chat.FolderID != 1
	case folder.Archive:
		return chat.FolderID == 1
	case folder.Kind == "telegram":
		return folderRulesMatch(folder.Rules, chat)
	case folderID > 0:
		return chat.FolderID == folderID
	case folderID == -1:
		return chat.Kind == "bot"
	case folderID == -2:
		return chat.Kind == "group" || chat.Kind == "chat" || chat.Subtitle == "group"
	case folderID == -3:
		return chat.Kind == "channel"
	default:
		return true
	}
}

func folderRulesMatch(rules telegram.FolderRules, chat telegram.Chat) bool {
	if containsString(rules.ExcludePeers, chat.ID) {
		return false
	}
	if rules.ExcludeArchived && chat.FolderID == 1 {
		return false
	}
	if rules.ExcludeRead && chat.Unread == 0 {
		return false
	}
	if containsString(rules.IncludePeers, chat.ID) || containsString(rules.PinnedPeers, chat.ID) {
		return true
	}
	if rules.Bots && chat.Subtitle == "bot" {
		return true
	}
	if rules.Groups && (chat.Kind == "chat" || chat.Subtitle == "group") {
		return true
	}
	if rules.Broadcasts && chat.Kind == "channel" && chat.Subtitle != "group" {
		return true
	}
	if rules.Contacts && chat.Kind == "user" && chat.Contact {
		return true
	}
	if rules.NonContacts && chat.Kind == "user" && !chat.Contact && chat.Subtitle != "bot" {
		return true
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (a *App) onReachOlderMessages() {
	switch a.currentChat {
	case "", "welcome", "empty":
		return
	}
	beforeID, _ := strconv.Atoi(a.messages.OldestMessageID())
	a.commands <- telegram.Command{Kind: telegram.CommandLoadOlder, PeerKey: a.currentChat, MessageID: beforeID}
	a.setStatusMsg(i18n.KeyStatusLoadingOlderMessages)
	a.applyMessagesPaneTitle()
}

func (a *App) setMessages(messages []telegram.Message, preserveViewport bool) {
	if messages == nil {
		messages = []telegram.Message{}
	}
	a.messages.SetMessagesReplace(messages, preserveViewport)
	if !preserveViewport {
		a.messages.ScrollToEnd()
	}
	a.applyMessagesPaneTitle()
	a.refreshStatusBar()
	a.scheduleGapFillsIfNeeded()
}

func (a *App) patchMessages(updates []telegram.Message) {
	if len(updates) == 0 {
		return
	}
	if a.messages.PatchMessages(updates) {
		a.applyMessagesPaneTitle()
	}
}

func (a *App) mergeMessages(messages []telegram.Message, preserveViewport bool) {
	byID := make(map[string]telegram.Message, len(a.messages.Messages())+len(messages))
	for _, msg := range a.messages.Messages() {
		byID[msg.ID] = msg
	}
	for _, msg := range messages {
		byID[msg.ID] = msg
	}
	merged := make([]telegram.Message, 0, len(byID))
	for _, msg := range byID {
		merged = append(merged, msg)
	}
	a.messages.SetMessagesReplace(merged, preserveViewport)
	if !preserveViewport {
		a.messages.ScrollToEnd()
	}
	a.applyMessagesPaneTitle()
	a.refreshStatusBar()
	a.scheduleGapFillsIfNeeded()
}

func (a *App) applyMessagesPaneTitle() {
	a.messages.SetTitle(" " + a.messagesPaneTitleText() + " ")
}

func (a *App) appendMessage(message telegram.Message) {
	if message.ChatID != a.currentChat {
		return
	}
	a.messages.AppendMessage(message)
	a.applyMessagesPaneTitle()
	if n := a.messages.PendingBelow(); n > 0 {
		a.setStatusMsg(i18n.KeyUINewBelow, n)
	}
}

func (a *App) selectedMessageRow() (telegram.Message, bool) {
	return a.messages.SelectedMessage()
}

func (a *App) selectMessageByID(id string) bool {
	return a.messages.SelectByID(id)
}

func (a *App) setStatus(text string) {
	a.setStatusText(text)
}

func (a *App) switchFocus() {
	switch a.app.GetFocus() {
	case a.folders:
		a.app.SetFocus(a.chats)
	case a.chats:
		a.app.SetFocus(a.messages)
	case a.messages:
		a.app.SetFocus(a.composer)
	default:
		a.app.SetFocus(a.folders)
	}
	a.updateFocusStyle()
}

func (a *App) updateFocusStyle() {
	focus := a.app.GetFocus()
	if focus == a.folders {
		a.folders.SetSelectedBackgroundColor(tcell.ColorDarkSlateGray).SetSelectedTextColor(tcell.ColorWhite)
		a.folders.SetBorderColor(a.theme.Accent).SetTitleColor(a.theme.Accent)
	} else {
		a.folders.SetSelectedBackgroundColor(tcell.ColorDefault).SetSelectedTextColor(a.theme.Dim)
		a.folders.SetBorderColor(a.theme.Border).SetTitleColor(a.theme.Dim)
	}
	if focus == a.chats {
		a.chats.SetSelectedBackgroundColor(tcell.ColorDarkSlateGray).SetSelectedTextColor(tcell.ColorWhite)
		a.chats.SetBorderColor(a.theme.Accent).SetTitleColor(a.theme.Accent)
	} else {
		a.chats.SetSelectedBackgroundColor(tcell.ColorDefault).SetSelectedTextColor(a.theme.Dim)
		a.chats.SetBorderColor(a.theme.Border).SetTitleColor(a.theme.Dim)
	}
	if focus == a.messages {
		a.messages.SetBorderColor(a.theme.Accent).SetTitleColor(a.theme.Accent)
	} else {
		a.messages.SetBorderColor(a.theme.Border).SetTitleColor(a.theme.Dim)
	}
	if focus == a.composer {
		a.composer.SetBorderColor(a.theme.Accent).SetTitleColor(a.theme.Accent)
	} else {
		a.composer.SetBorderColor(a.theme.Border).SetTitleColor(a.theme.Dim)
	}
}

func isStillImagePreviewPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	default:
		return false
	}
}

type messageAction struct {
	ID       string
	LabelKey string
}

func (a *App) showMessageActions() {
	msg, ok := a.selectedMessageRow()
	if !ok {
		a.setStatusMsg(i18n.KeyStatusNoMessageSelected)
		return
	}
	actions := []messageAction{
		{ID: "reply", LabelKey: i18n.KeyActionReply},
		{ID: "delete", LabelKey: i18n.KeyActionDelete},
		{ID: "copy", LabelKey: i18n.KeyActionCopy},
	}
	if msg.State == "failed" && msg.Outgoing && strings.TrimSpace(msg.Text) != "" {
		actions = append([]messageAction{{ID: "retry", LabelKey: i18n.KeyActionRetry}}, actions...)
	}
	if msg.Media.Kind != "" {
		actions = append(actions,
			messageAction{ID: "open_media", LabelKey: i18n.KeyActionOpenMedia},
			messageAction{ID: "download_media", LabelKey: i18n.KeyActionDownloadMedia},
		)
	}
	if msg.ReplyToID != "" {
		actions = append(actions, messageAction{ID: "jump_reply", LabelKey: i18n.KeyActionJumpReply})
	}
	actions = append(actions, messageAction{ID: "react", LabelKey: i18n.KeyActionReact})
	actions = append(actions, messageAction{ID: "cancel", LabelKey: i18n.KeyActionCancel})

	formH := len(actions)*2 + 4

	bodyText := tview.TranslateANSI(render.MessageDetailWithoutPreview(msg))
	detail := tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true).
		SetText(bodyText)
	detail.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyUIMessage) + " ")

	localPath := strings.TrimSpace(msg.Media.LocalPath)
	deferRaster := localPath != "" && isStillImagePreviewPath(localPath)

	var top *tview.Flex
	var imgView *tview.TextView
	if deferRaster {
		v := tview.NewTextView().
			SetDynamicColors(true).
			SetWordWrap(false).
			SetText(i18n.T(i18n.KeyUILoadingPreview))
		v.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyUIPreview) + " ")
		imgView = v
		top = tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(detail, 0, 1, true).
			AddItem(imgView, 0, 2, true)
	} else if localPath != "" {
		previewStr := a.messageActionMediaPreview(msg, 80, 20)
		if previewStr != "" {
			v := tview.NewTextView().
				SetDynamicColors(true).
				SetWordWrap(false).
				SetText(previewStr)
			v.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyUIPreview) + " ")
			imgView = v
			top = tview.NewFlex().SetDirection(tview.FlexColumn).
				AddItem(detail, 0, 1, true).
				AddItem(imgView, 0, 2, true)
		} else {
			top = tview.NewFlex().SetDirection(tview.FlexColumn).
				AddItem(detail, 0, 1, true)
		}
	} else {
		top = tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(detail, 0, 1, true)
	}

	form := tview.NewForm()
	for _, item := range actions {
		action := item
		form.AddButton(i18n.T(action.LabelKey), func() {
			a.restoreMessageFocus()
			if action.ID == "cancel" {
				return
			}
			a.runMessageAction(action.ID, msg)
		})
	}

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(top, 0, 1, true).
		AddItem(form, formH, 0, false)

	a.msgActionDetail = detail
	a.msgActionForm = form
	a.msgActionPreview = imgView
	a.msgActionSeq++
	seq := a.msgActionSeq

	layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			a.restoreMessageFocus()
			return nil
		}
		return event
	})
	a.app.SetRoot(layout, true)
	a.app.SetFocus(detail)

	if deferRaster && imgView != nil {
		pm := msg
		iv := imgView
		go func() {
			a.app.QueueUpdateDraw(func() {})
			var iw, ih int
			a.app.QueueUpdate(func() {
				_, _, iw, ih = iv.GetInnerRect()
			})
			if iw < 8 || ih < 4 {
				return
			}
			s := a.messageActionMediaPreview(pm, iw, ih)
			a.app.QueueUpdateDraw(func() {
				if a.msgActionSeq != seq || a.msgActionPreview != iv {
					return
				}
				if s != "" {
					iv.SetText(s)
				} else {
					iv.SetText(i18n.T(i18n.KeyUINoPreview))
				}
			})
		}()
	}
}

func (a *App) messageActionMediaPreview(msg telegram.Message, maxCols, maxRows int) string {
	if msg.Media.LocalPath == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(msg.Media.LocalPath))
	switch ext {
	case ".webm", ".mp4", ".mkv", ".mov", ".avi", ".m4v":
		return "[gray]此終端無法內嵌預覽影片。請用「Open media」或「Download media」。[-]"
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		// Use TextView inner dimensions as MaxCols×MaxRows (see showMessageActions deferred layout).
		opts := media.RasterPreviewOptions{MaxCols: maxCols, MaxRows: maxRows}
		return media.RasterPreviewANSIToTview(msg.Media.LocalPath, opts)
	default:
		return ""
	}
}

func (a *App) restoreMessageFocus() {
	a.msgActionSeq++
	a.msgActionDetail = nil
	a.msgActionPreview = nil
	a.msgActionForm = nil
	a.app.SetRoot(a.root, true)
	a.app.SetFocus(a.messages)
	a.updateFocusStyle()
}

func (a *App) runMessageAction(action string, msg telegram.Message) {
	switch action {
	case "retry":
		id, err := strconv.Atoi(msg.ID)
		if err != nil || id >= 0 {
			a.setStatusMsg(i18n.KeyStatusOnlyFailedOutgoingRetry)
			return
		}
		if strings.TrimSpace(msg.Text) == "" {
			a.setStatusMsg(i18n.KeyStatusNothingToSend)
			return
		}
		a.commands <- telegram.Command{Kind: telegram.CommandRetrySend, PeerKey: a.currentChat, MessageID: id}
		a.setStatusMsg(i18n.KeyStatusRetryingSend)
	case "reply":
		a.setReplyTarget(msg)
	case "delete":
		id, err := strconv.Atoi(msg.ID)
		if err != nil || id <= 0 {
			a.setStatusMsg(i18n.KeyStatusCannotDeleteLocal)
			return
		}
		a.commands <- telegram.Command{Kind: telegram.CommandDeleteMessage, PeerKey: a.currentChat, MessageID: id}
		a.setStatusMsg(i18n.KeyStatusDeletingMessage)
	case "copy":
		if strings.TrimSpace(msg.Text) == "" {
			a.setStatusMsg(i18n.KeyStatusNoTextCopy)
			return
		}
		if err := copyText(msg.Text); err != nil {
			a.setStatusMsg(i18n.KeyStatusCopyFailed, err.Error())
		} else {
			a.setStatusMsg(i18n.KeyStatusCopiedText)
		}
	case "open_media":
		if msg.Media.LocalPath == "" {
			a.setStatusMsg(i18n.KeyStatusNoCachedPreview)
			return
		}
		if err := media.OpenPath(msg.Media.LocalPath); err != nil {
			a.setStatusError(err)
		} else {
			a.setStatusMsg(i18n.KeyStatusOpenedExternally)
		}
	case "download_media":
		if msg.Media.Kind == "" {
			a.setStatusMsg(i18n.KeyStatusSelectedNoMedia)
			return
		}
		a.commands <- telegram.Command{Kind: telegram.CommandDownloadMedia, PeerKey: a.currentChat, Media: msg.Media}
		a.setStatusMsg(i18n.KeyStatusDownloadingMedia)
	case "jump_reply":
		if msg.ReplyToID == "" {
			a.setStatusMsg(i18n.KeyStatusNotAReply)
			return
		}
		if a.selectMessageByID(msg.ReplyToID) {
			a.setStatusMsg(i18n.KeyStatusJumpedToReply)
			return
		}
		beforeID, _ := strconv.Atoi(a.messages.OldestMessageID())
		a.commands <- telegram.Command{Kind: telegram.CommandLoadOlder, PeerKey: a.currentChat, MessageID: beforeID}
		a.setStatusMsg(i18n.KeyStatusReplyLoadingOlder)
	case "react":
		a.showReactionPanelFor(msg)
	case "cancel":
		return
	}
}

func (a *App) setReplyTarget(msg telegram.Message) {
	msg.Media.PreviewText = ""
	a.replyTarget = &msg
	label := strings.TrimSpace(msg.Text)
	if label == "" {
		label = msg.Media.Label
	}
	if label == "" {
		label = "message " + msg.ID
	}
	if len(label) > 40 {
		label = label[:40] + "..."
	}
	a.composer.SetTitle(" " + i18n.Tf(i18n.KeyUIReplyTo, label) + " ")
	a.app.SetFocus(a.composer)
	a.updateFocusStyle()
	a.setStatusMsg(i18n.KeyStatusReplyTargetSet)
}

func (a *App) clearReplyTarget() {
	a.replyTarget = nil
	if a.composer != nil {
		a.composer.SetTitle(" " + i18n.T(i18n.KeyUICompose) + " ")
	}
}

func copyText(text string) error {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("powershell", "-NoProfile", "-Command", "Set-Clipboard")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	default:
		if path, err := exec.LookPath("wl-copy"); err == nil {
			cmd := exec.Command(path)
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run()
		}
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
}

func (a *App) showSearch() {
	focus := a.app.GetFocus()
	if focus == a.composer {
		a.composer.SetText(a.composer.GetText() + "/")
		return
	}
	scope := "messages"
	label := i18n.T(i18n.KeySearchLabelMessages)
	if focus == a.chats || focus == a.folders {
		scope = "chats"
		label = i18n.T(i18n.KeySearchLabelChats)
	}
	input := tview.NewInputField().SetLabel(label).SetFieldWidth(40)
	form := tview.NewForm().
		AddFormItem(input).
		AddButton(i18n.T(i18n.KeySearchButton), func() {
			a.applySearch(scope, input.GetText())
			a.app.SetRoot(a.root, true)
			a.app.SetFocus(focus)
			a.updateFocusStyle()
		}).
		AddButton(i18n.T(i18n.KeySearchCancel), func() {
			a.app.SetRoot(a.root, true)
			a.app.SetFocus(focus)
			a.updateFocusStyle()
		})
	form.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyUISearch) + " ")
	panel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(form, 7, 0, true).
		AddItem(tview.NewBox(), 0, 1, false)
	centered := tview.NewFlex().
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(panel, 58, 0, true).
		AddItem(tview.NewBox(), 0, 1, false)
	a.app.SetRoot(centered, true)
	a.app.SetFocus(form)
}

func (a *App) applySearch(scope, query string) {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		a.setStatusMsg(i18n.KeyStatusSearchEmpty)
		return
	}
	switch scope {
	case "chats":
		visible := a.visibleChatsForFolder()
		for _, chat := range a.allChats {
			if strings.Contains(strings.ToLower(chat.Title+" "+chat.Subtitle+" "+chat.LastPreview), query) {
				idx := chatIndexByID(visible, chat.ID)
				if idx < 0 {
					a.setStatusMsg(i18n.KeyStatusChatOutsideFolder)
					return
				}
				a.chats.SetCurrentItem(idx)
				a.chatsHighlightPeer = chat.ID
				a.setStatusMsg(i18n.KeyStatusFoundChat, chat.Title)
				return
			}
		}
		a.setStatusMsg(i18n.KeyStatusNoMatchingChat)
	default:
		for _, msg := range a.messages.Messages() {
			if strings.Contains(strings.ToLower(msg.Author+" "+msg.Text+" "+msg.Media.Label), query) {
				a.selectMessageByID(msg.ID)
				a.setStatusMsg(i18n.KeyStatusFoundMessage, msg.CreatedAt.Local().Format("15:04"))
				return
			}
		}
		a.setStatusMsg(i18n.KeyStatusNoMatchingMessage)
	}
}

func (a *App) showProxySettings() {
	profiles, err := a.db.ListProxyProfiles(context.Background())
	if err != nil {
		a.setStatusError(err)
		return
	}

	list := tview.NewList().ShowSecondaryText(true)
	form := tview.NewForm()
	name := tview.NewInputField().SetLabel(i18n.T(i18n.KeyProxyNameLabel)).SetFieldWidth(36)
	rawURL := tview.NewInputField().SetLabel(i18n.T(i18n.KeyProxyURLLabel)).SetFieldWidth(56)
	status := tview.NewTextView().SetDynamicColors(true)
	var selected storage.ProxyProfile

	refreshForm := func(p storage.ProxyProfile) {
		selected = p
		name.SetText(p.Name)
		rawURL.SetText(proxyURL(p.Config))
		if p.ReadOnly {
			status.SetText(i18n.T(i18n.KeyProxyReadonlyEnv))
		} else {
			status.SetText(i18n.T(i18n.KeyProxyEditHint))
		}
	}

	list.AddItem(i18n.T(i18n.KeyProxyNoProxy), i18n.T(i18n.KeyProxyNoProxyDesc), 0, func() {
		refreshForm(storage.ProxyProfile{Name: i18n.T(i18n.KeyProxyNoProxy), Config: network.ProxyConfig{Kind: network.ProxyNone}, Active: !a.cfg.Proxy.Active(), ReadOnly: true})
	})
	for _, profile := range profiles {
		p := profile
		title := p.Name
		if p.Active {
			title = "* " + title
		}
		if p.ReadOnly {
			title = i18n.Tf(i18n.KeyProxyEnvironment, p.Config.EnvVar)
		}
		list.AddItem(title, string(p.Config.Kind)+" "+p.Config.MaskedAddress(), 0, func() {
			refreshForm(p)
		})
	}

	form.AddFormItem(name).
		AddFormItem(rawURL).
		AddButton(i18n.T(i18n.KeyProxySave), func() {
			if selected.ReadOnly {
				status.SetText(i18n.T(i18n.KeyProxyReadonlyEdit))
				return
			}
			cfg, err := network.ParseProxyURL(rawURL.GetText())
			if err != nil {
				status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
				return
			}
			cfg.Source = network.SourceManual
			id, err := a.db.SaveProxyProfile(context.Background(), storage.ProxyProfile{ID: selected.ID, Name: name.GetText(), Config: cfg, Active: selected.Active})
			if err != nil {
				status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
				return
			}
			status.SetText(i18n.Tf(i18n.KeyProxySaved, id))
		}).
		AddButton(i18n.T(i18n.KeyProxyNew), func() {
			refreshForm(storage.ProxyProfile{Name: i18n.T(i18n.KeyProxyNewName), Config: network.ProxyConfig{Kind: network.ProxySOCKS5, Source: network.SourceManual, Address: "127.0.0.1:1080"}})
		}).
		AddButton(i18n.T(i18n.KeyProxyEnable), func() {
			if selected.ID == 0 || selected.ReadOnly {
				status.SetText(i18n.T(i18n.KeyProxySelectFirst))
				return
			}
			if err := a.db.ActivateProxyProfile(context.Background(), selected.ID); err != nil {
				status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
				return
			}
			status.SetText(i18n.T(i18n.KeyProxyEnabled))
		}).
		AddButton(i18n.T(i18n.KeyProxyDelete), func() {
			if selected.ID == 0 || selected.ReadOnly {
				status.SetText(i18n.T(i18n.KeyProxySelectFirst))
				return
			}
			if err := a.db.DeleteProxyProfile(context.Background(), selected.ID); err != nil {
				status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
				return
			}
			status.SetText(i18n.T(i18n.KeyProxyDeleted))
		}).
		AddButton(i18n.T(i18n.KeyProxyTest), func() {
			cfg, err := network.ParseProxyURL(rawURL.GetText())
			if err != nil {
				status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
				return
			}
			if _, err := cfg.Resolver(); err != nil {
				status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
				return
			}
			status.SetText(i18n.T(i18n.KeyProxyParseOK))
		}).
		AddButton(i18n.T(i18n.KeyProxyClose), func() {
			a.app.SetRoot(a.root, true)
			a.app.SetFocus(a.chats)
			a.updateFocusStyle()
		})
	form.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyProxyTitleProfile) + " ")
	list.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyProxyTitleList) + " ")
	status.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyProxyTitleStatus) + " ")

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(form, 0, 1, true).
		AddItem(status, 4, 0, false)
	view := tview.NewFlex().
		AddItem(list, 34, 0, true).
		AddItem(right, 0, 1, false)
	a.app.SetRoot(view, true)
	a.app.SetFocus(list)
	if len(profiles) > 0 {
		refreshForm(profiles[0])
	}
}

func (a *App) showAuthPrompt(prompt *telegram.AuthPrompt) {
	input := tview.NewInputField().
		SetLabel(i18n.T(prompt.LabelKey) + ": ").
		SetFieldWidth(32)
	if prompt.Secret {
		input.SetMaskCharacter('*')
	}

	help := prompt.HelpMsg.String()
	if help == "" {
		help = i18n.T(i18n.KeyAuthHintSubmit)
	}

	form := tview.NewForm().
		AddFormItem(input).
		AddButton(i18n.T(i18n.KeyAuthSubmit), func() {
			a.submitAuthPrompt(prompt, input.GetText())
		})
	if prompt.CanCancel {
		form.AddButton(i18n.T(i18n.KeyAuthCancel), func() {
			replyAuthPrompt(prompt, telegram.AuthResponse{Err: fmt.Errorf("authentication cancelled")})
			a.app.SetRoot(a.root, true)
			a.app.SetFocus(a.chats)
		})
	}

	form.SetBorder(true).SetTitle(" " + i18n.T(prompt.TitleKey) + " ")
	form.SetCancelFunc(func() {
		if prompt.CanCancel {
			replyAuthPrompt(prompt, telegram.AuthResponse{Err: fmt.Errorf("authentication cancelled")})
		}
		a.app.SetRoot(a.root, true)
		a.app.SetFocus(a.chats)
	})
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.submitAuthPrompt(prompt, input.GetText())
		}
	})

	panel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(tview.NewTextView().SetTextAlign(tview.AlignCenter).SetText(help), 3, 0, false).
		AddItem(form, 9, 0, true).
		AddItem(tview.NewBox(), 0, 1, false)
	centered := tview.NewFlex().
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(panel, 58, 0, true).
		AddItem(tview.NewBox(), 0, 1, false)

	a.app.SetRoot(centered, true)
	a.app.SetFocus(form)
}

func (a *App) submitAuthPrompt(prompt *telegram.AuthPrompt, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		a.setStatusMsg(i18n.KeyStatusAuthEmpty)
		return
	}
	replyAuthPrompt(prompt, telegram.AuthResponse{Value: value})
	a.app.SetRoot(a.root, true)
	a.app.SetFocus(a.chats)
	a.updateFocusStyle()
	a.setStatusMsg(i18n.KeyStatusAuthSubmitted)
}

func replyAuthPrompt(prompt *telegram.AuthPrompt, response telegram.AuthResponse) {
	select {
	case prompt.Reply <- response:
	default:
	}
}

func proxyURL(cfg network.ProxyConfig) string {
	if !cfg.Active() {
		return "none"
	}
	if cfg.Kind == network.ProxyMTProxy {
		return fmt.Sprintf("mtproxy://%s@%s", cfg.Secret, cfg.Address)
	}
	user := ""
	if cfg.Username != "" || cfg.Password != "" {
		user = cfg.Username + ":" + cfg.Password + "@"
	}
	return fmt.Sprintf("%s://%s%s", cfg.Kind, user, cfg.Address)
}
