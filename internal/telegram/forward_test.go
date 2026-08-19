package telegram

import (
	"strconv"
	"testing"

	"github.com/nemo/Tsumugi/internal/storage"
)

// forwardLookup mimics the storage lookup the real path uses.
func forwardLookup(messages []storage.Message) func(int) (storage.Message, bool) {
	byID := make(map[int]storage.Message, len(messages))
	for _, msg := range messages {
		byID[msg.ID] = msg
	}
	return func(id int) (storage.Message, bool) {
		msg, ok := byID[id]
		return msg, ok
	}
}

func forwardStored() []storage.Message {
	return []storage.Message{
		{ID: 10, State: "synced"},
		{ID: 11, State: "synced"},
		{ID: 12, State: "pending"},
		{ID: 13, State: "failed"},
		{ID: 14, State: "deleted"},
		{ID: 15, State: "synced", ServiceKey: "service.pinned_message"},
		{ID: 16, State: "synced"},
	}
}

func TestSanitizeForwardIDsDropsWhatCannotBeForwarded(t *testing.T) {
	got, err := sanitizeForwardIDs(forwardLookup(forwardStored()), []string{"10", "12", "13", "14", "15", "16"})
	if err != nil {
		t.Fatal(err)
	}
	want := []int{10, 16}
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v (local, failed, deleted and service messages dropped)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// Telegram forwards in the order given, so the copy should read the same way as the source
// regardless of the order the user marked things in.
func TestSanitizeForwardIDsSortsAscending(t *testing.T) {
	got, err := sanitizeForwardIDs(forwardLookup(forwardStored()), []string{"16", "10", "11"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("ids = %v, want ascending", got)
		}
	}
}

func TestSanitizeForwardIDsIgnoresDuplicatesAndLocalIDs(t *testing.T) {
	got, err := sanitizeForwardIDs(forwardLookup(forwardStored()), []string{"10", "10", "local-1", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 10 {
		t.Fatalf("ids = %v, want just [10]", got)
	}
}

// An id the store has not cached is passed through, not dropped. The UI can only mark what it has
// displayed, so an unknown id means our cache is behind — and dropping it is exactly the silent
// loss that made selections disappear after a jump.
func TestSanitizeForwardIDsKeepsUncachedIDs(t *testing.T) {
	got, err := sanitizeForwardIDs(forwardLookup(forwardStored()), []string{"999999"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 999999 {
		t.Fatalf("ids = %v, want the uncached id forwarded anyway", got)
	}
}

// Erroring beats silently forwarding a prefix: the user would have no way to tell which of their
// messages actually went.
func TestSanitizeForwardIDsRejectsOverTheAPICap(t *testing.T) {
	var stored []storage.Message
	var wanted []string
	for i := 1; i <= forwardMaxMessages+1; i++ {
		stored = append(stored, storage.Message{ID: i, State: "synced"})
		wanted = append(wanted, strconv.Itoa(i))
	}

	if _, err := sanitizeForwardIDs(forwardLookup(stored), wanted); err == nil {
		t.Fatalf("want an error above the %d-message cap", forwardMaxMessages)
	}

	// Exactly at the cap must still work.
	if _, err := sanitizeForwardIDs(forwardLookup(stored), wanted[:forwardMaxMessages]); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
}

func TestSanitizeForwardIDsEmpty(t *testing.T) {
	got, err := sanitizeForwardIDs(forwardLookup(forwardStored()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("ids = %v, want empty", got)
	}
}
