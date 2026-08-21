package telegram

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/nemo/Tsumugi/internal/debuglog"
	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// forwardMaxMessages is Telegram's per-request cap for messages.forwardMessages.
const forwardMaxMessages = 100

// SavedMessagesTarget is the sentinel peer key for "forward to Saved Messages".
//
// A sentinel rather than the real self:<id> key, because the picker offers Saved Messages before
// any dialog sync has happened and therefore before that key is known. The backend maps it
// straight to InputPeerSelf, which needs no stored peer at all.
const SavedMessagesTarget = "__saved__"

// forwardableMessage reports whether a stored message can be forwarded.
//
// Local pending sends, failed sends, deleted rows and service messages have no forwardable
// server-side identity.
func forwardableMessage(msg storage.Message) bool {
	switch {
	case msg.ServiceKey != "":
		return false
	case msg.State == "pending", msg.State == "failed", msg.State == "deleted":
		return false
	default:
		return true
	}
}

// sanitizeForwardIDs turns the UI's message ids into ids Telegram will accept.
//
// lookup answers "what is this message", by id. It is a function rather than a preloaded slice
// because the alternative — loading recent history and dropping anything not in it — silently
// discards marks for older messages, which is precisely the limitation that made the UI throw
// selections away on every jump. A mark is valid because the user made it, not because the
// message happens to still be in the loaded window.
//
// An id the store has never heard of is passed through rather than dropped: the UI can only mark
// what it has displayed, so an unknown id means our cache is behind, not that the message is
// invalid, and Telegram is the authority on that.
//
// Errors above the API cap rather than silently forwarding a prefix the user cannot identify.
// Sorted ascending because Telegram forwards in the order given.
func sanitizeForwardIDs(lookup func(int) (storage.Message, bool), wanted []string) ([]int, error) {
	seen := make(map[int]struct{}, len(wanted))
	out := make([]int, 0, len(wanted))
	for _, raw := range wanted {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			// A local pending send has no server id at all.
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		if msg, known := lookup(id); known && !forwardableMessage(msg) {
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
func (c *GotdClient) forwardTargetPeer(ctx context.Context, accountID string, api *tg.Client, target string) (tg.InputPeerClass, error) {
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
	return c.resolveInputPeer(ctx, api, p)
}

// forwardMessages copies the given messages from one chat into another.
func (c *GotdClient) forwardMessages(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	if c.store == nil || api == nil || cmd.PeerKey == "" || cmd.ForwardTarget == "" {
		return
	}
	lookup := func(id int) (storage.Message, bool) {
		msg, ok, err := c.store.MessageByID(ctx, accountID, cmd.PeerKey, id)
		if err != nil {
			return storage.Message{}, false
		}
		return msg, ok
	}
	ids, err := sanitizeForwardIDs(lookup, cmd.ForwardIDs)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: err})
		return
	}
	if len(ids) == 0 {
		sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: cmd.PeerKey, StatusMsg: i18n.M(i18n.KeyStatusForwardNothing)})
		return
	}

	from, err := c.forwardTargetPeer(ctx, accountID, api, cmd.PeerKey)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: err})
		return
	}
	to, err := c.forwardTargetPeer(ctx, accountID, api, cmd.ForwardTarget)
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
	debuglog.Log("forward_request", map[string]any{
		"from_key": cmd.PeerKey,
		"to_key":   cmd.ForwardTarget,
		"from":     describeInputPeer(from),
		"to":       describeInputPeer(to),
		"ids":      ids,
	})
	send := c.forwardSend
	if send == nil {
		send = api.MessagesForwardMessages
	}
	updates, err := retryFloodWait(ctx, defaultMaxFloodWaits, "forwarding messages", func(ctx context.Context) (tg.UpdatesClass, error) {
		return send(ctx, req)
	})
	if err != nil {
		debuglog.Log("forward_failed", map[string]any{
			"from":    cmd.PeerKey,
			"to":      cmd.ForwardTarget,
			"ids":     ids,
			"raw_err": err.Error(),
		})
		// CHAT_ADMIN_REQUIRED here almost always means the destination is a broadcast channel the
		// account cannot post to, which the raw error code says nothing about: the picker lists every
		// dialog, and a channel you only read looks exactly like one you can write to.
		if tgerr.Is(err, "CHAT_ADMIN_REQUIRED") {
			sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: cmd.PeerKey, StatusMsg: i18n.M(i18n.KeyStatusForwardNotAllowed)})
			return
		}
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: rpcError("forward messages", err)})
		return
	}

	// Always recorded, whatever is on screen. The copies come back in this method's own reply
	// rather than through the update stream, so nothing else will ever deliver them: gating this
	// on the destination being focused meant a forward elsewhere was never written down, and the
	// message only appeared after a later history fetch happened to pick it up.
	//
	// Plain sendEvent, not sendFocusedEvent, for the same reason drafts use it: App.appendMessage
	// already drops anything whose ChatID is not the open chat, so filtering here only loses the
	// storage write.
	if targetKey := c.storageKeyForTarget(ctx, accountID, cmd.ForwardTarget); targetKey != "" {
		if echoed := c.messagesFromSendUpdates(ctx, api, accountID, targetKey, "", 0, updates); len(echoed) > 0 {
			if err := c.store.SaveMessages(ctx, echoed); err != nil {
				debuglog.Error("forward_echo_save", err, map[string]any{"peer_key": targetKey})
			} else {
				sendEvent(ctx, events, Event{
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
