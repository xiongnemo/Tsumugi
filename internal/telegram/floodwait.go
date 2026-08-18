package telegram

import (
	"context"
	"fmt"

	"github.com/gotd/td/tgerr"
)

// defaultMaxFloodWaits is how many FLOOD_WAIT sleeps a call tolerates before giving up.
// Interactive callers that would rather report the wait than sit behind it pass a lower value.
const defaultMaxFloodWaits = 2

// retryFloodWait calls fn, sleeping and retrying while Telegram answers FLOOD_WAIT.
//
// The subtlety this exists to contain: tgerr.FloodWait sleeps and then reports
// (true, originalErr) — its error is non-nil even when the wait completed successfully, and
// is nil only in the cancelled case, where it also reports false. So the error must be
// ignored whenever flood is true. Treating it as fatal is what made the two hand-written
// copies of this loop sleep once and then fail without ever retrying, which is the bug this
// replaces.
//
// maxFloodWaits counts sleeps, not calls: with 2, the call is made up to three times.
func retryFloodWait[T any](
	ctx context.Context,
	maxFloodWaits int,
	label string,
	fn func(context.Context) (T, error),
	opts ...tgerr.FloodWaitOption,
) (T, error) {
	var zero T
	floodWaits := 0
	for {
		res, err := fn(ctx)
		if err == nil {
			return res, nil
		}
		flood, waitErr := tgerr.FloodWait(ctx, err, opts...)
		if !flood {
			// Either not a flood wait at all, or the context was cancelled mid-sleep; in both
			// cases waitErr is the one worth reporting.
			return zero, waitErr
		}
		floodWaits++
		if floodWaits > maxFloodWaits {
			return zero, fmt.Errorf("telegram flood wait repeated while %s: %w", label, err)
		}
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
	}
}
