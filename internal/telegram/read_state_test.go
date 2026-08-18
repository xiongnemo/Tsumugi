package telegram

import "testing"

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
