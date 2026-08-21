package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/storage"
)

func refreshTestClient(t *testing.T, peers int) *GotdClient {
	t.Helper()
	ctx := context.Background()
	db, _, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows := make([]storage.Peer, 0, peers)
	now := time.Now().UTC()
	for i := 0; i < peers; i++ {
		rows = append(rows, storage.Peer{
			AccountID: "acct", Key: peerKey("channel", int64(i)), Kind: "channel", ID: int64(i),
			Title: "chat", LastMessageAt: now,
		})
	}
	if err := db.SavePeers(ctx, rows); err != nil {
		t.Fatal(err)
	}
	return &GotdClient{store: db}
}

// A burst of messages must not rebuild the list once per message. That rebuild is the whole account:
// on a real one it was megabytes of allocation per arriving message, for a render the UI throttles to
// 750ms anyway.
func TestChatListRefreshCoalescesABurst(t *testing.T) {
	c := refreshTestClient(t, 20)
	events := make(chan Event, 256)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		c.scheduleChatListRefresh(ctx, "acct", events)
	}

	immediate := len(events)
	if immediate != 1 {
		t.Fatalf("%d refreshes during the burst, want exactly the leading one", immediate)
	}

	// The trailing refresh is what guarantees the last message of the burst is reflected.
	deadline := time.Now().Add(3 * time.Second)
	for len(events) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 2 {
		t.Fatalf("%d refreshes after the window, want the leading one plus one trailing", len(events))
	}
	for len(events) > 0 {
		event := <-events
		if event.Kind != EventChats || len(event.Chats) != 20 {
			t.Fatalf("event = %q with %d chats, want the full chat list", event.Kind, len(event.Chats))
		}
	}
}

// An arrival into an idle session refreshes straight away: waiting out the window would make the
// sidebar lag behind a message the user is looking at.
func TestChatListRefreshIsImmediateWhenIdle(t *testing.T) {
	c := refreshTestClient(t, 3)
	events := make(chan Event, 8)

	c.scheduleChatListRefresh(context.Background(), "acct", events)

	select {
	case event := <-events:
		if event.Kind != EventChats {
			t.Fatalf("event = %q, want EventChats", event.Kind)
		}
	default:
		t.Fatal("no refresh at all for the first arrival")
	}
}

// What the coalescer is buying, per arriving message.
func BenchmarkEmitChatList(b *testing.B) {
	ctx := context.Background()
	db, _, err := storage.Open(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	rows := make([]storage.Peer, 0, 852)
	now := time.Now().UTC()
	for i := 0; i < 852; i++ {
		rows = append(rows, storage.Peer{
			AccountID: "acct", Key: peerKey("channel", int64(i)), Kind: "channel", ID: int64(i),
			Title: "chat", LastMessageAt: now,
		})
	}
	if err := db.SavePeers(ctx, rows); err != nil {
		b.Fatal(err)
	}
	c := &GotdClient{store: db}
	events := make(chan Event, 1)
	go func() {
		for range events {
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.emitChatList(ctx, "acct", events)
	}
}
