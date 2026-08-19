package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
	"github.com/nemo/Tsumugi/internal/version"
)

// markReadInterval coalesces read marks while the user scrolls. readHistory is monotonic, so
// dropping intermediate values is safe as long as the last one lands — which the pending slot
// guarantees.
const markReadInterval = 1200 * time.Millisecond

// applyReadInboxEvent refreshes one chat's unread badge.
//
// Before read state was tracked, the badge only changed when a dialog sync happened to come
// around, so reading a chat on your phone left a stale count here for minutes.
func (a *App) applyReadInboxEvent(event telegram.Event) {
	idx := chatIndexByID(a.allChats, event.PeerKey)
	if idx < 0 {
		return
	}
	// Lazily created: tests construct App directly rather than through New.
	if a.readInboxMaxID == nil {
		a.readInboxMaxID = make(map[string]int)
	}
	if event.ReadInboxMaxID > a.readInboxMaxID[event.PeerKey] {
		a.readInboxMaxID[event.PeerKey] = event.ReadInboxMaxID
	}
	if a.allChats[idx].Unread == event.Unread {
		return
	}
	a.allChats[idx].Unread = event.Unread
	a.refreshChats()
}

// applyHistoryWindow records whether an incoming message set is a window from the middle of the
// history, and which message the unread divider belongs above.
func (a *App) applyHistoryWindow(event telegram.Event) {
	a.historyWindowed = event.WindowedHistory
	a.unreadDividerID = event.FirstUnreadID
	a.messages.SetUnreadDividerID(event.FirstUnreadID)
}

// returnToTail re-opens the current chat anchored on the newest messages.
//
// No new backend code: CommandOpenChat without JumpToUnread emits the newest window, which is
// already cached, so this is effectively instant. Bound to End and G because a user dropped into
// the middle of a long history otherwise has no visible way back.
func (a *App) returnToTail() bool {
	if !a.historyWindowed || !a.draftablePeer(a.currentChat) || a.commands == nil {
		return false
	}
	a.historyWindowed = false
	a.unreadDividerID = ""
	a.messages.SetUnreadDividerID("")
	a.commands <- telegram.Command{Kind: telegram.CommandOpenChat, PeerKey: a.currentChat}
	a.setStatusMsg(i18n.KeyStatusAtTail)
	return true
}

// onMessageCursorMoved is the viewport's selection-changed callback: it both marks read and
// keeps the broadcast view count going.
func (a *App) onMessageCursorMoved() {
	a.onMessageSelectionChanged()
	a.scheduleMarkRead()
}

// scheduleMarkRead marks the chat read up to the selected message, coalesced.
//
// Driven by the cursor advancing rather than by opening a chat, which covers chat open for free
// because a non-preserving replace lands the selection on the newest message. Auto-jumping
// without marking read would strand the user on the same message forever.
func (a *App) scheduleMarkRead() {
	// Bots cannot read history, and there is nobody to report a read to in Saved Messages.
	if a.cfg.AuthMode == config.AuthBot || !a.readablePeer(a.currentChat) {
		return
	}
	msg, ok := a.messages.SelectedMessage()
	if !ok || msg.ChatID != a.currentChat {
		return
	}
	a.scheduleMarkReadUpTo(a.currentChat, msg.ID)
}

// scheduleMarkReadUpTo queues a read mark for one peer, coalescing with any already pending.
func (a *App) scheduleMarkReadUpTo(peerKey, messageID string) {
	if a.cfg.AuthMode == config.AuthBot || !a.readablePeer(peerKey) {
		return
	}
	id, err := strconv.Atoi(messageID)
	if err != nil || id <= 0 {
		// A local pending or failed send has no server id to read up to.
		return
	}
	if id <= a.markReadSentMaxID[peerKey] {
		return
	}
	if a.markReadPending == nil {
		a.markReadPending = make(map[string]int)
	}
	if id > a.markReadPending[peerKey] {
		a.markReadPending[peerKey] = id
	}
	if a.markReadTimer != nil {
		return
	}
	wait := markReadInterval - time.Since(a.markReadSentAt)
	if wait <= 0 {
		a.flushMarkRead()
		return
	}
	a.markReadTimer = time.AfterFunc(wait, func() {
		if a.app == nil {
			return
		}
		a.app.QueueUpdate(func() {
			a.markReadTimer = nil
			a.flushMarkRead()
		})
	})
}

