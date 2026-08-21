package storage

import (
	"context"
	"testing"
	"time"
)

// Mute lives in its own table because SavePeers is a full-column upsert run on every incoming
// message. This is the test that says so: saving a peer must not disturb the mute.
func TestMuteSurvivesAPeerSave(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.SetPeerMute(ctx, "acct", "chat:1", 2147483647); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePeers(ctx, []Peer{{AccountID: "acct", Key: "chat:1", Kind: "chat", ID: 1, Title: "Group"}}); err != nil {
		t.Fatal(err)
	}
	if !db.PeerMuted(ctx, "acct", "chat:1") {
		t.Fatal("saving the peer cleared its mute")
	}
}

func TestPeerMutesRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.SetPeerMute(ctx, "acct", "chat:1", 2147483647); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPeerMute(ctx, "acct", "chat:2", 0); err != nil {
		t.Fatal(err)
	}
	mutes, err := db.PeerMutes(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	if len(mutes) != 1 {
		t.Fatalf("mutes = %v, want only the muted chat", mutes)
	}
	if _, ok := mutes["chat:1"]; !ok {
		t.Fatalf("mutes = %v, want chat:1", mutes)
	}

	// Unmuting has to be storable, which is why this is a deadline and not a flag.
	if err := db.SetPeerMute(ctx, "acct", "chat:1", 0); err != nil {
		t.Fatal(err)
	}
	if db.PeerMuted(ctx, "acct", "chat:1") {
		t.Fatal("unmuting did not take")
	}
}

// Telegram writes a very distant deadline to mean "forever", so a stored value must be compared
// against now rather than treated as a date to show or as something long expired.
func TestMuteActive(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		until int
		want  bool
	}{
		{0, false},
		{-1, false},
		{int(now.Add(-time.Hour).Unix()), false},
		{int(now.Add(time.Hour).Unix()), true},
		{2147483647, true},
	}
	for _, tc := range cases {
		if got := MuteActive(tc.until, now); got != tc.want {
			t.Errorf("MuteActive(%d) = %v, want %v", tc.until, got, tc.want)
		}
	}
}

// The bug this exists to prevent: mute was applied by the callers, four separate places emit a chat
// list, and two of them were missed - so the startup sync published every chat as unmuted and the
// bell rang for muted groups. Filling it in ListPeers means no caller can forget.
func TestListPeersCarriesTheMute(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := db.SavePeers(ctx, []Peer{
		{AccountID: "acct", Key: "chat:1", Kind: "chat", ID: 1, Title: "Muted"},
		{AccountID: "acct", Key: "chat:2", Kind: "chat", ID: 2, Title: "Loud"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPeerMute(ctx, "acct", "chat:1", 2147483647); err != nil {
		t.Fatal(err)
	}

	peers, err := db.ListPeers(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]Peer{}
	for _, p := range peers {
		byKey[p.Key] = p
	}
	if !MuteActive(byKey["chat:1"].MuteUntil, time.Now()) {
		t.Errorf("chat:1 came back unmuted: MuteUntil = %d", byKey["chat:1"].MuteUntil)
	}
	if MuteActive(byKey["chat:2"].MuteUntil, time.Now()) {
		t.Errorf("chat:2 came back muted: MuteUntil = %d", byKey["chat:2"].MuteUntil)
	}
}
