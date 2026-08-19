package telegram

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
)

// forwardTestClient builds a client over real storage with the RPC replaced, so the whole
// forwardMessages path is exercised. There was no test for this function at all, which is why
// "the command is sent" and "the forward happens" could not be told apart.
func forwardTestClient(t *testing.T) (*GotdClient, *storage.DB, string) {
	t.Helper()
	ctx := context.Background()
	db, _, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(cipher)

	acct := "user:1"
	if err := db.SavePeers(ctx, []storage.Peer{
		{AccountID: acct, Key: "channel:500", Kind: "channel", ID: 500, AccessHash: 77, Title: "Source", LastMessageAt: time.Now().UTC()},
		{AccountID: acct, Key: "channel:600", Kind: "channel", ID: 600, AccessHash: 88, Title: "Target", LastMessageAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveMessages(ctx, []storage.Message{
		{AccountID: acct, PeerKey: "channel:500", ID: 11, Date: time.Now().UTC(), Text: "one", State: "synced"},
		{AccountID: acct, PeerKey: "channel:500", ID: 12, Date: time.Now().UTC(), Text: "two", State: "synced"},
	}); err != nil {
		t.Fatal(err)
	}
	return &GotdClient{store: db}, db, acct
}

func drainEvents(events chan Event) []Event {
	close(events)
	var out []Event
	for ev := range events {
		out = append(out, ev)
	}
	return out
}

func TestForwardMessagesSendsTheRightRequest(t *testing.T) {
	c, _, acct := forwardTestClient(t)
	var got *tg.MessagesForwardMessagesRequest
	c.forwardSend = func(_ context.Context, req *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
		got = req
		return &tg.Updates{}, nil
	}
	events := make(chan Event, 32)

	c.forwardMessages(context.Background(), acct, &tg.Client{}, events, Command{
		Kind:          CommandForwardMessages,
		PeerKey:       "channel:500",
		ForwardIDs:    []string{"11", "12"},
		ForwardTarget: "channel:600",
	})

	if got == nil {
		t.Fatal("the RPC was never reached")
	}
	from, ok := got.FromPeer.(*tg.InputPeerChannel)
	if !ok || from.ChannelID != 500 || from.AccessHash != 77 {
		t.Fatalf("FromPeer = %#v, want channel 500", got.FromPeer)
	}
	to, ok := got.ToPeer.(*tg.InputPeerChannel)
	if !ok || to.ChannelID != 600 || to.AccessHash != 88 {
		t.Fatalf("ToPeer = %#v, want channel 600", got.ToPeer)
	}
	if len(got.ID) != 2 || got.ID[0] != 11 || got.ID[1] != 12 {
		t.Fatalf("ID = %v, want [11 12]", got.ID)
	}
	// One per message: a short slice is an RPC error, a reused value is a silent drop.
	if len(got.RandomID) != len(got.ID) {
		t.Fatalf("RandomID has %d entries for %d ids", len(got.RandomID), len(got.ID))
	}
	if got.RandomID[0] == got.RandomID[1] {
		t.Fatal("RandomID values must differ")
	}

	for _, ev := range drainEvents(events) {
		if ev.Kind == EventError {
			t.Fatalf("unexpected error event: %v", ev.Error)
		}
	}
}

// Saved Messages needs no stored peer, which is the whole reason for the sentinel.
func TestForwardMessagesToSavedMessages(t *testing.T) {
	c, _, acct := forwardTestClient(t)
	var got *tg.MessagesForwardMessagesRequest
	c.forwardSend = func(_ context.Context, req *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
		got = req
		return &tg.Updates{}, nil
	}
	events := make(chan Event, 32)

	c.forwardMessages(context.Background(), acct, &tg.Client{}, events, Command{
		PeerKey:       "channel:500",
		ForwardIDs:    []string{"11"},
		ForwardTarget: SavedMessagesTarget,
	})

	if got == nil {
		t.Fatal("the RPC was never reached")
	}
	if _, ok := got.ToPeer.(*tg.InputPeerSelf); !ok {
		t.Fatalf("ToPeer = %#v, want InputPeerSelf", got.ToPeer)
	}
}

func TestForwardMessagesDropAuthor(t *testing.T) {
	c, _, acct := forwardTestClient(t)
	var got *tg.MessagesForwardMessagesRequest
	c.forwardSend = func(_ context.Context, req *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
		got = req
		return &tg.Updates{}, nil
	}
	events := make(chan Event, 32)

	c.forwardMessages(context.Background(), acct, &tg.Client{}, events, Command{
		PeerKey:           "channel:500",
		ForwardIDs:        []string{"11"},
		ForwardTarget:     "channel:600",
		ForwardDropAuthor: true,
	})

	if got == nil || !got.DropAuthor {
		t.Fatalf("DropAuthor not carried through: %#v", got)
	}
}

func TestForwardMessagesReportsAnRPCFailure(t *testing.T) {
	c, _, acct := forwardTestClient(t)
	c.forwardSend = func(_ context.Context, _ *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
		return nil, context.DeadlineExceeded
	}
	events := make(chan Event, 32)

	c.forwardMessages(context.Background(), acct, &tg.Client{}, events, Command{
		PeerKey:       "channel:500",
		ForwardIDs:    []string{"11"},
		ForwardTarget: "channel:600",
	})

	sawError := false
	for _, ev := range drainEvents(events) {
		if ev.Kind == EventError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("a failed forward must surface an error rather than looking like nothing happened")
	}
}

// An unknown destination must not be silently dropped either.
func TestForwardMessagesUnknownTargetErrors(t *testing.T) {
	c, _, acct := forwardTestClient(t)
	called := false
	c.forwardSend = func(_ context.Context, _ *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
		called = true
		return &tg.Updates{}, nil
	}
	events := make(chan Event, 32)

	c.forwardMessages(context.Background(), acct, &tg.Client{}, events, Command{
		PeerKey:       "channel:500",
		ForwardIDs:    []string{"11"},
		ForwardTarget: "channel:999",
	})

	if called {
		t.Fatal("no RPC should be attempted for an unresolvable destination")
	}
	sawError := false
	for _, ev := range drainEvents(events) {
		if ev.Kind == EventError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("an unresolvable destination must report an error")
	}
}

// forwardUpdates fakes the reply Telegram sends for a forward: the copies come back in the RPC
// response, not through the update stream, so this is the only chance to record them.
func forwardUpdates(peerKey string, id int) tg.UpdatesClass {
	var peer tg.PeerClass
	switch {
	case strings.HasPrefix(peerKey, "channel:"):
		peer = &tg.PeerChannel{ChannelID: 600}
	default:
		peer = &tg.PeerUser{UserID: 1}
	}
	return &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{Message: &tg.Message{
				ID:      id,
				PeerID:  peer,
				Out:     true,
				Date:    int(time.Now().Unix()),
				Message: "forwarded",
			}},
		},
	}
}

// The bug: the echo was only recorded when the destination happened to be the chat on screen, so
// forwarding anywhere else left nothing in storage and the message did not appear locally at all.
func TestForwardMessagesRecordsTheEchoWhenTheTargetIsNotFocused(t *testing.T) {
	c, db, acct := forwardTestClient(t)
	ctx := context.Background()
	// Looking at the source chat, forwarding elsewhere — the ordinary case.
	c.setFocusPeer("channel:500")
	c.forwardSend = func(_ context.Context, _ *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
		return forwardUpdates("channel:600", 4242), nil
	}
	events := make(chan Event, 32)

	c.forwardMessages(ctx, acct, &tg.Client{}, events, Command{
		PeerKey:       "channel:500",
		ForwardIDs:    []string{"11"},
		ForwardTarget: "channel:600",
	})

	stored, err := db.MessagesForPeer(ctx, acct, "channel:600", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range stored {
		if msg.ID == 4242 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the forwarded copy was not stored for the destination; stored %d messages", len(stored))
	}

	// And the UI is told, so an open destination updates without waiting for a history fetch.
	sawAppend := false
	for _, ev := range drainEvents(events) {
		if ev.Kind == EventMessages && ev.Append && ev.PeerKey == "channel:600" {
			sawAppend = true
		}
	}
	if !sawAppend {
		t.Fatal("no append event for the destination")
	}
}

// Saved Messages is a sentinel for the RPC but storage is keyed by peer, so the echo has to be
// filed under the real self:<id> row.
func TestForwardMessagesRecordsTheEchoForSavedMessages(t *testing.T) {
	c, db, acct := forwardTestClient(t)
	ctx := context.Background()
	if err := db.SavePeers(ctx, []storage.Peer{
		{AccountID: acct, Key: "self:1", Kind: "self", ID: 1, Title: "Saved Messages", LastMessageAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	c.forwardSend = func(_ context.Context, _ *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
		return forwardUpdates("self:1", 777), nil
	}
	events := make(chan Event, 32)

	c.forwardMessages(ctx, acct, &tg.Client{}, events, Command{
		PeerKey:       "channel:500",
		ForwardIDs:    []string{"11"},
		ForwardTarget: SavedMessagesTarget,
	})

	stored, err := db.MessagesForPeer(ctx, acct, "self:1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 {
		t.Fatal("the copy forwarded to Saved Messages was not stored under the self peer")
	}
	drainEvents(events)
}

func TestStorageKeyForTarget(t *testing.T) {
	c, _, acct := forwardTestClient(t)
	ctx := context.Background()

	if got := c.storageKeyForTarget(ctx, acct, "channel:600"); got != "channel:600" {
		t.Fatalf("ordinary target = %q, want it unchanged", got)
	}
	// acct is "user:1", so the self row is self:1.
	if got := c.storageKeyForTarget(ctx, acct, SavedMessagesTarget); got != "self:1" {
		t.Fatalf("Saved Messages target = %q, want self:1", got)
	}
}

func TestTelegramIDFromAccountID(t *testing.T) {
	cases := map[string]int64{
		"user:1234": 1234,
		"bot:99":    99,
		"unknown":   0,
		"":          0,
		"user:abc":  0,
	}
	for in, want := range cases {
		if got := telegramIDFromAccountID(in); got != want {
			t.Errorf("telegramIDFromAccountID(%q) = %d, want %d", in, got, want)
		}
	}
}
