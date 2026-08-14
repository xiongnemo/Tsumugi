package ui

import (
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func newCaptureTestApp() *App {
	return &App{
		app:      tview.NewApplication(),
		folders:  tview.NewList(),
		chats:    tview.NewList(),
		messages: NewMessageViewport(),
		composer: tview.NewInputField(),
		theme:    DefaultTheme(),
	}
}

func keyEvent(key tcell.Key) *tcell.EventKey {
	return tcell.NewEventKey(key, 0, tcell.ModNone)
}

func TestCaptureMainShellTabCyclesFocusForward(t *testing.T) {
	app := newCaptureTestApp()

	tests := []struct {
		name  string
		start tview.Primitive
		want  tview.Primitive
	}{
		{name: "folders to chats", start: app.folders, want: app.chats},
		{name: "chats to messages", start: app.chats, want: app.messages},
		{name: "messages to composer", start: app.messages, want: app.composer},
		{name: "composer to folders", start: app.composer, want: app.folders},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app.app.SetFocus(tt.start)
			if got := app.capture(keyEvent(tcell.KeyTAB)); got != nil {
				t.Fatalf("main shell Tab returned event, want consumed")
			}
			if got := app.app.GetFocus(); got != tt.want {
				t.Fatalf("focus after Tab = %T, want %T", got, tt.want)
			}
		})
	}
}

func TestCaptureMainShellBacktabCyclesFocusReverse(t *testing.T) {
	app := newCaptureTestApp()

	tests := []struct {
		name  string
		start tview.Primitive
		want  tview.Primitive
	}{
		{name: "folders to composer", start: app.folders, want: app.composer},
		{name: "chats to folders", start: app.chats, want: app.folders},
		{name: "messages to chats", start: app.messages, want: app.chats},
		{name: "composer to messages", start: app.composer, want: app.messages},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app.app.SetFocus(tt.start)
			if got := app.capture(keyEvent(tcell.KeyBacktab)); got != nil {
				t.Fatalf("main shell Backtab returned event, want consumed")
			}
			if got := app.app.GetFocus(); got != tt.want {
				t.Fatalf("focus after Backtab = %T, want %T", got, tt.want)
			}
		})
	}
}

func TestCapturePassesNavigationKeysToOverlayForms(t *testing.T) {
	keys := []tcell.Key{tcell.KeyTAB, tcell.KeyBacktab, tcell.KeyLeft, tcell.KeyRight}
	surfaces := []string{"onboarding", "search", "auth", "proxy", "reactions"}

	for _, surface := range surfaces {
		t.Run(surface, func(t *testing.T) {
			app := newCaptureTestApp()
			form := tview.NewForm().
				AddInputField("Value", "", 20, nil, nil).
				AddButton("OK", nil).
				AddButton("Cancel", nil)
			app.app.SetFocus(form)
			focused := app.app.GetFocus()

			for _, key := range keys {
				event := keyEvent(key)
				if got := app.capture(event); got != event {
					t.Fatalf("%s key %v was not passed through", surface, key)
				}
				if got := app.app.GetFocus(); got != focused {
					t.Fatalf("%s key %v changed focus to %T", surface, key, got)
				}
			}
		})
	}
}

func TestCapturePassesTabAndBacktabToNonMainOverlayList(t *testing.T) {
	app := newCaptureTestApp()
	list := tview.NewList()
	app.app.SetFocus(list)

	for _, key := range []tcell.Key{tcell.KeyTAB, tcell.KeyBacktab} {
		event := keyEvent(key)
		if got := app.capture(event); got != event {
			t.Fatalf("overlay list key %v was not passed through", key)
		}
		if got := app.app.GetFocus(); got != list {
			t.Fatalf("overlay list key %v changed focus to %T", key, got)
		}
	}
}

