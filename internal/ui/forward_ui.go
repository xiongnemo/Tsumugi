package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const (
	// forwardPickerRows caps how many destinations are listed at once. An account with thousands
	// of dialogs would otherwise build a list nobody scrolls; the filter is the way to reach the
	// rest.
	forwardPickerRows = 200
	// The picker is a full-width overlay, so rows have more room than the chat rail.
	forwardPickerTitleWidth = 60
)

// toggleForwardMark marks or unmarks the selected message.
func (a *App) toggleForwardMark() {
	marked, ok := a.messages.ToggleMark()
	if !ok {
		a.setStatusMsg(i18n.KeyStatusNoMessageSelected)
		return
	}
	if marked {
		a.setStatusMsg(i18n.KeyStatusMarkedForForward, a.messages.MarkedCount())
	} else {
		a.setStatusMsg(i18n.KeyStatusUnmarked, a.messages.MarkedCount())
	}
}

// clearForwardMarks drops the mark set. Esc does this before anything else, so a stray mark set
// cannot quietly follow the user around.
func (a *App) clearForwardMarks() bool {
	if a.messages.MarkedCount() == 0 {
		return false
	}
	a.messages.ClearMarks()
	a.setStatusMsg(i18n.KeyStatusMarksCleared)
	return true
}

// markedTitleHint shows how many messages are selected.
//
// Persistent rather than a transient status line, because a selection now survives scrolling and
// jumping: without a standing indicator the user could build a set, navigate away, and forget it
// was there. Graphical clients show the same thing as a permanent bar while a selection exists.
func (a *App) markedTitleHint() string {
	if n := a.messages.MarkedCount(); n > 0 {
		return i18n.Tf(i18n.KeyStatusMarkedCount, n)
	}
	return ""
}

// openForwardPicker asks where to forward the marked messages.
//
// dropAuthor is a second entry point (F) rather than a checkbox inside the picker, which keeps the
// picker a single-purpose list that Enter completes.
func (a *App) openForwardPicker(dropAuthor bool) {
	if a.messages.MarkedCount() == 0 {
		// Fall back to the message under the cursor, so forwarding one message needs no marking.
		if _, ok := a.messages.ToggleMark(); !ok {
			a.setStatusMsg(i18n.KeyStatusNothingMarked)
			return
		}
	}
	source := a.currentChat
	if !a.draftablePeer(source) {
		a.setStatusMsg(i18n.KeyStatusNoChatSelected)
		return
	}

	list := tview.NewList().ShowSecondaryText(true)
	input := tview.NewInputField().SetLabel(i18n.T(i18n.KeyForwardFilterLabel))
	hint := tview.NewTextView().SetDynamicColors(true).SetText("[gray]" + i18n.T(i18n.KeyForwardHint))

	a.forwardList = list
	a.forwardInput = input
	a.forwardSource = source
	a.forwardDropAuthor = dropAuthor

	input.SetChangedFunc(func(text string) { a.fillForwardList(text) })
	// Enter in the filter field commits the highlighted row, so a filter-then-Enter flow needs
	// no Tab into the list.
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.commitForwardPick()
		}
	})
	// Enter on the list and a double-click both arrive here. Without it the picker looks alive —
	// the highlight moves — but nothing can ever be chosen.
	list.SetSelectedFunc(func(int, string, string, rune) { a.commitForwardPick() })
	// Focus stays on the filter so typing always filters, which means the list's own movement
	// keys never reach it. Forwarding them is the deliberate custom focus arrangement AGENTS.md
	// allows; only keys with no meaning in a single-line field are taken, so Home/End/Left/Right
	// still edit the filter text.
	input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
			if handler := list.InputHandler(); handler != nil {
				handler(event, func(tview.Primitive) {})
			}
			return nil
		}
		return event
	})

	titleKey := i18n.KeyForwardTitle
	if dropAuthor {
		titleKey = i18n.KeyForwardTitleDropAuthor
	}
	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(input, 1, 0, true).
		AddItem(list, 0, 1, false).
		AddItem(hint, 1, 0, false)
	layout.SetBorder(true).SetTitle(" " + i18n.T(titleKey) + " ")
	// Esc is handled in App.capture, which runs first and would swallow it here anyway.

	a.fillForwardList("")
	a.app.SetRoot(layout, true)
	a.app.SetFocus(input)
}

func (a *App) closeForwardPicker() {
	a.forwardList = nil
	a.forwardInput = nil
	a.forwardSource = ""
	a.forwardDropAuthor = false
	a.restoreMessageFocus()
}

// fillForwardList rebuilds the destination rows for a filter string.
func (a *App) fillForwardList(filter string) {
	if a.forwardList == nil {
		return
	}
	a.forwardList.Clear()
	needle := strings.ToLower(strings.TrimSpace(filter))

	// Saved Messages is pinned first and targets a sentinel, so forwarding to yourself works
	// before any dialog sync has stored the self peer.
	if needle == "" || strings.Contains(strings.ToLower(i18n.T(i18n.KeyForwardSavedMessages)), needle) {
		a.forwardList.AddItem(i18n.T(i18n.KeyForwardSavedMessages), telegram.SavedMessagesTarget, 0, nil)
	}
	rows := 0
	for _, chat := range a.allChats {
		if chat.ID == a.forwardSource {
			continue
		}
		// Same predicate as applySearch: a second matching semantic for the same kind of
		// filtering would just be surprising.
		if needle != "" && !strings.Contains(strings.ToLower(chat.Title+" "+chat.Subtitle), needle) {
			continue
		}
		a.forwardList.AddItem(render.ChatRow(chat, forwardPickerTitleWidth), chat.ID, 0, nil)
		rows++
		if rows >= forwardPickerRows {
			break
		}
	}
}

// commitForwardPick sends the marked messages to the highlighted destination.
func (a *App) commitForwardPick() {
	if a.forwardList == nil || a.forwardList.GetItemCount() == 0 {
		return
	}
	_, target := a.forwardList.GetItemText(a.forwardList.GetCurrentItem())
	if target == "" {
		return
	}
	ids := a.messages.MarkedIDs()
	if len(ids) == 0 {
		a.setStatusMsg(i18n.KeyStatusNothingMarked)
		a.closeForwardPicker()
		return
	}
	a.commands <- telegram.Command{
		Kind:              telegram.CommandForwardMessages,
		PeerKey:           a.forwardSource,
		ForwardIDs:        ids,
		ForwardTarget:     target,
		ForwardDropAuthor: a.forwardDropAuthor,
	}
	// Clearing here rather than in the viewport: it is App that knows a forward was actually
	// requested, and openChat's two replaces must not be able to wipe a set being built.
	a.messages.ClearMarks()
	a.closeForwardPicker()
}
