package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/clock"
	"github.com/gotd/td/tgerr"
)

// firedTimer is already expired, so tgerr.FloodWait's sleep returns immediately and the test
// does not spend real seconds waiting.
type firedTimer struct{ ch chan time.Time }

func newFiredTimer() *firedTimer {
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return &firedTimer{ch: ch}
}

func (t *firedTimer) C() <-chan time.Time { return t.ch }
func (t *firedTimer) Stop() bool          { return true }
func (t *firedTimer) Reset(time.Duration) {}

// deadTimer never fires, so a cancelled context wins tgerr.FloodWait's select deterministically.
type deadTimer struct{}

func (deadTimer) C() <-chan time.Time { return nil }
func (deadTimer) Stop() bool          { return true }
func (deadTimer) Reset(time.Duration) {}

type testClock struct{ dead bool }

func (c testClock) Now() time.Time { return time.Time{} }
func (c testClock) Timer(time.Duration) clock.Timer {
	if c.dead {
		return deadTimer{}
	}
	return newFiredTimer()
}
func (c testClock) Ticker(time.Duration) clock.Ticker { panic("unused") }

func instantClock() tgerr.FloodWaitOption { return tgerr.FloodWaitWithClock(testClock{}) }

func floodErr() error {
	return &tgerr.Error{Code: 420, Type: tgerr.ErrFloodWait, Argument: 1}
}

// The bug this replaces: tgerr.FloodWait reports (true, originalErr) after a successful sleep,
// and the two hand-written loops treated that non-nil error as fatal, so they slept once and
// gave up. This asserts the retry actually happens.
func TestRetryFloodWaitRetriesAfterFloodWait(t *testing.T) {
	calls := 0
	got, err := retryFloodWait(context.Background(), 2, "testing", func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", floodErr()
		}
		return "ok", nil
	}, instantClock())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != "ok" {
		t.Fatalf("got = %q, want ok", got)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (one flood, then success)", calls)
	}
}

func TestRetryFloodWaitGivesUpAfterMaxSleeps(t *testing.T) {
	calls := 0
	_, err := retryFloodWait(context.Background(), 2, "testing", func(context.Context) (string, error) {
		calls++
		return "", floodErr()
	}, instantClock())
	if err == nil {
		t.Fatal("err = nil, want a repeated-flood-wait error")
	}
	// maxFloodWaits counts sleeps, so two sleeps means three calls.
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if !tgerr.Is(err, tgerr.ErrFloodWait) {
		t.Fatalf("err = %v, want the original flood error wrapped", err)
	}
}

func TestRetryFloodWaitReturnsOtherErrorsImmediately(t *testing.T) {
	sentinel := errors.New("boom")
	calls := 0
	_, err := retryFloodWait(context.Background(), 2, "testing", func(context.Context) (string, error) {
		calls++
		return "", sentinel
	}, instantClock())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry for non-flood errors)", calls)
	}
}

func TestRetryFloodWaitHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := retryFloodWait(ctx, 5, "testing", func(context.Context) (string, error) {
		calls++
		return "", floodErr()
	}, tgerr.FloodWaitWithClock(testClock{dead: true}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// Guard against a well-meaning simplification back to the broken shape.
func TestFloodWaitReportsOriginalErrorOnSuccessfulSleep(t *testing.T) {
	original := floodErr()
	flood, err := tgerr.FloodWait(context.Background(), original, instantClock())
	if !flood {
		t.Fatal("flood = false, want true")
	}
	if err == nil {
		t.Fatal("FloodWait now returns nil after sleeping; retryFloodWait's contract can be simplified")
	}
	if !errors.Is(err, original) {
		t.Fatalf("err = %v, want the original error", err)
	}
}
