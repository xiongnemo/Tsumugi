package telegram

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// forwardMaxMessages is Telegram's per-request cap for messages.forwardMessages.
const forwardMaxMessages = 100

// forwardLookupWindow is how much recent history is loaded to validate the requested ids. The
// UI can only mark messages it is displaying, and the viewport holds at most 1000.
const forwardLookupWindow = 1000

// SavedMessagesTarget is the sentinel peer key for "forward to Saved Messages".
//
// A sentinel rather than the real self:<id> key, because the picker offers Saved Messages before
// any dialog sync has happened and therefore before that key is known. The backend maps it
// straight to InputPeerSelf, which needs no stored peer at all.
const SavedMessagesTarget = "__saved__"

// sanitizeForwardIDs turns the UI's message ids into ids Telegram will accept.
//
// Drops everything that has no server-side identity — local pending sends, failed sends, deleted
// rows and service messages, none of which can be forwarded — and errors above the API cap rather
// than silently forwarding a prefix. Sorted ascending because Telegram forwards in the order
// given and the result should read the same way as the source.
func sanitizeForwardIDs(messages []storage.Message, wanted []string) ([]int, error) {
	byID := make(map[string]storage.Message, len(messages))
	for _, msg := range messages {
		byID[strconv.Itoa(msg.ID)] = msg
	}
	seen := make(map[int]struct{}, len(wanted))
	out := make([]int, 0, len(wanted))
	for _, raw := range wanted {
		msg, ok := byID[raw]
		if !ok {
			continue
		}
		if msg.ServiceKey != "" || msg.State == "pending" || msg.State == "failed" || msg.State == "deleted" {
			continue
		}
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) > forwardMaxMessages {
		return nil, fmt.Errorf("cannot forward %d messages at once; the limit is %d", len(out), forwardMaxMessages)
	}
	sort.Ints(out)
	return out, nil
}

// forwardTargetPeer resolves a picker target to an input peer.
func (c *GotdClient) forwardTargetPeer(ctx context.Context, accountID, target string) (tg.InputPeerClass, error) {
	if target == SavedMessagesTarget {
		return &tg.InputPeerSelf{}, nil
	}
	if c.store == nil {
		return nil, fmt.Errorf("peer %s not found", target)
	}
	p, ok, err := c.store.Peer(ctx, accountID, target)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("peer %s not found", target)
	}
	return inputPeer(p)
}

// forwardMessages copies the given messages from one chat into another.
func (c *GotdClient) forwardMessages(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	if c.store == nil || api == nil || cmd.PeerKey == "" || cmd.ForwardTarget == "" {
		return
	}
	stored, err := c.store.MessagesForPeer(ctx, accountID, cmd.PeerKey, forwardLookupWindow)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: err})
		return
	}
	ids, err := sanitizeForwardIDs(stored, cmd.ForwardIDs)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: err})
		return
	}
	if len(ids) == 0 {
		sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: cmd.PeerKey, StatusMsg: i18n.M(i18n.KeyStatusForwardNothing)})
		return
	}

	from, err := c.forwardTargetPeer(ctx, accountID, cmd.PeerKey)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: err})
		return
	}
	to, err := c.forwardTargetPeer(ctx, accountID, cmd.ForwardTarget)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: err})
		return
	}

	// One RandomID per message, from crypto/rand. A short slice is an RPC error and a reused
	// value is a silent drop, so both failure modes are invisible if this is got wrong.
	randomIDs := make([]int64, len(ids))
	for i := range randomIDs {
		n, err := randomInt64()
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: err})
			return
		}
		randomIDs[i] = n
	}

	req := &tg.MessagesForwardMessagesRequest{
		FromPeer:   from,
		ID:         ids,
		RandomID:   randomIDs,
		ToPeer:     to,
		DropAuthor: cmd.ForwardDropAuthor,
	}
	updates, err := retryFloodWait(ctx, defaultMaxFloodWaits, "forwarding messages", func(ctx context.Context) (tg.UpdatesClass, error) {
		return api.MessagesForwardMessages(ctx, req)
	})
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: fmt.Errorf("forward messages: %w", err)})
		return
	}

	// Echo into the destination only when it is the chat on screen; forwarding elsewhere is
	// confirmed by the status line, and the messages arrive through the normal update path.
	targetKey := cmd.ForwardTarget
	if targetKey != SavedMessagesTarget && c.isFocusedPeer(targetKey) {
		if echoed := c.messagesFromSendUpdates(ctx, api, accountID, targetKey, "", 0, updates); len(echoed) > 0 {
			if err := c.store.SaveMessages(ctx, echoed); err == nil {
				c.sendFocusedEvent(ctx, events, targetKey, Event{
					Kind:     EventMessages,
					PeerKey:  targetKey,
					Messages: c.telegramMessages(ctx, accountID, echoed),
					Append:   true,
				})
			}
		}
	}
	sendEvent(ctx, events, Event{
		Kind:      EventStatus,
		PeerKey:   cmd.PeerKey,
		StatusMsg: i18n.M(i18n.KeyStatusForwarded, len(ids)),
	})
}
