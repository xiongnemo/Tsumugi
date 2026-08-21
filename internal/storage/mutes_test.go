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

// The bug that made a client full of muted groups ring for all of them: Telegram reports no MuteUntil
// at all for a dialog that follows its type's default, and only 19 of 773 dialogs on the real account
// carried a per-chat value. Collapsing "no value" into 0 lost the other 754.
func TestMuteResolvesAgainstTheScopeDefault(t *testing.T) {
	const forever = 2147483647
	cases := []struct {
		name         string
		explicit     int
		hasExplicit  bool
		scopeDefault int
		want         int
	}{
		{"inherits a muted scope", MuteInherit, true, forever, forever},
		{"no row at all inherits too", 0, false, forever, forever},
		{"explicit unmute beats a muted scope", 0, true, forever, 0},
		{"explicit mute beats an unmuted scope", forever, true, 0, forever},
		{"inherits an unmuted scope", MuteInherit, true, 0, 0},
	}
	for _, tc := range cases {
		if got := ResolveMute(tc.explicit, tc.hasExplicit, tc.scopeDefault); got != tc.want {
			t.Errorf("%s: ResolveMute = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Telegram's "chats" scope covers basic groups and supergroups alike, while Tsumugi stores a
// supergroup as kind "channel" - so resolving a supergroup against the broadcast default would use
// the wrong setting entirely.
func TestPeerNotifyScope(t *testing.T) {
	cases := map[string]NotifyScope{
		"user|private":    NotifyScopeUsers,
		"self|":           NotifyScopeUsers,
		"chat|group":      NotifyScopeChats,
		"channel|group":   NotifyScopeChats,
		"channel|channel": NotifyScopeBroadcasts,
	}
	for input, want := range cases {
		kind, subtitle := input[:len(input)-len(want)], ""
		parts := 0
		for i := 0; i < len(input); i++ {
			if input[i] == '|' {
				kind, subtitle, parts = input[:i], input[i+1:], 1
				break
			}
		}
		if parts == 0 {
			t.Fatalf("bad case %q", input)
		}
		if got := PeerNotifyScope(kind, subtitle); got != want {
			t.Errorf("PeerNotifyScope(%q, %q) = %q, want %q", kind, subtitle, got, want)
		}
	}
}

// End to end through the query the chat list actually uses.
func TestListPeersResolvesInheritedMutes(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	const forever = 2147483647
	if err := db.SavePeers(ctx, []Peer{
		{AccountID: "acct", Key: "channel:1", Kind: "channel", ID: 1, Title: "Supergroup", Subtitle: "group"},
		{AccountID: "acct", Key: "channel:2", Kind: "channel", ID: 2, Title: "Broadcast", Subtitle: "channel"},
		{AccountID: "acct", Key: "user:3", Kind: "user", ID: 3, Title: "Alice", Subtitle: "private"},
	}); err != nil {
		t.Fatal(err)
	}
	// Every dialog follows its type, as almost all of them do in practice.
	for _, key := range []string{"channel:1", "channel:2", "user:3"} {
		if err := db.SetPeerMute(ctx, "acct", key, MuteInherit); err != nil {
			t.Fatal(err)
		}
	}
	// Groups muted, channels and people not.
	if err := db.SetNotifyDefault(ctx, "acct", NotifyScopeChats, forever); err != nil {
		t.Fatal(err)
	}
	if err := db.SetNotifyDefault(ctx, "acct", NotifyScopeBroadcasts, 0); err != nil {
		t.Fatal(err)
	}

	peers, err := db.ListPeers(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range peers {
		got[p.Key] = MuteActive(p.MuteUntil, time.Now())
	}
	if !got["channel:1"] {
		t.Error("a supergroup did not inherit the muted chats default")
	}
	if got["channel:2"] {
		t.Error("a broadcast inherited a mute it should not have")
	}
	if got["user:3"] {
		t.Error("a private chat inherited a mute it should not have")
	}
}
