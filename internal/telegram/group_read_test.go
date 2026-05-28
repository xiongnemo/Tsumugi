package telegram

import (
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"github.com/nemo/Tsumugi/internal/storage"
)

func TestPeerSupportsGroupReadMarks(t *testing.T) {
	cases := []struct {
		name string
		peer storage.Peer
		want bool
	}{
		{name: "basic group", peer: storage.Peer{Kind: "chat", Subtitle: "group"}, want: true},
		{name: "megagroup", peer: storage.Peer{Kind: "channel", Subtitle: "group"}, want: true},
		{name: "broadcast", peer: storage.Peer{Kind: "channel", Subtitle: "channel"}, want: false},
		{name: "private", peer: storage.Peer{Kind: "user", Subtitle: "private"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := peerSupportsGroupReadMarks(tc.peer); got != tc.want {
				t.Fatalf("peerSupportsGroupReadMarks() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelectRecentOutgoingGroupReadMessages(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	messages := []storage.Message{
		{ID: 1, Date: now.Add(-8 * 24 * time.Hour), Outgoing: true, State: "synced"},
		{ID: 2, Date: now.Add(-3 * time.Hour), Outgoing: false, State: "synced"},
		{ID: 3, Date: now.Add(-2 * time.Hour), Outgoing: true, State: "pending"},
		{ID: 4, Date: now.Add(-90 * time.Minute), Outgoing: true, State: "synced"},
		{ID: 5, Date: now.Add(-30 * time.Minute), Outgoing: true, State: "synced"},
	}
	got := selectRecentOutgoingGroupReadMessages(messages, now, 1)
	if len(got) != 1 || got[0].ID != 5 {
		t.Fatalf("selected = %+v, want only newest eligible ID 5", got)
	}
	got = selectRecentOutgoingGroupReadMessages(messages, now, 20)
	if len(got) != 2 || got[0].ID != 4 || got[1].ID != 5 {
		t.Fatalf("selected = %+v, want eligible IDs 4,5 in chronological order", got)
	}
}

func TestApplyGroupReadCountsOnlyForGroupOutgoing(t *testing.T) {
	client := &GotdClient{}
	if !client.setGroupReadCount("chat:1", 10, 3) {
		t.Fatal("expected count to change")
	}
	messages := []Message{
		{ID: "10", Outgoing: true, State: "synced"},
		{ID: "11", Outgoing: false, State: "synced"},
	}
	client.applyGroupReadCounts(messages, storage.Peer{Key: "chat:1", Kind: "chat", Subtitle: "group"})
	if messages[0].GroupReadCount != 3 {
		t.Fatalf("outgoing group message count = %d, want 3", messages[0].GroupReadCount)
	}
	if messages[1].GroupReadCount != 0 {
		t.Fatalf("incoming message should not get group read count: %+v", messages[1])
	}
	messages[0].GroupReadCount = 0
	client.applyGroupReadCounts(messages, storage.Peer{Key: "user:1", Kind: "user", Subtitle: "private"})
	if messages[0].GroupReadCount != 0 {
		t.Fatalf("private chat should not get group read count: %+v", messages[0])
	}
}

func TestGroupReadErrorClassification(t *testing.T) {
	if !groupReadUnsupportedError(tgerr.New(400, "CHAT_TOO_BIG")) {
		t.Fatal("CHAT_TOO_BIG should mark peer unsupported")
	}
	if !groupReadSkippableError(tgerr.New(400, "MSG_TOO_OLD")) {
		t.Fatal("MSG_TOO_OLD should be silently skippable")
	}
	if groupReadUnsupportedError(tgerr.New(500, "INTERNAL")) {
		t.Fatal("unrelated errors should not mark peer unsupported")
	}
}