func TestCapturePreservesMessageActionDetailPreviewCycling(t *testing.T) {
	app := newCaptureTestApp()
	detail := tview.NewTextView()
	preview := tview.NewTextView()
	form := tview.NewForm().AddButton("Cancel", nil)
	app.msgActionDetail = detail
	app.msgActionPreview = preview
	app.msgActionForm = form
	app.app.SetFocus(detail)

	if got := app.capture(keyEvent(tcell.KeyTAB)); got != nil {
		t.Fatalf("message action Tab returned event, want consumed")
	}
	if got := app.app.GetFocus(); got != preview {
		t.Fatalf("message action Tab focus = %T, want preview", got)
	}

	if got := app.capture(keyEvent(tcell.KeyTAB)); got != nil {
		t.Fatalf("message action second Tab returned event, want consumed")
	}
	if !form.HasFocus() {
		t.Fatalf("message action second Tab focus = %T, want form child", app.app.GetFocus())
	}

	if got := app.capture(keyEvent(tcell.KeyBacktab)); got != nil {
		t.Fatalf("message action Backtab returned event, want consumed")
	}
	if got := app.app.GetFocus(); got != preview {
		t.Fatalf("message action Backtab focus = %T, want preview", got)
	}
}

func TestCapturePassesNavigationKeysToLogoutModal(t *testing.T) {
	app := newCaptureTestApp()
	modal := tview.NewModal().SetText("Log out?").AddButtons([]string{"OK", "Cancel"})
	app.app.SetFocus(modal)
	focused := app.app.GetFocus()

	for _, key := range []tcell.Key{tcell.KeyTAB, tcell.KeyLeft, tcell.KeyRight, tcell.KeyEnter} {
		event := keyEvent(key)
		if got := app.capture(event); got != event {
			t.Fatalf("logout modal key %v was not passed through", key)
		}
		if got := app.app.GetFocus(); got != focused {
			t.Fatalf("logout modal key %v changed focus to %T", key, got)
		}
	}
}

func TestCaptureSettingsPassesNavigationKeysToFocusedForm(t *testing.T) {
	app := newCaptureTestApp()
	list := tview.NewList()
	form := tview.NewForm().AddButton("Save", nil).AddButton("Close", nil)
	app.settingsOverlay = &settingsOverlay{list: list, form: form}
	app.app.SetFocus(form)
	focused := app.app.GetFocus()

	for _, key := range []tcell.Key{tcell.KeyTAB, tcell.KeyBacktab, tcell.KeyLeft, tcell.KeyRight} {
		event := keyEvent(key)
		if got := app.captureSettings(event); got != event {
			t.Fatalf("settings form key %v was not passed through", key)
		}
		if got := app.app.GetFocus(); got != focused {
			t.Fatalf("settings form key %v changed focus to %T", key, got)
		}
	}
}

func TestCaptureSettingsCategoryListMovesToForm(t *testing.T) {
	app := newCaptureTestApp()
	list := tview.NewList()
	form := tview.NewForm().AddButton("Save", nil)
	app.settingsOverlay = &settingsOverlay{list: list, form: form}
	app.app.SetFocus(list)

	if got := app.captureSettings(keyEvent(tcell.KeyRight)); got != nil {
		t.Fatalf("settings category Right returned event, want consumed")
	}
	if !form.HasFocus() {
		t.Fatalf("settings category Right focus = %T, want form child", app.app.GetFocus())
	}

	app.app.SetFocus(list)
	if got := app.captureSettings(keyEvent(tcell.KeyTAB)); got != nil {
		t.Fatalf("settings category Tab returned event, want consumed")
	}
	if !form.HasFocus() {
		t.Fatalf("settings category Tab focus = %T, want form child", app.app.GetFocus())
	}
}

func TestSelectMessageByIDScrollsToReplyTarget(t *testing.T) {
	app := &App{
		messages: NewMessageViewport(),
	}
	app.setMessages([]telegram.Message{
		{ID: "10", Text: "first", CreatedAt: time.Unix(10, 0)},
		{ID: "11", Text: "reply", ReplyToID: "10", CreatedAt: time.Unix(11, 0)},
	}, false)

	if !app.selectMessageByID("10") {
		t.Fatal("reply target was not selectable")
	}
	selected, ok := app.selectedMessageRow()
	if !ok {
		t.Fatal("no selected message")
	}
	if got := selected.ID; got != "10" {
		t.Fatalf("selected message = %s, want 10", got)
	}
}

