package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func (a *App) setStatusMsg(key string, args ...any) {
	msg := i18n.M(key, args...)
	if isForegroundStatusKey(key) {
		a.foregroundStatusMsg = msg
		a.foregroundStatusAt = time.Now()
	} else {
		a.lastStatusMsg = msg
	}
	a.refreshStatusBar()
}

func (a *App) setStatusFrom(msg i18n.Msg) {
	if msg.IsZero() {
		return
	}
	switch msg.Key {
	case i18n.KeyStatusConnecting:
		a.connStatusMsg = msg
	case i18n.KeyStatusConnectedAs:
		a.connStatusMsg = msg
	default:
		if isForegroundStatusKey(msg.Key) {
			a.foregroundStatusMsg = msg
			a.foregroundStatusAt = time.Now()
		} else if !isBackgroundStatusKey(msg.Key) {
			a.lastStatusMsg = msg
		}
	}
	a.refreshStatusBar()
}

func (a *App) setBackgroundState(state *telegram.BackgroundState) {
	if state == nil || state.Kind == telegram.BackgroundIdle {
		a.backgroundState = nil
	} else {
		copy := *state
		a.backgroundState = &copy
	}
	a.refreshStatusBar()
}

func (a *App) setStatusText(text string) {
	a.foregroundStatusMsg = i18n.Msg{}
	a.lastStatusMsg = i18n.Msg{}
	if a.statusForeground != nil {
		a.statusForeground.SetText(truncateStatus(text, 48))
	}
}

func (a *App) setStatusError(err error) {
	if err == nil {
		return
	}
	a.setStatusMsg(i18n.KeyStatusError, err.Error())
}

func (a *App) renderLastStatus() {
	a.refreshStatusBar()
}

func (a *App) refreshStatusBar() {
	if a.statusConn == nil || a.statusForeground == nil || a.statusBackground == nil {
		return
	}
	a.statusConn.SetText(truncateStatus(a.connStatusLine(), 24))
	a.statusForeground.SetText(truncateStatus(a.foregroundStatusLine(), 56))
	a.statusBackground.SetText(truncateStatus(a.backgroundStatusLine(), 28))
}

func (a *App) connStatusLine() string {
	if !a.connStatusMsg.IsZero() {
		return a.connStatusMsg.String()
	}
	if a.connectedAs != "" {
		name := a.connectedAs
		if len([]rune(name)) > 12 {
			name = string([]rune(name)[:12]) + "…"
		}
		return i18n.Tf(i18n.KeyStatusConnectedAs, name)
	}
	return i18n.T(i18n.KeyStatusConnectedShort)
}

func foregroundStatusTTL(key string) time.Duration {
	switch key {
	case i18n.KeyStatusGapFilling, i18n.KeyStatusFetchingOlderNetwork,
		i18n.KeyStatusLoadingOlderMessages, i18n.KeyStatusLoadingMediaPreviews,
		i18n.KeyStatusLayoutSwitching:
		return 120 * time.Second
	default:
		return 5 * time.Second
	}
}

func (a *App) foregroundStatusLine() string {
	if !a.foregroundStatusMsg.IsZero() {
		if time.Since(a.foregroundStatusAt) > foregroundStatusTTL(a.foregroundStatusMsg.Key) {
			a.foregroundStatusMsg = i18n.Msg{}
		} else {
			return a.foregroundStatusMsg.String()
		}
	}
	if !a.lastStatusMsg.IsZero() {
		return a.lastStatusMsg.String()
	}
	return i18n.T(i18n.KeyStatusForegroundIdle)
}

func (a *App) backgroundStatusLine() string {
	if a.backgroundState == nil || a.backgroundState.Kind == telegram.BackgroundIdle {
		return ""
	}
	switch a.backgroundState.Kind {
	case telegram.BackgroundDialogs:
		if a.backgroundState.Detail != "" {
			return a.backgroundState.Detail
		}
		return i18n.T(i18n.KeyStatusSyncDialogs)
	case telegram.BackgroundBackfill:
		if a.backgroundState.Total > 0 {
			return i18n.Tf(i18n.KeyStatusBackfillProgress, a.backgroundState.Current, a.backgroundState.Total)
		}
		return i18n.T(i18n.KeyStatusSyncDialogs)
	case telegram.BackgroundPaused:
		return i18n.T(i18n.KeyStatusBackfillPaused)
	default:
		return ""
	}
}

func (a *App) messagesPaneTitleText() string {
	t := strings.TrimSpace(a.currentTitle)
	if t == "" {
		t = i18n.T(i18n.KeyUIMessages)
	}
	switch a.currentChat {
	case "", "welcome", "empty":
		return t
	}
	// Directly after the peer title: the title is truncated from the right on a narrow
	// terminal, and who is typing is worth more than the loaded-count and date range that
	// follow it.
	if hint := a.typingTitleHint(); hint != "" {
		t += " · " + hint
	}
	msgs := a.messages.Messages()
	if len(msgs) == 0 {
		return fmt.Sprintf("%s · %s", t, i18n.T(i18n.KeyStatusChatNoMessages))
	}
	oldest := msgs[0].CreatedAt.Local().Format("01-02")
	newest := msgs[len(msgs)-1].CreatedAt.Local().Format("01-02")
	title := fmt.Sprintf("%s · %s · %s→%s", t, i18n.Tf(i18n.KeyUIMessagesRecentLoaded, len(msgs)), oldest, newest)
	if hint := a.gapFillTitleHint(); hint != "" {
		title += " · " + hint
	} else if hint := a.mediaPreviewTitleHint(); hint != "" {
		title += " · " + hint
	} else if hint := a.olderLoadTitleHint(); hint != "" {
		title += " · " + hint
	} else if gap := a.historyGapHint(); gap != "" {
		title += " ⚠" + gap
	}
	if n := a.messages.PendingBelow(); n > 0 {
		title += " · " + fmt.Sprintf(i18n.T(i18n.KeyUINewInTitle), n)
	}
	if hint := a.markedTitleHint(); hint != "" {
		title += " · " + hint
	}
	if a.historyWindowed {
		// Without this the user is stranded in the middle of a long history with no visible
		// way back to the latest messages.
		title += " · " + i18n.T(i18n.KeyUIWindowedHistory)
	}
	return title
}

