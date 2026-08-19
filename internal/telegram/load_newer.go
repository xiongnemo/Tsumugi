package telegram

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// loadNewerLimit is how many messages one forward page fetches. Matched to the jump window so
// reading forward out of an unread jump advances by a comparable chunk.
const loadNewerLimit = 60

// loadNewer fetches the messages immediately after afterID.
//
// There was no forward-loading path at all: onReachOlder covered scrolling up, and nothing covered
// scrolling down. That was invisible while every chat opened at the newest message, because there
// was nothing newer to load — but landing on the first unread means reading *forward*, and the
// window simply ended.
//
// messages.getHistory walks backwards from OffsetID. A negative AddOffset walks forward instead, so
// this returns the page starting one window before afterID, which is the same trick jumpToMessage
// uses to centre its window. afterID itself comes back too; the merge dedupes by id.
func (c *GotdClient) loadNewer(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, afterID int) {
	if c.store == nil || api == nil || peerKey == "" || afterID <= 0 {
		return
	}
	// Shares the older-load guard, keyed by the anchor id, so repeated scrolling at the bottom
	// coalesces the same way it does at the top instead of firing a request per keystroke.
	if !c.tryBeginOlderLoad(peerKey, -afterID) {
		return
	}
	defer c.endOlderLoad(peerKey, -afterID)

	c.beginForegroundLoad()
	defer c.endForegroundLoad()
	c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusLoadingNewer)})

	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		return
	}
	input, err := c.resolveInputPeer(ctx, api, p)
	if err != nil {
		c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventError, PeerKey: peerKey, Error: err})
		return
	}

	history, err := c.messagesGetHistory(ctx, api, &tg.MessagesGetHistoryRequest{
		Peer:      input,
		OffsetID:  afterID,
		AddOffset: -loadNewerLimit,
		Limit:     loadNewerLimit,
	})
	if err != nil {
		c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventError, PeerKey: peerKey, Error: rpcError("load newer", err)})
		return
	}
	msgs := c.normalizeMessagesWithPreview(ctx, api, accountID, peerKey, history, false)
	if len(msgs) == 0 {
		c.sendFocusedEvent(ctx, events, peerKey, Event{
			Kind:          EventMessages,
			PeerKey:       peerKey,
			ReachedNewest: true,
			StatusMsg:     i18n.M(i18n.KeyStatusAtTail),
		})
		return
	}
	if err := c.store.SaveMessages(ctx, msgs); err != nil {
		c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventError, PeerKey: peerKey, Error: err})
		return
	}

	// Forward loading deliberately does not touch history_min_id: it only ever adds messages
	// newer than what is held, so the oldest known id is unchanged and lowering it would tell
	// the backfiller it holds history it does not.
	newest := maxStorageMessageID(msgs)
	c.sendFocusedEvent(ctx, events, peerKey, Event{
		Kind:             EventMessages,
		PeerKey:          peerKey,
		Messages:         c.telegramMessages(ctx, accountID, msgs),
		Merge:            true,
		PreserveViewport: true,
		// A short page means there was nothing more to fetch, so the viewport now reaches the
		// newest message and the way-back-to-latest hint can go.
		ReachedNewest: newest <= afterID || len(msgs) < loadNewerLimit,
		StatusMsg:     i18n.M(i18n.KeyStatusNewerLoaded, len(msgs)),
	})
	if c.isFocusedPeer(peerKey) {
		toEnrich := append([]storage.Message(nil), msgs...)
		go c.enrichPeerMessagePreviews(ctx, accountID, api, events, peerKey, toEnrich)
	}
}

// maxStorageMessageID is the newest id in a page.
func maxStorageMessageID(messages []storage.Message) int {
	out := 0
	for _, msg := range messages {
		if msg.ID > out {
			out = msg.ID
		}
	}
	return out
}