func TestSetInlineAnimDisabledClearsDecodedFrameCache(t *testing.T) {
	media.ClearInlineAnimCache()
	t.Cleanup(media.ClearInlineAnimCache)

	gifPath := filepath.Join(t.TempDir(), "anim.gif")
	writeTinyGIF(t, gifPath)

	_ = media.AnimatedInlineANSI(gifPath, 0, 4, 2)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stats := media.InlineAnimCacheStats(); stats.Entries > 0 && stats.Bytes > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stats := media.InlineAnimCacheStats(); stats.Entries == 0 || stats.Bytes == 0 {
		t.Fatalf("animation cache was not populated before disabling: %+v", stats)
	}

	viewport := NewMessageViewport()
	viewport.SetInlineAnim(false)
	if stats := media.InlineAnimCacheStats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("animation cache was not cleared after disabling inline animation: %+v", stats)
	}
}

func writeTinyGIF(t *testing.T, path string) {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.SetColorIndex(0, 0, 1)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, &gif.GIF{
		Image: []*image.Paletted{first, second},
		Delay: []int{5, 5},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMergeMessagesPreservesSelection(t *testing.T) {
	app := &App{
		messages: NewMessageViewport(),
	}
	app.setMessages([]telegram.Message{
		{ID: "10", Text: "first", CreatedAt: time.Unix(10, 0)},
		{ID: "11", Text: "second", CreatedAt: time.Unix(11, 0)},
	}, false)
	if !app.selectMessageByID("11") {
		t.Fatal("message was not selectable")
	}

	app.mergeMessages([]telegram.Message{
		{ID: "9", Text: "older", CreatedAt: time.Unix(9, 0)},
	}, true)

	if got := len(app.messages.Messages()); got != 3 {
		t.Fatalf("message count = %d, want 3", got)
	}
	selected, ok := app.selectedMessageRow()
	if !ok || selected.ID != "11" {
		t.Fatalf("selection not preserved: ok=%v %+v", ok, selected)
	}
	if got := app.messages.OldestMessageID(); got != "9" {
		t.Fatalf("oldest message ID = %s, want 9", got)
	}
}

func TestApplyEventIgnoresMessagesForOtherChat(t *testing.T) {
	app := &App{
		messages:    NewMessageViewport(),
		currentChat: "chat:1",
	}
	app.setMessages([]telegram.Message{
		{ID: "10", ChatID: "chat:1", Text: "current", CreatedAt: time.Unix(10, 0)},
	}, false)

	app.applyEvent(telegram.Event{
		Kind:             telegram.EventMessages,
		PeerKey:          "chat:2",
		Messages:         []telegram.Message{{ID: "10", ChatID: "chat:2", Text: "other", CreatedAt: time.Unix(11, 0)}},
		RemoveMessageIDs: []string{"10"},
		Append:           true,
	})

	got := app.messages.Messages()
	if len(got) != 1 || got[0].ChatID != "chat:1" || got[0].Text != "current" {
		t.Fatalf("cross-chat event mutated viewport: %+v", got)
	}
}

func TestRelocalizeVisibleMessagesDoesNotScheduleGapFill(t *testing.T) {
	i18n.SetLocale("en")
	t.Cleanup(func() { i18n.SetLocale("en") })

	commands := make(chan telegram.Command, 1)
	app := &App{
		messages:     NewMessageViewport(),
		currentChat:  "chat:1",
		currentTitle: "Chat",
		commands:     commands,
	}
	may13 := time.Date(2026, 5, 13, 20, 0, 0, 0, time.UTC)
	may24 := time.Date(2026, 5, 24, 23, 0, 0, 0, time.UTC)
	app.messages.SetMessages([]telegram.Message{
		{ID: "53391", ChatID: "chat:1", CreatedAt: may13, Media: telegram.MediaAttachment{Kind: "poll", LabelKey: i18n.KeyMediaPoll, Label: "[Poll]"}},
		{ID: "54599", ChatID: "chat:1", CreatedAt: may24, Text: "newer"},
	})
	if !app.selectMessageByID("53391") {
		t.Fatal("expected to select message before relocalize")
	}

	i18n.SetLocale("zh")
	app.relocalizeVisibleMessages()

	select {
	case cmd := <-commands:
		t.Fatalf("locale refresh scheduled command: %+v", cmd)
	default:
	}
	selected, ok := app.selectedMessageRow()
	if !ok || selected.ID != "53391" {
		t.Fatalf("selection not preserved after relocalize: ok=%v %+v", ok, selected)
	}
	if selected.Media.Label != "[投票]" {
		t.Fatalf("media label = %q, want [投票]", selected.Media.Label)
	}
}

func TestMessageViewportReplacePreservesAnchorWhenPrepending(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 8)
	var initial []telegram.Message
	for i := 0; i < 35; i++ {
		id := strconv.Itoa(10 + i)
		initial = append(initial, telegram.Message{
			ID:        id,
			Text:      "hi",
			CreatedAt: time.Unix(int64(10+i), 0),
		})
	}
	v.SetMessages(initial)
	lastID := strconv.Itoa(10 + 34)
	if !v.SelectByID(lastID) {
		t.Fatal("expected to select last message")
	}
	// Do not follow the live tail; user is reading history (e.g. after scrolling up).
	v.followEnd = false

	_, _, iw, ih := v.GetInnerRect()
	v.layout(iw)
	oldOff := v.blocks[v.selected].offset
	oldScroll := v.scroll
	maxScrBefore := maxInt(0, v.totalHeight()-ih)
	if maxScrBefore <= 0 {
		t.Fatal("test setup: content should overflow viewport")
	}

	var withOlder []telegram.Message
	withOlder = append(withOlder, telegram.Message{ID: "9", Text: "older", CreatedAt: time.Unix(9, 0)})
	withOlder = append(withOlder, initial...)
	v.SetMessagesReplace(withOlder, true)

	sel, ok := v.SelectedMessage()
	if !ok || sel.ID != lastID {
		t.Fatalf("selection not preserved: ok=%v %+v", ok, sel)
	}
	v.layout(iw)
	newOff := v.blocks[v.selected].offset
	wantScroll := oldScroll + (newOff - oldOff)
	maxScrAfter := maxInt(0, v.totalHeight()-ih)
	if wantScroll > maxScrAfter {
		wantScroll = maxScrAfter
	}
	if v.scroll != wantScroll {
		t.Fatalf("scroll = %d want %d (anchor offset %d -> %d)", v.scroll, wantScroll, oldOff, newOff)
	}
	if v.scroll > maxScrAfter {
		t.Fatalf("scroll %d above maxScr %d", v.scroll, maxScrAfter)
	}
}

func TestMessageViewportAppendPreservesSelectedMessage(t *testing.T) {
	viewport := NewMessageViewport()
	viewport.SetMessages([]telegram.Message{
		{ID: "10", Text: "first", CreatedAt: time.Unix(10, 0)},
		{ID: "11", Text: "second", CreatedAt: time.Unix(11, 0)},
	})
	if !viewport.SelectByID("10") {
		t.Fatal("message was not selectable")
	}
	viewport.AppendMessage(telegram.Message{ID: "12", Text: "new", CreatedAt: time.Unix(12, 0)})
	selected, ok := viewport.SelectedMessage()
	if !ok || selected.ID != "10" {
		t.Fatalf("selection moved after append: %+v ok=%v", selected, ok)
	}
}

func TestMessageViewportAppendAtBottomPreservesHighlight(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 8)
	var initial []telegram.Message
	for i := 0; i < 20; i++ {
		id := strconv.Itoa(10 + i)
		initial = append(initial, telegram.Message{
			ID:        id,
			Text:      "line",
			CreatedAt: time.Unix(int64(10+i), 0),
		})
	}
	v.SetMessages(initial)
	lastID := strconv.Itoa(29)
	if !v.SelectByID(lastID) {
		t.Fatal("select last")
	}
	v.ScrollToEnd()
	v.layout(40)
	maxScr := maxInt(0, v.totalHeight()-8)
	v.scroll = maxScr

	v.AppendMessage(telegram.Message{ID: "30", Text: "newest", CreatedAt: time.Unix(30, 0)})
	selected, ok := v.SelectedMessage()
	if !ok || selected.ID != lastID {
		t.Fatalf("highlight jumped on append at bottom: got %v want %s", selected, lastID)
	}
	if v.PendingBelow() != 0 {
		t.Fatalf("pendingBelow = %d, want 0 when scrolled to bottom", v.PendingBelow())
	}
}

