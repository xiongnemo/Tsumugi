package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/telegram"
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
