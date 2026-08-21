package telegram

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

var defaultQuickReactions = []ReactionSummary{
	{Emoji: "👍"},
	{Emoji: "❤️"},
	{Emoji: "🔥"},
	{Emoji: "👏"},
	{Emoji: "😁"},
	{Emoji: "😢"},
	{Emoji: "🎉"},
	{Emoji: "🤔"},
}

func DefaultQuickReactions() []ReactionSummary {
	out := make([]ReactionSummary, len(defaultQuickReactions))
	copy(out, defaultQuickReactions)
	return out
}

func (c *GotdClient) refreshChannelViews(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peer storage.Peer, msgs []storage.Message) {
	if api == nil || c.store == nil || !IsBroadcastChannel(peer.Kind, peer.Subtitle) || len(msgs) == 0 {
		return
	}
	ids := intMessageIDs(storageToTelegramIDs(msgs))
	if len(ids) == 0 {
		return
	}
	input, err := inputPeer(peer)
	if err != nil {
		return
	}
	resp, err := api.MessagesGetMessagesViews(ctx, &tg.MessagesGetMessagesViewsRequest{
		Peer:      input,
		ID:        ids,
		Increment: false,
	})
	if err != nil {
		return
	}
	views := map[int]messageViewCounts{}
	for i, view := range resp.Views {
		if i >= len(ids) {
			continue
		}
		entry := messageViewCounts{}
		if v, ok := view.GetViews(); ok {
			entry.Views = v
		}
		if f, ok := view.GetForwards(); ok {
			entry.Forwards = f
		}
		views[ids[i]] = entry
	}
	if len(views) == 0 {
		return
	}
	updated := applyViewsToStorage(msgs, views)
	if err := c.store.SaveMessages(ctx, updated); err != nil {
		return
	}
	sendEvent(ctx, events, Event{
		Kind:     EventMessages,
		PeerKey:  peer.Key,
		Messages: c.telegramMessages(ctx, accountID, updated),
		Patch:    true,
	})
}

func storageToTelegramIDs(msgs []storage.Message) []Message {
	out := make([]Message, len(msgs))
	for i, msg := range msgs {
		out[i] = Message{ID: strconv.Itoa(msg.ID)}
	}
	return out
}

func applyViewsToStorage(msgs []storage.Message, views map[int]messageViewCounts) []storage.Message {
	out := make([]storage.Message, 0, len(views))
	for _, msg := range msgs {
		v, ok := views[msg.ID]
		if !ok {
			continue
		}
		msg.Views = v.Views
		msg.Forwards = v.Forwards
		out = append(out, msg)
	}
	return out
}

func (c *GotdClient) markMessageViewed(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, messageID int) {
	if api == nil || c.store == nil || messageID <= 0 || peerKey == "" {
		return
	}
	key := peerKey + "/" + strconv.Itoa(messageID)
	c.viewedMu.Lock()
	if _, seen := c.viewedIDs[key]; seen {
		c.viewedMu.Unlock()
		return
	}
	c.viewedIDs[key] = struct{}{}
	c.viewedMu.Unlock()

	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok || !IsBroadcastChannel(p.Kind, p.Subtitle) {
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		return
	}
	resp, err := api.MessagesGetMessagesViews(ctx, &tg.MessagesGetMessagesViewsRequest{
		Peer:      input,
		ID:        []int{messageID},
		Increment: true,
	})
	if err != nil || len(resp.Views) == 0 {
		return
	}
	views := 0
	if v, ok := resp.Views[0].GetViews(); ok {
		views = v
	}
	if views <= 0 {
		return
	}
	if err := c.store.UpdateMessageViews(ctx, accountID, peerKey, messageID, views); err != nil {
		return
	}
	patched, ok, err := c.store.MessageByID(ctx, accountID, peerKey, messageID)
	if err != nil || !ok {
		return
	}
	sendEvent(ctx, events, Event{
		Kind:     EventMessages,
		PeerKey:  peerKey,
		Messages: c.telegramMessages(ctx, accountID, []storage.Message{patched}),
		Patch:    true,
	})
}

