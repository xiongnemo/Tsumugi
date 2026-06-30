package storage

import (
	"context"
	"testing"
)

func TestClearGlobalPinsAndUpdateDialogFilterPinnedPeers(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := db.SavePeers(ctx, []Peer{
		{AccountID: "user:1", Key: "user:2", Kind: "user", ID: 2, Title: "Pin", Pinned: true, PinnedOrder: 1, FolderID: 0},
		{AccountID: "user:1", Key: "user:3", Kind: "user", ID: 3, Title: "Other", Pinned: true, PinnedOrder: 2, FolderID: 0},
		{AccountID: "user:1", Key: "user:4", Kind: "user", ID: 4, Title: "Archived", Pinned: true, PinnedOrder: 1, FolderID: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearGlobalPins(ctx, "user:1"); err != nil {
		t.Fatal(err)
	}
	peers, err := db.ListPeers(ctx, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		switch p.Key {
		case "user:4":
			if !p.Pinned {
				t.Fatal("archived peer pin should be preserved")
			}
		default:
			if p.Pinned {
				t.Fatalf("peer %s should have global pin cleared", p.Key)
			}
		}
	}
	if err := db.ApplyGlobalPins(ctx, "user:1", []string{"user:3", "user:2"}); err != nil {
		t.Fatal(err)
	}
	peers, err = db.ListPeers(ctx, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]Peer{}
	for _, p := range peers {
		byKey[p.Key] = p
	}
	if !byKey["user:2"].Pinned || byKey["user:2"].PinnedOrder != 2 {
		t.Fatalf("user:2 pin = %+v, want pinned order 2", byKey["user:2"])
	}
	if !byKey["user:3"].Pinned || byKey["user:3"].PinnedOrder != 1 {
		t.Fatalf("user:3 pin = %+v, want pinned order 1", byKey["user:3"])
	}
	if err := db.SaveDialogFilters(ctx, "user:1", []DialogFilter{
		{AccountID: "user:1", ID: 2, Title: "Work", Kind: "telegram"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDialogFilterPinnedPeers(ctx, "user:1", 2, []string{"user:9", "user:8"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListDialogFilters(ctx, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].PinnedPeers) != 2 || got[0].PinnedPeers[0] != "user:9" {
		t.Fatalf("pinned peers = %+v", got[0].PinnedPeers)
	}
}

func TestApplyGlobalPinsSelfKeyAliasesUserRow(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := db.SavePeers(ctx, []Peer{
		{AccountID: "user:1", Key: "user:2", Kind: "user", ID: 2, Title: "Saved", FolderID: 0},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyGlobalPins(ctx, "user:1", []string{"self:2"}); err != nil {
		t.Fatal(err)
	}
	peers, err := db.ListPeers(ctx, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || !peers[0].Pinned || peers[0].PinnedOrder != 1 {
		t.Fatalf("user:2 pin = %+v, want pinned order 1", peers[0])
	}
}

func TestSavePeersPinnedMainFolderClearsArchiveFolder(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := db.SavePeers(ctx, []Peer{
		{AccountID: "user:1", Key: "channel:9", Kind: "channel", ID: 9, Title: "News", FolderID: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePeers(ctx, []Peer{
		{AccountID: "user:1", Key: "channel:9", Kind: "channel", ID: 9, Title: "News", FolderID: 0, Pinned: true, PinnedOrder: 1},
	}); err != nil {
		t.Fatal(err)
	}
	peer, ok, err := db.Peer(ctx, "user:1", "channel:9")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if peer.FolderID != 0 || !peer.Pinned {
		t.Fatalf("channel:9 = %+v, want main-folder pin", peer)
	}
}
