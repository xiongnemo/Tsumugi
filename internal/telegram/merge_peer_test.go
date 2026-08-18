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
		ReadInboxMaxID:  90,
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
		ReadInboxMaxID:  190,
		LastMessageAt:   time.Unix(1700000000, 0).UTC(),
	}

	got := mergeDialogPeer(storedPeer(), dialog)
	if got.Unread != 0 {
		t.Fatalf("Unread = %d, want 0 from the dialog", got.Unread)
	}
	if got.ReadOutboxMaxID != 200 {
		t.Fatalf("ReadOutboxMaxID = %d, want 200 from the dialog", got.ReadOutboxMaxID)
	}
	if got.ReadInboxMaxID != 190 {
		t.Fatalf("ReadInboxMaxID = %d, want 190 from the dialog", got.ReadInboxMaxID)
	}
}

// A local mark-read runs ahead of the server. A dialog sync that has not caught up yet must not
// drag the read pointer back, or the next chat open jumps to messages already read.
func TestMergeDialogPeerNeverWalksReadInboxBackwards(t *testing.T) {
	existing := storedPeer()
	existing.ReadInboxMaxID = 300

	got := mergeDialogPeer(existing, storage.Peer{
		AccountID:      "user:1",
		Key:            "chat:3",
		Kind:           "chat",
		ID:             3,
		Title:          "Nemo group",
		ReadInboxMaxID: 190,
	})

	if got.ReadInboxMaxID != 300 {
		t.Fatalf("ReadInboxMaxID = %d, want the local 300 kept", got.ReadInboxMaxID)
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
	if got.ReadInboxMaxID != 90 {
		t.Fatalf("ReadInboxMaxID = %d, want the stored 90", got.ReadInboxMaxID)
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
	viaActivity.ReadInboxMaxID = viaDialog.ReadInboxMaxID
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
