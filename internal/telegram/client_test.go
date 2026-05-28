package telegram

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/storage"
)

func TestDeviceConfigUsesTsumugiMetadata(t *testing.T) {
	cfg := deviceConfig()
	hostname, _ := os.Hostname()
	if hostname != "" && !strings.Contains(cfg.DeviceModel, hostname) {
		t.Fatalf("device model %q does not include hostname %q", cfg.DeviceModel, hostname)
	}
	if !strings.Contains(cfg.DeviceModel, "Tsumugi") {
		t.Fatalf("device model = %q", cfg.DeviceModel)
	}
	if strings.HasPrefix(cfg.AppVersion, "v0.143.0") {
		t.Fatalf("app version should not be gotd version: %q", cfg.AppVersion)
	}
	if cfg.SystemVersion == "" {
		t.Fatal("system version is empty")
	}
}

func TestSortChatsUsesPinnedThenActivity(t *testing.T) {
	chats := []Chat{
		{ID: "old", Title: "Old", LastMessageAt: time.Unix(10, 0)},
		{ID: "new", Title: "New", LastMessageAt: time.Unix(20, 0)},
		{ID: "pin", Title: "Pin", Pinned: true, LastMessageAt: time.Unix(1, 0)},
	}
	sortChats(chats)
	if got := []string{chats[0].ID, chats[1].ID, chats[2].ID}; strings.Join(got, ",") != "pin,new,old" {
		t.Fatalf("unexpected order: %+v", chats)
	}
}

func TestSortChatsUsesPinnedOrderAmongPinned(t *testing.T) {
	chats := []Chat{
		{ID: "b", Title: "B", Pinned: true, PinnedOrder: 2, LastMessageAt: time.Unix(20, 0)},
		{ID: "a", Title: "A", Pinned: true, PinnedOrder: 1, LastMessageAt: time.Unix(1, 0)},
		{ID: "c", Title: "C", LastMessageAt: time.Unix(30, 0)},
	}
	sortChats(chats)
	if got := []string{chats[0].ID, chats[1].ID, chats[2].ID}; strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("unexpected order: %+v", chats)
	}
}

func TestMergePeerActivityPreservesPinnedMetadata(t *testing.T) {
	existing := storage.Peer{
		Key:              "user:1",
		Pinned:           true,
		PinnedOrder:      3,
		Unread:           5,
		FolderID:         2,
		ReadOutboxMaxID:  7,
	}
	activity := storage.Peer{
		Key:           "user:1",
		Title:         "Updated",
		LastMessageAt: time.Unix(99, 0),
		TopMessageID:  42,
	}
	got := mergePeerActivity(existing, activity)
	if !got.Pinned || got.PinnedOrder != 3 || got.Unread != 5 || got.FolderID != 2 || got.ReadOutboxMaxID != 7 {
		t.Fatalf("mergePeerActivity() = %+v, want pinned metadata preserved", got)
	}
	if got.Title != "Updated" || got.TopMessageID != 42 {
		t.Fatalf("mergePeerActivity() = %+v, want activity fields updated", got)
	}
}

func TestMergePeerActivityPreservesTitleWhenUpdateLacksUserEntity(t *testing.T) {
	existing := storage.Peer{
		Key:        "user:732529034",
		ID:         732529034,
		Title:      "Alice",
		Username:   "alice",
		Subtitle:   "private",
		AccessHash: 123,
		Contact:    true,
	}
	activity := storage.Peer{
		Key:           "user:732529034",
		ID:            732529034,
		Title:         "user 732529034",
		LastMessageAt: time.Unix(99, 0),
		TopMessageID:  42,
	}
	got := mergePeerActivity(existing, activity)
	if got.Title != "Alice" || got.Username != "alice" || got.AccessHash != 123 || !got.Contact {
		t.Fatalf("mergePeerActivity() = %+v, want identity preserved", got)
	}
	if got.TopMessageID != 42 {
		t.Fatalf("mergePeerActivity() = %+v, want activity fields updated", got)
	}
}

func TestIsPlaceholderPeerTitle(t *testing.T) {
	if !isPlaceholderPeerTitle("user 42", 42) {
		t.Fatal("expected user placeholder")
	}
	if isPlaceholderPeerTitle("Alice", 42) {
		t.Fatal("real title should not be placeholder")
	}
}

