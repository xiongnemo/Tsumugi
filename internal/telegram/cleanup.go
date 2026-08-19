package telegram

import (
	"context"
	"time"

	"github.com/nemo/Tsumugi/internal/debuglog"
	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

const (
	// A cleanup is user-initiated, so it works in much larger batches than the background pruner:
	// the point is to finish, not to stay out of the way.
	cleanupPeersPerRound = 100
	// Bounded so a bug in the terminating condition cannot spin forever on a live account.
	cleanupMaxRounds = 500
)

// cleanupRowsPerPeer is how many rows one delete statement removes. A var so a test can shrink it
// and drive the multi-round path without seeding twenty thousand messages.
var cleanupRowsPerPeer = 20000

// cleanupStorage applies the current retention setting immediately, then reclaims the freed space.
//
// The background pruner gets there eventually, but "eventually" is not an answer when someone has
// just turned retention down and wants to see the database shrink. It is also the only thing that
// runs VACUUM: pruning leaves free pages that SQLite happily reuses, so without a compaction the
// file stays exactly as large as it ever was.
//
// Deliberately reports through EventStorage rather than the status bar. The cleanup is started from
// the settings overlay, which covers the status bar, so a status-bar-only report would be invisible
// for the whole operation.
func (c *GotdClient) cleanupStorage(ctx context.Context, accountID string, events chan<- Event) {
	if c.store == nil {
		return
	}
	if !c.cleanupRunning.CompareAndSwap(false, true) {
		sendEvent(ctx, events, Event{
			Kind:        EventStorage,
			StatusMsg:   i18n.M(i18n.KeyStatusCleanupBusy),
			StorageBusy: true,
		})
		return
	}
	defer c.cleanupRunning.Store(false)

	// Counts as foreground work, which pauses the backfill and the background pruner. Not politeness:
	// the database has a single connection, so anything else writing during the compaction would
	// simply queue behind it.
	c.beginForegroundLoad()
	defer c.endForegroundLoad()

	before, err := c.store.DatabaseFileBytes(ctx)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventStorage, Error: err})
		return
	}
	sendEvent(ctx, events, Event{
		Kind:         EventStorage,
		StatusMsg:    i18n.M(i18n.KeyStatusCleanupStarted),
		StorageBytes: before,
	})

	removed, err := c.pruneToRetention(ctx, accountID, events)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventStorage, Error: err})
		return
	}

	sendEvent(ctx, events, Event{
		Kind:      EventStorage,
		StatusMsg: i18n.M(i18n.KeyStatusCleanupCompact, removed),
	})
	if err := c.store.Compact(ctx); err != nil {
		// The pruning already happened and is not undone by a failed compaction, so this reports the
		// error and still tells the user what it did remove.
		debuglog.Error("cleanup_compact", err, map[string]any{"removed": removed})
		sendEvent(ctx, events, Event{Kind: EventStorage, Error: err, StorageRemoved: removed})
		return
	}

	after, err := c.store.DatabaseFileBytes(ctx)
	if err != nil {
		after = 0
	}
	debuglog.Log("cleanup", map[string]any{"removed": removed, "before": before, "after": after})
	sendEvent(ctx, events, Event{
		Kind:           EventStorage,
		StatusMsg:      i18n.M(i18n.KeyStatusCleanupDone, removed, storage.FormatBytes(before), storage.FormatBytes(after)),
		StorageBytes:   after,
		StorageRemoved: removed,
	})
}

// pruneToRetention deletes everything the retention setting no longer covers, in batches.
//
// Runs to completion rather than a fixed number of rounds, which is what separates it from the
// background pruner. Stopping is decided by "this round removed nothing", not by the peer list
// emptying: PrunablePeers knows a peer holds old messages but not that the keepNewest floor protects
// them, so a quiet chat under that floor is on the list every round forever. The exhausted set is
// what keeps such a peer from being re-attempted on every round of a long cleanup; a message
// arriving mid-cleanup could in principle make one deletable again, which the background pruner
// picks up later.
func (c *GotdClient) pruneToRetention(ctx context.Context, accountID string, events chan<- Event) (int64, error) {
	keep := c.retentionWindow(ctx)
	if keep == 0 {
		// Keeping everything: there is nothing to delete, but compacting still reclaims whatever
		// earlier prunes left behind, so this is not a no-op overall.
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-keep)
	exhausted := make(map[string]bool)

	var total int64
	for round := 0; round < cleanupMaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		peers, err := c.store.PrunablePeers(ctx, accountID, cutoff, cleanupPeersPerRound)
		if err != nil {
			return total, err
		}
		var removed int64
		for _, peerKey := range peers {
			if exhausted[peerKey] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return total, err
			}
			n, err := c.store.PruneMessagesForPeer(ctx, accountID, peerKey, cutoff, RetentionKeepNewest, cleanupRowsPerPeer)
			if err != nil {
				return total, err
			}
			if n == 0 {
				exhausted[peerKey] = true
				continue
			}
			removed += n
		}
		total += removed
		if removed == 0 {
			return total, nil
		}
		sendEvent(ctx, events, Event{
			Kind:           EventStorage,
			StatusMsg:      i18n.M(i18n.KeyStatusCleanupPruning, total),
			StorageRemoved: total,
		})
	}
	return total, nil
}
