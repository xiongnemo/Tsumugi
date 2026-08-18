package ui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const (
	// The composer grows with its content between these bounds. One row keeps the common
	// single-line case as compact as the old InputField; six stops a pasted essay from
	// squeezing the message pane out of existence.
	composerMinTextRows = 1
	composerMaxTextRows = 6
	composerBorderRows  = 2
	composeGhostRows    = 1
)

// composerSendKey reports whether a key event should send the message rather than reach the
// editor.
//
// Enter inserts a newline, which is what tview.TextArea does natively, so sending needs a
// modifier. No single modifier+Enter combination is deliverable everywhere:
//
//   - Ctrl+Enter arrives as KeyEnter+ModCtrl only when the terminal speaks an enhanced
//     keyboard protocol (tcell auto-enables modifyOtherKeys, kitty CSI-u and
//     win32-input-mode). A bare Linux VT speaks none of them and maps Ctrl+Return to plain
//     CR, indistinguishable from Enter.
//   - Alt+Enter arrives as KeyEnter+ModAlt on most Unix terminals, but Windows Terminal
//     swallows it for its own fullscreen toggle.
//   - Ctrl+J is the literal control byte 0x0A. Every terminal can emit it, tcell always
//     reports it as KeyCtrlJ, and it can never be confused with Enter, which is 13.
//
// So all three are accepted and Ctrl+J is the one documented as always working.
func composerSendKey(event *tcell.EventKey) bool {
	if event == nil {
		return false
	}
	switch event.Key() {
	case tcell.KeyCtrlJ: // == tcell.KeyLF (0x0A)
		return true
	case tcell.KeyEnter:
		return event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0
	default:
		return false
	}
}

// composerTextRows is composerTextRowsRaw clamped to the composer's growth bounds.
func composerTextRows(text string, innerWidth int) int {
	rows := composerTextRowsRaw(text, innerWidth)
	if rows < composerMinTextRows {
		rows = composerMinTextRows
	}
	if rows > composerMaxTextRows {
		rows = composerMaxTextRows
	}
	return rows
}

