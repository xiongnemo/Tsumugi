package telegram

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/storage"
)

// draftReplyToID extracts the message a draft replies to. Story and monoforum reply headers
// carry no message id, so they degrade to "no reply target" rather than a bogus one.
func draftReplyToID(reply tg.InputReplyToClass) int {
	if header, ok := reply.(*tg.InputReplyToMessage); ok {
		return header.ReplyToMsgID
	}
	return 0
}

// draftFromDialog converts a dialog's draft into storage form. An empty or missing draft is
// reported with ok == true and no text, which SaveServerDrafts reads as "cleared elsewhere".
func draftFromDialog(accountID, peerKey string, dialog *tg.Dialog) (storage.Draft, bool) {
	if dialog == nil {
		return storage.Draft{}, false
	}
	raw, ok := dialog.GetDraft()
	if !ok {
		return storage.Draft{}, false
	}
	draft := storage.Draft{AccountID: accountID, PeerKey: peerKey}
	switch d := raw.(type) {
	case *tg.DraftMessage:
		draft.Text = d.Message
		draft.ServerDate = d.Date
		if reply, ok := d.GetReplyTo(); ok {
			draft.ReplyToID = draftReplyToID(reply)
		}
	case *tg.DraftMessageEmpty:
		if date, ok := d.GetDate(); ok {
			draft.ServerDate = date
		}
	default:
		return storage.Draft{}, false
	}
	return draft, true
}

// saveDraft persists the draft locally and then tells Telegram about it.
//
// The local write happens first and unconditionally: it is what makes a draft survive a crash
// or a quit before the round trip, and it is marked dirty so the background dialog sweep cannot
// overwrite it while the network call is in flight. An empty draft clears both sides, which is
// how Telegram itself models clearing (saveDraft with only a peer).
func (c *GotdClient) saveDraft(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	if c.store == nil || api == nil || cmd.PeerKey == "" {
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, cmd.PeerKey)
	if err != nil || !ok {
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		return
	}

	local := storage.Draft{
		AccountID: accountID,
		PeerKey:   cmd.PeerKey,
		Text:      cmd.Text,
		ReplyToID: cmd.ReplyToID,
	}
	if err := c.store.SaveLocalDraft(ctx, local); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: fmt.Errorf("save draft: %w", err)})
		return
	}

	req := &tg.MessagesSaveDraftRequest{Peer: input, Message: cmd.Text, NoWebpage: true}
	if cmd.ReplyToID != 0 {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: cmd.ReplyToID})
	}
	if _, err := retryFloodWait(ctx, defaultMaxFloodWaits, "saving draft", func(ctx context.Context) (bool, error) {
		return api.MessagesSaveDraft(ctx, req)
	}); err != nil {
		// Stay dirty so a later sync does not replace our text with the server's older copy.
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: fmt.Errorf("save draft: %w", err)})
		return
	}
	if cmd.Text == "" && cmd.ReplyToID == 0 {
		_ = c.store.DeleteDraft(ctx, accountID, cmd.PeerKey)
		return
	}
	_ = c.store.MarkDraftSynced(ctx, accountID, cmd.PeerKey, 0)
}

// clearDraftAfterSend drops the draft once the message it was holding actually went out.
func (c *GotdClient) clearDraftAfterSend(ctx context.Context, accountID string, api *tg.Client, peerKey string) {
	if c.store == nil || peerKey == "" {
		return
	}
	if _, ok, err := c.store.Draft(ctx, accountID, peerKey); err != nil || !ok {
		return
	}
	_ = c.store.DeleteDraft(ctx, accountID, peerKey)
	if api == nil {
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		return
	}
	// Best effort: the message is already sent, so a failure here only leaves a stale draft on
	// other clients and must not surface as an error.
	_, _ = api.MessagesSaveDraft(ctx, &tg.MessagesSaveDraftRequest{Peer: input})
}

// sendPeerDraft hands the stored draft to the UI so opening a chat restores what was typed.
func (c *GotdClient) sendPeerDraft(ctx context.Context, accountID string, events chan<- Event, peerKey string) {
	if c.store == nil {
		return
	}
	draft, ok, err := c.store.Draft(ctx, accountID, peerKey)
	if err != nil || !ok {
		return
	}
	c.sendFocusedEvent(ctx, events, peerKey, Event{
		Kind:           EventDraft,
		PeerKey:        peerKey,
		DraftText:      draft.Text,
		DraftReplyToID: draft.ReplyToID,
	})
}

// applyDraftUpdate handles a draft changed in another session.
//
// Deliberately not sendFocusedEvent: a remote draft for a background chat still has to reach
// storage, and the UI decides on its own whether the event concerns the open chat.
func (c *GotdClient) applyDraftUpdate(ctx context.Context, accountID string, events chan<- Event, update *tg.UpdateDraftMessage) {
	if c.store == nil || update == nil {
		return
	}
	if topMsgID, ok := update.GetTopMsgID(); ok && topMsgID != 0 {
		// Forum topic drafts are a separate space; deferred.
		return
	}
	kind, id := peerRefParts(update.Peer)
	if kind == "" || id == 0 {
		return
	}
	peerKey := peerKey(kind, id)
	draft := storage.Draft{AccountID: accountID, PeerKey: peerKey}
	switch d := update.Draft.(type) {
	case *tg.DraftMessage:
		draft.Text = d.Message
		draft.ServerDate = d.Date
		if reply, ok := d.GetReplyTo(); ok {
			draft.ReplyToID = draftReplyToID(reply)
		}
	case *tg.DraftMessageEmpty:
		if date, ok := d.GetDate(); ok {
			draft.ServerDate = date
		}
	default:
		return
	}
	if err := c.store.SaveServerDrafts(ctx, []storage.Draft{draft}); err != nil {
		return
	}
	sendEvent(ctx, events, Event{
		Kind:           EventDraft,
		PeerKey:        peerKey,
		DraftText:      draft.Text,
		DraftReplyToID: draft.ReplyToID,
		DraftRemote:    true,
	})
}
