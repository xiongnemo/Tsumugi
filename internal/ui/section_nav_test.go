package ui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"

	"github.com/nemo/Tsumugi/internal/i18n"
)

// pressInSettings runs one key through the real capture and then, if the capture passed it on,
// delivers it to the focused primitive the way the Application would.
//
// Going through the whole path matters here: the fix works by rewriting Up and Down into Backtab and
// Tab, so a test that only inspected the returned event would prove nothing about where the focus
// actually lands.
func pressInSettings(app *App, key tcell.Key) {
	event := keyEvent(key)
	out := app.capture(event)
	if out == nil {
		return
	}
	if handler := app.app.GetFocus().InputHandler(); handler != nil {
		handler(out, func(p tview.Primitive) { app.app.SetFocus(p) })
	}
}

// focusName describes where the focus is, in terms a failure message can be read from.
func focusName(app *App, overlay *settingsOverlay) string {
	focus := app.app.GetFocus()
	if focus == overlay.list {
		return "category list"
	}
	if overlay.form != nil {
		if item, button := overlay.form.GetFocusedItemIndex(); item >= 0 {
			return fmt.Sprintf("item[%d]", item)
		} else if button >= 0 {
			return fmt.Sprintf("button[%d]", button)
		}
	}
	return fmt.Sprintf("%T", focus)
}

func navTestApp(t *testing.T, build func(*App, *settingsOverlay) *tview.Form) (*App, *settingsOverlay) {
	t.Helper()
	app, overlay := storageTestApp(t)
	overlay.form = build(app, overlay)
	overlay.content.AddItem(overlay.form, 0, 1, true)
	app.populateSettingsList(overlay)
	app.root = tview.NewFlex()
	app.app.SetFocus(overlay.list)
	return app, overlay
}

func storageSection(app *App, overlay *settingsOverlay) *tview.Form {
	return app.settingsStorageForm(overlay)
}

// The reported bug: the right-hand pane could only be walked with Tab, because tview's Form ignores
// the arrow keys entirely. Down has to reach the next field and the buttons after it.
func TestArrowsWalkTheSectionForm(t *testing.T) {
	app, overlay := navTestApp(t, storageSection)

	pressInSettings(app, tcell.KeyRight)
	if got := focusName(app, overlay); got != "item[0]" {
		t.Fatalf("Right from the list focused %s, want item[0]", got)
	}
	// item[2] is the database-size text view, which cannot take focus, so Down skips it: the whole
	// reason movement is delegated to tview rather than counted here.
	want := []string{"item[1]", "button[0]", "button[1]", "button[2]"}
	for _, expect := range want {
		pressInSettings(app, tcell.KeyDown)
		if got := focusName(app, overlay); got != expect {
			t.Fatalf("Down focused %s, want %s", got, expect)
		}
	}
	for i := len(want) - 2; i >= 0; i-- {
		pressInSettings(app, tcell.KeyUp)
		if got := focusName(app, overlay); got != want[i] {
			t.Fatalf("Up focused %s, want %s", got, want[i])
		}
	}
}

// The other half of the same bug: Tab wraps around inside the form forever, so there was no keyboard
// route back to the category rail at all. Esc was the only exit and it closes the whole panel.
func TestArrowsLeaveTheSectionForm(t *testing.T) {
	app, overlay := navTestApp(t, storageSection)

	pressInSettings(app, tcell.KeyRight)
	pressInSettings(app, tcell.KeyUp)
	if got := focusName(app, overlay); got != "category list" {
		t.Fatalf("Up from the first field focused %s, want the category list", got)
	}
	if app.settingsOverlay == nil {
		t.Fatal("stepping back to the list closed the panel")
	}

	// And from a button, where Left carries no other meaning.
	pressInSettings(app, tcell.KeyRight)
	pressInSettings(app, tcell.KeyDown)
	pressInSettings(app, tcell.KeyDown)
	if got := focusName(app, overlay); got != "button[0]" {
		t.Fatalf("focus is %s, want button[0] before testing Left", got)
	}
	pressInSettings(app, tcell.KeyLeft)
	if got := focusName(app, overlay); got != "category list" {
		t.Fatalf("Left from a button focused %s, want the category list", got)
	}
}

// Left inside a text field moves the cursor. Stealing it for pane switching would make a proxy URL
// uneditable, which is why only the non-text items treat it as "go back".
func TestLeftStaysInsideATextField(t *testing.T) {
	app, overlay := navTestApp(t, storageSection)

	pressInSettings(app, tcell.KeyRight)
	if got := focusName(app, overlay); got != "item[0]" {
		t.Fatalf("focus is %s, want the first field", got)
	}
	pressInSettings(app, tcell.KeyLeft)
	if got := focusName(app, overlay); got != "item[0]" {
		t.Fatalf("Left in a text field focused %s, want to stay in the field", got)
	}
}

