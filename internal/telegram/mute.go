package telegram

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
)

// muteForever is the deadline used when muting with no end.
//
// Telegram has no "muted indefinitely" flag; every client writes a deadline far enough out to be one.
// This is what the official apps do, and reading a stored value back only ever asks "is it in the
// future", so the exact number never reaches the UI.
const muteForever = 2147483647

// storeDialogMute records the notify settings a dialog sync carried.
//
// This is the only place mute state arrives from: tg.Dialog.NotifySettings is populated by
// messages.getDialogs and by nothing else, which is why an incoming message must never be allowed to
// write this field.
func (c *GotdClient) storeDialogMute(ctx context.Context, accountID, peerKey string, settings tg.PeerNotifySettings) {
	if c.store == nil || peerKey == "" {
		return
	}
	until, ok := settings.GetMuteUntil()
	if !ok {
		// No value means "inherit the default for this peer type", which is unmuted as far as a
		// per-chat marker is concerned.
		until = 0
	}
	_ = c.store.SetPeerMute(ctx, accountID, peerKey, until)
}

// mutePeer mutes or unmutes a chat.
func (c *GotdClient) mutePeer(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
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

	until := 0
	if command.Mute {
		until = muteForever
	}
	settings := tg.InputPeerNotifySettings{}
	settings.SetMuteUntil(until)
	send := c.notifySend
	if send == nil {
		send = api.AccountUpdateNotifySettings
	}
	if _, err := retryFloodWait(ctx, defaultMaxFloodWaits, "updating notify settings", func(ctx context.Context) (bool, error) {
		return send(ctx, &tg.AccountUpdateNotifySettingsRequest{
			Peer:     &tg.InputNotifyPeer{Peer: input},
			Settings: settings,
		})
	}); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: command.PeerKey, Error: rpcError("mute", err)})
		return
	}

	// Stored locally only after the server accepted it, so the marker never claims a state Telegram
	// disagrees with.
	_ = c.store.SetPeerMute(ctx, accountID, command.PeerKey, until)
	status := i18n.KeyStatusUnmuted
	if command.Mute {
		status = i18n.KeyStatusMuted
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: command.PeerKey, StatusMsg: i18n.M(status)})
	c.scheduleChatListRefresh(ctx, accountID, events)
}
