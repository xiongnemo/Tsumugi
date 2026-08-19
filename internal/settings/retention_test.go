package settings

import (
	"testing"
)

// The default has to sit deeper than the default backfill horizon. If they met, retention would
// delete a message just past the boundary and the backfill would fetch it straight back — the two
// would spin forever downloading and deleting the same history.
func TestDefaultRetentionIsDeeperThanTheBackfillHorizon(t *testing.T) {
	const defaultBackfillDays = 30
	if DefaultRetentionDays <= defaultBackfillDays {
		t.Fatalf("DefaultRetentionDays = %d, must exceed the %d-day backfill horizon",
			DefaultRetentionDays, defaultBackfillDays)
	}
}

func TestDefaultsIncludeRetention(t *testing.T) {
	if got := Defaults().RetentionDays; got != DefaultRetentionDays {
		t.Fatalf("RetentionDays = %d, want %d", got, DefaultRetentionDays)
	}
}

// Zero is a real choice, not an unset value: it means keep everything.
func TestRetentionForeverIsZero(t *testing.T) {
	if RetentionForever != 0 {
		t.Fatalf("RetentionForever = %d, want 0", RetentionForever)
	}
}
