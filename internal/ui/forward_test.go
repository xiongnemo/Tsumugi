package ui

import (
	"testing"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func tviewListForTest() *tview.List {
	return tview.NewList().ShowSecondaryText(true)
}

// closeForwardPicker restores the main root, which the bare test App does not have.
func giveTestAppARoot(app *App) {
	app.root = tview.NewFlex().AddItem(app.messages, 0, 1, true)
}

func TestToggleMarkKeepsViewportOrder(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	v.SetMessages(readTestMessages("chat:1", 10, 11, 12, 13))

	// Mark out of order; the result must still read oldest-first.
	v.SelectByID("13")
	v.ToggleMark()
	v.SelectByID("11")
	v.ToggleMark()

	got := v.MarkedIDs()
	if len(got) != 2 || got[0] != "11" || got[1] != "13" {
		t.Fatalf("MarkedIDs = %v, want [11 13] in viewport order", got)
	}
}

func TestToggleMarkUnmarks(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	v.SetMessages(readTestMessages("chat:1", 10, 11))
	v.SelectByID("11")

	if marked, _ := v.ToggleMark(); !marked {
		t.Fatal("first toggle should mark")
	}
	if marked, _ := v.ToggleMark(); marked {
		t.Fatal("second toggle should unmark")
	}
	if v.MarkedCount() != 0 {
		t.Fatalf("MarkedCount = %d, want 0", v.MarkedCount())
	}
}

// The safety property: it must be impossible to forward a message you cannot see.
func TestMarksArePrunedByAJump(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	v.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	v.SelectByID("11")
	v.ToggleMark()
	v.SelectByID("12")
	v.ToggleMark()
	v.TakeMarkedPruned()

	// A jump replaces the window with a disjoint range.
	v.SetMessages(readTestMessages("chat:1", 500, 501))

	if v.MarkedCount() != 0 {
		t.Fatalf("MarkedCount = %d, want 0 — marks for messages out of view must go", v.MarkedCount())
	}
	if n := v.TakeMarkedPruned(); n != 2 {
		t.Fatalf("pruned = %d, want 2 reported so the UI can tell the user", n)
	}
	if n := v.TakeMarkedPruned(); n != 0 {
		t.Fatalf("pruned = %d on the second read, want the counter reset", n)
	}
}

// Paging up to grab older messages should keep the set: that is the whole point of building one
// across a long history.
func TestMarksSurviveAPreservingMerge(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	v.SetMessages(readTestMessages("chat:1", 20, 21, 22))
	v.SelectByID("21")
	v.ToggleMark()

	merged := append(readTestMessages("chat:1", 10, 11), readTestMessages("chat:1", 20, 21, 22)...)
	v.SetMessagesReplace(merged, true)

	if v.MarkedCount() != 1 {
		t.Fatalf("MarkedCount = %d, want the mark kept across a preserving merge", v.MarkedCount())
	}
	if got := v.MarkedIDs(); len(got) != 1 || got[0] != "21" {
		t.Fatalf("MarkedIDs = %v, want [21]", got)
	}
}

// openChat fires two non-preserving replaces about a second apart. If the viewport cleared marks
// on SetMessages, a set built in between would silently vanish.
func TestSetMessagesDoesNotClearMarksForVisibleMessages(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	v.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	v.SelectByID("11")
	v.ToggleMark()

	v.SetMessages(readTestMessages("chat:1", 10, 11, 12, 13))

	if v.MarkedCount() != 1 {
		t.Fatalf("MarkedCount = %d, want the mark to survive a same-window replace", v.MarkedCount())
	}
}

func TestGutterMarkerEncodesBothStates(t *testing.T) {
	mark := glyphs().Marked
	cases := []struct {
		selected, marked bool
		want             string
	}{
		{false, false, "  "},
		{true, false, "> "},
		{false, true, mark + " "},
		{true, true, ">" + mark},
	}
	for _, tc := range cases {
		if got := gutterMarker(tc.selected, tc.marked); got != tc.want {
			t.Errorf("gutterMarker(%v, %v) = %q, want %q", tc.selected, tc.marked, got, tc.want)
		}
	}
	// The gutter is exactly two cells wide; a wider marker would shift every message's content.
	for _, tc := range cases {
		if n := len([]rune(gutterMarker(tc.selected, tc.marked))); n != 2 {
			t.Errorf("gutterMarker(%v, %v) is %d cells, want 2", tc.selected, tc.marked, n)
		}
	}
}

func TestForwardPickerOffersSavedMessagesFirst(t *testing.T) {
	app := newSuggestionTestApp()
	app.allChats = []telegram.Chat{
		{ID: "chat:1", Title: "Current", Subtitle: "group"},
		{ID: "user:2", Title: "Alice", Subtitle: "private"},
	}
	app.forwardList = tviewListForTest()
	app.forwardSource = "chat:1"

	app.fillForwardList("")

	if app.forwardList.GetItemCount() < 2 {
		t.Fatalf("rows = %d, want Saved Messages plus at least one chat", app.forwardList.GetItemCount())
	}
	_, target := app.forwardList.GetItemText(0)
	if target != telegram.SavedMessagesTarget {
		t.Fatalf("first row targets %q, want the Saved Messages sentinel", target)
	}
}

// The source chat is not a useful forward destination and would be confusing at the top of the
// list next to Saved Messages.
func TestForwardPickerExcludesTheSourceChat(t *testing.T) {
	app := newSuggestionTestApp()
	app.allChats = []telegram.Chat{
		{ID: "chat:1", Title: "Current", Subtitle: "group"},
		{ID: "user:2", Title: "Alice", Subtitle: "private"},
	}
	app.forwardList = tviewListForTest()
	app.forwardSource = "chat:1"

	app.fillForwardList("")

	for i := 0; i < app.forwardList.GetItemCount(); i++ {
		if _, target := app.forwardList.GetItemText(i); target == "chat:1" {
			t.Fatal("the source chat should not be offered as a destination")
		}
	}
}

func TestForwardPickerFilters(t *testing.T) {
	app := newSuggestionTestApp()
	app.allChats = []telegram.Chat{
		{ID: "user:2", Title: "Alice", Subtitle: "private"},
		{ID: "user:3", Title: "Bob", Subtitle: "private"},
	}
	app.forwardList = tviewListForTest()
	app.forwardSource = "chat:1"

	app.fillForwardList("bob")

	targets := map[string]bool{}
	for i := 0; i < app.forwardList.GetItemCount(); i++ {
		_, target := app.forwardList.GetItemText(i)
		targets[target] = true
	}
	if targets["user:2"] {
		t.Fatal("Alice should be filtered out")
	}
	if !targets["user:3"] {
		t.Fatal("Bob should match")
	}
}

func TestCommitForwardPickSendsMarkedIDs(t *testing.T) {
	app, cmds := newSendTestApp()
	app.messages.SetRect(0, 0, 40, 12)
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	app.messages.SelectByID("11")
	app.messages.ToggleMark()
	app.messages.SelectByID("12")
	app.messages.ToggleMark()

	app.forwardList = tviewListForTest()
	app.forwardSource = "chat:1"
	app.forwardDropAuthor = true
	app.fillForwardList("")
	giveTestAppARoot(app)

	app.commitForwardPick()

	var forward *telegram.Command
	for {
		select {
		case cmd := <-cmds:
			if cmd.Kind == telegram.CommandForwardMessages {
				c := cmd
				forward = &c
			}
			continue
		default:
		}
		break
	}
	if forward == nil {
		t.Fatal("no forward command sent")
	}
	if forward.ForwardTarget != telegram.SavedMessagesTarget {
		t.Fatalf("target = %q, want the highlighted first row", forward.ForwardTarget)
	}
	if len(forward.ForwardIDs) != 2 {
		t.Fatalf("ids = %v, want both marks", forward.ForwardIDs)
	}
	if !forward.ForwardDropAuthor {
		t.Fatal("DropAuthor should be carried through from the F entry point")
	}
	// A completed forward clears the set; leaving it marked invites an accidental repeat.
	if app.messages.MarkedCount() != 0 {
		t.Fatalf("MarkedCount = %d, want 0 after forwarding", app.messages.MarkedCount())
	}
}

func TestMarksClearedOnChatSwitch(t *testing.T) {
	app, _ := newSendTestApp()
	app.messages.SetRect(0, 0, 40, 12)
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.messages.SelectByID("11")
	app.messages.ToggleMark()

	app.leaveChatForDraft()

	if app.messages.MarkedCount() != 0 {
		t.Fatalf("MarkedCount = %d, want marks dropped on leaving the chat", app.messages.MarkedCount())
	}
}
