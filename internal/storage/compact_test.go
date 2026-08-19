package storage

import (
	"context"
	"testing"
	"time"
)

// Pruning alone never gives a byte back: SQLite keeps the freed pages for its own reuse, so the file
// stays as large as it ever was. That is the whole reason the settings panel has a cleanup button
// rather than just a retention number, and this is the test that the button can actually deliver.
func TestCompactReleasesSpaceThatPruningOnlyFreedInternally(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:1", 4000, now.Add(-4000*time.Hour), time.Hour)

	grown, err := db.DatabaseFileBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.PruneMessagesForPeer(ctx, "acct", "chat:1", now, 0, 100000); err != nil {
		t.Fatal(err)
	}
	pruned, err := db.DatabaseFileBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pruned < grown {
		t.Fatalf("database shrank from %d to %d on its own; this test no longer proves anything", grown, pruned)
	}

	if err := db.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	compacted, err := db.DatabaseFileBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if compacted >= pruned {
		t.Fatalf("size after compaction = %d, want less than the %d left by pruning", compacted, pruned)
	}
}

// The database has to keep working afterwards: VACUUM rewrites every page, and a compaction that
// left the store unreadable would be a spectacular way to lose a message history.
func TestCompactKeepsWhatRetentionKept(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedAging(t, db, "chat:1", 300, now.Add(-300*time.Hour), time.Hour)

	if _, err := db.PruneMessagesForPeer(ctx, "acct", "chat:1", now, 100, 100000); err != nil {
		t.Fatal(err)
	}
	before := countMessages(t, db, "chat:1")
	if err := db.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if after := countMessages(t, db, "chat:1"); after != before {
		t.Fatalf("%d messages after compaction, want the %d that survived pruning", after, before)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2.0 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{3*1024*1024*1024 + 700*1024*1024, "3.7 GB"},
		// Beyond the largest unit it keeps counting in it rather than reporting nonsense.
		{4096 * 1024 * 1024 * 1024, "4096.0 GB"},
	}
	for _, tc := range cases {
		if got := FormatBytes(tc.in); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
