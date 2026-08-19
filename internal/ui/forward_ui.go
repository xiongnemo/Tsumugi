package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// forwardTarget is one picker row destination.
type forwardTarget struct {
	Key   string
	Title string
}

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
	// The key line changes meaning with the mark set, so it has to be redrawn here.
	a.refreshFooter()
	a.applyMessagesPaneTitle()
}

// clearForwardMarks drops the mark set. Esc does this before anything else, so a stray mark set
// cannot quietly follow the user around.
func (a *App) clearForwardMarks() bool {
	if a.messages.MarkedCount() == 0 {
		return false
	}
	a.messages.ClearMarks()
	a.setStatusMsg(i18n.KeyStatusMarksCleared)
	a.refreshFooter()
	a.applyMessagesPaneTitle()
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
//
// Saved Messages is pinned first only while the filter is empty, or when the filter is aiming at
// it. Pinning it unconditionally meant a filter typed at some other chat could leave Saved
// Messages as row 0 and therefore highlighted, so Enter forwarded there instead, and nothing on
// screen said where the messages had gone.
func (a *App) fillForwardList(filter string) {
	if a.forwardList == nil {
		return
	}
	a.forwardList.Clear()
	a.forwardTargets = a.forwardTargets[:0]
	needle := strings.ToLower(strings.TrimSpace(filter))

	saved := i18n.T(i18n.KeyForwardSavedMessages)
	if needle == "" {
		// The convenience default, and it needs no stored peer, so it leads when nothing has
		// been typed.
		a.addForwardRow(saved, telegram.SavedMessagesTarget, saved)
		a.appendForwardChats(needle, false)
		return
	}
	// With a filter, real chats outrank Saved Messages unconditionally. Ranking cannot be made
	// unambiguous — "sa" is a legitimate prefix of both "Saved Messages" and a chat called
	// "Saved team chat" — so the tie is broken by intent: someone who typed a filter is looking
	// for a conversation, not for the one destination that is always one keystroke away.
	// Prefix matches lead, because typing a name means that name.
	a.appendForwardChats(needle, true)
	a.appendForwardChats(needle, false)
	if strings.Contains(strings.ToLower(saved), needle) {
		a.addForwardRow(saved, telegram.SavedMessagesTarget, saved)
	}
}

// appendForwardChats adds matching chats, either the prefix matches or the rest.
func (a *App) appendForwardChats(needle string, prefixOnly bool) {
	for _, chat := range a.allChats {
		if chat.ID == a.forwardSource {
			continue
		}
		if len(a.forwardTargets) >= forwardPickerRows {
			return
		}
		if needle != "" {
			if !strings.Contains(strings.ToLower(chat.Title+" "+chat.Subtitle), needle) {
				continue
			}
			if strings.HasPrefix(strings.ToLower(chat.Title), needle) != prefixOnly {
				continue
			}
		}
		a.addForwardRow(render.ChatRow(chat, forwardPickerTitleWidth), chat.ID, chat.Title)
	}
}

// addForwardRow keeps the visible row and its destination in step.
//
// The destination and its display name live alongside the list rather than in the row secondary
// text, which both leaked internal peer keys like "channel:600" into the UI and left no way to
// name the destination back to the user.
func (a *App) addForwardRow(label, target, title string) {
	a.forwardList.AddItem(label, "", 0, nil)
	a.forwardTargets = append(a.forwardTargets, forwardTarget{Key: target, Title: title})
}

// commitForwardPick sends the marked messages to the highlighted destination.
func (a *App) commitForwardPick() {
	if a.forwardList == nil {
		return
	}
	index := a.forwardList.GetCurrentItem()
	if index < 0 || index >= len(a.forwardTargets) {
		// Previously a silent return, which is indistinguishable from a dead key: a filter that
		// matched nothing looked exactly like Enter not working.
		a.setStatusMsg(i18n.KeyStatusForwardNoTarget)
		return
	}
	target := a.forwardTargets[index]
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
		ForwardTarget:     target.Key,
		ForwardDropAuthor: a.forwardDropAuthor,
	}
	// Naming the destination is the point. The old status reported only a count, so a forward
	// that landed somewhere unintended was indistinguishable from one that worked.
	a.setStatusMsg(i18n.KeyStatusForwardingTo, len(ids), target.Title)
	// Clearing here rather than in the viewport: it is App that knows a forward was actually
	// requested, and openChat's two replaces must not be able to wipe a set being built.
	a.messages.ClearMarks()
	a.refreshFooter()
	a.closeForwardPicker()
}