func TestMessageViewportCapsSetAndAppend(t *testing.T) {
	v := NewMessageViewport()
	v.setMessageLimitForTest(5)
	v.SetRect(0, 0, 40, 8)

	v.SetMessages(viewportTestMessages(0, 7))
	if got := len(v.Messages()); got != 5 {
		t.Fatalf("message count after set = %d, want 5", got)
	}
	if oldest := v.OldestMessageID(); oldest != "2" {
		t.Fatalf("oldest after set = %q, want 2", oldest)
	}

	v.layout(40)
	if stats := v.Stats(); stats.Blocks != 5 {
		t.Fatalf("blocks after layout = %d, want 5", stats.Blocks)
	}

	v.AppendMessage(telegram.Message{ID: "7", Text: "new", CreatedAt: time.Unix(7, 0)})
	msgs := v.Messages()
	if got := len(msgs); got != 5 {
		t.Fatalf("message count after append = %d, want 5", got)
	}
	if msgs[0].ID != "3" || msgs[len(msgs)-1].ID != "7" {
		t.Fatalf("append cap kept IDs %s..%s, want 3..7", msgs[0].ID, msgs[len(msgs)-1].ID)
	}
	if stats := v.Stats(); stats.Blocks != 0 {
		t.Fatalf("stale blocks retained after append: %+v", stats)
	}
}

