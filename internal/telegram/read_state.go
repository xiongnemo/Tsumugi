package telegram

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/storage"
)

// inboxPeerKey resolves the stored peer key for an inbox read update.
//
// It cannot just build the key from the update's peer type. Saved Messages is stored as
// self:<id> but a read update for it arrives as a PeerUser, so peerKey() would produce
// user:<id> and address nothing. The lookup is restricted to the kinds a given peer type could
// have been stored as, because Telegram ids are only unique within a type.
func (c *GotdClient) inboxPeerKey(ctx context.Context, accountID string, ref tg.PeerClass) (string, bool) {
	kind, id, ok := peerKindID(ref)
	if !ok {
		return "", false
	}
	var kinds []string
	switch kind {
	case "user":
		kinds = []string{"user", "self"}
	default:
		kinds = []string{kind}
	}
	if c.store != nil {
		if key, found, err := c.store.PeerKeyForTelegramID(ctx, accountID, id, kinds...); err == nil && found {
			return key, true
		}
	}
	// The peer is not in storage yet (a dialog we have never synced). The plain key is still
	// the right guess for everything except Saved Messages, and a missed update there only
	// costs a stale badge until the next dialog sync.
	return peerKey(kind, id), true
}

// applyReadInbox records that incoming messages up to maxID have been read.
//
// unread is the server's StillUnreadCount, which is authoritative and may move in either
// direction; pass a negative value to leave the stored count alone.
func (c *GotdClient) applyReadInbox(ctx context.Context, accountID string, events chan<- Event, peerKey string, maxID, unread int) {
	if c.store == nil || peerKey == "" {
		return
	}
	if err := c.store.UpdatePeerReadInbox(ctx, accountID, peerKey, maxID, unread); err != nil {
		return
	}
	// Read back rather than echoing the arguments. unread may have been deliberately left
	// alone, and the watermark is monotonic, so the stored row is the only truthful source for
	// what the UI should now show.
	if p, ok, err := c.store.Peer(ctx, accountID, peerKey); err == nil && ok {
		unread = p.Unread
		maxID = p.ReadInboxMaxID
	} else if unread < 0 {
		return
	}
	// Plain sendEvent, not sendFocusedEvent: the unread badge belongs to the chat list, so a
	// read in a background chat is exactly the case that has to get through.
	sendEvent(ctx, events, Event{
		Kind:           EventReadInbox,
		PeerKey:        peerKey,
		ReadInboxMaxID: maxID,
		Unread:         unread,
	})
}

// markRead tells Telegram we have read up to maxID.
//
// Split by peer type because channels use a different method with a different request shape.
// Bots cannot read history at all, which the caller guards on.
func (c *GotdClient) markRead(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	if c.store == nil || api == nil || cmd.PeerKey == "" || cmd.MessageID <= 0 {
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, cmd.PeerKey)
	if err != nil || !ok {
		return
	}
	if p.ReadInboxMaxID >= cmd.MessageID {
		// Already read this far. readHistory is monotonic server-side too, but skipping saves
		// a request per scroll in a chat with no unread messages, which is the common case.
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		return
	}
	switch channel := input.(type) {
	case *tg.InputPeerChannel:
		_, err = retryFloodWait(ctx, defaultMaxFloodWaits, "marking channel read", func(ctx context.Context) (bool, error) {
			return api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
				Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
				MaxID:   cmd.MessageID,
			})
		})
	default:
		_, err = retryFloodWait(ctx, defaultMaxFloodWaits, "marking read", func(ctx context.Context) (*tg.MessagesAffectedMessages, error) {
			return api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: input, MaxID: cmd.MessageID})
		})
	}
	if err != nil {
		// A failed read mark is not worth a status line; the next scroll retries it.
		return
	}
	// Apply locally rather than waiting for the server's echo, so the badge clears at once.
	// The echo carries StillUnreadCount and corrects the count if this guess was wrong.
	c.applyReadInbox(ctx, accountID, events, cmd.PeerKey, cmd.MessageID, localUnreadAfterRead(p.Unread, p.TopMessageID, cmd.MessageID))
}

// firstUnreadStorageID picks the message to land on when opening a chat with unread messages.
//
// It is the oldest incoming message above the read watermark. Outgoing messages are skipped
// because our own sends are never unread, and a chat whose newest message is ours would
// otherwise "jump" to it and look like nothing happened.
//
// Returns 0 when the window contains no such message, which is a normal outcome and not an
// error: the first unread id may be a deleted message, a service message, or simply outside the
// window we asked for. Callers must resolve the target from what came back rather than asserting
// a specific id exists.
func firstUnreadStorageID(messages []storage.Message, readInboxMaxID int) int {
	best := 0
	for _, msg := range messages {
		if msg.Outgoing || msg.ID <= readInboxMaxID {
			continue
		}
		if best == 0 || msg.ID < best {
			best = msg.ID
		}
	}
	return best
}

// localUnreadAfterRead guesses the unread count after reading up to maxID.
//
// Reading to or past the newest message we know of clears the badge, which is the case worth
// getting right — it is what happens on opening a chat. A partial read is unknowable locally,
// because message ids are not dense and the gap between watermarks is therefore not a message
// count; -1 keeps the stored value until the server's echo supplies the real one.
func localUnreadAfterRead(unread, topMessageID, maxID int) int {
	if unread <= 0 {
		return 0
	}
	if topMessageID > 0 && maxID >= topMessageID {
		return 0
	}
	return -1
}
