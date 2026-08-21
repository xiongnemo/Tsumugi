package telegram

import (
	"context"
	"fmt"
	mrand "math/rand"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

func (c *GotdClient) peerHistoryLock(peerKey string) *sync.Mutex {
	c.peerHistoryMu.Lock()
	defer c.peerHistoryMu.Unlock()
	if c.peerHistoryLocks == nil {
		c.peerHistoryLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := c.peerHistoryLocks[peerKey]
	if !ok {
		mu = &sync.Mutex{}
		c.peerHistoryLocks[peerKey] = mu
	}
	return mu
}

func (c *GotdClient) beginForegroundLoad() {
	c.fgLoadMu.Lock()
	c.fgLoads++
	c.fgLoadMu.Unlock()
}

func (c *GotdClient) endForegroundLoad() {
	c.fgLoadMu.Lock()
	if c.fgLoads > 0 {
		c.fgLoads--
	}
	c.fgLoadMu.Unlock()
}

func (c *GotdClient) foregroundActive() bool {
	c.fgLoadMu.Lock()
	defer c.fgLoadMu.Unlock()
	return c.fgLoads > 0
}

func sendBackgroundState(ctx context.Context, events chan<- Event, state BackgroundState) {
	sendEvent(ctx, events, Event{Background: &state})
}

func (c *GotdClient) syncDialogMetadataOnly(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	if c.store == nil {
		return
	}
	sendBackgroundState(ctx, events, BackgroundState{Kind: BackgroundDialogs, Detail: i18n.T(i18n.KeyStatusSyncDialogs)})
	// Before the dialog pages, because the first chat list is emitted from inside them and every
	// dialog without a mute of its own resolves against these.
	c.syncNotifyDefaults(ctx, accountID, api)
	_ = c.syncDialogPages(ctx, accountID, api, events, 0, false)
	_ = c.syncDialogPages(ctx, accountID, api, events, 1, true)
	c.syncAllPinnedDialogs(ctx, accountID, api, events)
	c.refreshChatsFromStore(ctx, accountID, events)
	sendBackgroundState(ctx, events, BackgroundState{Kind: BackgroundIdle})
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusDialogsReady)})
}

