package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// App.capture handles Esc unconditionally and returns nil, so an overlay's own
// SetInputCapture never sees it. The pinned panel relied on that dead branch, which left
// a.pinnedList non-nil after every dismissal — a later result would then fill an overlay
// the user had already closed.
func TestCaptureEscClosesPinnedPanel(t *testing.T) {
	app := newCaptureTestApp()
	app.root = tview.NewFlex()
	app.currentChat = "chat:1"
	app.openPinnedPanel("chat:1", nil)

	if app.pinnedList == nil {
		t.Fatal("panel did not open")
	}
	if got := app.capture(keyEvent(tcell.KeyEsc)); got != nil {
		t.Fatalf("Esc returned event, want consumed")
	}
	if app.pinnedList != nil {
		t.Fatal("pinnedList still set after Esc; the panel state leaked")
	}
	if app.pinnedPeer != "" {
		t.Fatalf("pinnedPeer = %q after Esc, want empty", app.pinnedPeer)
	}
}
