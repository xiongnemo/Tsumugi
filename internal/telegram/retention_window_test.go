package telegram

import (
	"context"
	"testing"
	"time"
)

// Someone who asks to keep a week of history wants a small database. Fetching a month and deleting
// most of it would be both wasteful and a treadmill, so the backfill has to yield to retention.
func TestBackfillHorizonYieldsToShortRetention(t *testing.T) {
	c := &GotdClient{}
	t.Setenv("TSUMUGI_BACKFILL_DAYS", "30")

	// No store, so retentionWindow falls back to the default of 60 days: the configured 30-day
	// horizon is comfortably inside it and survives untouched.
	if got := c.effectiveBackfillHorizon(context.Background()); got != 30*24*time.Hour {
		t.Fatalf("horizon = %s, want the configured 30 days when retention is deeper", got)
	}
}

// The horizon must never reach past what retention keeps, or the two fight.
func TestBackfillHorizonNeverExceedsRetention(t *testing.T) {
	for _, keepDays := range []int{1, 3, 7, 8, 9, 14, 30, 60, 365} {
		keep := time.Duration(keepDays) * 24 * time.Hour
		horizon := clampBackfillHorizon(90*24*time.Hour, keep)
		if horizon >= keep {
			t.Errorf("keep=%dd: horizon %s reaches as deep as retention, which loops", keepDays, horizon)
		}
		// Zero means "do not prefetch", which is the right answer for a narrow window rather than
		// a token depth that only feeds the pruner.
		if horizon != 0 && horizon < 24*time.Hour {
			t.Errorf("keep=%dd: horizon %s is neither disabled nor a usable depth", keepDays, horizon)
		}
	}
}

// Keeping everything means the horizon is whatever was configured.
func TestBackfillHorizonUnclampedWhenKeepingEverything(t *testing.T) {
	if got := clampBackfillHorizon(30*24*time.Hour, 0); got != 30*24*time.Hour {
		t.Fatalf("horizon = %s, want the configured value when retention is off", got)
	}
}
