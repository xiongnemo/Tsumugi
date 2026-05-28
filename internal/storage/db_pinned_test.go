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
