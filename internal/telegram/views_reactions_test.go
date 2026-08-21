package telegram

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
)

// reactionTestClient builds a client over real storage; the reaction path is exercised without a
// network by handing it an updates container directly.
func reactionTestClient(t *testing.T) (*GotdClient, *storage.DB, string) {
	t.Helper()
	ctx := context.Background()
	db, _, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(cipher)
	acct := "user:1"
	if err := db.SavePeers(ctx, []storage.Peer{
		{AccountID: acct, Key: "channel:500", Kind: "channel", ID: 500, AccessHash: 7, Title: "Chat", LastMessageAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	return &GotdClient{store: db}, db, acct
}

// gotd hands RPC updates back to the caller rather than feeding them to the dispatcher, so
// OnMessageReactions never sees our own reaction: discarding the sendReaction response was why a
// reaction you added did not appear until something else refreshed the message.
func TestSendReactionAppliesItsOwnResponse(t *testing.T) {
	c, db, acct := reactionTestClient(t)
	ctx := context.Background()
	if err := db.SaveMessages(ctx, []storage.Message{
		{AccountID: acct, PeerKey: "channel:500", ID: 77, Date: time.Now().UTC(), Text: "hi", State: "synced"},
	}); err != nil {
		t.Fatal(err)
	}

	events := make(chan Event, 16)
	reactions := &tg.MessageReactions{}
	reactions.Results = []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 1}}
	c.applyReactionUpdates(ctx, acct, "channel:500", 77, &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateMessageReactions{
			Peer:      &tg.PeerChannel{ChannelID: 500},
			MsgID:     77,
			Reactions: *reactions,
		},
	}}, events)

	stored, ok, err := db.MessageByID(ctx, acct, "channel:500", 77)
	if err != nil || !ok {
		t.Fatalf("message gone: %v", err)
	}
	if stored.ReactionsJSON == "" {
		t.Fatal("the reaction was not stored, so it would vanish on the next redraw")
	}
	got := drainEvents(events)
	if len(got) == 0 {
		t.Fatal("nothing was emitted, so the view never learns about it")
	}
	last := got[len(got)-1]
	if !last.Patch || len(last.Messages) != 1 {
		t.Fatalf("event = %+v, want a single patched message", last)
	}
	if len(last.Messages[0].Reactions) != 1 || last.Messages[0].Reactions[0].Emoji != "👍" {
		t.Fatalf("patched reactions = %+v", last.Messages[0].Reactions)
	}
}
