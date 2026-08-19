package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
)

// smallestFormHeightThatFits draws a real Form and reports the least height at which every button
// lands inside it.
//
// The point of measuring rather than reasoning: reading tview's layout arithmetic is what produced
// both previous wrong answers — first twenty-odd rows per action, then one row per wrap when a wrap
// costs two.
func smallestFormHeightThatFits(t *testing.T, labels []string, width int) int {
	t.Helper()
	for h := 1; h <= 40; h++ {
		form := tview.NewForm()
		form.SetHorizontal(true)
		form.SetBorder(true)
		for _, label := range labels {
			form.AddButton(label, nil)
		}
		screen := tcell.NewSimulationScreen("UTF-8")
		if err := screen.Init(); err != nil {
			t.Fatal(err)
		}
		form.SetRect(0, 0, width, h)
		form.Draw(screen)
		fits := true
		for i := range labels {
			_, y, w, bh := form.GetButton(i).GetRect()
			if w <= 0 || bh <= 0 || y < 0 || y >= h {
				fits = false
				break
			}
		}
		screen.Fini()
		if fits {
			return h
		}
	}
	return -1
}

func messageActionLabelsForLocale(t *testing.T, locale string) []string {
	t.Helper()
	i18n.SetLocale(locale)
	t.Cleanup(func() { i18n.SetLocale("en") })
	keys := []string{
		i18n.KeyActionReply, i18n.KeyActionDelete, i18n.KeyActionCopy,
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

// The guarantee that matters: the height reserved is never less than what a drawn Form needs, or
// buttons fall outside it and become invisible.
func TestMessageActionFormHeightMatchesRealForm(t *testing.T) {
	for _, locale := range []string{"en", "zh"} {
		labels := messageActionLabelsForLocale(t, locale)
		for _, width := range []int{60, 70, 80, 100, 120, 160, 200, 240} {
			reserved := messageActionFormHeight(labels, width-2) + 2
			need := smallestFormHeightThatFits(t, labels, width)
			if need < 0 {
				t.Fatalf("locale %s width %d: no height placed every button", locale, width)
			}
			if reserved < need {
				t.Errorf("locale %s width %d: reserved %d rows but the form needs %d — buttons would be hidden",
					locale, width, reserved, need)
			}
			// Over-reserving costs preview rows, so it should stay tight.
			if reserved > need+2 {
				t.Errorf("locale %s width %d: reserved %d rows for a form needing %d — too generous",
					locale, width, reserved, need)
			}
		}
	}
}

// Double-width labels wrap on terminals that look far too wide to need it, which is why display
// width is load-bearing rather than a nicety.
func TestMessageActionFormHeightCountsChineseLabelsAsDoubleWidth(t *testing.T) {
	zh := messageActionLabelsForLocale(t, "zh")
	i18n.SetLocale("en")
	en := []string{"Reply", "Delete", "Copy", "Open media", "Download media",
		"React", "Forward this message", "Mark for forwarding (v)", "Cancel"}

	if messageActionFormHeight(zh, 100) < messageActionFormHeight(en, 100) {
		t.Fatal("Chinese labels must not be measured as narrower than the English ones")
	}
}

// The default Form mode silently drops buttons that do not fit, which is why wrapping is enabled.
func TestFormDefaultModeDropsButtonsThatDoNotFit(t *testing.T) {
	labels := []string{"Reply", "Delete", "Copy", "Open media", "Download media",
		"React", "Forward this message", "Mark for forwarding (v)", "Cancel"}
	form := tview.NewForm() // deliberately NOT SetHorizontal(true)
	for _, label := range labels {
		form.AddButton(label, nil)
	}
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	form.SetRect(0, 0, 60, 20)
	form.Draw(screen)

	placed := 0
	for i := range labels {
		if _, _, w, _ := form.GetButton(i).GetRect(); w > 0 {
			placed++
		}
	}
	if placed == len(labels) {
		t.Skip("upstream no longer drops overflowing buttons; SetHorizontal(true) may be unnecessary")
	}
	if placed >= len(labels) {
		t.Fatal("unreachable")
	}
}
