package telegram

import (
	"strconv"
	"time"

	"github.com/nemo/Tsumugi/internal/storage"
)

// DefaultHistoryGapThreshold treats adjacent messages more than 36h apart as a history gap.
const DefaultHistoryGapThreshold = 36 * time.Hour

// HistoryGap describes a discontinuity between two consecutive messages in sorted order.
type HistoryGap struct {
	// NewerOffsetID is the message ID on the newer side of the gap (getHistory offset).
	NewerOffsetID int
	OlderTime     time.Time
	NewerTime     time.Time
}

// FindHistoryGaps scans time-sorted messages and returns newer-side message IDs for each gap.
func FindHistoryGaps(msgs []Message, threshold time.Duration) []int {
	gaps := FindHistoryGapDetails(msgs, threshold)
	out := make([]int, 0, len(gaps))
	for _, g := range gaps {
		if g.NewerOffsetID > 0 {
			out = append(out, g.NewerOffsetID)
		}
	}
	return out
}

// FindHistoryGapDetails returns structured gap info for tests and UI.
func FindHistoryGapDetails(msgs []Message, threshold time.Duration) []HistoryGap {
	if threshold <= 0 {
		threshold = DefaultHistoryGapThreshold
	}
	if len(msgs) < 2 {
		return nil
	}
	var out []HistoryGap
	for i := 1; i < len(msgs); i++ {
		delta := msgs[i].CreatedAt.Sub(msgs[i-1].CreatedAt)
		if delta <= threshold {
			continue
		}
		id, ok := messageIDInt(msgs[i].ID)
		if !ok || id <= 0 {
			continue
		}
		out = append(out, HistoryGap{
			NewerOffsetID: id,
			OlderTime:     msgs[i-1].CreatedAt,
			NewerTime:     msgs[i].CreatedAt,
		})
	}
	return out
}

func messageIDInt(id string) (int, bool) {
	if id == "" {
		return 0, false
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return 0, false
	}
	return n, true
}

// olderCacheIsAdjacent reports whether cached rows are likely contiguous with beforeID.
func olderCacheIsAdjacent(cached []storage.Message, beforeID int) bool {
	if len(cached) == 0 || beforeID <= 0 {
		return false
	}
	newest := 0
	for _, msg := range cached {
		if msg.ID > newest {
			newest = msg.ID
		}
	}
	if newest <= 0 {
		return false
	}
	// Same getHistory page is typically within ~100 ids; larger jumps imply a DB hole.
	return beforeID-newest <= 100
}
