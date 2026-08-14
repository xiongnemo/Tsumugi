package telegram

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// jumpWindowLimit is how many messages a jump loads around its target. Graphical clients use
// a window of this order so the target lands mid-screen with context above and below.
const jumpWindowLimit = 60

// jumpToMessage loads one window of history centred on messageID. This is how graphical
// Telegram clients implement "jump to message": messages.getHistory with OffsetID set to the
// target and AddOffset pulled back half the window, so the reply contains the target plus its
// neighbours on both sides in a single round trip.
//
// The previous approach paged backwards from the viewport's oldest message, needing one round
// trip per 50 messages. Jumping to a pin from months ago therefore never arrived, and each
// page re-triggered gap detection, which is what left the status bar stuck on "filling gap".
//
// The window replaces the viewport rather than merging into it. Merging a distant cluster
// would splice two non-adjacent ranges together, which is exactly the false adjacency that
// makes the gap detector chase a hole forever.
func (c *GotdClient) jumpToMessage(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, messageID int) {
	if c.store == nil || api == nil || messageID <= 0 {
		return
	}
	c.beginForegroundLoad()
	defer c.endForegroundLoad()

	sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: peerKey, StatusMsg: i18n.M(i18n.KeyStatusJumpingToMessage)})

	var msgs []storage.Message
	func() {
		c.peerHistoryLock(peerKey).Lock()
		defer c.peerHistoryLock(peerKey).Unlock()

		p, ok, err := c.store.Peer(ctx, accountID, peerKey)
		if err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("peer %s not found", peerKey)
			}
			sendEvent(ctx, events, Event{Kind: EventError, PeerKey: peerKey, Error: err})
			return
		}
		input, err := inputPeer(p)
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, PeerKey: peerKey, Error: err})
			return
		}

		history, err := c.messagesGetHistory(ctx, api, &tg.MessagesGetHistoryRequest{
			Peer:      input,
			OffsetID:  messageID,
			AddOffset: -jumpWindowLimit / 2,
			Limit:     jumpWindowLimit,
		})
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, PeerKey: peerKey, Error: fmt.Errorf("jump to message: %w", err)})
			return
		}
		window := c.normalizeMessagesWithPreview(ctx, api, accountID, peerKey, history, false)
		if len(window) == 0 {
			sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: peerKey, StatusMsg: i18n.M(i18n.KeyStatusMessageNotFound)})
			return
		}
		if err := c.store.SaveMessages(ctx, window); err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, PeerKey: peerKey, Error: err})
			return
		}
		msgs = window
	}()
	if len(msgs) == 0 {
		return
	}

	if !containsStorageMessageID(msgs, messageID) {
		// Deleted, or in a topic/range the window did not cover. Show what came back rather
		// than silently doing nothing, but do not claim the jump succeeded.
		sendEvent(ctx, events, Event{
			Kind:      EventMessages,
			PeerKey:   peerKey,
			Messages:  c.telegramMessages(ctx, accountID, msgs),
			StatusMsg: i18n.M(i18n.KeyStatusMessageNotFound),
		})
	} else {
		sendEvent(ctx, events, Event{
			Kind:            EventMessages,
			PeerKey:         peerKey,
			Messages:        c.telegramMessages(ctx, accountID, msgs),
			SelectMessageID: strconv.Itoa(messageID),
			StatusMsg:       i18n.M(i18n.KeyStatusJumpedToMessage),
		})
	}

	// The window is fetched without raster previews, so photos would land as bare labels.
	// openChat and loadOlder both hydrate afterwards; jumps have to do the same or the
	// destination looks worse than the place you jumped from. Animated media survives either
	// way because resolveAnimatedLocalPath recovers an already-downloaded file from disk.
	if c.isFocusedPeer(peerKey) {
		toEnrich := append([]storage.Message(nil), msgs...)
		go c.enrichPeerMessagePreviews(ctx, accountID, api, events, peerKey, toEnrich)
	}
}

func containsStorageMessageID(messages []storage.Message, id int) bool {
	for _, msg := range messages {
		if msg.ID == id {
			return true
		}
	}
	return false
}