func TestSenderColorDeterministic(t *testing.T) {
	first := senderColor("user", 42, "Nemo")
	second := senderColor("user", 42, "Nemo")
	if first != second {
		t.Fatalf("sender color changed: %d != %d", first, second)
	}
}

func TestStoredTelegramFoldersDoNotAddFallbackFolders(t *testing.T) {
	got := storageFiltersToFolders([]storage.DialogFilter{
		{ID: 0, Title: "All", Kind: "all"},
		{ID: 1, Title: "Work", Kind: "telegram"},
	})
	if len(got) != 2 {
		t.Fatalf("folders = %+v", got)
	}
	for _, folder := range got {
		if folder.Title == "Channels" || strings.HasPrefix(folder.Title, "Folder ") {
			t.Fatalf("unexpected fallback folder in official folder list: %+v", got)
		}
	}
}

func TestDialogFilterRulesAreConverted(t *testing.T) {
	filter := storageDialogFilter("user:1", 2, "Work", &tg.DialogFilter{
		Groups:          true,
		ExcludeArchived: true,
		IncludePeers: []tg.InputPeerClass{
			&tg.InputPeerUser{UserID: 42},
			&tg.InputPeerChannel{ChannelID: 99},
		},
		ExcludePeers: []tg.InputPeerClass{&tg.InputPeerChat{ChatID: 5}},
	})
	if !filter.Groups || !filter.ExcludeArchived {
		t.Fatalf("category rules not converted: %+v", filter)
	}
	if strings.Join(filter.IncludePeers, ",") != "user:42,channel:99" {
		t.Fatalf("include peers not converted: %+v", filter.IncludePeers)
	}
	if strings.Join(filter.ExcludePeers, ",") != "chat:5" {
		t.Fatalf("exclude peers not converted: %+v", filter.ExcludePeers)
	}
}

func TestArchiveFolderInsertedAfterAll(t *testing.T) {
	got := withArchiveFolder([]Folder{{ID: 0, Title: "All", Kind: "all"}, {ID: 2, Title: "Work", Kind: "telegram"}})
	if len(got) != 3 || !got[1].Archive || got[1].ID != ArchiveFolderID {
		t.Fatalf("archive folder not inserted after All: %+v", got)
	}
}

func TestClassifyDocumentGIF(t *testing.T) {
	got := classifyDocument(&tg.Document{
		ID:       1,
		MimeType: "video/mp4",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAnimated{},
			&tg.DocumentAttributeFilename{FileName: "clip.mp4"},
		},
	})
	if got.Kind != "gif" || got.Label != "[GIF]" || got.FileName != "clip.mp4" {
		t.Fatalf("unexpected media classification: %+v", got)
	}
	if got.DocumentID != 1 || got.DownloadKey != "document:1" {
		t.Fatalf("missing document locator metadata: %+v", got)
	}
}

func TestPendingMessageCanBeTakenOnce(t *testing.T) {
	client := NewGotdClient(testConfig(), nil)
	client.rememberPending("chat:1", "hello", -42)
	first := client.takeMatchingPending("chat:1", "hello", true)
	second := client.takeMatchingPending("chat:1", "hello", true)
	if len(first) != 1 || first[0] != -42 {
		t.Fatalf("unexpected first take: %+v", first)
	}
	if len(second) != 0 {
		t.Fatalf("pending was not consumed: %+v", second)
	}
}

func TestPendingMessageDuplicateTextIsFIFO(t *testing.T) {
	client := NewGotdClient(testConfig(), nil)
	client.rememberPending("chat:1", "hello", -1)
	client.rememberPending("chat:1", "hello", -2)

	first := client.takeMatchingPending("chat:1", "hello", true)
	second := client.takeMatchingPending("chat:1", "hello", true)
	if len(first) != 1 || first[0] != -1 {
		t.Fatalf("unexpected first take: %+v", first)
	}
	if len(second) != 1 || second[0] != -2 {
		t.Fatalf("unexpected second take: %+v", second)
	}
}

func TestSenderDisplayNameMissingEntityIsEmpty(t *testing.T) {
	got := senderDisplayName(&tg.PeerUser{UserID: 732529034}, entitiesByID{})
	if got != "" {
		t.Fatalf("missing entity should not render raw user id, got %q", got)
	}
}

func testConfig() config.Config {
	return config.Config{}
}
