package state

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func TestStoreAppliesChatsAndMessages(t *testing.T) {
	store := NewStore()
	store.Apply(telegram.Event{
		StatusMsg: i18n.M(i18n.KeyStatusConnectedAs, "test"),
		Chats: []telegram.Chat{
			{ID: "b", Title: "Beta"},
			{ID: "a", Title: "Alpha", Pinned: true},
		},
		Messages: []telegram.Message{
			{ID: "1", ChatID: "a", Text: "hello"},
		},
	})

	snapshot := store.Snapshot()
	want := i18n.Tf(i18n.KeyStatusConnectedAs, "test")
	if snapshot.Status != want {
		t.Fatalf("status = %q, want %q", snapshot.Status, want)
	}
	if len(snapshot.Chats) != 2 || snapshot.Chats[0].ID != "a" {
		t.Fatalf("unexpected chats: %+v", snapshot.Chats)
	}
	if len(snapshot.Messages["a"]) != 1 {
		t.Fatalf("unexpected messages: %+v", snapshot.Messages)
	}
}