func TestMessageViewportPreserveReplaceKeepsAnchorWithinCap(t *testing.T) {
	v := NewMessageViewport()
	v.setMessageLimitForTest(5)
	v.SetRect(0, 0, 40, 8)
	v.SetMessages(viewportTestMessages(10, 5))
	if !v.SelectByID("12") {
		t.Fatal("expected anchor selectable")
	}

	v.SetMessagesReplace(viewportTestMessages(0, 15), true)
	selected, ok := v.SelectedMessage()
	if !ok || selected.ID != "12" {
		t.Fatalf("selection after capped replace = %+v ok=%v, want 12", selected, ok)
	}
	msgs := v.Messages()
	if got := len(msgs); got != 5 {
		t.Fatalf("message count after capped replace = %d, want 5", got)
	}
	if msgs[0].ID != "10" || msgs[len(msgs)-1].ID != "14" {
		t.Fatalf("capped replace kept IDs %s..%s, want 10..14", msgs[0].ID, msgs[len(msgs)-1].ID)
	}
}

func TestMessageViewportTrimDropsRasterPreviewPayloads(t *testing.T) {
	v := NewMessageViewport()
	v.setMessageLimitForTest(2)
	v.SetMessages([]telegram.Message{
		{ID: "1", CreatedAt: time.Unix(1, 0), Media: telegram.MediaAttachment{PreviewText: strings.Repeat("x", 1024)}},
		{ID: "2", CreatedAt: time.Unix(2, 0)},
		{ID: "3", CreatedAt: time.Unix(3, 0)},
	})
	for _, msg := range v.Messages() {
		if msg.ID == "1" || msg.Media.PreviewText != "" {
			t.Fatalf("trimmed preview payload retained in viewport: %+v", msg)
		}
	}
}

func viewportTestMessages(start, count int) []telegram.Message {
	messages := make([]telegram.Message, 0, count)
	for i := 0; i < count; i++ {
		id := strconv.Itoa(start + i)
		messages = append(messages, telegram.Message{
			ID:        id,
			Text:      "line",
			CreatedAt: time.Unix(int64(start+i), 0),
		})
	}
	return messages
}

func TestFolderRulesMatchTelegramFiltersAndArchive(t *testing.T) {
	work := telegram.Folder{
		ID:    2,
		Title: "Work",
		Kind:  "telegram",
		Rules: telegram.FolderRules{Groups: true, ExcludeArchived: true, ExcludePeers: []string{"chat:3"}},
	}
	if !folderRulesMatch(work.Rules, telegram.Chat{ID: "chat:2", Kind: "chat", Subtitle: "group"}) {
		t.Fatal("group should match folder rules")
	}
	if folderRulesMatch(work.Rules, telegram.Chat{ID: "chat:2", Kind: "chat", Subtitle: "group", FolderID: 1}) {
		t.Fatal("archived chat should be excluded")
	}
	if folderRulesMatch(work.Rules, telegram.Chat{ID: "chat:3", Kind: "chat", Subtitle: "group"}) {
		t.Fatal("explicitly excluded chat should not match")
	}
}

func TestAllFolderExcludesArchivedChats(t *testing.T) {
	app := &App{currentFolder: 0}
	if app.chatInCurrentFolder(telegram.Chat{ID: "user:2", FolderID: 1}) {
		t.Fatal("archived chat should not appear in All")
	}
	if !app.chatInCurrentFolder(telegram.Chat{ID: "user:3", FolderID: 0}) {
		t.Fatal("active chat should appear in All")
	}
}

