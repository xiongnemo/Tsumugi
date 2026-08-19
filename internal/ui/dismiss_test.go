package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func qKey() *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
}

// q reaching for the way out of a viewer is the pager convention, and it was doing nothing in every
// overlay.
func TestQClosesTheMessageActionOverlay(t *testing.T) {
	app := newSuggestionTestApp()
	giveTestAppARoot(app)
	app.msgActionPreview = tviewTextViewForTest()
	app.msgActionDetail = tviewTextViewForTest()
	app.app.SetFocus(app.msgActionPreview)

	if got := app.capture(qKey()); got != nil {
		t.Fatal("q should be consumed while an overlay is open")
	}
	if app.msgActionPreview != nil || app.msgActionDetail != nil {
		t.Fatal("the overlay should have been dismissed")
	}
}

func TestQClosesEachOverlayKind(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*App) tview.Primitive
		open  func(*App) bool
	}{
		{
			"pinned",
			func(a *App) tview.Primitive { a.pinnedList = tviewListForTest(); return a.pinnedList },
			func(a *App) bool { return a.pinnedList != nil },
		},
		{
			"forward",
			func(a *App) tview.Primitive { a.forwardList = tviewListForTest(); return a.forwardList },
			func(a *App) bool { return a.forwardList != nil },
		},
		{
			"search",
			func(a *App) tview.Primitive { a.searchList = tviewListForTest(); return a.searchList },
			func(a *App) bool { return a.searchList != nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newSuggestionTestApp()
			giveTestAppARoot(app)
			app.app.SetFocus(tc.setup(app))

			if got := app.capture(qKey()); got != nil {
				t.Fatal("q should be consumed")
			}
			if tc.open(app) {
				t.Fatal("the overlay should have been dismissed")
			}
		})
	}
}

// The one thing q must never do: get typed into a field and vanish.
func TestQIsTypedIntoTextInputs(t *testing.T) {
	app := newSuggestionTestApp()
	giveTestAppARoot(app)

	// Composer.
	app.app.SetFocus(app.composer)
	if got := app.capture(qKey()); got == nil {
		t.Fatal("q must reach the composer")
	}

	// A filter field, with an overlay open behind it — the tempting case to get wrong.
	app.forwardList = tviewListForTest()
	input := tview.NewInputField()
	app.forwardInput = input
	app.app.SetFocus(input)
	if got := app.capture(qKey()); got == nil {
		t.Fatal("q must reach the forward filter, not close the picker")
	}
	if app.forwardList == nil {
		t.Fatal("the picker must stay open while its filter has focus")
	}
}

// With nothing open, q keeps its original meaning.
func TestQStillQuitsWithNoOverlay(t *testing.T) {
	app := newSuggestionTestApp()
	giveTestAppARoot(app)
	app.messages.SetMessages(readTestMessages("chat:1", 10))
	app.app.SetFocus(app.messages)

	if app.dismissTopOverlay() {
		t.Fatal("nothing was open, so nothing should have been dismissed")
	}
}

// Esc and q share one chain precisely so they cannot disagree about what is on top.
func TestEscAndQDismissTheSameOverlay(t *testing.T) {
	for _, useQ := range []bool{false, true} {
		app := newSuggestionTestApp()
		giveTestAppARoot(app)
		app.pinnedList = tviewListForTest()
		app.app.SetFocus(app.pinnedList)

		if useQ {
			app.capture(qKey())
		} else {
			app.capture(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
		}
		if app.pinnedList != nil {
			t.Fatalf("useQ=%v: the pinned panel survived", useQ)
		}
	}
}

// A half-finished login is not something to lose to a stray keystroke.
func TestQDoesNotAbandonTheQRLogin(t *testing.T) {
	app := newSuggestionTestApp()
	giveTestAppARoot(app)
	app.qrPrompt = &telegram.AuthPrompt{Kind: telegram.AuthPromptQR}
	app.qrView = tviewTextViewForTest()

	if app.dismissTopOverlay() {
		t.Fatal("the QR prompt must not be dismissible by q")
	}
	if app.qrPrompt == nil {
		t.Fatal("the QR prompt should be untouched")
	}
}
