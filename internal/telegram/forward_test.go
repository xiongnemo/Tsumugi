package telegram

import (
	"strconv"
	"testing"

	"github.com/nemo/Tsumugi/internal/storage"
)

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
	got, err := sanitizeForwardIDs(forwardStored(), []string{"10", "12", "13", "14", "15", "16"})
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
	got, err := sanitizeForwardIDs(forwardStored(), []string{"16", "10", "11"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("ids = %v, want ascending", got)
		}
	}
}

func TestSanitizeForwardIDsIgnoresUnknownAndDuplicates(t *testing.T) {
	got, err := sanitizeForwardIDs(forwardStored(), []string{"10", "10", "999", "local-1", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 10 {
		t.Fatalf("ids = %v, want just [10]", got)
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

	if _, err := sanitizeForwardIDs(stored, wanted); err == nil {
		t.Fatalf("want an error above the %d-message cap", forwardMaxMessages)
	}

	// Exactly at the cap must still work.
	if _, err := sanitizeForwardIDs(stored, wanted[:forwardMaxMessages]); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
}

func TestSanitizeForwardIDsEmpty(t *testing.T) {
	got, err := sanitizeForwardIDs(forwardStored(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("ids = %v, want empty", got)
	}
}
