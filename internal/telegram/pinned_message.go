package telegram

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/storage"
)

func (c *GotdClient) loadPeerPinnedMessage(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string) {
	if c.store == nil || api == nil || !c.isFocusedPeer(peerKey) {
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		return
	}
	pinnedID, err := c.pinnedMessageID(ctx, api, p)
	if err != nil || pinnedID <= 0 {
		if c.isFocusedPeer(peerKey) {
			sendEvent(ctx, events, Event{Kind: EventPeerPinned, PeerKey: peerKey})
		}
		return
	}
	msg, ok, err := c.fetchPeerMessageByID(ctx, api, accountID, peerKey, p, pinnedID)
	if err != nil || !ok {
		if c.isFocusedPeer(peerKey) {
			sendEvent(ctx, events, Event{Kind: EventPeerPinned, PeerKey: peerKey})
		}
		return
	}
	preview := pinnedMessagePreview(msg)
	if preview == "" {
		if c.isFocusedPeer(peerKey) {
			sendEvent(ctx, events, Event{Kind: EventPeerPinned, PeerKey: peerKey})
		}
		return
	}
	if c.isFocusedPeer(peerKey) {
		sendEvent(ctx, events, Event{Kind: EventPeerPinned, PeerKey: peerKey, PinnedPreview: preview})
	}
}

func (c *GotdClient) pinnedMessageID(ctx context.Context, api *tg.Client, p storage.Peer) (int, error) {
	switch p.Kind {
	case "chat":
		full, err := api.MessagesGetFullChat(ctx, p.ID)
		if err != nil {
			return 0, err
		}
		if chatFull, ok := full.FullChat.(*tg.ChatFull); ok {
			if id, ok := chatFull.GetPinnedMsgID(); ok && id > 0 {
				return id, nil
			}
		}
	case "channel":
		full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash})
		if err != nil {
			return 0, err
		}
		if channelFull, ok := full.FullChat.(*tg.ChannelFull); ok {
			if id, ok := channelFull.GetPinnedMsgID(); ok && id > 0 {
				return id, nil
			}
		}
	}
	return 0, nil
}

func (c *GotdClient) fetchPeerMessageByID(ctx context.Context, api *tg.Client, accountID, peerKey string, p storage.Peer, messageID int) (storage.Message, bool, error) {
	if messageID <= 0 {
		return storage.Message{}, false, nil
	}
	msgID := []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}}
	var modified tg.MessagesMessagesClass
	var err error
	switch p.Kind {
	case "channel":
		modified, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash},
			ID:      msgID,
		})
	default:
		modified, err = api.MessagesGetMessages(ctx, msgID)
	}
	if err != nil {
		return storage.Message{}, false, fmt.Errorf("get pinned message: %w", err)
	}
	msgs := c.normalizeMessagesWithPreview(ctx, api, accountID, peerKey, modified, false)
	for _, msg := range msgs {
		if msg.ID == messageID {
			return msg, true, nil
		}
	}
	return storage.Message{}, false, nil
}

func pinnedMessagePreview(msg storage.Message) string {
	text := msg.Text
	if text == "" && msg.MediaJSON != "" {
		var media MediaAttachment
		if err := json.Unmarshal([]byte(msg.MediaJSON), &media); err == nil {
			media = LocalizeMediaAttachment(media)
			if media.Label != "" {
				text = media.Label
			}
		}
	}
	return text
}
