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
	composer         *tview.TextArea
	composeStack     *tview.Flex
	rightPane        *tview.Flex
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
	// Pinned message list overlay. The panel opens immediately on '#' and is filled in
	// place when the search result arrives, so the key press feels instant.
	pinnedList      *tview.List
	pinnedHint      *tview.TextView
	pinnedPeer      string
	pinnedCache     []telegram.Message
	pinnedCachePeer string
	// Message actions modal (non-nil while detail/form overlay is open).
	msgActionDetail  *tview.TextView
	msgActionPreview *tview.TextView
	msgActionForm    *tview.Form
	msgActionSeq     int
	// Preview zoom, in steps from fit-to-pane; see preview_zoom.go. msgActionMsg is kept so a
	// zoom can re-rasterise without reopening the overlay.
	msgActionZoom     int
	msgActionZoomable bool
	msgActionMsg      telegram.Message
	settingsOverlay   *settingsOverlay
	lastChatRefresh   time.Time
	gapFillQueued     map[string]struct{}
	control           chan<- ControlEvent
	onboardingActive  bool
	suggest           *composeSuggestState
	// Draft debounce state. draftPeer is the chat the pending save belongs to, so a switch
	// away files it against the right peer.
	draftTimer       *time.Timer
	draftPeer        string
	draftFirstEditAt time.Time
	// Incoming typing, per peer then per typist. Kept per peer rather than for the open chat
	// only so that switching back and forth cannot show another chat's indicator.
	typingByPeer       map[string]map[string]typingState
	typingExpiry       *time.Timer
	typingNotifiedPeer string
	typingNotifiedAt   time.Time
	// readInboxMaxID is the highest read incoming message per peer, mirrored from the client so
	// the unread divider and the jump target can be computed without a round trip.
	readInboxMaxID map[string]int
	// Mark-read coalescing. markReadPending holds the highest id waiting to be sent per peer,
	// keyed by peer so a chat switch mid-interval cannot lose or misfile a read mark.
	markReadPending   map[string]int
	markReadSentMaxID map[string]int
	markReadSentAt    time.Time
	markReadTimer     *time.Timer
	// historyWindowed is true while the pane shows a window from the middle of the history
	// rather than the newest messages, which is what End and G use to offer a way back.
	historyWindowed bool
	unreadDividerID string
	// Forward picker overlay. Tracked in App because Esc has to be dismissed from the global
	// capture; an overlay's own SetInputCapture never sees it.
	forwardList       *tview.List
	forwardInput      *tview.InputField
	forwardSource     string
	forwardDropAuthor bool
	// forwardTargets is parallel to the picker rows, so the highlighted index resolves to a
	// destination without hiding peer keys in visible text.
	forwardTargets []forwardTarget
	// Search state. searchCursor is a hit identity, not an index, so a viewport replacement
	// cannot silently repoint it at a different result.
	searchList      *tview.List
	searchHits      []telegram.SearchHit
	searchCursor    string
	searchQuery     string
	searchScope     string
	searchRequestID int64
	// QR login overlay. Rebuilt never, refreshed in place: QR.Auth re-invokes its show callback
	// on every token expiry.
	qrView    *tview.TextView
	qrURLView *tview.TextView
	qrPrompt  *telegram.AuthPrompt
	// Narrow-terminal layout state; see narrow_layout.go.
	layoutTier        layoutTier
	layoutTierApplied bool
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
		composer:         tview.NewTextArea(),
		footer:           tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		statusConn:       tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		statusForeground: tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		statusBackground: tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft),
		theme:            theme,
	}
	tui.messages.SetInlineAnim(tui.settings.InlineAnim)
	tui.messages.SetLayoutMode(render.ParseLayoutMode(tui.settings.OutgoingLayout))
	tui.initComposeSuggestions()
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
	a.app.EnablePaste(true)
	a.folders.SetBorder(true)
	a.chats.SetBorder(true)
	a.messages.SetBorder(true)
	a.composer.SetBorder(true)
	a.composer.SetPlaceholder(i18n.T(i18n.KeyUIComposePlaceholder))
	a.statusConn.SetWrap(false)
	a.statusForeground.SetWrap(false)
	a.statusBackground.SetWrap(false)
	a.statusBar = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.statusConn, 0, 2, false).
		AddItem(a.statusForeground, 0, 4, false).
		AddItem(a.statusBackground, 0, 2, false)
	// Deliberately borderless. It is allotted a single row, and a bordered box spends both of
	// its rows on the border — the inner rect ends up zero-height and the text is never drawn,
	// which is why the status bar looked permanently empty. A border would cost two more rows,
	// which the 80x25 console target cannot spare for decoration.

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
	a.messages.SetOnReachNewer(a.onReachNewerMessages)
	a.messages.SetOnSelectionChanged(a.onMessageCursorMoved)
	// SetWrap, not SetWordWrap: the latter only disables wrapping at word boundaries and still
	// wraps mid-word, which pushed the second footer line off the bottom of its two rows and took
	// the proxy address and version with it.
	a.footer.SetWrap(false)
	a.refreshFooter()

	a.composeStack = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.suggest.panel, 0, 0, false).
		AddItem(a.suggest.grid, 0, 0, false).
		AddItem(a.composer, a.composerBoxRows(), 0, false).
		AddItem(a.suggest.ghost, 1, 0, false)

	a.rightPane = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.messages, 0, 1, false).
		AddItem(a.composeStack, a.composeStackRows(), 0, false).
		AddItem(a.statusBar, 1, 0, false).
		AddItem(a.footer, 2, 0, false)

	a.root = tview.NewFlex().
		AddItem(a.folders, folderRailWidth, 0, false).
		AddItem(a.chats, chatRailWidth, 0, true).
		AddItem(a.rightPane, 0, 1, false)

	a.chats.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if a.chatsListRestoring {
			return
		}
		vis := a.visibleChatsForFolder()
		if index >= 0 && index < len(vis) {
			a.chatsHighlightPeer = vis[index].ID
		}
	})

	// TextArea has no SetDoneFunc and swallows Enter as a newline, so sending is triggered
	// from App.capture via composerSendKey.
	a.composer.SetChangedFunc(func() {
		a.onComposerChanged(a.composer.GetText())
		a.syncComposerLayout()
		a.scheduleDraftSave()
		a.notifyTyping()
	})

	a.app.SetRoot(a.root, true)
	a.app.SetInputCapture(a.capture)
	// The composer's scroll has to be corrected after the editor's own cursor handling has
	// run, which the changed callback is too early for; see clampComposerScroll.
	a.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		a.clampComposerScroll()
		// tview exposes no resize hook, and the draw pass is where a new width first becomes
		// visible. applyLayoutTier is idempotent, so this costs a comparison per frame.
		width, _ := screen.Size()
		a.applyLayoutTier(width)
		return false
	})
	a.updateFocusStyle()
	a.refreshStatusBar()
}

