package telegram

import (
	"context"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// typingRefreshInterval is how often an ongoing "typing" is re-sent. Telegram expires the
// indicator after about six seconds, so this keeps it alive with room to spare while still
// collapsing a burst of keystrokes into one request.
const typingRefreshInterval = 4 * time.Second

// typingActionKey maps a Telegram action to a translation key.
//
// One key per verb rather than composing "X is" + "typing": that concatenation does not
// survive translation into Chinese. Unrecognised actions degrade to a generic "active" rather
// than being dropped, so a new Telegram action type shows something sensible.
func typingActionKey(action tg.SendMessageActionClass) (key string, active bool) {
	switch action.(type) {
	case nil, *tg.SendMessageCancelAction:
		return "", false
	case *tg.SendMessageTypingAction:
		return i18n.KeyTypingIsTyping, true
	case *tg.SendMessageRecordAudioAction, *tg.SendMessageRecordRoundAction:
		return i18n.KeyTypingIsRecordingVoice, true
	case *tg.SendMessageUploadPhotoAction, *tg.SendMessageUploadVideoAction,
		*tg.SendMessageUploadDocumentAction, *tg.SendMessageUploadAudioAction,
		*tg.SendMessageUploadRoundAction:
		return i18n.KeyTypingIsUploading, true
	default:
		return i18n.KeyTypingIsActive, true
	}
}

// setTyping tells Telegram we are composing, throttled per peer.
//
// Deliberately not retried on flood wait: a typing notice is worthless by the time a wait
// elapses, and failures are swallowed rather than surfaced — a missing indicator is not worth a
// status line. There is also no keep-alive ticker; keystrokes provide the refresh, and if the
// user stops typing the receiver's indicator expiring on its own is the correct outcome.
func (c *GotdClient) setTyping(ctx context.Context, accountID string, api *tg.Client, peerKey string, typing bool) {
	if api == nil || c.store == nil || peerKey == "" {
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		return
	}
	if !peerSupportsTyping(p) {
		return
	}
	if !c.claimTypingSlot(peerKey, typing) {
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		return
	}
	var action tg.SendMessageActionClass = &tg.SendMessageTypingAction{}
	if !typing {
		action = &tg.SendMessageCancelAction{}
	}
	send := c.typingSend
	if send == nil {
		send = api.MessagesSetTyping
	}
	_, _ = send(ctx, &tg.MessagesSetTypingRequest{Peer: input, Action: action})
}

// claimTypingSlot reports whether this notification should actually go out. Cancels always do;
// repeats within the refresh interval do not.
func (c *GotdClient) claimTypingSlot(peerKey string, typing bool) bool {
	c.typingMu.Lock()
	defer c.typingMu.Unlock()
	if c.typingSentAt == nil {
		c.typingSentAt = make(map[string]time.Time)
	}
	if !typing {
		delete(c.typingSentAt, peerKey)
		return true
	}
	if last, ok := c.typingSentAt[peerKey]; ok && time.Since(last) < typingRefreshInterval {
		return false
	}
	c.typingSentAt[peerKey] = time.Now()
	return true
}

// peerSupportsTyping excludes Saved Messages, where there is nobody to notify.
func peerSupportsTyping(p storage.Peer) bool {
	return p.Kind != "self"
}

// emitTyping forwards a peer's typing state to the UI.
//
// Gated on the focused peer: typing updates arrive for every dialog, and un-gated they would
// mean a QueueUpdateDraw storm on a busy account. The chat-list indicator that graphical
// clients also show would need the un-gated path, and is deferred.
func (c *GotdClient) emitTyping(ctx context.Context, accountID string, events chan<- Event, peerKey string, from tg.PeerClass, entities entitiesByID, action tg.SendMessageActionClass) {
	if !c.isFocusedPeer(peerKey) {
		return
	}
	key, active := typingActionKey(action)
	name := senderDisplayName(from, entities)
	if name == "" && c.store != nil {
		if p, ok, err := c.store.Peer(ctx, accountID, peerKey); err == nil && ok {
			name = p.Title
		}
	}
	c.sendFocusedEvent(ctx, events, peerKey, Event{
		Kind:            EventTyping,
		PeerKey:         peerKey,
		TypingName:      name,
		TypingActionKey: key,
		TypingActive:    active,
	})
}
