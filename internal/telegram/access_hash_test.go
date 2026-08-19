package telegram

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/storage"
)

// peerFromRef produces a real title with a zero access hash whenever the update carried no entity
// for the peer. The merge only restored the hash inside the placeholder-title branch, so exactly
// that shape slipped through and the stored credential was lost.
func TestMergePeerActivityKeepsAccessHashWithARealTitle(t *testing.T) {
	existing := storage.Peer{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 8899, Title: "Group",
	}
	incoming := storage.Peer{
		AccountID: "acct", Key: "channel:600", Kind: "channel", ID: 600,
		AccessHash: 0, Title: "Group",
	}

	if got := mergePeerActivity(existing, incoming); got.AccessHash != 8899 {
		t.Fatalf("AccessHash = %d, want the stored 8899 kept", got.AccessHash)
	}
}

func TestMergePeerActivityTakesANewAccessHash(t *testing.T) {
	existing := storage.Peer{Key: "channel:600", Kind: "channel", ID: 600, AccessHash: 1111, Title: "Group"}
	incoming := storage.Peer{Key: "channel:600", Kind: "channel", ID: 600, AccessHash: 2222, Title: "Group"}

	if got := mergePeerActivity(existing, incoming); got.AccessHash != 2222 {
		t.Fatalf("AccessHash = %d, want the refreshed 2222", got.AccessHash)
	}
}

// The exact path that corrupted real data: a global search result for a peer whose entity was not
// in the reply.
func TestPeerFromRefWithoutEntityHasNoAccessHash(t *testing.T) {
	entities := entitiesByID{
		users:    map[int64]*tg.User{},
		chats:    map[int64]*tg.Chat{},
		channels: map[int64]*tg.Channel{},
	}

	got, ok := peerFromRef("acct", &tg.PeerChannel{ChannelID: 600}, entities)
	if !ok {
		t.Fatal("peerFromRef failed")
	}
	if got.AccessHash != 0 {
		t.Fatalf("AccessHash = %d; this test documents that it is 0, which is why the merge and the upsert both have to guard", got.AccessHash)
	}
}

// Telegram answers a zero hash with a bare PEER_ID_INVALID that names neither the peer nor the
// reason, so the request is not worth sending.
func TestInputPeerRefusesAZeroAccessHash(t *testing.T) {
	for _, kind := range []string{"user", "channel"} {
		_, err := inputPeer(storage.Peer{Key: kind + ":600", Kind: kind, ID: 600, AccessHash: 0, Title: "Group"})
		if err == nil {
			t.Fatalf("kind %q with no access hash should not build an input peer", kind)
		}
		if !strings.Contains(err.Error(), "access hash") {
			t.Fatalf("kind %q error = %q, want it to name the missing access hash", kind, err)
		}
	}
}

// Kinds that need no hash must keep working, or Saved Messages and basic groups break.
func TestInputPeerAllowsKindsWithoutAHash(t *testing.T) {
	if _, err := inputPeer(storage.Peer{Kind: "self", ID: 5}); err != nil {
		t.Fatalf("self: %v", err)
	}
	if _, err := inputPeer(storage.Peer{Kind: "chat", ID: 7}); err != nil {
		t.Fatalf("chat: %v", err)
	}
}
