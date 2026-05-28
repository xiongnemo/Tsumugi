package telegram

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/storage"
)

func TestApplyOutboxReadMarksPrivateOutgoingMessages(t *testing.T) {
	messages := []Message{
		{ID: "10", Outgoing: true, State: "synced"},
		{ID: "11", Outgoing: true, State: "synced"},
		{ID: "12", Outgoing: false, State: "synced"},
		{ID: "13", Outgoing: true, State: "pending"},
	}
	applyOutboxRead(messages, storage.Peer{Kind: "user", ReadOutboxMaxID: 11})
	if !messages[0].ReadByPeer || !messages[1].ReadByPeer {
		t.Fatalf("expected first two outgoing messages read: %+v", messages)
	}
	if messages[2].ReadByPeer || messages[3].ReadByPeer {
		t.Fatalf("incoming/pending should not be marked read: %+v", messages)
	}
}

func TestApplyOutboxReadSkipsGroups(t *testing.T) {
	messages := []Message{{ID: "10", Outgoing: true, State: "synced"}}
	applyOutboxRead(messages, storage.Peer{Kind: "chat", ReadOutboxMaxID: 99})
	if messages[0].ReadByPeer {
		t.Fatal("group chat should not get outbox read marks")
	}
}

func TestApplyOutboxReadMaxIDUpdatesMessages(t *testing.T) {
	messages := []Message{{ID: "5", Outgoing: true, State: "synced"}}
	applyOutboxReadMaxID(messages, 5)
	if !messages[0].ReadByPeer {
		t.Fatal("expected message marked read")
	}
}
