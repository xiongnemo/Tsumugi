package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
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

// Pinning was display-only: Tsumugi could show every pinned message and pin none. These are the
// filters that decide whether the action is offered at all.
func TestPinnableMessageExcludesWhatCannotBePinned(t *testing.T) {
	app := newCaptureTestApp()
	good := telegram.Message{ID: "12", ChatID: "chat:1", State: "synced"}
	if !app.pinnableMessage(good) {
		t.Fatal("a synced message should be pinnable")
	}
	for name, msg := range map[string]telegram.Message{
		"service": {ID: "12", ChatID: "chat:1", State: "synced", ServiceKey: "service.pinned_message"},
		"pending": {ID: "12", ChatID: "chat:1", State: "pending"},
		"local":   {ID: "-5", ChatID: "chat:1", State: "synced"},
	} {
		if app.pinnableMessage(msg) {
			t.Errorf("%s: should not be pinnable", name)
		}
	}
}

// Which of pin/unpin is offered comes from the same cached list the banner and the panel show, so
// the action always matches what is on screen.
func TestMessageIsPinnedReadsTheCachedList(t *testing.T) {
	app := newCaptureTestApp()
	msg := telegram.Message{ID: "12", ChatID: "chat:1", State: "synced"}

	if app.messageIsPinned(msg) {
		t.Error("nothing is cached, so nothing should read as pinned")
	}
	app.pinnedCachePeer = "chat:1"
	app.pinnedCache = []telegram.Message{{ID: "12", ChatID: "chat:1"}}
	if !app.messageIsPinned(msg) {
		t.Error("the cached pinned message was not recognised")
	}
	// A cache belonging to another chat must not leak into this one.
	app.pinnedCachePeer = "chat:2"
	if app.messageIsPinned(msg) {
		t.Error("another chat's pinned list was used")
	}
}

func TestRequestPinSendsTheCommand(t *testing.T) {
	app, cmds := newSendTestApp()
	app.requestPin(telegram.Message{ID: "12", ChatID: "chat:1", State: "synced"}, true)

	select {
	case cmd := <-cmds:
		if cmd.Kind != telegram.CommandPinMessage || cmd.MessageID != 12 || !cmd.Unpin {
			t.Fatalf("command = %+v, want an unpin of message 12", cmd)
		}
	default:
		t.Fatal("no command was sent")
	}
}
