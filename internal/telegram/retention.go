package telegram

import (
	"context"
	"time"

	"github.com/nemo/Tsumugi/internal/debuglog"
	"github.com/nemo/Tsumugi/internal/settings"
)

const (
	// retentionMargin is how much shallower than the retention window the backfill is allowed to
	// reach.
	//
	// This gap is the whole design. If the two met at the same depth they would fight: retention
	// removes a message just past the boundary, the backfill sees it no longer holds anything that
	// old, fetches it again, and the pair spin forever downloading and deleting the same history.
	// Retention is the authority — it is what the user chose — so the backfill yields to it.
	retentionMargin = 7 * 24 * time.Hour
	// Per round, so a prune is a bounded amount of work and never a long write lock.
	retentionPeersPerRound   = 10
	retentionRowsPerPeer     = 2000
	retentionKeepNewest      = 200
	retentionRoundInterval   = 60 * time.Second
	retentionIdleInterval    = 30 * time.Minute
	retentionForegroundPause = 2 * time.Second
)

// retentionWindow is how much history the user wants kept, or zero for all of it.
func (c *GotdClient) retentionWindow(ctx context.Context) time.Duration {
	days := settings.DefaultRetentionDays
	if c.store != nil {
		days = settings.Load(ctx, c.store).RetentionDays
	}
	if days <= settings.RetentionForever {
		return 0
	}
	return time.Duration(days) * 24 * time.Hour
}

// effectiveBackfillHorizon is how far back the backfill may reach given what retention will keep.
//
// Retention wins. Someone who asks to keep a week of history wants a small database, so fetching a
// month and deleting most of it would be both wasteful and a treadmill. Clamped to at least a day so
// a very short retention still leaves the backfill something to do.
func (c *GotdClient) effectiveBackfillHorizon(ctx context.Context) time.Duration {
	return clampBackfillHorizon(backfillHorizon(), c.retentionWindow(ctx))
}

// clampBackfillHorizon keeps the backfill strictly shallower than what retention preserves, and
// returns zero to mean "do not prefetch at all".
//
// Pure so the invariant that matters — the horizon never reaches as deep as retention, for any
// combination of settings — can be checked directly.
//
// Below a day of usable depth the answer is zero rather than some token amount. Asking to keep a
// day or two of history is asking for a small database, and there is nothing worth prefetching
// inside a window that narrow: on-demand loading already covers scrolling back, and prefetching
// what retention is about to delete is the treadmill this whole arrangement exists to avoid.
func clampBackfillHorizon(horizon, keep time.Duration) time.Duration {
	if keep <= 0 {
		return horizon
	}
	if limit := keep - retentionMargin; limit < horizon {
		horizon = limit
	}
	if horizon < 24*time.Hour {
		return 0
	}
	return horizon
}

// pruneOldMessages trims stored history that the backfill has stopped maintaining.
//
// The messages table had no prune path at all, so it only ever grew — on a real account to six
// million rows and 3.7GB. Capping the backfill stops that growing; this is what brings an already
// grown database back down.
//
// Yields to foreground work the same way the backfill does: reading a chat must never wait on
// housekeeping.
func (c *GotdClient) pruneOldMessages(ctx context.Context, accountID string, events chan<- Event) {
	if c.store == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if c.foregroundActive() {
			if !sleepCtx(ctx, retentionForegroundPause) {
				return
			}
			continue
		}

		keep := c.retentionWindow(ctx)
		if keep == 0 {
			// Keeping everything, by choice. Re-checked on a long timer rather than returning,
			// so changing the setting takes effect without a restart.
			if !sleepCtx(ctx, retentionIdleInterval) {
				return
			}
			continue
		}
		cutoff := time.Now().UTC().Add(-keep)
		peers, err := c.store.PrunablePeers(ctx, accountID, cutoff, retentionPeersPerRound)
		if err != nil || len(peers) == 0 {
			// Nothing stale: this is the steady state, so wait a long time before looking again.
			if !sleepCtx(ctx, retentionIdleInterval) {
				return
			}
			continue
		}

		var removed int64
		for _, peerKey := range peers {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if c.foregroundActive() {
				break
			}
			n, err := c.store.PruneMessagesForPeer(ctx, accountID, peerKey, cutoff, retentionKeepNewest, retentionRowsPerPeer)
			if err != nil {
				debuglog.Error("retention", err, map[string]any{"peer_key": peerKey})
				continue
			}
			removed += n
		}
		if removed > 0 {
			debuglog.Log("retention", map[string]any{
				"removed": removed,
				"peers":   len(peers),
				"cutoff":  cutoff.Format(time.RFC3339),
			})
		}
		if !sleepCtx(ctx, retentionRoundInterval) {
			return
		}
	}
}

// sleepCtx waits, reporting false if the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
