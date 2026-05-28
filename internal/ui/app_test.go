package ui

import (
	"strconv"
	"testing"
	"time"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

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
