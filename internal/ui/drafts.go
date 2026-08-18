package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const (
	// Long enough that ordinary typing does not produce a request per keystroke, short enough
	// that a quick switch away still lands.
	draftDebounce = 1200 * time.Millisecond
	// A steady typist never stops long enough to trip the debounce, so force a save after this
	// much unsaved editing.
	draftMaxLatency = 5 * time.Second
)

// scheduleDraftSave debounces a draft save for the current chat.
//
// The debounce lives here rather than in the client because the UI is where keystrokes are, but
// the write itself has to happen in the client: the UI never sees an accountID, and the drafts
// table is keyed by it.
func (a *App) scheduleDraftSave() {
	if a.suggest != nil && a.suggest.internalSet {
		// Programmatic edit (suggestion accepted, draft restored), not the user typing.
		return
	}
	if !a.draftablePeer(a.currentChat) {
		return
	}
	peerKey := a.currentChat
	if a.draftPeer != peerKey {
		a.draftPeer = peerKey
		a.draftFirstEditAt = time.Now()
	}
	if a.draftTimer != nil {
		a.draftTimer.Stop()
	}
	if time.Since(a.draftFirstEditAt) >= draftMaxLatency {
		a.flushDraft(peerKey)
		return
	}
	text := a.composer.GetText()
	replyToID := a.replyTargetID()
	a.draftTimer = time.AfterFunc(draftDebounce, func() {
		a.sendDraftCommand(peerKey, text, replyToID)
	})
}

// flushDraft saves immediately. Called when waiting is no longer an option: leaving the chat,
// leaving the composer, sending, or quitting.
func (a *App) flushDraft(peerKey string) {
	if !a.draftablePeer(peerKey) {
		return
	}
	if a.draftTimer != nil {
		a.draftTimer.Stop()
		a.draftTimer = nil
	}
	a.draftPeer = ""
	a.draftFirstEditAt = time.Time{}
	a.sendDraftCommand(peerKey, a.composer.GetText(), a.replyTargetID())
}

func (a *App) sendDraftCommand(peerKey, text string, replyToID int) {
	if a.commands == nil {
		return
	}
	select {
	case a.commands <- telegram.Command{
		Kind:      telegram.CommandSaveDraft,
		PeerKey:   peerKey,
		Text:      strings.TrimSpace(text),
		ReplyToID: replyToID,
	}:
	default:
		// Dropping a debounced draft save is survivable; the next edit reschedules one.
	}
}

// draftablePeer excludes the synthetic rows shown before a connection exists.
func (a *App) draftablePeer(peerKey string) bool {
	switch peerKey {
	case "", "welcome", "empty":
		return false
	default:
		return a.composer != nil
	}
}

func (a *App) replyTargetID() int {
	if a.replyTarget == nil {
		return 0
	}
	id, err := strconv.Atoi(a.replyTarget.ID)
	if err != nil {
		return 0
	}
	return id
}

// leaveChatForDraft flushes and clears the composer before the chat changes.
//
// Must run before a.currentChat is reassigned, or the draft is filed against the chat being
// opened instead of the one being left. Clearing is also the fix for text leaking across chats:
// the composer used to keep whatever was typed when you switched away.
func (a *App) leaveChatForDraft() {
	if a.currentChat != "" {
		a.flushDraft(a.currentChat)
		// Stop our own indicator on the peer we are abandoning, and forget theirs so coming
		// back does not briefly show a stale one.
		a.cancelTyping()
		a.clearTypingForPeer(a.currentChat)
	}
	if a.composer != nil && a.composer.GetText() != "" {
		a.setComposerText("")
		a.syncComposerLayout()
	}
}

// applyDraftEvent restores a stored draft into the composer.
//
// A remote change never overwrites text the user has already typed. Replacing characters under
// a live cursor is the one failure this feature cannot afford, so the update is stored (the
// client already did that) and the user is only told about it.
func (a *App) applyDraftEvent(event telegram.Event) {
	if a.composer == nil || event.PeerKey != a.currentChat {
		return
	}
	current := a.composer.GetText()
	if current != "" && current != event.DraftText {
		if event.DraftRemote {
			a.setStatusMsg(i18n.KeyStatusDraftUpdatedElsewhere)
		}
		return
	}
	if current == event.DraftText {
		return
	}
	a.setComposerText(event.DraftText)
	a.syncComposerLayout()
	if event.DraftReplyToID != 0 {
		want := strconv.Itoa(event.DraftReplyToID)
		for _, msg := range a.messages.Messages() {
			if msg.ID == want {
				a.setReplyTarget(msg)
				break
			}
		}
	}
	if event.DraftText != "" {
		a.setStatusMsg(i18n.KeyStatusDraftRestored)
	}
}