func (a *App) capture(event *tcell.EventKey) *tcell.EventKey {
	if a.settingsOverlay != nil {
		return a.captureSettings(event)
	}
	focus := a.app.GetFocus()
	// Checked before the suggestion panel, which matches bare Enter/Tab with no modifier test
	// and would otherwise accept a suggestion instead of sending the message.
	if a.composerHasFocus() && composerSendKey(event) {
		a.submitComposer()
		return nil
	}
	if a.captureComposeSuggestions(event) {
		return nil
	}
	if a.focusedOverlayPrimitiveOwnsNavigation(focus, event) {
		return event
	}
	switch event.Key() {
	case tcell.KeyCtrlC:
		a.flushDraft(a.currentChat)
		a.flushMarkRead()
		a.app.Stop()
		return nil
	case tcell.KeyEnd:
		// Only intercepted while windowed; otherwise End is the viewport's own jump-to-bottom.
		if focus == a.messages && a.returnToTail() {
			return nil
		}
	case tcell.KeyUp:
		if focus == a.messages {
			a.messages.SelectDelta(-1)
			return nil
		}
	case tcell.KeyDown:
		if focus == a.messages {
			a.messages.SelectDelta(1)
			return nil
		}
	case tcell.KeyEnter:
		if focus == a.messages {
			a.showMessageActions()
			return nil
		}
	case tcell.KeyTAB:
		if a.composerHasFocus() {
			a.flushDraft(a.currentChat)
		}
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
		if a.composerHasFocus() {
			a.flushDraft(a.currentChat)
		}
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
		a.switchFocusPrevious()
		return nil
	case tcell.KeyEsc:
		if a.composerHasFocus() {
			a.flushDraft(a.currentChat)
		}
		// Every overlay must be dismissed from here. This capture runs before the focused
		// primitive and returns nil unconditionally, so an overlay's own SetInputCapture
		// never sees Esc and any cleanup it does there is dead code.
		if a.msgActionForm != nil {
			a.restoreMessageFocus()
			return nil
		}
		if a.pinnedList != nil {
			a.closePinnedPanel()
			return nil
		}
		if a.forwardList != nil {
			a.closeForwardPicker()
			return nil
		}
		if a.searchList != nil {
			a.closeSearchResults()
			return nil
		}
		// Clearing a pending mark set comes before falling back to the chat list, so Esc
		// always has an obvious local meaning first.
		if a.clearForwardMarks() {
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

	// A mouse click moves focus onto the forward picker's list, after which plain letters would
	// fall through to the global rune switch and trigger unrelated actions. Route them back into
	// the filter instead. Fed by hand rather than by refocusing, because tview resolves the
	// target primitive before this capture runs, so SetFocus here would not redirect this event.
	if a.forwardList != nil && a.forwardInput != nil && focus == a.forwardList &&
		event.Key() == tcell.KeyRune && event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == 0 {
		a.forwardInput.SetText(a.forwardInput.GetText() + string(event.Rune()))
		a.app.SetFocus(a.forwardInput)
		return nil
	}

	// The composer owns every key that reached this far: Enter inserts a newline, the send
	// keys were handled above, and Tab/Backtab/Esc were handled by the switch. Without this
	// the global rune switch below would see ordinary typing and 'q' would quit the app.
	if a.composerHasFocus() {
		return event
	}
	if isTextInputFocus(focus) {
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
			return nil
		}
	case 'k':
		if focus == a.messages {
			a.messages.SelectDelta(-1)
			return nil
		}
	case 'n':
		a.stepSearch(1)
		return nil
	case 'N':
		a.stepSearch(-1)
		return nil
	case 'v':
		if focus == a.messages {
			a.toggleForwardMark()
			return nil
		}
	case 'f':
		if focus == a.messages {
			a.openForwardPicker(false)
			return nil
		}
	case 'F':
		if focus == a.messages {
			a.openForwardPicker(true)
			return nil
		}
	case 'G':
		if focus == a.messages {
			a.jumpToLatest()
			return nil
		}
		a.showSearchWithScope("global")
		return nil
	case 'q':
		a.flushDraft(a.currentChat)
		a.flushMarkRead()
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
	case '#':
		if focus == a.messages {
			a.requestPinnedMessages()
			return nil
		}
	case '/':
		a.showSearch()
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
	switch event.Kind {
	case telegram.EventMentionSuggestions, telegram.EventBotCommandSuggestions, telegram.EventInlineResultSuggestions:
		a.applyComposeSuggestions(event)
		return
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
		// A removal-only event carries no Messages; the removals were already applied above.
		// Falling through to the replace branch would hand setMessages an empty list and blank
		// the whole conversation, which is what happened when a message was deleted from
		// another session.
		if len(event.Messages) == 0 && event.ReachedNewest {
			// A forward page that came back empty: the pane already holds the newest message.
			a.historyWindowed = false
			a.applyMessagesPaneTitle()
			return
		}
		if len(event.Messages) == 0 && len(event.RemoveMessageIDs) > 0 {
			a.applyMessagesPaneTitle()
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
			if event.ReachedNewest {
				a.historyWindowed = false
				a.applyMessagesPaneTitle()
			}
		} else {
			// Must run before the replace: setMessages consults historyWindowed to decide
			// whether to scroll to the tail, and the divider changes block heights.
			a.applyHistoryWindow(event)
			a.setMessages(event.Messages, event.PreserveViewport)
		}
		if event.SelectMessageID != "" && a.selectMessageByID(event.SelectMessageID) && event.WindowedHistory {
			// ensureSelectedVisible scrolls the minimum, which lands the first unread on the
			// bottom edge with none of the unread messages below it visible.
			a.messages.ScrollSelectedToTop()
		}
	case telegram.EventPeerPinned:
		if event.PeerKey != a.currentChat {
			return
		}
		a.messages.SetPinnedBanner(event.PinnedPreview)
		a.applyMessagesPaneTitle()
	case telegram.EventPinnedMessages:
		a.applyPinnedMessages(event.PeerKey, event.Messages)
	case telegram.EventInlineResultThumb:
		a.applyInlineThumb(event)
	case telegram.EventDraft:
		a.applyDraftEvent(event)
	case telegram.EventTyping:
		a.applyTypingEvent(event)
	case telegram.EventReadInbox:
		a.applyReadInboxEvent(event)
	case telegram.EventSearchResults:
		a.applySearchResultsEvent(event)
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
		a.chats.AddItem(render.ChatRow(display, rowWidth), render.Truncate(display.LastPreview, rowWidth), 0, func() {
			a.leaveChatForDraft()
			a.currentChat = peerID
			a.resetGapFillQueue()
			a.currentTitle = title
			a.currentBroadcast = telegram.ChatIsBroadcast(chat)
			a.currentGroupRead = telegram.ChatSupportsGroupReadMarks(chat)
			a.messages.SetBroadcastChannel(a.currentBroadcast)
			a.messages.SetGroupReadMarks(a.currentGroupRead)
			a.chatsHighlightPeer = peerID
			a.clearReplyTarget()
			a.messages.SetPinnedBanner("")
			a.pinnedCache = nil
			a.pinnedCachePeer = ""
			a.setMessages(nil, false)
			a.commands <- telegram.Command{Kind: telegram.CommandFocusChat, PeerKey: peerID}
			a.setStatusMsg(i18n.KeyStatusLoadingHistory)
			a.applyMessagesPaneTitle()
			a.refreshStatusBar()
			a.app.SetFocus(a.messages)
			a.updateFocusStyle()
			a.commands <- telegram.Command{
				Kind:         telegram.CommandOpenChat,
				PeerKey:      peerID,
				JumpToUnread: a.settings.JumpToFirstUnread,
			}
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
		if a.historyWindowed {
			a.messages.SetFollowEnd(false)
		} else {
			a.messages.ScrollToEnd()
		}
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
	} else if !message.Outgoing {
		// Nothing pending below means the view is following the tail, so the arrival is on
		// screen and has been read. The highlight deliberately does not move (see
		// MessageViewport.AppendMessage), so this cannot ride on the selection callback.
		a.scheduleMarkReadUpTo(message.ChatID, message.ID)
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

func (a *App) switchFocusPrevious() {
	switch a.app.GetFocus() {
	case a.composer:
		a.app.SetFocus(a.messages)
	case a.messages:
		a.app.SetFocus(a.chats)
	case a.chats:
		a.app.SetFocus(a.folders)
	default:
		a.app.SetFocus(a.composer)
	}
	a.updateFocusStyle()
}

func (a *App) updateFocusStyle() {
	// G means different things in different panes, and the footer says which.
	defer a.refreshFooter()
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
	// Service rows set ReplyToID for pins, so this is where "jump to pinned message" shows up.
	if msg.ReplyToID != "" {
		actions = append(actions, messageAction{ID: "jump_reply", LabelKey: i18n.KeyActionJumpReply})
	}
	actions = append(actions, messageAction{ID: "react", LabelKey: i18n.KeyActionReact})
	// Enter is what a user presses on a message, so forwarding has to be reachable from here
	// and not only from a key they have to already know about.
	if a.messages.MarkedCount() > 0 {
		actions = append(actions,
			messageAction{ID: "forward_marked", LabelKey: i18n.KeyActionForwardMarked},
			messageAction{ID: "unmark", LabelKey: i18n.KeyActionUnmark},
		)
	} else {
		actions = append(actions,
			messageAction{ID: "forward", LabelKey: i18n.KeyActionForward},
			messageAction{ID: "mark", LabelKey: i18n.KeyActionMark},
		)
	}
	actions = append(actions, messageAction{ID: "cancel", LabelKey: i18n.KeyActionCancel})

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
			SetWrap(false).
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
				SetWrap(false).
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
	// Counter-intuitively named: "horizontal" is about item layout, but it is also what makes
	// tview *wrap* buttons onto further lines. Left at the default, a button that does not fit
	// the width is silently dropped — see the `break` in Form.Draw — so actions would simply
	// become unreachable on a narrow terminal, which is exactly where they are hardest to lose.
	form.SetHorizontal(true)
	// Bordered and titled on purpose. Measurement says the buttons land inside their rect at every
	// width, so the earlier report of an invisible bar was a single unbordered row hugging the
	// bottom edge — indistinguishable from nothing being there. Two rows of chrome is a cheap
	// price for a region you can actually find, now that the bar is not wasting twenty.
	form.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyUIActions) + " ")
	labels := make([]string, 0, len(actions))
	for _, item := range actions {
		action := item
		label := i18n.T(action.LabelKey)
		labels = append(labels, label)
		form.AddButton(label, func() {
			a.restoreMessageFocus()
			if action.ID == "cancel" {
				return
			}
			a.runMessageAction(action.ID, msg)
		})
	}
	formH := messageActionFormHeight(labels, a.overlayWidth())

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(top, 0, 1, true).
		AddItem(form, formH, 0, false)

	a.msgActionDetail = detail
	a.msgActionForm = form
	a.msgActionPreview = imgView
	a.msgActionMsg = msg
	// Only a still raster can be rescaled; a video placeholder or a bare label has nothing to
	// resample.
	a.msgActionZoomable = imgView != nil && isStillImagePreviewPath(localPath)
	a.msgActionZoom = 0
	a.applyPreviewTitle()
	a.msgActionSeq++
	seq := a.msgActionSeq

	layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			a.restoreMessageFocus()
			return nil
		}
		switch event.Rune() {
		case '-', '_':
			if a.adjustPreviewZoom(-1) {
				return nil
			}
		case '=', '+':
			if a.adjustPreviewZoom(1) {
				return nil
			}
		case '0':
			// Back to fit-to-pane, so there is a way out of eight steps of zooming.
			if a.msgActionZoomable && a.msgActionZoom != 0 {
				a.adjustPreviewZoom(-a.msgActionZoom)
				return nil
			}
		}
		return event
	})
	a.app.SetRoot(layout, true)
	// The preview takes focus when there is one: arrows pan it, and the whole point of zoom is
	// lost if the first thing the user has to do is Tab. Tab still reaches the detail text and
	// the buttons.
	if imgView != nil {
		a.app.SetFocus(imgView)
	} else {
		a.app.SetFocus(detail)
	}

	if deferRaster && imgView != nil {
		a.renderPreviewAsync(msg, imgView, seq)
	}
}

func (a *App) messageActionMediaPreview(msg telegram.Message, maxCols, maxRows int) string {
	if msg.Media.LocalPath == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(msg.Media.LocalPath))
	switch ext {
	case ".webm", ".mp4", ".mkv", ".mov", ".avi", ".m4v":
		return "[gray]" + i18n.T(i18n.KeyUIPreviewVideoUnsupported) + "[-]"
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
	a.msgActionZoom = 0
	a.msgActionZoomable = false
	a.msgActionMsg = telegram.Message{}
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
		a.jumpToMessageID(msg.ReplyToID)
	case "react":
		a.showReactionPanelFor(msg)
	case "forward", "forward_marked":
		a.openForwardPicker(false)
	case "mark", "unmark":
		a.toggleForwardMark()
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
	a.showSearchWithScope("")
}

// showSearchWithScope opens the search form. An empty forceScope picks the scope from the focused
// pane, which is what '/' does; 'G' passes "global" to go straight to searching every chat.
func (a *App) showSearchWithScope(forceScope string) {
	// No composer branch here: capture returns early while a text input has focus, so '/'
	// never reaches the search key and is typed into the composer directly.
	focus := a.app.GetFocus()
	scope := "messages"
	label := i18n.T(i18n.KeySearchLabelMessages)
	if focus == a.chats || focus == a.folders {
		scope = "chats"
		label = i18n.T(i18n.KeySearchLabelChats)
	}
	if forceScope != "" {
		scope = forceScope
	}
	// Order matches searchScopeValues; the dropdown is pre-set from the focused pane so the
	// common case needs no interaction with it.
	scopeIndex := 0
	for i, value := range searchScopeValues {
		if value == scope {
			scopeIndex = i
			break
		}
	}
	input := tview.NewInputField().SetLabel(label).SetFieldWidth(40)
	form := tview.NewForm().
		AddFormItem(input).
		AddDropDown(i18n.T(i18n.KeySearchScopeLabel), searchScopeLabels(), scopeIndex, func(_ string, index int) {
			if index >= 0 && index < len(searchScopeValues) {
				scope = searchScopeValues[index]
			}
		}).
		AddButton(i18n.T(i18n.KeySearchButton), func() {
			a.runSearch(scope, input.GetText())
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
		AddItem(form, 9, 0, true).
		AddItem(tview.NewBox(), 0, 1, false)
	centered := tview.NewFlex().
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(panel, 58, 0, true).
		AddItem(tview.NewBox(), 0, 1, false)
	a.app.SetRoot(centered, true)
	a.app.SetFocus(form)
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
	if prompt.Kind == telegram.AuthPromptQR {
		a.showQRPrompt(prompt)
		return
	}
	// Any other prompt means the QR stage is over (typically the 2FA password after a scan).
	a.closeQRPrompt()
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
