package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/settings"
)

// The whole before-draw path, run through a real Application against a simulation screen.
//
// Application.draw holds the write lock while it calls the before-draw hook, and a sync.RWMutex is
// not reentrant — so anything in that hook which takes a read lock, such as GetFocus, deadlocks the
// very first draw. The screen is already initialised and cleared by then, so the symptom is a black
// terminal with the process still alive and Run never returning: no panic, no error, nothing in the
// terminal to go on.
//
// Asserted with a timeout rather than by inspection, because the failure mode is a hang.
func TestBeforeDrawHookDoesNotDeadlock(t *testing.T) {
	app := &App{
		app:              tview.NewApplication(),
		folders:          tview.NewList(),
		chats:            tview.NewList(),
		messages:         NewMessageViewport(),
		composer:         tview.NewTextArea(),
		footer:           tview.NewTextView().SetDynamicColors(true),
		statusConn:       tview.NewTextView(),
		statusForeground: tview.NewTextView(),
		statusBackground: tview.NewTextView(),
		theme:            DefaultTheme(),
		settings:         settings.Defaults(),
	}
	app.initComposeSuggestions()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)
	app.app.SetScreen(screen)
	app.build()
	app.app.SetRoot(app.root, true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// ForceDraw, not Draw: Draw queues the work and waits for the event loop, which is not
		// running here, so it would block for reasons that have nothing to do with the hook.
		// ForceDraw calls draw directly, taking the same write lock the real first draw does.
		//
		// Twice: the first pass takes the not-yet-applied layout branch and the second the
		// already-applied one, and only one of them reaches the footer refresh.
		app.app.ForceDraw()
		app.app.ForceDraw()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the before-draw hook deadlocked; something in it takes an Application lock")
	}

	if !app.loggedFirstDraw {
		t.Fatal("the hook did not run to completion")
	}
}

// The cached flag has to actually track focus, or the footer describes G wrongly.
func TestMessagePaneFocusFlagTracksFocus(t *testing.T) {
	app := newSuggestionTestApp()
	giveTestAppARoot(app)

	app.app.SetFocus(app.messages)
	app.updateFocusStyle()
	if !app.messagePaneFocused {
		t.Fatal("focus is on the message pane but the flag says otherwise")
	}

	app.app.SetFocus(app.chats)
	app.updateFocusStyle()
	if app.messagePaneFocused {
		t.Fatal("focus moved to the chat list but the flag still says message pane")
	}
}