// composerTextRowsRaw is how many screen rows the text really needs, unclamped, so callers can
// tell whether it fits the box. innerWidth <= 0 means the primitive has no geometry yet, in
// which case only hard line breaks can be counted.
func composerTextRowsRaw(text string, innerWidth int) int {
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		if innerWidth <= 0 {
			rows++
			continue
		}
		// Display width, not rune count: CJK is double-width and would otherwise
		// under-count the rows a line needs.
		width := render.StringWidth(line)
		if width == 0 {
			rows++
			continue
		}
		rows += (width + innerWidth - 1) / innerWidth
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (a *App) composerBoxRows() int {
	if a.composer == nil {
		return composerMinTextRows + composerBorderRows
	}
	_, _, innerWidth, _ := a.composer.GetInnerRect()
	return composerTextRows(a.composer.GetText(), innerWidth) + composerBorderRows
}

func (a *App) composeStackRows() int {
	panelRows := 0
	if a.suggest != nil {
		panelRows = a.suggest.panelRows
	}
	return panelRows + a.composerBoxRows() + composeGhostRows
}

// syncComposerLayout is the single place that resizes the composer and the stack around it.
// The suggestion panel owns its own ResizeItem calls and then calls this.
func (a *App) syncComposerLayout() {
	if a.composeStack == nil || a.composer == nil {
		return
	}
	a.composeStack.ResizeItem(a.composer, a.composerBoxRows(), 0)
	if a.rightPane != nil {
		a.rightPane.ResizeItem(a.composeStack, a.composeStackRows(), 0)
	}
}

// clampComposerScroll undoes over-scrolling the editor did while the box was still smaller.
//
// TextArea keeps the cursor visible using the height from its *last* Draw, so any edit that
// adds rows scrolls against the old, smaller height: pressing Enter in a one-row box pushes
// rowOffset to 1, and pasting ten lines pushes it to 9. Growing the box afterwards does not
// undo that, because Draw only ever increases rowOffset to chase the cursor and never
// decreases it when there is room again. The visible effects were the first line vanishing
// after Enter, and a multi-line paste showing only its last line above blank rows.
//
// The rule is: never scroll further than needed to reach the bottom of the text. Text that
// fits pins to the top; text that overflows scrolls no further than filling the box.
//
// This must NOT be called from the changed callback. TextArea.replace defers t.changed(), so
// the callback fires while the editor is only half done — PasteHandler then calls
// findCursor(true, …), which re-derives rowOffset from the stale height and overwrites
// anything we set. It runs from Application.SetBeforeDrawFunc instead, which is after all
// event handling and before the frame renders.
//
// The new inner height is computed rather than read from GetInnerRect, because the Flex has not
// applied the resize yet at that point.
func (a *App) clampComposerScroll() {
	if a.composer == nil {
		return
	}
	_, _, innerWidth, _ := a.composer.GetInnerRect()
	text := a.composer.GetText()
	innerHeight := composerTextRows(text, innerWidth)
	maxOffset := composerTextRowsRaw(text, innerWidth) - innerHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if row, _ := a.composer.GetOffset(); row > maxOffset {
		a.composer.SetOffset(maxOffset, 0)
	}
}

func (a *App) composerHasFocus() bool {
	return a.app != nil && a.composer != nil && a.app.GetFocus() == a.composer
}

// isTextInputFocus reports whether the focused primitive is a text editor that owns its own
// keys. Overlay forms hand focus to the field itself rather than the Form, so this check is
// still needed alongside the explicit composer identity test.
func isTextInputFocus(focus tview.Primitive) bool {
	switch focus.(type) {
	case *tview.InputField, *tview.TextArea:
		return true
	default:
		return false
	}
}

// composerCursor is the cursor's byte offset into the composer text. With no selection
// TextArea reports start == end == cursor; with one, the end is the live edge.
func (a *App) composerCursor() int {
	if a.composer == nil {
		return 0
	}
	_, _, end := a.composer.GetSelection()
	return end
}

// replaceComposerRange splices text by byte offsets, leaving the cursor after the insert.
// Used for suggestion acceptance, where a full SetText would move the cursor to the end of a
// multi-line buffer.
func (a *App) replaceComposerRange(start, end int, insert string) {
	if a.composer == nil {
		return
	}
	if a.suggest != nil {
		a.suggest.internalSet = true
		defer func() { a.suggest.internalSet = false }()
	}
	a.composer.Replace(start, end, insert)
}

func (a *App) setComposerText(text string) {
	if a.composer == nil {
		return
	}
	if a.suggest != nil {
		a.suggest.internalSet = true
		defer func() { a.suggest.internalSet = false }()
	}
	a.composer.SetText(text, true)
}

// submitComposer sends whatever is in the composer. This was TextArea's predecessor's
// SetDoneFunc; TextArea has no such hook and consumes Enter itself, so the trigger lives in
// App.capture now.
func (a *App) submitComposer() {
	if a.composer == nil {
		return
	}
	rawText := a.composer.GetText()
	text := strings.TrimSpace(rawText)
	if text != "" {
		replyToID := 0
		if a.replyTarget != nil {
			replyToID, _ = strconv.Atoi(a.replyTarget.ID)
		}
		entities := a.takeMentionEntitiesForSend(rawText, text)
		a.commands <- telegram.Command{
			Kind:            telegram.CommandSendText,
			PeerKey:         a.currentChat,
			Text:            text,
			ReplyToID:       replyToID,
			MentionEntities: entities,
		}
		// Cancel any pending draft save: the text is on its way out and the client clears the
		// server-side draft once the send is confirmed.
		if a.draftTimer != nil {
			a.draftTimer.Stop()
			a.draftTimer = nil
		}
		a.draftPeer = ""
		a.setComposerText("")
		a.closeComposeSuggestions()
		a.clearReplyTarget()
		a.setStatusMsg(i18n.KeyStatusSending)
	}
	a.syncComposerLayout()
	a.updateFocusStyle()
}
