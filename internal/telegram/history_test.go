package telegram

import (
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/storage"
)

func TestFindHistoryGapsMayCluster(t *testing.T) {
	may13 := time.Date(2026, 5, 13, 20, 0, 0, 0, time.UTC)
	may24 := time.Date(2026, 5, 24, 23, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "53391", CreatedAt: may13},
		{ID: "54599", CreatedAt: may24},
	}
	gaps := FindHistoryGaps(msgs, DefaultHistoryGapThreshold)
	if len(gaps) != 1 {
		t.Fatalf("gaps = %v, want 1", gaps)
	}
	if gaps[0] != 54599 {
		t.Fatalf("gap offset = %d, want 54599", gaps[0])
	}
}

func TestFindHistoryGapsContinuous(t *testing.T) {
	base := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "1", CreatedAt: base},
		{ID: "2", CreatedAt: base.Add(time.Hour)},
	}
	if gaps := FindHistoryGaps(msgs, DefaultHistoryGapThreshold); len(gaps) != 0 {
		t.Fatalf("expected no gaps, got %v", gaps)
	}
}

func TestOlderCacheIsAdjacent(t *testing.T) {
	cached := []storage.Message{{ID: 54550}, {ID: 54549}}
	if !olderCacheIsAdjacent(cached, 54599) {
		t.Fatal("expected adjacent cache")
	}
	far := []storage.Message{{ID: 53391}, {ID: 53390}}
	if olderCacheIsAdjacent(far, 54599) {
		t.Fatal("expected non-adjacent cache")
	}
}
