package storage

import (
	"context"
	"testing"
	"time"
)

func savePeerForRead(t *testing.T, db *DB, key, kind string, telegramID int64, unread int) {
	t.Helper()
	err := db.SavePeers(context.Background(), []Peer{{
		AccountID:     "acct",
		Key:           key,
		Kind:          kind,
		ID:            telegramID,
		Title:         key,
		Unread:        unread,
		LastMessageAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdatePeerReadInboxIsMonotonic(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	savePeerForRead(t, db, "user:1", "user", 1, 5)

	if err := db.UpdatePeerReadInbox(ctx, "acct", "user:1", 100, 0); err != nil {
		t.Fatal(err)
	}
	// Updates can arrive out of order; a lower watermark must not undo a higher one, or the
	// unread jump would send the user back to messages they have already read.
	if err := db.UpdatePeerReadInbox(ctx, "acct", "user:1", 40, 3); err != nil {
		t.Fatal(err)
	}

	p, ok, err := db.Peer(ctx, "acct", "user:1")
	if err != nil || !ok {
		t.Fatalf("Peer: %v ok=%v", err, ok)
	}
	if p.ReadInboxMaxID != 100 {
		t.Errorf("ReadInboxMaxID = %d, want 100 (never walked back)", p.ReadInboxMaxID)
	}
	// The unread count is not monotonic: StillUnreadCount from the server is authoritative and
	// legitimately rises when new messages arrive.
	if p.Unread != 3 {
		t.Errorf("Unread = %d, want the server's 3", p.Unread)
	}
}

func TestUpdatePeerReadInboxLeavesUnreadAloneWhenNegative(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	savePeerForRead(t, db, "user:1", "user", 1, 7)

	if err := db.UpdatePeerReadInbox(ctx, "acct", "user:1", 50, -1); err != nil {
		t.Fatal(err)
	}

	p, _, err := db.Peer(ctx, "acct", "user:1")
	if err != nil {
		t.Fatal(err)
	}
	if p.ReadInboxMaxID != 50 {
		t.Errorf("ReadInboxMaxID = %d, want 50", p.ReadInboxMaxID)
	}
	if p.Unread != 7 {
		t.Errorf("Unread = %d, want 7 kept — a partial read cannot compute a count locally", p.Unread)
	}
}

func TestSavePeersReadInboxMaxIDIsMonotonic(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	savePeerForRead(t, db, "user:1", "user", 1, 0)

	if err := db.UpdatePeerReadInbox(ctx, "acct", "user:1", 100, 0); err != nil {
		t.Fatal(err)
	}
	// A dialog sync that has not yet seen our local mark-read must not undo it.
	savePeerForRead(t, db, "user:1", "user", 1, 4)

	p, _, err := db.Peer(ctx, "acct", "user:1")
	if err != nil {
		t.Fatal(err)
	}
	if p.ReadInboxMaxID != 100 {
		t.Errorf("ReadInboxMaxID = %d, want 100 preserved through a dialog sync", p.ReadInboxMaxID)
	}
}

// Saved Messages is stored as self:<id> but a read update for it arrives as a PeerUser, so
// resolution has to go through the stored kind rather than building the key from the update.
func TestPeerKeyForTelegramIDFindsSavedMessages(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	savePeerForRead(t, db, "self:42", "self", 42, 0)

	key, ok, err := db.PeerKeyForTelegramID(ctx, "acct", 42, "user", "self")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || key != "self:42" {
		t.Fatalf("key = %q ok=%v, want self:42", key, ok)
	}
}

// Telegram ids are only unique within a peer type, so the kind filter is load-bearing.
func TestPeerKeyForTelegramIDRespectsKinds(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	savePeerForRead(t, db, "user:7", "user", 7, 0)
	savePeerForRead(t, db, "chat:7", "chat", 7, 0)

	key, ok, err := db.PeerKeyForTelegramID(ctx, "acct", 7, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || key != "chat:7" {
		t.Fatalf("key = %q ok=%v, want chat:7 and not the same-id user", key, ok)
	}
}

func TestPeerKeyForTelegramIDMissing(t *testing.T) {
	db := testDB(t)

	_, ok, err := db.PeerKeyForTelegramID(context.Background(), "acct", 99, "user", "self")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("want not found for an unsynced peer")
	}
}
