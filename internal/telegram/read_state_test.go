package telegram

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/storage"
)

func TestFirstUnreadStorageID(t *testing.T) {
	window := []storage.Message{
		{ID: 10},
		{ID: 11},
		{ID: 12, Outgoing: true},
		{ID: 13},
		{ID: 14},
	}
	cases := []struct {
		name           string
		messages       []storage.Message
		readInboxMaxID int
		want           int
	}{
		{"oldest above the watermark", window, 11, 13},
		{"everything unread", window, 0, 10},
		{"everything read", window, 14, 0},
		// Our own sends are never unread. A chat whose newest message is ours would otherwise
		// "jump" to it and look like nothing happened.
		{"skips our own message", window, 11, 13},
		{"only outgoing above the watermark", []storage.Message{{ID: 20, Outgoing: true}}, 10, 0},
		// A normal outcome, not an error: the first unread id may be deleted or outside the
		// window we asked for.
		{"empty window", nil, 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstUnreadStorageID(tc.messages, tc.readInboxMaxID); got != tc.want {
				t.Fatalf("firstUnreadStorageID(_, %d) = %d, want %d", tc.readInboxMaxID, got, tc.want)
			}
		})
	}
}

// The window comes back newest-first from Telegram, so the helper must not just take the first
// match it sees.
func TestFirstUnreadStorageIDIgnoresOrdering(t *testing.T) {
	descending := []storage.Message{{ID: 14}, {ID: 13}, {ID: 12}, {ID: 11}}

	if got := firstUnreadStorageID(descending, 11); got != 12 {
		t.Fatalf("got %d, want the lowest unread id 12 regardless of slice order", got)
	}
}

func TestLocalUnreadAfterRead(t *testing.T) {
	cases := []struct {
		name         string
		unread       int
		topMessageID int
		maxID        int
		want         int
	}{
		// Opening a chat lands on the last message, which is the case that has to clear.
		{"read to the newest message", 5, 100, 100, 0},
		{"read past the newest message", 5, 100, 120, 0},
		// Ids are not dense, so the gap between watermarks is not a message count. -1 keeps the
		// stored value until the server's echo supplies the real one.
		{"partial read", 5, 100, 60, -1},
		{"already clear", 0, 100, 60, 0},
		// A peer we have never synced has no top message to compare against.
		{"unknown newest", 5, 0, 60, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := localUnreadAfterRead(tc.unread, tc.topMessageID, tc.maxID); got != tc.want {
				t.Fatalf("localUnreadAfterRead(%d, %d, %d) = %d, want %d",
					tc.unread, tc.topMessageID, tc.maxID, got, tc.want)
			}
		})
	}
}
