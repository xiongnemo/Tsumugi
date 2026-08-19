package storage

import (
	"context"
	"testing"
	"time"
)

// An access hash cannot be re-derived locally, so losing it breaks every later RPC on that peer
// with PEER_ID_INVALID. Any peer built from an update that carried no entity for it has a zero
// hash, and the upsert used to write that straight over the stored one.
func TestSavePeersNeverZeroesAccessHash(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 8899, Title: "Group", LastMessageAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	// The shape a search result or an entity-less update produces: real title, no hash.
	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 0, Title: "Group", LastMessageAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	p, ok, err := db.Peer(ctx, "acct", "channel:600")
	if err != nil || !ok {
		t.Fatalf("Peer: %v ok=%v", err, ok)
	}
	if p.AccessHash != 8899 {
		t.Fatalf("AccessHash = %d, want the stored 8899 preserved", p.AccessHash)
	}
}

// A genuinely new hash must still land: Telegram rotates them.
func TestSavePeersAcceptsANewAccessHash(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 1111, Title: "Group", LastMessageAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 2222, Title: "Group", LastMessageAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	p, _, err := db.Peer(ctx, "acct", "channel:600")
	if err != nil {
		t.Fatal(err)
	}
	if p.AccessHash != 2222 {
		t.Fatalf("AccessHash = %d, want the refreshed 2222", p.AccessHash)
	}
}

// A restart re-syncs dialogs, which is the recovery path for a row already damaged before the
// guard existed. It only works if real hashes still overwrite zeros.
func TestSavePeersRepairsAZeroedAccessHash(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 0, Title: "Group", LastMessageAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 4242, Title: "Group", LastMessageAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	p, _, err := db.Peer(ctx, "acct", "channel:600")
	if err != nil {
		t.Fatal(err)
	}
	if p.AccessHash != 4242 {
		t.Fatalf("AccessHash = %d, want a dialog sync to repair it", p.AccessHash)
	}
}
