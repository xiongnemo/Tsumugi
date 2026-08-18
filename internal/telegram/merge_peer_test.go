package telegram

import (
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/storage"
)

func storedPeer() storage.Peer {
	return storage.Peer{
		AccountID:       "user:1",
		Key:             "chat:3",
		Kind:            "chat",
		ID:              3,
		Title:           "Nemo group",
		Pinned:          true,
		PinnedOrder:     2,
		Unread:          5,
		ReadOutboxMaxID: 100,
		FolderID:        1,
		FolderTitle:     "Archive",
		HistoryMinID:    42,
	}
}

// A dialog carries the server's own read state, so a sync must adopt it.
func TestMergeDialogPeerTakesServerReadState(t *testing.T) {
	dialog := storage.Peer{
		AccountID:       "user:1",
		Key:             "chat:3",
		Kind:            "chat",
		ID:              3,
		Title:           "Nemo group",
		Unread:          0,
		ReadOutboxMaxID: 200,
		LastMessageAt:   time.Unix(1700000000, 0).UTC(),
	}

	got := mergeDialogPeer(storedPeer(), dialog)
	if got.Unread != 0 {
		t.Fatalf("Unread = %d, want 0 from the dialog", got.Unread)
	}
	if got.ReadOutboxMaxID != 200 {
		t.Fatalf("ReadOutboxMaxID = %d, want 200 from the dialog", got.ReadOutboxMaxID)
	}
}

// The message-driven path carries no read state and must keep what is stored. This is the
// behaviour the dialog loop used to inherit, which pinned the badge to the first sync.
func TestMergePeerActivityKeepsStoredReadState(t *testing.T) {
	activity := storage.Peer{
		AccountID:     "user:1",
		Key:           "chat:3",
		Kind:          "chat",
		ID:            3,
		Title:         "Nemo group",
		LastMessageAt: time.Unix(1700000000, 0).UTC(),
	}

	got := mergePeerActivity(storedPeer(), activity)
	if got.Unread != 5 {
		t.Fatalf("Unread = %d, want the stored 5", got.Unread)
	}
	if got.ReadOutboxMaxID != 100 {
		t.Fatalf("ReadOutboxMaxID = %d, want the stored 100", got.ReadOutboxMaxID)
	}
}

// Read state is the only intended difference. Pinned ordering and folder placement in
// particular must stay identical, or the pinned-chats feature regresses.
func TestMergeDialogPeerDiffersOnlyInReadState(t *testing.T) {
	incoming := storage.Peer{
		AccountID: "user:1",
		Key:       "chat:3",
		Kind:      "chat",
		ID:        3,
		Title:     "group 3", // placeholder title, should be replaced from the store
		Unread:    0,
	}

	viaActivity := mergePeerActivity(storedPeer(), incoming)
	viaDialog := mergeDialogPeer(storedPeer(), incoming)

	// Normalise the one intended difference, then everything else must match.
	viaActivity.Unread = viaDialog.Unread
	viaActivity.ReadOutboxMaxID = viaDialog.ReadOutboxMaxID
	if viaActivity != viaDialog {
		t.Fatalf("merge results differ beyond read state:\nactivity = %+v\ndialog   = %+v", viaActivity, viaDialog)
	}

	if !viaDialog.Pinned || viaDialog.PinnedOrder != 2 {
		t.Fatalf("pinned state lost: pinned=%v order=%d", viaDialog.Pinned, viaDialog.PinnedOrder)
	}
	if viaDialog.FolderID != 1 || viaDialog.FolderTitle != "Archive" {
		t.Fatalf("folder lost: id=%d title=%q", viaDialog.FolderID, viaDialog.FolderTitle)
	}
	if viaDialog.HistoryMinID != 42 {
		t.Fatalf("HistoryMinID = %d, want the stored 42", viaDialog.HistoryMinID)
	}
	if viaDialog.Title != "Nemo group" {
		t.Fatalf("Title = %q, want the stored title to replace the placeholder", viaDialog.Title)
	}
}
