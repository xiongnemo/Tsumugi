package ui

import (
	"testing"

	"github.com/rivo/tview"
)

// A bare Linux console is 80 columns. Folders (16) plus the chat list (34) leave the message pane
// under 30, which is unusable, so the folder rail has to go.
func TestLayoutTierForWidth(t *testing.T) {
	cases := []struct {
		width int
		want  layoutTier
	}{
		{0, layoutFull},   // no geometry yet
		{120, layoutFull}, // roomy terminal
		{100, layoutFull},
		{90, layoutFull},
		{80, layoutNoFolders}, // the console case
		{70, layoutNarrowChats},
		{60, layoutNarrowChats},
		{40, layoutNarrowChats},
	}
	for _, tc := range cases {
		if got := layoutTierForWidth(tc.width); got != tc.want {
			t.Errorf("layoutTierForWidth(%d) = %v, want %v", tc.width, got, tc.want)
		}
	}
}

// Whatever the tier, the message pane must keep enough room to read in.
func TestLayoutTierLeavesTheMessagePaneUsable(t *testing.T) {
	for width := 40; width <= 200; width++ {
		var left int
		switch layoutTierForWidth(width) {
		case layoutFull:
			left = folderRailWidth + chatRailWidth
		case layoutNoFolders:
			left = chatRailWidth
		default:
			left = chatRailNarrowWidth
		}
		if remaining := width - left; remaining < messagePaneMinWidth && width >= chatRailNarrowWidth+messagePaneMinWidth {
			t.Fatalf("width %d leaves %d columns for messages, want at least %d",
				width, remaining, messagePaneMinWidth)
		}
	}
}

func TestApplyLayoutTierHidesTheFolderRail(t *testing.T) {
	app := newCaptureTestApp()
	app.rightPane = tview.NewFlex()
	app.root = tview.NewFlex().
		AddItem(app.folders, folderRailWidth, 0, false).
		AddItem(app.chats, chatRailWidth, 0, true).
		AddItem(app.rightPane, 0, 1, false)

	app.applyLayoutTier(80)
	if app.layoutTier != layoutNoFolders {
		t.Fatalf("tier = %v, want layoutNoFolders at 80 columns", app.layoutTier)
	}

	// Hidden by resizing to zero rather than removed, so Tab focus and every existing folder
	// call site keep working.
	if app.folders == nil {
		t.Fatal("the folder list must still exist after being hidden")
	}
	if app.root.GetItemCount() != 3 {
		t.Fatalf("root items = %d, want the folder rail resized rather than removed", app.root.GetItemCount())
	}
}

func TestApplyLayoutTierIsIdempotent(t *testing.T) {
	app := newCaptureTestApp()
	app.rightPane = tview.NewFlex()
	app.root = tview.NewFlex().
		AddItem(app.folders, folderRailWidth, 0, false).
		AddItem(app.chats, chatRailWidth, 0, true).
		AddItem(app.rightPane, 0, 1, false)

	app.applyLayoutTier(80)
	first := app.layoutTier
	app.applyLayoutTier(80)

	if app.layoutTier != first {
		t.Fatalf("tier changed on a repeat call: %v then %v", first, app.layoutTier)
	}
}

func TestApplyLayoutTierRestoresChromeWhenWidened(t *testing.T) {
	app := newCaptureTestApp()
	app.rightPane = tview.NewFlex()
	app.root = tview.NewFlex().
		AddItem(app.folders, folderRailWidth, 0, false).
		AddItem(app.chats, chatRailWidth, 0, true).
		AddItem(app.rightPane, 0, 1, false)

	app.applyLayoutTier(60)
	app.applyLayoutTier(140)

	if app.layoutTier != layoutFull {
		t.Fatalf("tier = %v, want layoutFull once there is room again", app.layoutTier)
	}
}

// The inline result grid must fall back to one cell per row rather than dividing by zero or
// drawing off-screen.
func TestInlineGridFallsBackToOneColumn(t *testing.T) {
	for _, width := range []int{-1, 0, 1, 5, 10} {
		if got := inlineGridColumns(width); got < 1 {
			t.Fatalf("inlineGridColumns(%d) = %d, want at least 1", width, got)
		}
	}
	if got := inlineGridColumns(20); got != 1 {
		t.Fatalf("inlineGridColumns(20) = %d, want 1 on a narrow terminal", got)
	}
}