func (c *GotdClient) sendReaction(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, messageID int, reaction ReactionSummary) {
	if api == nil || c.store == nil || messageID <= 0 || peerKey == "" {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusNoMessageSelected)})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("peer %s not found", peerKey)})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	tgReaction := ReactionToTG(reaction)
	var reactions []tg.ReactionClass
	if tgReaction != nil {
		reactions = []tg.ReactionClass{tgReaction}
	}
	req := &tg.MessagesSendReactionRequest{
		Peer:  input,
		MsgID: messageID,
	}
	if len(reactions) > 0 {
		req.SetReaction(reactions)
	}
	updates, err := api.MessagesSendReaction(ctx, req)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("send reaction: %w", err)})
		return
	}
	// The response carries the new reaction state, and it has to be read here: gotd hands RPC
	// updates back to the caller rather than feeding them to the dispatcher, so OnMessageReactions
	// never sees our own reaction. Discarding it was why a reaction you added did not appear until
	// something else refreshed the message - the same reason sendText parses its own response.
	c.applyReactionUpdates(ctx, accountID, peerKey, messageID, updates, events)
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusReactionSent)})
}

func (c *GotdClient) loadAvailableReactions(ctx context.Context, api *tg.Client) []ReactionSummary {
	if api == nil {
		return DefaultQuickReactions()
	}
	resp, err := api.MessagesGetAvailableReactions(ctx, 0)
	if err != nil {
		return DefaultQuickReactions()
	}
	modified, ok := resp.AsModified()
	if !ok {
		return DefaultQuickReactions()
	}
	out := make([]ReactionSummary, 0, 8)
	for _, item := range modified.Reactions {
		if item.Inactive || item.Reaction == "" {
			continue
		}
		out = append(out, ReactionSummary{Emoji: item.Reaction})
		if len(out) >= 8 {
			break
		}
	}
	if len(out) == 0 {
		return DefaultQuickReactions()
	}
	return out
}

// applyReactionUpdates pulls the new reaction state out of a sendReaction response.
func (c *GotdClient) applyReactionUpdates(ctx context.Context, accountID, peerKey string, messageID int, updates tg.UpdatesClass, events chan<- Event) {
	var list []tg.UpdateClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch u := updates.(type) {
	case *tg.Updates:
		list, users, chats = u.Updates, u.Users, u.Chats
	case *tg.UpdatesCombined:
		list, users, chats = u.Updates, u.Users, u.Chats
	case *tg.UpdateShort:
		list = []tg.UpdateClass{u.Update}
	}
	entities := dialogEntities(users, chats)
	for _, update := range list {
		reactions, ok := update.(*tg.UpdateMessageReactions)
		if !ok {
			continue
		}
		_ = c.applyMessageReactions(ctx, accountID, peerKey, reactions.MsgID, &reactions.Reactions, entities, events)
		return
	}
	// Some peers answer with an edited message rather than a reactions update. Re-reading the row
	// keeps one code path for what the UI receives.
	if stored, ok := c.storedMessage(ctx, accountID, peerKey, messageID); ok {
		sendEvent(ctx, events, Event{
			Kind:     EventMessages,
			PeerKey:  peerKey,
			Messages: c.telegramMessages(ctx, accountID, []storage.Message{stored}),
			Patch:    true,
		})
	}
}

// applyMessageReactions stores a message's reactions and patches it into the view.
//
// Shared by the update handler and the send path so the two cannot disagree about what a reaction
// change looks like.
func (c *GotdClient) applyMessageReactions(ctx context.Context, accountID, peerKey string, messageID int, reactions *tg.MessageReactions, entities entitiesByID, events chan<- Event) error {
	if c.store == nil || reactions == nil {
		return nil
	}
	if err := c.store.UpdateMessageReactions(ctx, accountID, peerKey, messageID, ReactionsJSON(ParseMessageReactions(reactions))); err != nil {
		return err
	}
	stored, ok, err := c.store.MessageByID(ctx, accountID, peerKey, messageID)
	if err != nil || !ok {
		return err
	}
	msgs := c.telegramMessages(ctx, accountID, []storage.Message{stored})
	if len(msgs) > 0 {
		msgs[0].RecentReact = ParseRecentReactions(reactions, entities)
	}
	sendEvent(ctx, events, Event{Kind: EventMessages, PeerKey: peerKey, Messages: msgs, Patch: true})
	return nil
}
