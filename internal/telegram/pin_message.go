package telegram

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
)

// pinMessage pins or unpins one message.
//
// The display side of pinning was complete - the banner, the pinned list, the pin service message -
// and this was the missing outbound half: Tsumugi could show you every pinned message and not pin
// one.
//
// Whether the account may pin here is not knowable client-side without tracking admin rights per
// chat, so the request goes out and CHAT_ADMIN_REQUIRED comes back through describeRPCError. That is
// the same choice editing makes, for the same reason: a guess that is wrong hides a legal action.
func (c *GotdClient) pinMessage(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	if command.MessageID <= 0 {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusPinNotSynced)})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, command.PeerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", command.PeerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}

	request := &tg.MessagesUpdatePinnedMessageRequest{
		Peer: input,
		ID:   command.MessageID,
		// Silent: pinning already produces a service message in the chat, and Telegram's default
		// also notifies every member. Tsumugi pins quietly because a terminal client is not where
		// anyone expects to wake a thousand-member group.
		Silent: true,
		Unpin:  command.Unpin,
	}
	send := c.pinSend
	if send == nil {
		send = api.MessagesUpdatePinnedMessage
	}
	if _, err := retryFloodWait(ctx, defaultMaxFloodWaits, "pinning message", func(ctx context.Context) (tg.UpdatesClass, error) {
		return send(ctx, request)
	}); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: command.PeerKey, Error: rpcError("pin message", err)})
		return
	}

	status := i18n.KeyStatusPinned
	if command.Unpin {
		status = i18n.KeyStatusUnpinned
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: command.PeerKey, StatusMsg: i18n.M(status)})
	// The banner and the pinned list both read from the server rather than from a local guess, so
	// refreshing is what makes the change visible.
	c.loadPeerPinnedMessage(ctx, accountID, api, events, command.PeerKey)
}
