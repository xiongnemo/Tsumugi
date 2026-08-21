package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// The reaction buttons sit in a row, so Left/Right is what a hand reaches for - and a tview Form
// moves on Tab and Backtab only, which is why the panel could previously only be walked with Tab.
func TestFormArrowRewriteHorizontal(t *testing.T) {
	cases := map[tcell.Key]tcell.Key{
		tcell.KeyLeft:  tcell.KeyBacktab,
		tcell.KeyRight: tcell.KeyTAB,
	}
	for from, want := range cases {
		if got := formArrowRewrite(keyEvent(from), true); got.Key() != want {
			t.Errorf("horizontal %v became %v, want %v", from, got.Key(), want)
		}
	}
	// The other axis keeps whatever meaning it had; rewriting both would break a panel that scrolls.
	for _, key := range []tcell.Key{tcell.KeyUp, tcell.KeyDown, tcell.KeyEnter, tcell.KeyRune} {
		event := keyEvent(key)
		if got := formArrowRewrite(event, true); got != event {
			t.Errorf("horizontal %v was rewritten to %v, want it untouched", key, got.Key())
		}
	}
}

func TestFormArrowRewriteVertical(t *testing.T) {
	if got := formArrowRewrite(keyEvent(tcell.KeyUp), false); got.Key() != tcell.KeyBacktab {
		t.Errorf("vertical Up became %v", got.Key())
	}
	if got := formArrowRewrite(keyEvent(tcell.KeyDown), false); got.Key() != tcell.KeyTAB {
		t.Errorf("vertical Down became %v", got.Key())
	}
	left := keyEvent(tcell.KeyLeft)
	if got := formArrowRewrite(left, false); got != left {
		t.Error("vertical mode rewrote Left, which a text field needs")
	}
	if formArrowRewrite(nil, true) != nil {
		t.Error("a nil event should stay nil")
	}
}
