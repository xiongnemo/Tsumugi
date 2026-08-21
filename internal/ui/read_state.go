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
	// Read from a cached flag rather than a.app.GetFocus(). refreshFooter is reachable from the
	// before-draw hook, and Application.draw holds the write lock while it runs that hook, so
	// asking the Application anything that takes RLock deadlocks on the first draw — a
	// sync.RWMutex is not reentrant. That is a black screen with the process still alive.
	state.MessagePaneFocused = a.messagePaneFocused
	// The footer sits in the right pane, so its own width is the one that matters, not the
	// terminal's. Zero before the first draw, which FooterKeys reads as "list everything".
	if _, _, w, _ := a.footer.GetInnerRect(); w > 0 {
		state.Width = w
	}
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

// dismissTopOverlay closes the frontmost overlay, reporting whether there was one.
//
// Shared by Esc and q so the two can never disagree about what is on top, and so a new overlay is
// covered by both the moment it is added here. The order is outermost-last: the message action
// panel sits above the pinned, forward and search panels because it is opened from them.
//
// The QR login prompt is deliberately absent. Abandoning a half-finished login to a stray keystroke
// is not a dismissal, it is data loss; that overlay offers p to fall back and Ctrl+C to give up.
func (a *App) dismissTopOverlay() bool {
	switch {
	case a.msgActionForm != nil || a.msgActionPreview != nil || a.msgActionDetail != nil:
		a.restoreMessageFocus()
		return true
	case a.pinnedList != nil:
		a.closePinnedPanel()
		return true
	case a.forwardList != nil:
		a.closeForwardPicker()
		return true
	case a.searchList != nil:
		a.closeSearchResults()
		return true
	case a.attachList != nil:
		a.closeAttachPicker()
		return true
	case a.pollList != nil:
		a.closePollVote()
		return true
	case a.chatOpsList != nil:
		a.closeChatOps()
		return true
	case a.proxyOverlay != nil:
		// Its own capture normally gets Esc and q first; registered here anyway because an overlay
		// missing from this list is one nothing else can dismiss, which is how the pinned panel
		// leaked its state on every Esc.
		a.closeProxyPanel()
		return true
	default:
		return false
	}
}
