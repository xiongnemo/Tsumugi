package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// A min entity's access hash is non-zero and unusable: Telegram sends min constructors for peers it
// mentions in passing, and their hash only works in the context it arrived in. Storing one overwrites
// a working hash with one that answers CHANNEL_INVALID, and the "never overwrite with zero" guard in
// SavePeers cannot catch it because the value is not zero.
//
// TDLib draws the same line - its min branch updates the title and flags and never touches
// access_hash - and returning zero here is what makes the storage guard keep the good one.
func TestMinEntitiesContributeNoAccessHash(t *testing.T) {
	if got := usableAccessHash(12345, true); got != 0 {
		t.Errorf("min entity contributed hash %d, want none", got)
	}
	if got := usableAccessHash(12345, false); got != 12345 {
		t.Errorf("non-min entity gave %d, want its hash", got)
	}
	if got := usableAccessHash(0, false); got != 0 {
		t.Errorf("a hashless non-min entity gave %d", got)
	}
}

func TestNormalizeChatDropsAMinChannelHash(t *testing.T) {
	min := &tg.Channel{ID: 500, AccessHash: 999, Title: "Seen in passing", Min: true}
	peer, _, ok := normalizeChat("acct", min)
	if !ok {
		t.Fatal("a min channel should still produce a peer for its title")
	}
	if peer.AccessHash != 0 {
		t.Fatalf("AccessHash = %d, want none from a min entity", peer.AccessHash)
	}
	if peer.Title != "Seen in passing" {
		t.Errorf("Title = %q; the title is still worth keeping", peer.Title)
	}

	full := &tg.Channel{ID: 500, AccessHash: 999, Title: "Real"}
	if peer, _, _ := normalizeChat("acct", full); peer.AccessHash != 999 {
		t.Fatalf("a non-min channel gave AccessHash = %d, want 999", peer.AccessHash)
	}
}

// The backfill retried unreachable peers every few seconds: 951 CHANNEL_INVALID errors in one session
// across fifty peers, which buried every other error in the log.
func TestTerminalBackfillErrors(t *testing.T) {
	for _, kind := range []string{"CHANNEL_INVALID", "CHANNEL_PRIVATE", "PEER_ID_INVALID", "CHAT_FORBIDDEN"} {
		if !terminalBackfillError(tgerr.New(400, kind)) {
			t.Errorf("%s should stop the retries", kind)
		}
	}
	// Everything transient stays retryable: being wrong in that direction silently stops syncing a
	// chat that was only briefly unavailable.
	for _, kind := range []string{"FLOOD_WAIT_30", "TIMEOUT", "INTERNAL_SERVER_ERROR", "MSG_ID_INVALID"} {
		if terminalBackfillError(tgerr.New(400, kind)) {
			t.Errorf("%s should stay retryable", kind)
		}
	}
	if terminalBackfillError(nil) {
		t.Error("nil is not a terminal error")
	}
	if terminalBackfillError(errNotRPC{}) {
		t.Error("a non-RPC error is not terminal")
	}
}

type errNotRPC struct{}

func (errNotRPC) Error() string { return "some local failure" }

func TestBackfillSkipsRemembersPerPeer(t *testing.T) {
	var skips backfillSkips
	if skips.skipped("channel:1") {
		t.Fatal("nothing is skipped to begin with")
	}
	skips.skip("channel:1", "CHANNEL_INVALID")
	if !skips.skipped("channel:1") {
		t.Error("the failing peer was not remembered")
	}
	if skips.skipped("channel:2") {
		t.Error("an unrelated peer was skipped")
	}
	// An empty key would skip every peer whose key failed to load.
	skips.skip("", "nonsense")
	if skips.skipped("") || skips.count() != 1 {
		t.Errorf("an empty key was recorded: count = %d", skips.count())
	}
}
