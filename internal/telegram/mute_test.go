package telegram

import (
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/storage"
)

// Every path that turns peers into chats goes through peersToChats, which is why the mute belongs
// here: applied at the emit sites instead, the startup sync missed it and the bell rang for muted
// groups.
func TestPeersToChatsCarriesTheMute(t *testing.T) {
	chats := peersToChats([]storage.Peer{
		{Key: "chat:1", Kind: "chat", Title: "Muted forever", MuteUntil: 2147483647},
		{Key: "chat:2", Kind: "chat", Title: "Not muted"},
		{Key: "chat:3", Kind: "chat", Title: "Mute expired", MuteUntil: int(time.Now().Add(-time.Hour).Unix())},
	})
	byID := map[string]Chat{}
	for _, chat := range chats {
		byID[chat.ID] = chat
	}
	if !byID["chat:1"].Muted {
		t.Error("a muted chat was published as unmuted")
	}
	if byID["chat:2"].Muted {
		t.Error("an unmuted chat was published as muted")
	}
	// A deadline in the past is not a mute: Telegram's timed mutes simply run out.
	if byID["chat:3"].Muted {
		t.Error("an expired mute still read as muted")
	}
}