func TestArchiveFolderMatchesPeerFolderOne(t *testing.T) {
	app := &App{allFolders: []telegram.Folder{{ID: telegram.ArchiveFolderID, Title: "Archived chats", Kind: "archive", Archive: true}}, currentFolder: telegram.ArchiveFolderID}
	if !app.chatInCurrentFolder(telegram.Chat{ID: "user:2", FolderID: 1}) {
		t.Fatal("archived chat was not shown in archive folder")
	}
	if app.chatInCurrentFolder(telegram.Chat{ID: "user:3", FolderID: 0}) {
		t.Fatal("main chat should not be shown in archive folder")
	}
}

func TestRefreshFoldersRestoresSelectionByFolderID(t *testing.T) {
	app := &App{
		folders: tview.NewList(),
		allFolders: []telegram.Folder{
			{ID: 0, Title: "All", Kind: "all"},
			{ID: 3, Title: "Work", Kind: "telegram"},
		},
		currentFolder: 3,
		allChats:      []telegram.Chat{},
	}
	app.refreshFolders()
	if idx := app.folders.GetCurrentItem(); idx != 1 {
		t.Fatalf("folder selection index = %d, want 1 (Work)", idx)
	}
}

func TestRefreshFoldersPreservesKeyboardCursor(t *testing.T) {
	app := &App{
		folders: tview.NewList(),
		allFolders: []telegram.Folder{
			{ID: 0, Title: "All", Kind: "all"},
			{ID: 1, Title: "A", Kind: "telegram"},
			{ID: 2, Title: "B", Kind: "telegram"},
		},
		currentFolder: 0,
		allChats:      []telegram.Chat{},
	}
	app.refreshFolders()
	app.folders.SetCurrentItem(2)
	app.refreshFolders()
	if idx := app.folders.GetCurrentItem(); idx != 2 {
		t.Fatalf("folder browse index = %d, want 2", idx)
	}
}

func TestRefreshChatsRestoresHighlightByPeerID(t *testing.T) {
	app := &App{
		chats:              tview.NewList(),
		currentFolder:      0,
		chatsHighlightPeer: "user:2",
		allChats: []telegram.Chat{
			{ID: "user:1", Title: "A", Subtitle: "x"},
			{ID: "user:2", Title: "B", Subtitle: "x"},
			{ID: "user:3", Title: "C", Subtitle: "x"},
		},
	}
	app.refreshChats()
	if idx := app.chats.GetCurrentItem(); idx != 1 {
		t.Fatalf("chat list index = %d, want 1 (user:2)", idx)
	}
}

func TestChatListRowWidthUsesInnerRectWithSafetyMargin(t *testing.T) {
	chats := tview.NewList()
	chats.SetBorder(true)
	chats.SetRect(0, 0, 34, 8)
	app := &App{chats: chats}

	if got := app.chatListRowWidth(); got != 31 {
		t.Fatalf("chat list row width = %d, want 31", got)
	}
}

func TestChatListRowWidthFallsBackBeforeDraw(t *testing.T) {
	app := &App{chats: tview.NewList()}

	if got := app.chatListRowWidth(); got != 31 {
		t.Fatalf("fallback chat list row width = %d, want 31", got)
	}
}

func TestRefreshChatsFitsRowsToChatListWidth(t *testing.T) {
	chats := tview.NewList().ShowSecondaryText(true)
	chats.SetBorder(true)
	chats.SetRect(0, 0, 20, 8)
	app := &App{
		chats:         chats,
		currentFolder: 0,
		allChats: []telegram.Chat{
			{
				ID:          "chat:1",
				Title:       "聊天群組😊聊天群組😊聊天群組",
				Subtitle:    "群組副標題😊群組副標題😊",
				LastPreview: "最新訊息內容😊最新訊息內容",
			},
		},
	}

	app.refreshChats()
	main, secondary := app.chats.GetItemText(0)
	rowWidth := app.chatListRowWidth()
	if render.StringWidth(main) > rowWidth {
		t.Fatalf("main row width = %d, want <= %d: %q", render.StringWidth(main), rowWidth, main)
	}
	if render.StringWidth(secondary) > rowWidth {
		t.Fatalf("secondary row width = %d, want <= %d: %q", render.StringWidth(secondary), rowWidth, secondary)
	}
}

