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

func TestSecretSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := db.SetSecretSetting(ctx, "telegram.api_hash", "secret-hash"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetSecretSetting(ctx, "telegram.api_hash")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != "secret-hash" {
		t.Fatalf("secret setting = %q ok=%v", got, ok)
	}
	if err := db.DeleteSettings(ctx, "telegram.api_hash"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GetSecretSetting(ctx, "telegram.api_hash"); err != nil || ok {
		t.Fatalf("deleted secret setting ok=%v err=%v", ok, err)
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

func TestServiceMessageRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := db.SaveMessages(ctx, []Message{{
		AccountID:  "user:1",
		PeerKey:    "chat:3",
		ID:         512,
		Date:       time.Now().UTC(),
		State:      "synced",
		ServiceKey: "service.users_added",
		ServiceArg: "Ada Lovelace",
	}, {
		AccountID: "user:1",
		PeerKey:   "chat:3",
		ID:        513,
		Date:      time.Now().UTC(),
		State:     "synced",
		Text:      "ordinary",
	}}); err != nil {
		t.Fatal(err)
	}

	messages, err := db.MessagesForPeer(ctx, "user:1", "chat:3", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}

	byID := map[int]Message{}
	for _, m := range messages {
		byID[m.ID] = m
	}
	if got := byID[512]; got.ServiceKey != "service.users_added" || got.ServiceArg != "Ada Lovelace" {
		t.Fatalf("service row = %q/%q", got.ServiceKey, got.ServiceArg)
	}
	if got := byID[513]; got.ServiceKey != "" || got.ServiceArg != "" {
		t.Fatalf("ordinary row picked up service fields: %q/%q", got.ServiceKey, got.ServiceArg)
	}
}

func TestPeerPreviewKeyRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := db.SavePeers(ctx, []Peer{{
		AccountID:      "user:1",
		Key:            "chat:3",
		Kind:           "chat",
		ID:             3,
		Title:          "Nemo",
		LastPreview:    "added Ada",
		LastPreviewKey: "service.users_added",
		LastPreviewArg: "Ada",
	}}); err != nil {
		t.Fatal(err)
	}

	peer, ok, err := db.Peer(ctx, "user:1", "chat:3")
	if err != nil || !ok {
		t.Fatalf("Peer: %v ok=%v", err, ok)
	}
	if peer.LastPreviewKey != "service.users_added" || peer.LastPreviewArg != "Ada" {
		t.Fatalf("peer preview = %q/%q", peer.LastPreviewKey, peer.LastPreviewArg)
	}

	peers, err := db.ListPeers(ctx, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].LastPreviewKey != "service.users_added" {
		t.Fatalf("ListPeers = %+v", peers)
	}
}

func TestDraftRoundTripSealsText(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := db.SaveLocalDraft(ctx, Draft{
		AccountID: "user:1",
		PeerKey:   "chat:3",
		Text:      "half typed 中文",
		ReplyToID: 512,
	}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := db.Draft(ctx, "user:1", "chat:3")
	if err != nil || !ok {
		t.Fatalf("Draft: %v ok=%v", err, ok)
	}
	if got.Text != "half typed 中文" || got.ReplyToID != 512 {
		t.Fatalf("draft = %q/%d", got.Text, got.ReplyToID)
	}
	if !got.Dirty {
		t.Fatal("a local draft must be dirty so a dialog sync cannot overwrite it")
	}

	// Draft text is user content; it must not be readable in the clear.
	var blob []byte
	if err := db.sql.QueryRowContext(ctx, `SELECT text_blob FROM drafts WHERE peer_key = 'chat:3'`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("half typed")) {
		t.Fatal("draft text stored in the clear")
	}
}

// The background dialog sweep repeats, so a server draft must never overwrite text the user is
// still typing.
func TestSaveServerDraftsRespectsDirtyAndAge(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := db.SaveLocalDraft(ctx, Draft{AccountID: "user:1", PeerKey: "chat:3", Text: "mine"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveServerDrafts(ctx, []Draft{{AccountID: "user:1", PeerKey: "chat:3", Text: "theirs", ServerDate: 999}}); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.Draft(ctx, "user:1", "chat:3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "mine" {
		t.Fatalf("draft = %q, want the dirty local draft preserved", got.Text)
	}

	// Once synced, a newer server draft wins.
	if err := db.MarkDraftSynced(ctx, "user:1", "chat:3", 100); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveServerDrafts(ctx, []Draft{{AccountID: "user:1", PeerKey: "chat:3", Text: "theirs", ServerDate: 999}}); err != nil {
		t.Fatal(err)
	}
	got, _, err = db.Draft(ctx, "user:1", "chat:3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "theirs" {
		t.Fatalf("draft = %q, want the newer server draft", got.Text)
	}

	// An older server draft must not win.
	if err := db.SaveServerDrafts(ctx, []Draft{{AccountID: "user:1", PeerKey: "chat:3", Text: "stale", ServerDate: 5}}); err != nil {
		t.Fatal(err)
	}
	got, _, err = db.Draft(ctx, "user:1", "chat:3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "theirs" {
		t.Fatalf("draft = %q, want the stale server draft ignored", got.Text)
	}
}

func TestSaveServerDraftsEmptyClearsOnlyCleanRows(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := db.SaveLocalDraft(ctx, Draft{AccountID: "user:1", PeerKey: "chat:3", Text: "typing"}); err != nil {
		t.Fatal(err)
	}
	// Cleared elsewhere, but we have unsynced local text: keep ours.
	if err := db.SaveServerDrafts(ctx, []Draft{{AccountID: "user:1", PeerKey: "chat:3"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.Draft(ctx, "user:1", "chat:3"); !ok {
		t.Fatal("dirty draft was deleted by an empty server draft")
	}

	if err := db.MarkDraftSynced(ctx, "user:1", "chat:3", 10); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveServerDrafts(ctx, []Draft{{AccountID: "user:1", PeerKey: "chat:3"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.Draft(ctx, "user:1", "chat:3"); ok {
		t.Fatal("clean draft should have been cleared")
	}
}
