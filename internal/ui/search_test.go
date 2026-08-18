package ui

import (
	"strconv"
	"testing"
	"time"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func searchHits(ids ...int) []telegram.SearchHit {
	var out []telegram.SearchHit
	for _, id := range ids {
		out = append(out, telegram.SearchHit{
			PeerKey:   "chat:1",
			PeerTitle: "Chat",
			MessageID: id,
			Preview:   "hit " + strconv.Itoa(id),
			Date:      time.Unix(int64(id), 0),
		})
	}
	return out
}

func TestStepSearchCursorForwardAndBack(t *testing.T) {
	hits := searchHits(10, 11, 12)

	first, wrapped := stepSearchCursor(hits, "", 1)
	if first != "chat:1/10" || wrapped {
		t.Fatalf("first step = (%q, %v), want chat:1/10 without wrapping", first, wrapped)
	}
	second, _ := stepSearchCursor(hits, first, 1)
	if second != "chat:1/11" {
		t.Fatalf("second = %q, want chat:1/11", second)
	}
	back, _ := stepSearchCursor(hits, second, -1)
	if back != first {
		t.Fatalf("back = %q, want %q", back, first)
	}
}

func TestStepSearchCursorWraps(t *testing.T) {
	hits := searchHits(10, 11)

	next, wrapped := stepSearchCursor(hits, "chat:1/11", 1)
	if next != "chat:1/10" || !wrapped {
		t.Fatalf("forward from the end = (%q, %v), want chat:1/10 with wrapped", next, wrapped)
	}
	prev, wrapped := stepSearchCursor(hits, "chat:1/10", -1)
	if prev != "chat:1/11" || !wrapped {
		t.Fatalf("back from the start = (%q, %v), want chat:1/11 with wrapped", prev, wrapped)
	}
}

// A remembered hit can vanish — deleted, or scrolled out of a replaced window. Restarting from the
// end matching the direction keeps `n` going the way the user was already going.
func TestStepSearchCursorRestartsWhenTheHitIsGone(t *testing.T) {
	hits := searchHits(10, 11, 12)

	forward, _ := stepSearchCursor(hits, "chat:1/999", 1)
	if forward != "chat:1/10" {
		t.Fatalf("forward restart = %q, want the first hit", forward)
	}
	backward, _ := stepSearchCursor(hits, "chat:1/999", -1)
	if backward != "chat:1/12" {
		t.Fatalf("backward restart = %q, want the last hit", backward)
	}
}

func TestStepSearchCursorEmpty(t *testing.T) {
	if next, wrapped := stepSearchCursor(nil, "", 1); next != "" || wrapped {
		t.Fatalf("got (%q, %v), want empty", next, wrapped)
	}
}

// Message ids are per-peer, so the cursor identity has to include the chat or a global result set
// spanning two chats would confuse hits with the same id.
func TestHitKeyIsChatQualified(t *testing.T) {
	a := telegram.SearchHit{PeerKey: "chat:1", MessageID: 500}
	b := telegram.SearchHit{PeerKey: "chat:2", MessageID: 500}

	if hitKey(a) == hitKey(b) {
		t.Fatalf("hitKey collides across chats: %q", hitKey(a))
	}
}

func TestFindChatMatches(t *testing.T) {
	chats := []telegram.Chat{
		{ID: "user:1", Title: "Alice", Subtitle: "private"},
		{ID: "user:2", Title: "Bob", Subtitle: "private", LastPreview: "about alice"},
		{ID: "chat:3", Title: "Team", Subtitle: "group"},
	}

	got := findChatMatches(chats, "alice")
	if len(got) != 2 {
		t.Fatalf("matches = %d, want 2 (title and preview)", len(got))
	}
	if got[0].ID != "user:1" {
		t.Fatalf("first match = %q, want chat-list order", got[0].ID)
	}
	if len(findChatMatches(chats, "   ")) != 0 {
		t.Fatal("a blank query should match nothing")
	}
}

func TestFindMessageMatchesSkipsLocalIDs(t *testing.T) {
	msgs := []telegram.Message{
		{ID: "10", ChatID: "chat:1", Text: "needle here", CreatedAt: time.Unix(10, 0)},
		{ID: "local-1", ChatID: "chat:1", Text: "needle pending", CreatedAt: time.Unix(11, 0)},
		{ID: "12", ChatID: "chat:1", Text: "nothing", CreatedAt: time.Unix(12, 0)},
	}

	got := findMessageMatches(msgs, "needle")
	if len(got) != 1 {
		t.Fatalf("matches = %d, want 1 — a local send has no id to jump to", len(got))
	}
	if got[0].MessageID != 10 {
		t.Fatalf("MessageID = %d, want 10", got[0].MessageID)
	}
}

// A search for a caption legitimately matches a photo, so the media label is searched and used as
// the preview when there is no text.
func TestFindMessageMatchesUsesMediaLabel(t *testing.T) {
	msgs := []telegram.Message{
		{ID: "10", ChatID: "chat:1", Media: telegram.MediaAttachment{Label: "[Photo]"}, CreatedAt: time.Unix(10, 0)},
	}

	got := findMessageMatches(msgs, "photo")
	if len(got) != 1 {
		t.Fatalf("matches = %d, want 1", len(got))
	}
	if got[0].Preview != "[Photo]" {
		t.Fatalf("Preview = %q, want the media label", got[0].Preview)
	}
}

// A hit outside the current folder used to abort with "outside the current folder", telling the
// user their search had failed when it had actually succeeded.
func TestSearchChatsSwitchesFolderInsteadOfAborting(t *testing.T) {
	app := &App{
		chats:         tview.NewList(),
		currentFolder: 2,
		allFolders: []telegram.Folder{
			{ID: 2, Title: "Work", Kind: "telegram", Rules: telegram.FolderRules{IncludePeers: []string{"user:2"}}},
			{ID: 3, Title: "Personal", Kind: "telegram", Rules: telegram.FolderRules{IncludePeers: []string{"user:9"}}},
		},
		allChats: []telegram.Chat{
			{ID: "user:2", Title: "Colleague", FolderID: 2},
			{ID: "user:9", Title: "Needle", FolderID: 3},
		},
		statusForeground: tview.NewTextView(),
	}
	app.refreshChats()

	app.searchChats("needle")

	if app.currentFolder != 3 {
		t.Fatalf("currentFolder = %d, want 3 — the folder containing the match", app.currentFolder)
	}
	if app.chatsHighlightPeer != "user:9" {
		t.Fatalf("highlight = %q, want user:9", app.chatsHighlightPeer)
	}
}

// A slow earlier search must not overwrite the results of a newer one.
func TestApplySearchResultsIgnoresStaleRequests(t *testing.T) {
	app := newSuggestionTestApp()
	giveTestAppARoot(app)
	app.searchRequestID = 7
	app.searchHits = searchHits(10)

	app.applySearchResultsEvent(telegram.Event{
		Kind:       telegram.EventSearchResults,
		RequestID:  3,
		SearchHits: searchHits(99, 98),
	})

	if len(app.searchHits) != 1 || app.searchHits[0].MessageID != 10 {
		t.Fatalf("hits = %+v, want the stale reply ignored", app.searchHits)
	}
}

func TestApplySearchResultsAcceptsTheCurrentRequest(t *testing.T) {
	app := newSuggestionTestApp()
	giveTestAppARoot(app)
	app.searchRequestID = 7

	app.applySearchResultsEvent(telegram.Event{
		Kind:       telegram.EventSearchResults,
		RequestID:  7,
		SearchHits: searchHits(99, 98),
	})

	if len(app.searchHits) != 2 {
		t.Fatalf("hits = %d, want 2", len(app.searchHits))
	}
	if app.searchCursor != "" {
		t.Fatalf("cursor = %q, want reset for a fresh result set", app.searchCursor)
	}
}

// Both would dispatch with `go`, so their EventMessages ordering is nondeterministic and
// openChat's window could land after the jump and destroy the selection.
func TestJumpToSearchHitDoesNotAlsoOpenTheChat(t *testing.T) {
	app, cmds := newSendTestApp()
	giveTestAppARoot(app)
	app.allChats = []telegram.Chat{{ID: "chat:2", Title: "Other"}}

	app.jumpToSearchHit(telegram.SearchHit{PeerKey: "chat:2", PeerTitle: "Other", MessageID: 55})

	var kinds []telegram.CommandKind
	for {
		select {
		case cmd := <-cmds:
			kinds = append(kinds, cmd.Kind)
			continue
		default:
		}
		break
	}
	for _, kind := range kinds {
		if kind == telegram.CommandOpenChat {
			t.Fatalf("commands = %v, must not include CommandOpenChat alongside the jump", kinds)
		}
	}
	sawJump := false
	for _, kind := range kinds {
		if kind == telegram.CommandJumpToMessage {
			sawJump = true
		}
	}
	if !sawJump {
		t.Fatalf("commands = %v, want a jump", kinds)
	}
}

func TestSearchScopeValuesMatchLabels(t *testing.T) {
	if len(searchScopeValues) != len(searchScopeLabels()) {
		t.Fatalf("values = %d, labels = %d; the dropdown index maps between them",
			len(searchScopeValues), len(searchScopeLabels()))
	}
}