func (c *GotdClient) lazyBackfill(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	if c.store == nil {
		return
	}
	const recentWindow = 7 * 24 * time.Hour
	const maxPeersPerRound = 20
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if c.foregroundActive() {
			sendBackgroundState(ctx, events, BackgroundState{Kind: BackgroundPaused})
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		// Filtered and limited in SQL rather than by loading every peer and discarding almost
		// all of it: at a few thousand dialogs that was tens of megabytes of garbage every few
		// seconds to choose twenty rows. The horizon is what makes this terminate at all — see
		// RecentPeersForBackfill.
		now := time.Now().UTC()
		depth := c.effectiveBackfillHorizon(ctx)
		if depth == 0 {
			// Retention is short enough that prefetching would only feed the pruner. On-demand
			// loading still covers scrolling back. Re-checked on a timer so changing the setting
			// takes effect without a restart.
			sendBackgroundState(ctx, events, BackgroundState{Kind: BackgroundIdle})
			if !sleepCtx(ctx, 5*time.Minute) {
				return
			}
			continue
		}
		cutoff := now.Add(-recentWindow)
		horizon := now.Add(-depth)
		recent, err := c.store.RecentPeersForBackfill(ctx, accountID, cutoff, horizon, maxPeersPerRound)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		if len(recent) == 0 {
			sendBackgroundState(ctx, events, BackgroundState{Kind: BackgroundIdle})
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for i, p := range recent {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if c.foregroundActive() {
				sendBackgroundState(ctx, events, BackgroundState{Kind: BackgroundPaused})
				break
			}
			if c.backfillSkips.skipped(p.Key) {
				// Failed terminally earlier this session. Retrying it is what turned the backfill
				// into a busy loop against fifty unreachable peers.
				continue
			}
			sendBackgroundState(ctx, events, BackgroundState{
				Kind:    BackgroundBackfill,
				Current: i + 1,
				Total:   len(recent),
				Detail:  p.Title,
			})
			c.syncPeerHistory(ctx, accountID, api, p, events, 1)
			jitter := time.Duration(800+mrand.Intn(1200)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}
		}
		sendBackgroundState(ctx, events, BackgroundState{Kind: BackgroundIdle})
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *GotdClient) fillHistoryGap(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, offsetID int) {
	if c.store == nil || peerKey == "" || offsetID <= 0 {
		return
	}
	if !c.tryBeginOlderLoad(peerKey, offsetID) {
		return
	}
	defer c.endOlderLoad(peerKey, offsetID)

	c.beginForegroundLoad()
	defer c.endForegroundLoad()

	c.peerHistoryLock(peerKey).Lock()
	defer c.peerHistoryLock(peerKey).Unlock()

	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}

	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusGapFilling)})

	var collected []storage.Message
	for round := 0; round < 8; round++ {
		select {
		case <-ctx.Done():
			return
		default:
		}
		history, err := c.messagesGetHistory(ctx, api, &tg.MessagesGetHistoryRequest{
			Peer:     input,
			OffsetID: offsetID,
			Limit:    50,
		})
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("fill history gap: %w", err)})
			return
		}
		msgs := c.normalizeMessagesWithPreview(ctx, api, accountID, peerKey, history, false)
		if len(msgs) == 0 {
			break
		}
		if err := c.store.SaveMessages(ctx, msgs); err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save gap history: %w", err)})
			return
		}
		_ = c.store.UpdatePeerHistory(ctx, accountID, peerKey, minStorageMessageID(msgs), time.Now().UTC())
		collected = append(collected, msgs...)
		c.refreshChannelViews(ctx, accountID, api, events, p, msgs)

		newOffset := minStorageMessageID(msgs)
		if newOffset <= 0 || newOffset == offsetID {
			break
		}
		offsetID = newOffset
		if len(msgs) < 50 {
			break
		}
	}
	if len(collected) > 0 {
		sendEvent(ctx, events, Event{
			Kind:             EventMessages,
			PeerKey:          peerKey,
			Messages:         c.telegramMessages(ctx, accountID, collected),
			Merge:            true,
			PreserveViewport: true,
			StatusMsg:        i18n.M(i18n.KeyStatusGapFilled),
		})
	}
}

func (c *GotdClient) fillKnownGapsForPeer(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string) {
	if c.store == nil || peerKey == "" {
		return
	}
	if !c.tryBeginGapFillSession(peerKey) {
		return
	}
	defer c.endGapFillSession(peerKey)

	const gapScanLimit = 5000
	stored, err := c.store.MessagesForPeer(ctx, accountID, peerKey, gapScanLimit)
	if err != nil || len(stored) < 2 {
		return
	}
	msgs := c.telegramMessages(ctx, accountID, stored)
	gaps := FindHistoryGaps(msgs, DefaultHistoryGapThreshold)
	const maxGapsPerPass = 3
	if len(gaps) > maxGapsPerPass {
		gaps = gaps[:maxGapsPerPass]
	}
	for _, offsetID := range gaps {
		select {
		case <-ctx.Done():
			return
		default:
		}
		c.fillHistoryGap(ctx, accountID, api, events, peerKey, offsetID)
	}
}

func (c *GotdClient) startSyncWorkers(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	if c.cfg.SyncMode == config.SyncFull {
		go c.backgroundSync(ctx, accountID, api, events)
		go c.backfillHistory(ctx, accountID, api, events)
		// Full sync deliberately keeps everything, so retention would undo its whole point.
		return
	}
	go c.syncDialogMetadataOnly(ctx, accountID, api, events)
	go c.lazyBackfill(ctx, accountID, api, events)
	go c.pruneOldMessages(ctx, accountID)
}

// How deep the backfill reaches is settings.KeyBackfillDays, resolved by
// effectiveBackfillHorizon. It needs to be a horizon rather than "as far as possible": without one
// the backfill walked every recent peer's history backwards a page per round indefinitely, which on
// a real account meant six million stored messages and a 3.7GB database inside a day.
