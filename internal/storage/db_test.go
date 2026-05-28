package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/network"
	"github.com/nemo/Tsumugi/internal/secure"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, _, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(cipher)
	return db
}

func TestSavePeersAndMessages(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := db.SavePeers(ctx, []Peer{{
		AccountID:  "user:1",
		Key:        "user:2",
		Kind:       "user",
		ID:         2,
		AccessHash: 42,
		Title:      "Nemo",
	}}); err != nil {
		t.Fatal(err)
	}
	peer, ok, err := db.Peer(ctx, "user:1", "user:2")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || peer.Title != "Nemo" || peer.AccessHash != 42 {
		t.Fatalf("unexpected peer: %+v ok=%v", peer, ok)
	}

	msg := Message{
		AccountID: "user:1",
		PeerKey:   "user:2",
		ID:        10,
		Date:      time.Unix(100, 0),
		Text:      "secret message",
		State:     "synced",
	}
	if err := db.SaveMessages(ctx, []Message{msg}); err != nil {
		t.Fatal(err)
	}
	messages, err := db.MessagesForPeer(ctx, "user:1", "user:2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Text != "secret message" {
		t.Fatalf("unexpected messages: %+v", messages)
	}
}

func TestMessageByID(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := db.SavePeers(ctx, []Peer{{
		AccountID: "user:1",
		Key:       "user:2",
		Kind:      "user",
		ID:        2,
		Title:     "Nemo",
	}}); err != nil {
		t.Fatal(err)
	}
	msg := Message{
		AccountID: "user:1",
		PeerKey:   "user:2",
		ID:        -55,
		Date:      time.Unix(100, 0),
		Text:      "failed send",
		State:     "failed",
		Outgoing:  true,
	}
	if err := db.SaveMessages(ctx, []Message{msg}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.MessageByID(ctx, "user:1", "user:2", -55)
	if err != nil || !ok {
		t.Fatalf("MessageByID: ok=%v err=%v", ok, err)
	}
	if got.Text != "failed send" || got.State != "failed" || !got.Outgoing {
		t.Fatalf("unexpected row: %+v", got)
	}
	_, ok, err = db.MessageByID(ctx, "user:1", "user:2", 999)
	if err != nil || ok {
		t.Fatalf("expected missing message ok=%v err=%v", ok, err)
	}
}

func TestPeerKeysForMessageIDs(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	msgs := []Message{
		{AccountID: "user:1", PeerKey: "chat:1", ID: 10, Date: time.Unix(10, 0), Text: "one", State: "synced"},
		{AccountID: "user:1", PeerKey: "chat:2", ID: 20, Date: time.Unix(20, 0), Text: "two", State: "synced"},
		{AccountID: "user:1", PeerKey: "chat:3", ID: 30, Date: time.Unix(30, 0), Text: "deleted", State: "deleted"},
	}
	if err := db.SaveMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	got, err := db.PeerKeysForMessageIDs(ctx, "user:1", []int{10, 20, 30})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "chat:1,chat:2" {
		t.Fatalf("peer keys = %+v", got)
	}
}

func TestOlderMessagesForPeerExcludesLocalNegativeIDs(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	msgs := []Message{
		{AccountID: "user:1", PeerKey: "chat:1", ID: -1, Date: time.Unix(30, 0), Text: "failed", State: "failed"},
		{AccountID: "user:1", PeerKey: "chat:1", ID: 9, Date: time.Unix(9, 0), Text: "older", State: "synced"},
		{AccountID: "user:1", PeerKey: "chat:1", ID: 10, Date: time.Unix(10, 0), Text: "anchor", State: "synced"},
	}
	if err := db.SaveMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	got, err := db.OlderMessagesForPeer(ctx, "user:1", "chat:1", 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 9 {
		t.Fatalf("older messages = %+v, want only id 9", got)
	}
}

func TestListPeersOrdersPinnedThenRecentActivity(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	now := time.Unix(200, 0)
	if err := db.SavePeers(ctx, []Peer{
		{AccountID: "user:1", Key: "user:2", Kind: "user", ID: 2, Title: "Old", LastMessageAt: now.Add(-time.Hour)},
		{AccountID: "user:1", Key: "user:3", Kind: "user", ID: 3, Title: "New", LastMessageAt: now},
		{AccountID: "user:1", Key: "user:4", Kind: "user", ID: 4, Title: "Pin", Pinned: true, PinnedOrder: 1, LastMessageAt: now.Add(-2 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	peers, err := db.ListPeers(ctx, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{peers[0].Key, peers[1].Key, peers[2].Key}; strings.Join(got, ",") != "user:4,user:3,user:2" {
		t.Fatalf("unexpected peer order: %+v", peers)
	}
}

func TestDialogFiltersRoundTripRules(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	in := []DialogFilter{
		{AccountID: "user:1", ID: 0, Title: "All", Kind: "all"},
		{AccountID: "user:1", ID: -10, Title: "Archived chats", Kind: "archive", Archive: true},
		{
			AccountID:       "user:1",
			ID:              2,
			Title:           "Work",
			Kind:            "telegram",
			Groups:          true,
			ExcludeArchived: true,
			IncludePeers:    []string{"user:2"},
			ExcludePeers:    []string{"channel:3"},
			PinnedPeers:     []string{"chat:4"},
		},
	}
	if err := db.SaveDialogFilters(ctx, "user:1", in); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListDialogFilters(ctx, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !got[1].Archive || !got[2].Groups || !got[2].ExcludeArchived {
		t.Fatalf("unexpected filters: %+v", got)
	}
	if strings.Join(got[2].IncludePeers, ",") != "user:2" || strings.Join(got[2].PinnedPeers, ",") != "chat:4" {
		t.Fatalf("rules did not round-trip: %+v", got[2])
	}
}

func TestProxyProfilesRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	id, err := db.SaveProxyProfile(ctx, ProxyProfile{
		Name: "Local SOCKS",
		Config: network.ProxyConfig{
			Kind:     network.ProxySOCKS5,
			Source:   network.SourceManual,
			Address:  "127.0.0.1:1080",
			Username: "user",
			Password: "pass",
		},
		Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected inserted id")
	}

	profiles, err := db.ListProxyProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Config.Password != "pass" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}
}
