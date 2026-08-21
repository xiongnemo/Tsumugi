package ui

import (
	"strconv"
	"strings"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// beginEdit loads a sent message back into the composer to be rewritten.
//
// Reusing the composer rather than opening a form: the text may be long and multi-line, and the
// composer is the only editor here that grows, wraps by display width and accepts a paste as one
// insert. It also means the send key is the same key, which is what a user reaches for.
func (a *App) beginEdit(msg telegram.Message) {
	if !telegram.EditableMessage(msg) {
		a.setStatusMsg(i18n.KeyStatusEditNotAllowed)
		return
	}
	// Editing text and sending a file are different operations on different objects, and Telegram's
	// edit does not carry media. Clearing rather than refusing keeps one rule instead of two.
	if a.clearAttachment() {
		a.setStatusMsg(i18n.KeyStatusAttachmentCleared)
	}
	// The draft belongs to the composer's normal contents, and the composer is about to hold
	// something else entirely. Flushing first is what keeps the draft from being overwritten by the
	// message being edited.
	a.flushDraft(a.currentChat)

	target := msg
	a.editTarget = &target
	a.setComposerText(msg.Text)
	a.applyComposerTitle()
	a.syncComposerLayout()
	a.app.SetFocus(a.composer)
	a.updateFocusStyle()
	a.setStatusMsg(i18n.KeyStatusEditing)
}

// cancelEdit leaves edit mode, emptying the composer of the message being rewritten.
//
// The composer text is dropped rather than kept: it is a copy of a message that still exists, and
// leaving it behind would make the next send post a duplicate.
func (a *App) cancelEdit() bool {
	if a.editTarget == nil {
		return false
	}
	a.editTarget = nil
	a.setComposerText("")
	a.applyComposerTitle()
	a.syncComposerLayout()
	a.setStatusMsg(i18n.KeyStatusEditCancelled)
	return true
}

// submitEdit sends the rewritten text. Reports whether it handled the submission.
func (a *App) submitEdit(text string) bool {
	if a.editTarget == nil {
		return false
	}
	target := *a.editTarget
	a.editTarget = nil
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		// An empty edit is a delete in Telegram's model, which is a different action with a
		// different confirmation. Refusing here rather than in the backend keeps the composer's
		// contents so nothing is lost.
		a.editTarget = &target
		a.setStatusMsg(i18n.KeyStatusEditEmpty)
		return true
	}
	if trimmed == strings.TrimSpace(target.Text) {
		// Telegram answers MESSAGE_NOT_MODIFIED, which is a red error line for something that is
		// really just "nothing to do".
		a.setComposerText("")
		a.applyComposerTitle()
		a.syncComposerLayout()
		a.setStatusMsg(i18n.KeyStatusEditCancelled)
		return true
	}
	messageID, err := strconv.Atoi(target.ID)
	if err != nil || messageID <= 0 {
		a.setStatusMsg(i18n.KeyStatusEditNotSynced)
		return true
	}
	a.commands <- telegram.Command{
		Kind:            telegram.CommandEditMessage,
		PeerKey:         target.ChatID,
		MessageID:       messageID,
		Text:            trimmed,
		MentionEntities: a.takeMentionEntitiesForSend(text, trimmed),
	}
	a.setComposerText("")
	a.applyComposerTitle()
	a.syncComposerLayout()
	a.closeComposeSuggestions()
	a.cancelTyping()
	a.setStatusMsg(i18n.KeyStatusEditing)
	return true
}

// editTitle is the composer border while a message is being rewritten.
func (a *App) editTitle() string {
	if a.editTarget == nil {
		return ""
	}
	return i18n.Tf(i18n.KeyUIEditing, render.Truncate(strings.TrimSpace(a.editTarget.Text), 32))
}