// A dropdown opens and picks options with Up and Down. Rewriting them into form movement would leave
// Enter as the only way to open it and no way to choose anything.
func TestDropDownKeepsTheArrowKeys(t *testing.T) {
	app, overlay := navTestApp(t, func(app *App, overlay *settingsOverlay) *tview.Form {
		return app.settingsGeneralForm(overlay)
	})
	dropdown, ok := overlay.form.GetFormItem(0).(*tview.DropDown)
	if !ok {
		t.Fatalf("General item 0 is %T, want the language dropdown", overlay.form.GetFormItem(0))
	}

	pressInSettings(app, tcell.KeyRight)
	pressInSettings(app, tcell.KeyDown)
	if !dropdown.IsOpen() {
		t.Fatal("Down on the language dropdown did not open it")
	}

	// Esc belongs to the open list, not to the panel: closing the panel out from under it would be
	// two surprises at once.
	pressInSettings(app, tcell.KeyEsc)
	if dropdown.IsOpen() {
		t.Fatal("Esc left the dropdown open")
	}
	if app.settingsOverlay == nil {
		t.Fatal("Esc closed the settings panel instead of the dropdown list")
	}
	// With nothing open, Esc means what it always means.
	pressInSettings(app, tcell.KeyEsc)
	if app.settingsOverlay != nil {
		t.Fatal("Esc did not close the panel once the dropdown was closed")
	}
}

// q in a settings screen used to quit Tsumugi outright. Losing the session to a habitual keystroke is
// the trade the global q rule already rejected everywhere else.
func TestQuitKeyClosesTheSettingsPanel(t *testing.T) {
	app, overlay := navTestApp(t, storageSection)

	if got := app.capture(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)); got != nil {
		t.Fatalf("q returned %v, want it consumed", got)
	}
	if app.settingsOverlay != nil {
		t.Fatal("q did not close the settings panel")
	}
	_ = overlay
}

// The proxy panel is the same shape one keystroke away and had none of this. It was worse off than the
// settings panel: tview's List reads Tab as "next item", so Tab in the profile list only moved the
// selection and the form could not be reached from the keyboard at all.
func TestProxyPanelSharesTheSectionNavigation(t *testing.T) {
	app, _ := storageTestApp(t)
	app.settingsOverlay = nil
	app.root = tview.NewFlex().AddItem(app.messages, 0, 1, true)

	list := tview.NewList().ShowSecondaryText(true)
	list.AddItem("No proxy", "direct", 0, nil)
	form := tview.NewForm().
		AddInputField(i18n.T(i18n.KeyProxyNameLabel), "", 20, nil, nil).
		AddButton(i18n.T(i18n.KeyProxySave), nil).
		AddButton(i18n.T(i18n.KeyProxyClose), nil)
	overlay := &settingsOverlay{root: tview.NewFlex(), list: list, form: form}
	app.proxyOverlay = overlay
	app.app.SetFocus(list)

	pressInSettings(app, tcell.KeyTAB)
	if got := focusName(app, overlay); got != "item[0]" {
		t.Fatalf("Tab from the proxy list focused %s, want the first field", got)
	}
	if app.app.GetFocus() == app.chats || app.app.GetFocus() == app.folders {
		t.Fatal("Tab moved the focus into the main shell, which is not on screen")
	}
	pressInSettings(app, tcell.KeyDown)
	if got := focusName(app, overlay); got != "button[0]" {
		t.Fatalf("Down focused %s, want the Save button", got)
	}
	pressInSettings(app, tcell.KeyLeft)
	if got := focusName(app, overlay); got != "category list" {
		t.Fatalf("Left focused %s, want the proxy list", got)
	}
	pressInSettings(app, tcell.KeyEsc)
	if app.proxyOverlay != nil {
		t.Fatal("Esc did not close the proxy panel")
	}
}

// The hint is the only place these keys are written down, and its box is a single no-wrap row. Text
// that overflows is silently cut, which is how the status bar and the action bar both shipped
// invisible; 80 columns is the narrowest terminal Tsumugi targets.
func TestSettingsHintFitsItsBox(t *testing.T) {
	const narrowestTerminal = 80
	avail := narrowestTerminal - settingsCategoryWidth - 2 // the status box borders
	for _, locale := range []string{"en", "zh"} {
		i18n.SetLocale(locale)
		hint := i18n.T(i18n.KeySettingsHint)
		if width := uniseg.StringWidth(hint); width > avail {
			t.Errorf("locale %s: hint is %d cells wide, only %d fit: %q", locale, width, avail, hint)
		}
	}
	i18n.SetLocale("en")
}
