package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func seedAging(t *testing.T, db *DB, peerKey string, count int, oldest time.Time, step time.Duration) {
	t.Helper()
	ctx := context.Background()
	msgs := make([]Message, 0, count)
	for i := 0; i < count; i++ {
		msgs = append(msgs, Message{
			AccountID: "acct", PeerKey: peerKey, ID: i + 1,
			Date: oldest.Add(time.Duration(i) * step),
			Text: fmt.Sprintf("message %d", i), State: "synced",
		})
	}
	if err := db.SaveMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
}

func countMessages(t *testing.T, db *DB, peerKey string) int {
	t.Helper()
	msgs, err := db.MessagesForPeer(context.Background(), "acct", peerKey, 100000)
	if err != nil {
		t.Fatal(err)
	}
	return len(msgs)
}

func TestPruneRemovesOldMessages(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	// 400 messages, one per day going back.
	seedAging(t, db, "chat:1", 400, now.Add(-400*24*time.Hour), 24*time.Hour)

	removed, err := db.PruneMessagesForPeer(ctx, "acct", "chat:1", now.Add(-60*24*time.Hour), 200, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("nothing was pruned from 400 days of history")
	}
	if left := countMessages(t, db, "chat:1"); left < 200 {
		t.Fatalf("%d messages left, want at least the protected 200", left)
	}
}

// An inactive chat must never be emptied: opening it offline has to still show something, however
// old that conversation is. Retention that can blank a chat is worse than a large database.
func TestPruneNeverEmptiesAChat(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	// Everything is ancient and there are fewer messages than the keep floor.
	seedAging(t, db, "chat:old", 50, now.Add(-2000*24*time.Hour), 24*time.Hour)

	removed, err := db.PruneMessagesForPeer(ctx, "acct", "chat:old", now.Add(-60*24*time.Hour), 200, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed %d from a chat with fewer than the keep floor", removed)
	}
	if left := countMessages(t, db, "chat:old"); left != 50 {
		t.Fatalf("%d messages left, want all 50 kept", left)
	}
}

// The keep floor applies even when every message is older than the cutoff.
func TestPruneKeepsNewestEvenWhenAllAreStale(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:1", 500, now.Add(-1000*24*time.Hour), 24*time.Hour)

	if _, err := db.PruneMessagesForPeer(ctx, "acct", "chat:1", now, 200, 100000); err != nil {
		t.Fatal(err)
	}
	if left := countMessages(t, db, "chat:1"); left != 200 {
		t.Fatalf("%d messages left, want exactly the protected 200", left)
	}
}

// A pending or failed row is the only copy of text the user typed and has not sent. Age is not a
// reason to destroy it.
func TestPruneNeverDeletesUnsentMessages(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:1", 400, now.Add(-400*24*time.Hour), 24*time.Hour)
	if err := db.SaveMessages(ctx, []Message{
		{AccountID: "acct", PeerKey: "chat:1", ID: 900001, Date: now.Add(-399 * 24 * time.Hour),
			Text: "never sent", State: "pending"},
		{AccountID: "acct", PeerKey: "chat:1", ID: 900002, Date: now.Add(-398 * 24 * time.Hour),
			Text: "failed to send", State: "failed"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.PruneMessagesForPeer(ctx, "acct", "chat:1", now, 0, 100000); err != nil {
		t.Fatal(err)
	}

	msgs, err := db.MessagesForPeer(ctx, "acct", "chat:1", 100000)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]bool{}
	for _, m := range msgs {
		states[m.State] = true
	}
	if !states["pending"] || !states["failed"] {
		t.Fatalf("unsent messages were pruned; remaining states = %v", states)
	}
}

// Batched, so a multi-million-row table never becomes one long write lock and a WAL the size of
// what it deleted.
func TestPruneRespectsTheBatchLimit(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:1", 400, now.Add(-400*24*time.Hour), 24*time.Hour)

	removed, err := db.PruneMessagesForPeer(ctx, "acct", "chat:1", now, 0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 25 {
		t.Fatalf("removed %d, want exactly the batch limit of 25", removed)
	}
}

func TestPrunablePeersRanksStalestFirst(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:few", 5, now.Add(-500*24*time.Hour), 24*time.Hour)
	seedAging(t, db, "chat:many", 100, now.Add(-500*24*time.Hour), 24*time.Hour)

	peers, err := db.PrunablePeers(ctx, "acct", now.Add(-60*24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) == 0 || peers[0] != "chat:many" {
		t.Fatalf("peers = %v, want the peer with the most stale history first", peers)
	}
}

func TestPrunablePeersEmptyWhenNothingIsStale(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:1", 10, now.Add(-3*24*time.Hour), time.Hour)

	peers, err := db.PrunablePeers(ctx, "acct", now.Add(-60*24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 0 {
		t.Fatalf("peers = %v, want none when all history is recent", peers)
	}
}

// keepNewest of zero means no floor at all, not "keep one".
func TestPruneWithNoKeepFloorDeletesEverythingStale(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:1", 40, now.Add(-400*24*time.Hour), 24*time.Hour)

	if _, err := db.PruneMessagesForPeer(ctx, "acct", "chat:1", now, 0, 100000); err != nil {
		t.Fatal(err)
	}
	if left := countMessages(t, db, "chat:1"); left != 0 {
		t.Fatalf("%d messages left with no keep floor, want none", left)
	}
}
