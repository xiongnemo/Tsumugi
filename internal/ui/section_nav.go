package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// A "section pane" is the category-list-plus-form layout used by the settings overlay and the proxy
// panel. Both are described by settingsOverlay and both route their keys through here.
//
// tview's Form moves between its items on Tab, Enter and Backtab only — nothing in it looks at the
// arrow keys. So the right-hand pane could be entered but not walked: every field and button needed
// a Tab, the arrows did nothing at all, and because Tab wraps around inside the form there was no
// keyboard route back to the category list. Esc was the only way out, and it closes the whole panel.

// sectionArrowNavigation makes the arrow keys move around the section form.
//
// Up and Down are rewritten to Backtab and Tab rather than moving focus here, so tview stays the
// single authority on which item comes next: it skips the labels and non-scrollable text views that
// cannot take focus, and it knows about disabled buttons. Returning a rewritten event works because
// the Application delivers whatever the input capture returns.
func (a *App) sectionArrowNavigation(overlay *settingsOverlay, event *tcell.EventKey) (*tcell.EventKey, bool) {
	if overlay == nil || overlay.form == nil || overlay.list == nil {
		return event, false
	}
	item, button := overlay.form.GetFocusedItemIndex()
	if item < 0 && button < 0 {
		// The form does not hold the focus, so this is the category list's own navigation.
		return event, false
	}
	focused := sectionFocusedItem(overlay.form, item)
	if dropdown, ok := focused.(*tview.DropDown); ok {
		// A dropdown opens and picks with Up and Down. Taking them away would leave Enter as the
		// only way to open it and no way at all to choose an option.
		_ = dropdown
		return event, false
	}

	switch event.Key() {
	case tcell.KeyUp:
		if item == 0 {
			// Walking off the top of the form returns to the category rail. Without this the only
			// exit is Esc, which closes the panel instead of stepping back out of it.
			a.app.SetFocus(overlay.list)
			return nil, true
		}
		return tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone), true
	case tcell.KeyDown:
		return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), true
	case tcell.KeyLeft:
		if _, editing := focused.(*tview.InputField); editing {
			// Left is a cursor move inside a text field. A proxy URL cannot be edited otherwise,
			// so pane switching yields here — Up out of the first field, or Down to a button,
			// still leaves the form.
			return event, false
		}
		a.app.SetFocus(overlay.list)
		return nil, true
	}
	return event, false
}

// sectionFocusedItem is the focused form item, or nil when a button has the focus.
func sectionFocusedItem(form *tview.Form, item int) tview.FormItem {
	if item < 0 || item >= form.GetFormItemCount() {
		return nil
	}
	return form.GetFormItem(item)
}

// sectionDropDownOpen reports the open dropdown in a section form, if one is open.
//
// Needed for Esc: an open dropdown closes on Esc through its own capture, and the panel capture runs
// first, so without this check Esc would tear down the whole panel from under an open list.
func sectionDropDownOpen(overlay *settingsOverlay) *tview.DropDown {
	if overlay == nil || overlay.form == nil {
		return nil
	}
	for i := 0; i < overlay.form.GetFormItemCount(); i++ {
		if dropdown, ok := overlay.form.GetFormItem(i).(*tview.DropDown); ok && dropdown.IsOpen() {
			return dropdown
		}
	}
	return nil
}

// sectionEnterForm moves the focus from the category list into the section form.
func (a *App) sectionEnterForm(overlay *settingsOverlay, focus tview.Primitive) bool {
	if overlay == nil || overlay.form == nil || focus != overlay.list {
		return false
	}
	a.app.SetFocus(overlay.form)
	return true
}
