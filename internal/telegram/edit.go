package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// editMessage rewrites the text of a message already sent.
//
// Telegram allows this for your own messages inside a window (and indefinitely for channel admins),
// but the window is not knowable client-side: it differs by chat type and by whether you are an
// admin. Guessing it would hide a legal action, so the request goes out and the server's answer -
// MESSAGE_EDIT_TIME_EXPIRED, MESSAGE_NOT_MODIFIED, CHAT_ADMIN_REQUIRED - is what the user sees, led
// by its Telegram name so it survives the truncated status column.
func (c *GotdClient) editMessage(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	peerKey := command.PeerKey
	text := strings.TrimSpace(command.Text)
	if text == "" {
		// Telegram has no "clear the text" edit: an empty message is a delete, which is a different
		// action with a different confirmation.
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusEditEmpty)})
		return
	}
	if command.MessageID <= 0 {
		// Negative ids are local rows that Telegram has never seen.
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusEditNotSynced)})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}

	request := &tg.MessagesEditMessageRequest{
		Peer: input,
		ID:   command.MessageID,
	}
	request.SetMessage(text)
	if entities := buildMentionNameEntities(command.MentionEntities); len(entities) > 0 {
		request.SetEntities(entities)
	}
	send := c.editSend
	if send == nil {
		send = api.MessagesEditMessage
	}
	updates, err := retryFloodWait(ctx, defaultMaxFloodWaits, "editing message", func(ctx context.Context) (tg.UpdatesClass, error) {
		return send(ctx, request)
	})
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: peerKey, Error: rpcError("edit message", err)})
		return
	}

	edited := c.messagesFromEditUpdates(ctx, api, accountID, peerKey, updates)
	if len(edited) == 0 {
		// The update stream will bring it; MessageViewport keys rows by id, so it replaces rather
		// than duplicating.
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusEditSubmitted)})
		return
	}
	_ = c.store.SaveMessages(ctx, edited)
	sendEvent(ctx, events, Event{
		Kind:      EventMessages,
		PeerKey:   peerKey,
		Messages:  c.telegramMessages(ctx, accountID, edited),
		Patch:     true,
		StatusMsg: i18n.M(i18n.KeyStatusEdited),
	})
}

// messagesFromEditUpdates pulls the edited message out of an edit response.
//
// A sibling of messagesFromSendUpdates rather than an extension of it: an edit answers with
// updateEditMessage, which that function does not look at, and widening it would make a send accept
// an edit as its own result.
func (c *GotdClient) messagesFromEditUpdates(ctx context.Context, api *tg.Client, accountID, peerKey string, updates tg.UpdatesClass) []storage.Message {
	var out []storage.Message
	collect := func(list []tg.UpdateClass, users []tg.UserClass, chats []tg.ChatClass) {
		entities := dialogEntities(users, chats)
		for _, update := range list {
			switch item := update.(type) {
			case *tg.UpdateEditMessage:
				out = append(out, c.normalizeUpdateMessageClass(ctx, api, accountID, peerKey, item.Message, entities)...)
			case *tg.UpdateEditChannelMessage:
				out = append(out, c.normalizeUpdateMessageClass(ctx, api, accountID, peerKey, item.Message, entities)...)
			}
		}
	}
	switch u := updates.(type) {
	case *tg.Updates:
		collect(u.Updates, u.Users, u.Chats)
	case *tg.UpdatesCombined:
		collect(u.Updates, u.Users, u.Chats)
	case *tg.UpdateShort:
		collect([]tg.UpdateClass{u.Update}, nil, nil)
	}
	return out
}

// EditableMessage reports whether a message can be edited.
//
// Exported and pure so the UI can decide what to offer without duplicating the rule. Deliberately
// not a time check: see editMessage.
func EditableMessage(msg Message) bool {
	return msg.Outgoing &&
		msg.State == "synced" &&
		msg.ServiceKey == "" &&
		strings.TrimSpace(msg.Text) != ""
}
