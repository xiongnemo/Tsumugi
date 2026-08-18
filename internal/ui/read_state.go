package ui

import (
	"github.com/nemo/Tsumugi/internal/telegram"
)

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
