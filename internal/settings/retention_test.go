package settings

import (
	"context"
	"testing"
)

// The default has to sit deeper than the default backfill horizon. If they met, retention would
// delete a message just past the boundary and the backfill would fetch it straight back — the two
// would spin forever downloading and deleting the same history.
func TestDefaultRetentionIsDeeperThanTheBackfillHorizon(t *testing.T) {
	if DefaultRetentionDays <= DefaultBackfillDays {
		t.Fatalf("DefaultRetentionDays = %d, must exceed the %d-day backfill horizon",
			DefaultRetentionDays, DefaultBackfillDays)
	}
}

func TestDefaultsIncludeRetention(t *testing.T) {
	if got := Defaults().RetentionDays; got != DefaultRetentionDays {
		t.Fatalf("RetentionDays = %d, want %d", got, DefaultRetentionDays)
	}
	if got := Defaults().BackfillDays; got != DefaultBackfillDays {
		t.Fatalf("BackfillDays = %d, want %d", got, DefaultBackfillDays)
	}
}

// Zero has to survive as a stored value for both day counts, and it means something different in
// each: keep everything, versus prefetch nothing.
func TestZeroIsAChoiceForBothDayCounts(t *testing.T) {
	if RetentionForever != 0 || BackfillOff != 0 {
		t.Fatalf("RetentionForever = %d, BackfillOff = %d, want both 0", RetentionForever, BackfillOff)
	}
}

// Zero is a real choice, not an unset value: it means keep everything.
func TestRetentionForeverIsZero(t *testing.T) {
	if RetentionForever != 0 {
		t.Fatalf("RetentionForever = %d, want 0", RetentionForever)
	}
}

// Zero has to come back out of the database as zero. An earlier version of this loader treated a
// stored 0 as "not set" and fell through to the default, so choosing "prefetch nothing" or "keep
// everything" silently did not stick.
func TestStoredZeroSurvivesAReload(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	want := Defaults()
	want.RetentionDays = RetentionForever
	want.BackfillDays = BackfillOff
	if err := want.Save(ctx, db); err != nil {
		t.Fatal(err)
	}

	loaded := Load(ctx, db)
	if loaded.RetentionDays != RetentionForever {
		t.Errorf("RetentionDays = %d, want the stored 0", loaded.RetentionDays)
	}
	if loaded.BackfillDays != BackfillOff {
		t.Errorf("BackfillDays = %d, want the stored 0", loaded.BackfillDays)
	}
}

// The environment is a fallback, not an override: a value the user chose in the settings panel has to
// win over one left in a shell profile.
func TestStoredDayCountsBeatTheEnvironment(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	t.Setenv("TSUMUGI_RETENTION_DAYS", "999")
	t.Setenv("TSUMUGI_BACKFILL_DAYS", "999")

	want := Defaults()
	want.RetentionDays = 14
	want.BackfillDays = 3
	if err := want.Save(ctx, db); err != nil {
		t.Fatal(err)
	}

	loaded := Load(ctx, db)
	if loaded.RetentionDays != 14 || loaded.BackfillDays != 3 {
		t.Fatalf("loaded %d/%d days, want the stored 14/3", loaded.RetentionDays, loaded.BackfillDays)
	}
}

func TestEnvironmentDayCountsApplyWithoutADatabase(t *testing.T) {
	t.Setenv("TSUMUGI_RETENTION_DAYS", "90")
	t.Setenv("TSUMUGI_BACKFILL_DAYS", "0")

	loaded := Load(context.Background(), nil)
	if loaded.RetentionDays != 90 {
		t.Errorf("RetentionDays = %d, want 90 from the environment", loaded.RetentionDays)
	}
	if loaded.BackfillDays != BackfillOff {
		t.Errorf("BackfillDays = %d, want the prefetch turned off from the environment", loaded.BackfillDays)
	}
}

// A nonsense value is ignored rather than silently turning a feature off.
func TestUnparseableDayCountFallsBackToTheDefault(t *testing.T) {
	t.Setenv("TSUMUGI_BACKFILL_DAYS", "later")
	if got := Load(context.Background(), nil).BackfillDays; got != DefaultBackfillDays {
		t.Fatalf("BackfillDays = %d, want the default %d", got, DefaultBackfillDays)
	}
}