func (a *App) historyGapHint() string {
	gaps := telegram.FindHistoryGapDetails(a.messages.Messages(), telegram.DefaultHistoryGapThreshold)
	if len(gaps) == 0 {
		return ""
	}
	g := gaps[0]
	days := int(g.NewerTime.Sub(g.OlderTime).Hours() / 24)
	if days < 1 {
		days = 1
	}
	return i18n.Tf(i18n.KeyStatusHistoryGap, days)
}

func (a *App) mediaPreviewTitleHint() string {
	if !a.foregroundStatusMsg.IsZero() &&
		a.foregroundStatusMsg.Key == i18n.KeyStatusLoadingMediaPreviews &&
		time.Since(a.foregroundStatusAt) < foregroundStatusTTL(i18n.KeyStatusLoadingMediaPreviews) {
		return a.foregroundStatusMsg.String()
	}
	return ""
}

func (a *App) olderLoadTitleHint() string {
	if !a.foregroundStatusMsg.IsZero() {
		switch a.foregroundStatusMsg.Key {
		case i18n.KeyStatusLoadingOlderMessages, i18n.KeyStatusFetchingOlderNetwork:
			if time.Since(a.foregroundStatusAt) < foregroundStatusTTL(a.foregroundStatusMsg.Key) {
				return a.foregroundStatusMsg.String()
			}
		}
	}
	return ""
}

func (a *App) gapFillTitleHint() string {
	if !a.foregroundStatusMsg.IsZero() &&
		a.foregroundStatusMsg.Key == i18n.KeyStatusGapFilling &&
		time.Since(a.foregroundStatusAt) < foregroundStatusTTL(i18n.KeyStatusGapFilling) {
		return i18n.T(i18n.KeyStatusGapFilling)
	}
	if len(a.gapFillQueued) > 0 {
		return i18n.T(i18n.KeyStatusGapFilling)
	}
	return ""
}

func (a *App) resetGapFillQueue() {
	a.gapFillQueued = nil
}

func gapFillQueueKey(peerKey string, offsetID int) string {
	return fmt.Sprintf("%s:%d", peerKey, offsetID)
}

func (a *App) pruneGapFillQueue() {
	if len(a.gapFillQueued) == 0 || a.currentChat == "" {
		return
	}
	active := make(map[string]struct{})
	for _, offsetID := range telegram.FindHistoryGaps(a.messages.Messages(), telegram.DefaultHistoryGapThreshold) {
		active[gapFillQueueKey(a.currentChat, offsetID)] = struct{}{}
	}
	for key := range a.gapFillQueued {
		if _, ok := active[key]; !ok {
			delete(a.gapFillQueued, key)
		}
	}
}

func (a *App) scheduleGapFillsIfNeeded() {
	if a.currentChat == "" || a.currentChat == "welcome" || a.currentChat == "empty" {
		return
	}
	gaps := telegram.FindHistoryGaps(a.messages.Messages(), telegram.DefaultHistoryGapThreshold)
	if len(gaps) == 0 {
		a.pruneGapFillQueue()
		return
	}
	a.pruneGapFillQueue()
	for _, offsetID := range gaps {
		key := gapFillQueueKey(a.currentChat, offsetID)
		if a.gapFillQueued != nil {
			if _, ok := a.gapFillQueued[key]; ok {
				continue
			}
		}
		if a.gapFillQueued == nil {
			a.gapFillQueued = make(map[string]struct{})
		}
		a.gapFillQueued[key] = struct{}{}
		a.commands <- telegram.Command{
			Kind:      telegram.CommandFillHistoryGap,
			PeerKey:   a.currentChat,
			MessageID: offsetID,
		}
	}
}

func isForegroundStatusKey(key string) bool {
	switch key {
	case i18n.KeyStatusLoadingHistory,
		i18n.KeyStatusHistoryLoaded,
		i18n.KeyStatusLoadingOlderMessages,
		i18n.KeyStatusLoadingMediaPreviews,
		i18n.KeyStatusMediaPreviewsReady,
		i18n.KeyStatusOlderCachedLoaded,
		i18n.KeyStatusFetchingOlderNetwork,
		i18n.KeyStatusOlderMessagesLoaded,
		i18n.KeyStatusOlderCacheOnlyGapFilled,
		i18n.KeyStatusNoOlderMessages,
		i18n.KeyStatusGapFilling,
		i18n.KeyStatusGapFilled,
		i18n.KeyStatusLayoutSwitching,
		i18n.KeyStatusSending,
		i18n.KeyStatusRetryingSend,
		i18n.KeyStatusDownloadingMedia,
		i18n.KeyStatusDeletingMessage,
		i18n.KeyStatusReplyLoadingOlder,
		i18n.KeyStatusError:
		return true
	default:
		return false
	}
}

func isBackgroundStatusKey(key string) bool {
	switch key {
	case i18n.KeyStatusSyncingBackground,
		i18n.KeyStatusSyncingProgress,
		i18n.KeyStatusSyncComplete,
		i18n.KeyStatusSyncDialogs,
		i18n.KeyStatusDialogsReady:
		return true
	default:
		return false
	}
}

func truncateStatus(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes-1]) + "…"
}
