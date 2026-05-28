package telegram

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/storage"
)

func TestUniquePeersPrefersMainFolderAndPinned(t *testing.T) {
	peers := []storage.Peer{
		{Key: "user:1", FolderID: 0, Pinned: true, PinnedOrder: 1, Title: "Main"},
		{Key: "user:1", FolderID: 1, Pinned: false, Title: "Archive"},
		{Key: "user:2", FolderID: 0, Pinned: false, Title: "Unpinned"},
		{Key: "user:2", FolderID: 0, Pinned: true, PinnedOrder: 2, Title: "Pinned"},
	}
	got := uniquePeers(peers)
	if len(got) != 2 {
		t.Fatalf("uniquePeers() len = %d, want 2", len(got))
	}
	byKey := map[string]storage.Peer{got[0].Key: got[0], got[1].Key: got[1]}
	if !byKey["user:1"].Pinned || byKey["user:1"].FolderID != 0 {
		t.Fatalf("user:1 = %+v, want main pinned row", byKey["user:1"])
	}
	if !byKey["user:2"].Pinned {
		t.Fatalf("user:2 = %+v, want pinned row kept", byKey["user:2"])
	}
}

func TestPreferPeerKeepsMainOverArchive(t *testing.T) {
	main := storage.Peer{FolderID: 0, Pinned: true}
	archive := storage.Peer{FolderID: 1, Pinned: false}
	if !preferPeer(main, archive) {
		t.Fatal("expected to keep main-folder peer")
	}
	if preferPeer(archive, main) {
		t.Fatal("expected to replace archive row with main row")
	}
}