// flushMarkRead sends the highest pending read mark for every peer that has one.
//
// Every peer, not just the open one: a read mark must complete even if the user switched chats
// while it was waiting out the interval. For the same reason the client deliberately does not
// re-check whether the peer is still focused.
func (a *App) flushMarkRead() {
	if len(a.markReadPending) == 0 {
		return
	}
	if a.markReadSentMaxID == nil {
		a.markReadSentMaxID = make(map[string]int)
	}
	for peerKey, id := range a.markReadPending {
		if id <= a.markReadSentMaxID[peerKey] {
			continue
		}
		if !a.sendMarkReadCommand(peerKey, id) {
			continue
		}
		a.markReadSentMaxID[peerKey] = id
	}
	a.markReadPending = nil
	a.markReadSentAt = time.Now()
}

func (a *App) sendMarkReadCommand(peerKey string, messageID int) bool {
	if a.commands == nil {
		return false
	}
	select {
	case a.commands <- telegram.Command{Kind: telegram.CommandMarkRead, PeerKey: peerKey, MessageID: messageID}:
		return true
	default:
		// Leave it pending rather than recording it as sent, so the next cursor move retries.
		return false
	}
}

// readablePeer excludes the synthetic rows shown before a connection exists, and Saved Messages,
// which has no unread state of its own.
func (a *App) readablePeer(peerKey string) bool {
	switch {
	case peerKey == "", peerKey == "welcome", peerKey == "empty":
		return false
	case strings.HasPrefix(peerKey, "self:"):
		return false
	default:
		return true
	}
}

// refreshFooter redraws the footer for the current context.
//
// Called from anywhere that changes what the keys mean, so the hints never describe a state the
// user has already left.
func (a *App) refreshFooter() {
	if a.footer == nil {
		return
	}
	state := render.FooterState{}
	if a.messages != nil {
		state.MarkedCount = a.messages.MarkedCount()
	}
	state.SearchHits = len(a.searchHits)
	a.footer.SetText(render.FooterWithState(state, string(a.cfg.AuthMode), version.String(), a.cfg.Proxy))
}

// onReachNewerMessages asks for the page after the newest message held.
//
// Only meaningful while the pane shows a mid-history window: at the tail there is nothing newer, and
// asking would be a round trip per scroll.
func (a *App) onReachNewerMessages() {
	if !a.historyWindowed || !a.draftablePeer(a.currentChat) || a.commands == nil {
		return
	}
	msgs := a.messages.Messages()
	if len(msgs) == 0 {
		return
	}
	newest, err := strconv.Atoi(msgs[len(msgs)-1].ID)
	if err != nil || newest <= 0 {
		return
	}
	select {
	case a.commands <- telegram.Command{Kind: telegram.CommandLoadNewer, PeerKey: a.currentChat, MessageID: newest}:
	default:
		// Dropping one is fine; the next scroll at the boundary asks again.
	}
}

// jumpToLatest reloads the chat at its newest messages.
//
// Unconditional, unlike returnToTail: bound to G it has to mean "take me to the end" whether or not
// the pane happens to be showing a window, because a user who wants the latest message should not
// have to know which state they are in. At the tail it is a cache hit and effectively free.
func (a *App) jumpToLatest() {
	if !a.draftablePeer(a.currentChat) || a.commands == nil {
		a.messages.ScrollToEnd()
		return
	}
	a.historyWindowed = false
	a.unreadDividerID = ""
	a.messages.SetUnreadDividerID("")
	a.commands <- telegram.Command{Kind: telegram.CommandOpenChat, PeerKey: a.currentChat}
	a.setStatusMsg(i18n.KeyStatusAtTail)
}