func TestApplySearchUsesVisibleChatIndex(t *testing.T) {
	app := &App{
		chats:            tview.NewList(),
		statusForeground: tview.NewTextView(),
		currentFolder:    2,
		allFolders: []telegram.Folder{
			{ID: 2, Title: "Folder", Kind: "telegram", Rules: telegram.FolderRules{IncludePeers: []string{"user:2", "user:3"}}},
		},
		allChats: []telegram.Chat{
			{ID: "user:1", Title: "Outside", FolderID: 0},
			{ID: "user:2", Title: "Needle", FolderID: 2},
			{ID: "user:3", Title: "Other", FolderID: 2},
		},
	}
	app.refreshChats()
	app.applySearch("chats", "needle")
	if idx := app.chats.GetCurrentItem(); idx != 0 {
		t.Fatalf("chat search index = %d, want 0 within visible folder", idx)
	}
	if app.chatsHighlightPeer != "user:2" {
		t.Fatalf("highlight peer = %q, want user:2", app.chatsHighlightPeer)
	}
}

func TestRefreshChatsPreservesHighlightPeerDuringListRebuild(t *testing.T) {
	app := &App{
		chats:         tview.NewList(),
		currentFolder: 0,
		allChats: []telegram.Chat{
			{ID: "user:1", Title: "A"},
			{ID: "user:2", Title: "B"},
			{ID: "user:3", Title: "C"},
		},
		chatsHighlightPeer: "user:3",
	}
	app.chats.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if app.chatsListRestoring {
			return
		}
		vis := app.visibleChatsForFolder()
		if index >= 0 && index < len(vis) {
			app.chatsHighlightPeer = vis[index].ID
		}
	})
	app.refreshChats()
	if app.chatsHighlightPeer != "user:3" {
		t.Fatalf("chatsHighlightPeer = %q, want user:3", app.chatsHighlightPeer)
	}
	if idx := app.chats.GetCurrentItem(); idx != 2 {
		t.Fatalf("chat list index = %d, want 2", idx)
	}
}

func TestChatsVisibleIndexFromListIndexSkipsWelcomeRow(t *testing.T) {
	app := &App{chats: tview.NewList()}
	app.chats.AddItem("Tsumugi", "Waiting for Telegram connection", 0, nil)
	app.chats.AddItem("Row", "Sec", 0, nil)
	if got := app.chatsVisibleIndexFromListIndex(0); got != -1 {
		t.Fatalf("welcome row: got %d, want -1", got)
	}
	if got := app.chatsVisibleIndexFromListIndex(1); got != 0 {
		t.Fatalf("after welcome: got %d, want 0", got)
	}
	if got := app.chatsVisibleIndexFromListIndex(2); got != 1 {
		t.Fatalf("third row: got %d, want 1", got)
	}
}

// A delete arriving from another session emits EventMessages carrying only RemoveMessageIDs.
// Treating that as a full replace blanked the entire conversation.
func TestRemovalOnlyEventKeepsRemainingMessages(t *testing.T) {
	app := &App{
		messages: NewMessageViewport(),
	}
	app.setMessages([]telegram.Message{
		{ID: "10", Text: "first", CreatedAt: time.Unix(10, 0)},
		{ID: "11", Text: "second", CreatedAt: time.Unix(11, 0)},
		{ID: "12", Text: "third", CreatedAt: time.Unix(12, 0)},
	}, false)

	app.applyEvent(telegram.Event{
		Kind:             telegram.EventMessages,
		RemoveMessageIDs: []string{"11"},
	})

	if app.selectMessageByID("11") {
		t.Fatal("deleted message is still present")
	}
	for _, id := range []string{"10", "12"} {
		if !app.selectMessageByID(id) {
			t.Fatalf("message %s was wiped by a removal-only event", id)
		}
	}
}

// A genuine replace with an empty list must still clear, e.g. opening a chat with no history.
func TestEmptyReplaceStillClearsMessages(t *testing.T) {
	app := &App{
		messages: NewMessageViewport(),
	}
	app.setMessages([]telegram.Message{
		{ID: "10", Text: "first", CreatedAt: time.Unix(10, 0)},
	}, false)

	app.applyEvent(telegram.Event{Kind: telegram.EventMessages})

	if app.selectMessageByID("10") {
		t.Fatal("empty replace did not clear the viewport")
	}
}
