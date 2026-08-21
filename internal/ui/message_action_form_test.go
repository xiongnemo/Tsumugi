package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
)

// drawnActionBar renders a bordered action bar and reports how many of its rows actually contain
// glyphs, plus how many rows of buttons tview placed.
//
// screen.Show() is essential: GetContents reads the front buffer, and without it every measurement
// reports an empty screen — which is how an earlier attempt "proved" that no Form configuration
// draws anything at all.
func drawnActionBar(t *testing.T, labels []string, width, height int) (rowsWithGlyphs, buttonRows int) {
	t.Helper()
	form := tview.NewForm()
	form.SetHorizontal(true)
	form.SetBorder(true).SetTitle(" x ")
	for _, label := range labels {
		form.AddButton(label, nil)
	}
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(width, height+4)
	form.SetRect(0, 0, width, height)
	form.Draw(screen)
	screen.Show()

	cells, w, h := screen.GetContents()
	for y := 1; y < height-1 && y < h; y++ {
		for x := 1; x < w-1; x++ {
			r := cells[y*w+x].Runes
			if len(r) > 0 && r[0] != 0 && r[0] != ' ' {
				rowsWithGlyphs++
				break
			}
		}
	}

	lo, hi := 1<<30, -1
	for i := range labels {
		_, y, bw, _ := form.GetButton(i).GetRect()
		if bw <= 0 {
			continue
		}
		if y < lo {
			lo = y
		}
		if y > hi {
			hi = y
		}
	}
	if hi >= 0 {
		buttonRows = (hi-lo)/formWrapRows + 1
	}
	return rowsWithGlyphs, buttonRows
}

func actionLabels(t *testing.T, locale string) []string {
	t.Helper()
	i18n.SetLocale(locale)
	t.Cleanup(func() { i18n.SetLocale("en") })
	// Every label the menu can show at once. Extended as actions are added, because the height
	// model is measured against this set rather than trusted: the action bar shipped as an empty box
	// twice by being modelled instead of drawn.
	keys := []string{
		i18n.KeyActionReply, i18n.KeyActionDelete, i18n.KeyActionCopy,
		i18n.KeyActionEdit, i18n.KeyActionVote, i18n.KeyActionPin, i18n.KeyActionUnpin,
		i18n.KeyActionOpenMedia, i18n.KeyActionDownloadMedia,
		i18n.KeyActionReact, i18n.KeyActionForward, i18n.KeyActionMark,
		i18n.KeyActionCancel,
	}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, i18n.T(key))
	}
	return out
}

// The guarantee: at the height reserved, every row of buttons tview placed actually renders. Too
// short and the bar draws as an empty box, which is what happened twice.
func TestMessageActionFormHeightMatchesRealForm(t *testing.T) {
	for _, locale := range []string{"en", "zh"} {
		labels := actionLabels(t, locale)
		for _, width := range []int{60, 70, 80, 100, 120, 140, 200, 240} {
			reserved := messageActionFormHeight(labels, width)
			rowsWithGlyphs, buttonRows := drawnActionBar(t, labels, width, reserved)
			if buttonRows == 0 {
				t.Fatalf("locale %s width %d: tview placed no buttons at all", locale, width)
			}
			if rowsWithGlyphs < buttonRows {
				t.Errorf("locale %s width %d: reserved %d rows, tview placed %d button rows but only %d rendered",
					locale, width, reserved, buttonRows, rowsWithGlyphs)
			}
		}
	}
}

// The two constants the height rests on, re-derived from a drawn Form so an upstream change is a
// test failure rather than an invisible action bar.
func TestFormGeometryConstantsStillHold(t *testing.T) {
	single := []string{"A", "B"}
	if _, rows := drawnActionBar(t, single, 200, formBorderedBaseHeight); rows != 1 {
		t.Fatalf("two short buttons wrapped at 200 columns; the wrap model is off")
	}
	for h := 1; h < formBorderedBaseHeight; h++ {
		if glyphs, _ := drawnActionBar(t, single, 200, h); glyphs > 0 {
			t.Fatalf("a bordered form rendered buttons at height %d; formBorderedBaseHeight of %d is now too generous",
				h, formBorderedBaseHeight)
		}
	}
	if glyphs, _ := drawnActionBar(t, single, 200, formBorderedBaseHeight); glyphs == 0 {
		t.Fatalf("a bordered form rendered nothing at height %d; formBorderedBaseHeight is too small",
			formBorderedBaseHeight)
	}
}

// Double-width labels wrap on terminals that look far too wide to need it, which is why display
// width is load-bearing rather than a nicety — the interface being in Chinese is what exposed it.
func TestMessageActionFormHeightCountsChineseLabelsAsDoubleWidth(t *testing.T) {
	zh := actionLabels(t, "zh")
	i18n.SetLocale("en")
	en := []string{"Reply", "Delete", "Copy", "Open media", "Download media",
		"React", "Forward this message", "Mark for forwarding (v)", "Cancel"}

	if messageActionFormHeight(zh, 100) < messageActionFormHeight(en, 100) {
		t.Fatal("Chinese labels must not be measured as narrower than the English ones")
	}
}

// The default Form mode silently drops buttons that do not fit, which is why wrapping is enabled.
func TestFormDefaultModeDropsButtonsThatDoNotFit(t *testing.T) {
	labels := actionLabels(t, "en")
	form := tview.NewForm() // deliberately NOT SetHorizontal(true)
	for _, label := range labels {
		form.AddButton(label, nil)
	}
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(60, 20)
	form.SetRect(0, 0, 60, 20)
	form.Draw(screen)
	screen.Show()

	placed := 0
	for i := range labels {
		if _, _, w, _ := form.GetButton(i).GetRect(); w > 0 {
			placed++
		}
	}
	if placed >= len(labels) {
		t.Skip("upstream no longer drops overflowing buttons; SetHorizontal(true) may be unnecessary")
	}
}
