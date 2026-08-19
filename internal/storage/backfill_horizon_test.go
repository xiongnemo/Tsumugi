package storage

import (
	"context"
	"testing"
	"time"
)

func seedPeerWithOldest(t *testing.T, db *DB, key string, lastMessage, oldest time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: key, Kind: "user", ID: 1, AccessHash: 2,
		Title: key, LastMessageAt: lastMessage,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveMessages(ctx, []Message{{
		AccountID: "acct", PeerKey: key, ID: 1, Date: oldest, Text: "old", State: "synced",
	}}); err != nil {
		t.Fatal(err)
	}
}

// The bug this closes: the backfill had no notion of far enough, so it walked every recent peer's
// history backwards a page per round forever. On a real account that meant six million messages and
// a 3.7GB database in about a day.
func TestRecentPeersForBackfillStopsAtTheHorizon(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cutoff := now.Add(-7 * 24 * time.Hour)
	horizon := now.Add(-30 * 24 * time.Hour)

	// Reached back past the horizon already: nothing left to do.
	seedPeerWithOldest(t, db, "user:done", now.Add(-time.Hour), now.Add(-40*24*time.Hour))
	// Only has recent history: still needs backfilling.
	seedPeerWithOldest(t, db, "user:shallow", now.Add(-time.Hour), now.Add(-2*24*time.Hour))

	got, err := db.RecentPeersForBackfill(ctx, "acct", cutoff, horizon, 20)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, p := range got {
		keys[p.Key] = true
	}
	if keys["user:done"] {
		t.Error("a peer whose history already reaches past the horizon must drop out of the candidate set")
	}
	if !keys["user:shallow"] {
		t.Error("a peer with only recent history still needs backfilling")
	}
}

// A steady state has to do no work at all, or the loop is still a treadmill.
func TestRecentPeersForBackfillConvergesToEmpty(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cutoff := now.Add(-7 * 24 * time.Hour)
	horizon := now.Add(-30 * 24 * time.Hour)

	for _, key := range []string{"user:a", "user:b", "user:c"} {
		seedPeerWithOldest(t, db, key, now.Add(-time.Hour), now.Add(-45*24*time.Hour))
	}

	got, err := db.RecentPeersForBackfill(ctx, "acct", cutoff, horizon, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d candidates, want none once every peer has reached the horizon", len(got))
	}
}

// Peers outside the recency window are not backfilled at all, horizon or not.
func TestRecentPeersForBackfillRespectsTheRecencyCutoff(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	seedPeerWithOldest(t, db, "user:stale", now.Add(-90*24*time.Hour), now.Add(-91*24*time.Hour))
	seedPeerWithOldest(t, db, "user:active", now.Add(-time.Hour), now.Add(-2*24*time.Hour))

	got, err := db.RecentPeersForBackfill(ctx, "acct", now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "user:active" {
		t.Fatalf("candidates = %+v, want only the active peer", got)
	}
}

// A peer with no stored messages at all is the first thing that should be backfilled.
func TestRecentPeersForBackfillIncludesEmptyPeers(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: "user:new", Kind: "user", ID: 9, AccessHash: 3,
		Title: "New", LastMessageAt: now.Add(-time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := db.RecentPeersForBackfill(ctx, "acct", now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "user:new" {
		t.Fatalf("candidates = %+v, want the peer with no history", got)
	}
}
