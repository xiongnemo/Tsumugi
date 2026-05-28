package telegram

import (
	"strings"
	"testing"
	"time"
)

func TestSortChatsForFolderUsesPinnedPeersOrder(t *testing.T) {
	folder := Folder{
		ID:    2,
		Kind:  "telegram",
		Title: "Discussion",
		Rules: FolderRules{
			Groups:      true,
			PinnedPeers: []string{"chat:3", "chat:1", "user:5"},
		},
	}
	chats := []Chat{
		{ID: "chat:2", Title: "Recent", LastMessageAt: time.Unix(100, 0)},
		{ID: "chat:1", Title: "Pin 2", LastMessageAt: time.Unix(1, 0)},
		{ID: "user:5", Title: "Pin 3", LastMessageAt: time.Unix(2, 0)},
		{ID: "chat:3", Title: "Pin 1", LastMessageAt: time.Unix(3, 0)},
	}
	SortChatsForFolder(chats, folder)
	got := []string{chats[0].ID, chats[1].ID, chats[2].ID, chats[3].ID}
	want := "chat:3,chat:1,user:5,chat:2"
	if strings.Join(got, ",") != want {
		t.Fatalf("order = %s, want %s", strings.Join(got, ","), want)
	}
}

func TestChatForFolderDisplayUsesFolderPinsNotGlobal(t *testing.T) {
	folder := Folder{
		ID:    2,
		Kind:  "telegram",
		Title: "Discussion",
		Rules: FolderRules{PinnedPeers: []string{"chat:9"}},
	}
	globalPin := Chat{ID: "chat:1", Title: "Global", Pinned: true, PinnedOrder: 1}
	folderPin := Chat{ID: "chat:9", Title: "Folder", Pinned: false}

	gotGlobal := ChatForFolderDisplay(globalPin, folder)
	if gotGlobal.Pinned {
		t.Fatal("global pin should not display in telegram folder without folder pin")
	}
	gotFolder := ChatForFolderDisplay(folderPin, folder)
	if !gotFolder.Pinned || gotFolder.PinnedOrder != 1 {
		t.Fatalf("folder pin display = %+v", gotFolder)
	}
}

func TestSortChatsForFolderAllUsesGlobalPins(t *testing.T) {
	folder := Folder{ID: 0, Title: "All", Kind: "all"}
	chats := []Chat{
		{ID: "chat:2", LastMessageAt: time.Unix(20, 0)},
		{ID: "chat:1", Pinned: true, PinnedOrder: 1, LastMessageAt: time.Unix(1, 0)},
	}
	SortChatsForFolder(chats, folder)
	if chats[0].ID != "chat:1" {
		t.Fatalf("global pin order = %+v", chats)
	}
}
