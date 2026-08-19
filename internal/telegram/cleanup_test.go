package telegram

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/storage"
)

const cleanupTestAccount = "user:1"

func cleanupTestClient(t *testing.T, retentionDays int) *GotdClient {
	t.Helper()
	ctx := context.Background()
	db, _, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetSetting(ctx, settings.KeyRetentionDays, strconv.Itoa(retentionDays)); err != nil {
		t.Fatal(err)
	}
	return &GotdClient{store: db}
}

// seedAgingMessages writes count messages ending `newest` ago, one per hour going back.
func seedAgingMessages(t *testing.T, db *storage.DB, peerKey string, count int, oldest time.Time) {
	t.Helper()
	msgs := make([]storage.Message, 0, count)
	for i := 0; i < count; i++ {
		msgs = append(msgs, storage.Message{
			AccountID: cleanupTestAccount, PeerKey: peerKey, ID: i + 1,
			Date:  oldest.Add(time.Duration(i) * time.Hour),
			Text:  fmt.Sprintf("message %d", i),
			State: "synced",
		})
	}
	if err := db.SaveMessages(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
}

func storedCount(t *testing.T, db *storage.DB, peerKey string) int {
	t.Helper()
	msgs, err := db.MessagesForPeer(context.Background(), cleanupTestAccount, peerKey, 1000000)
	if err != nil {
		t.Fatal(err)
	}
	return len(msgs)
}

// The background pruner takes ten peers a minute and yields to anything in the foreground. A user who
// has just turned retention down and pressed the button expects the whole database dealt with, so this
// path has to keep going until there is nothing left to remove.
func TestPruneToRetentionRunsToCompletion(t *testing.T) {
	c := cleanupTestClient(t, 30)
	// Small batches, so finishing takes several rounds per peer: the loop, not one big delete.
	defer func(prev int) { cleanupRowsPerPeer = prev }(cleanupRowsPerPeer)
	cleanupRowsPerPeer = 40

	old := time.Now().UTC().Add(-2000 * time.Hour)
	for _, peerKey := range []string{"chat:1", "chat:2", "chat:3"} {
		seedAgingMessages(t, c.store, peerKey, 900, old)
	}

	events := make(chan Event, 256)
	removed, err := c.pruneToRetention(context.Background(), cleanupTestAccount, events)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("nothing removed from 2000 hours of history with a 30-day window")
	}

	// Whatever is left has to be the keep floor or newer than the cutoff, never less.
	for _, peerKey := range []string{"chat:1", "chat:2", "chat:3"} {
		if left := storedCount(t, c.store, peerKey); left < RetentionKeepNewest {
			t.Fatalf("%s has %d messages left, want at least the protected %d", peerKey, left, RetentionKeepNewest)
		}
	}

	// A second pass has nothing to do. If it does, the first one stopped early.
	again, err := c.pruneToRetention(context.Background(), cleanupTestAccount, events)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("second pass removed %d more, so the first pass did not finish", again)
	}
}

// PrunablePeers reports that a peer holds old messages, not that any of them can be deleted. A quiet
// chat entirely under the keep floor is therefore on that list every single round, so a loop that only
// stopped when the list emptied would never stop at all.
func TestPruneToRetentionTerminatesOnProtectedPeers(t *testing.T) {
	c := cleanupTestClient(t, 30)
	// Everything is ancient, and there is far less than the keep floor, so nothing is deletable.
	seedAgingMessages(t, c.store, "chat:quiet", 5, time.Now().UTC().Add(-5000*time.Hour))

	done := make(chan int64, 1)
	go func() {
		removed, err := c.pruneToRetention(context.Background(), cleanupTestAccount, make(chan Event, 8))
		if err != nil {
			t.Error(err)
		}
		done <- removed
	}()
	select {
	case removed := <-done:
		if removed != 0 {
			t.Fatalf("removed %d from a chat that is entirely protected", removed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pruneToRetention did not return: it is asking for the same protected peer forever")
	}
	if left := storedCount(t, c.store, "chat:quiet"); left != 5 {
		t.Fatalf("%d messages left, want all 5 kept", left)
	}
}

// Keeping everything means keeping everything. The cleanup still runs — it compacts — but it must not
// delete a single message.
func TestPruneToRetentionKeepsEverythingWhenRetentionIsForever(t *testing.T) {
	c := cleanupTestClient(t, settings.RetentionForever)
	seedAgingMessages(t, c.store, "chat:1", 500, time.Now().UTC().Add(-20000*time.Hour))

	removed, err := c.pruneToRetention(context.Background(), cleanupTestAccount, make(chan Event, 8))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed %d with retention set to forever", removed)
	}
	if left := storedCount(t, c.store, "chat:1"); left != 500 {
		t.Fatalf("%d messages left, want all 500", left)
	}
}

func TestCleanupStorageReportsWhatItDid(t *testing.T) {
	c := cleanupTestClient(t, 30)
	seedAgingMessages(t, c.store, "chat:1", 900, time.Now().UTC().Add(-2000*time.Hour))

	events := make(chan Event, 256)
	c.cleanupStorage(context.Background(), cleanupTestAccount, events)
	close(events)

	var last Event
	for event := range events {
		if event.Kind != EventStorage {
			t.Fatalf("event kind = %q, want every cleanup report to be EventStorage", event.Kind)
		}
		if event.Error != nil {
			t.Fatalf("cleanup reported %v", event.Error)
		}
		last = event
	}
	if last.StatusMsg.Key != i18n.KeyStatusCleanupDone {
		t.Fatalf("final status = %q, want %q", last.StatusMsg.Key, i18n.KeyStatusCleanupDone)
	}
	if last.StorageRemoved == 0 {
		t.Fatal("final report says nothing was removed")
	}
	// The size after compaction is what the panel shows; a zero would blank it.
	if last.StorageBytes <= 0 {
		t.Fatalf("final size = %d, want the compacted size", last.StorageBytes)
	}
	if left := storedCount(t, c.store, "chat:1"); left != RetentionKeepNewest {
		t.Fatalf("%d messages left, want the protected %d", left, RetentionKeepNewest)
	}
}

// A second cleanup must not start while one is running: VACUUM holds the single database connection,
// so the two would queue up and double a freeze that is already the longest thing Tsumugi does.
func TestCleanupStorageRefusesWhenAlreadyRunning(t *testing.T) {
	c := cleanupTestClient(t, 30)
	c.cleanupRunning.Store(true)

	events := make(chan Event, 4)
	c.cleanupStorage(context.Background(), cleanupTestAccount, events)
	close(events)

	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want one refusal", len(got))
	}
	if !got[0].StorageBusy || got[0].StatusMsg.Key != i18n.KeyStatusCleanupBusy {
		t.Fatalf("event = %+v, want a busy report", got[0])
	}
}
