package ui

import (
	"strconv"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// requestPinnedMessages opens the pinned list straight away and asks the backend to fill it.
// The search is a network round trip, so waiting for the result before drawing anything made
// the key press feel dead; the panel appears now and the rows arrive into it.
func (a *App) requestPinnedMessages() {
	if a.currentChat == "" {
		a.setStatusMsg(i18n.KeyStatusNoChatSelected)
		return
	}
	peerKey := a.currentChat
	cached := a.pinnedCache
	if a.pinnedCachePeer != peerKey {
		cached = nil
	}
	a.openPinnedPanel(peerKey, cached)
	a.commands <- telegram.Command{Kind: telegram.CommandLoadPinned, PeerKey: peerKey}
}

func (a *App) openPinnedPanel(peerKey string, cached []telegram.Message) {
	list := tview.NewList().ShowSecondaryText(true)
	hint := tview.NewTextView().SetDynamicColors(true)

	a.pinnedList = list
	a.pinnedHint = hint
	a.pinnedPeer = peerKey

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(list, 0, 1, true).
		AddItem(hint, 1, 0, false)
	layout.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyPinnedTitleShort) + " ")
	// Esc is handled in App.capture, which runs before this and would swallow it anyway;
	// see the Esc branch there.

	// Showing the previous result for this chat while the refresh runs keeps a reopen
	// instant instead of flashing a spinner over content we already had.
	a.fillPinnedList(cached, true)

	a.app.SetRoot(layout, true)
	a.app.SetFocus(list)
}

func (a *App) closePinnedPanel() {
	a.pinnedList = nil
	a.pinnedHint = nil
	a.pinnedPeer = ""
	a.restoreMessageFocus()
}

// fillPinnedList rebuilds the panel rows. loading marks the state where a refresh is still in
// flight, which changes only the footer hint and the placeholder for an empty list.
func (a *App) fillPinnedList(messages []telegram.Message, loading bool) {
	if a.pinnedList == nil {
		return
	}
	a.pinnedList.Clear()
	if len(messages) == 0 {
		if loading {
			a.pinnedList.AddItem(i18n.T(i18n.KeyPinnedLoading), "", 0, nil)
		} else {
			a.pinnedList.AddItem(i18n.T(i18n.KeyPinnedEmpty), "", 0, nil)
		}
	}
	rowWidth := a.chatListRowWidth()
	for _, msg := range messages {
		id := msg.ID
		a.pinnedList.AddItem(render.PinnedRow(msg, rowWidth), msg.Author, 0, func() {
			a.closePinnedPanel()
			a.jumpToMessageID(id)
		})
	}
	if a.pinnedHint != nil {
		if loading {
			a.pinnedHint.SetText("[gray]" + i18n.T(i18n.KeyPinnedLoading))
		} else {
			a.pinnedHint.SetText(i18n.T(i18n.KeyPinnedHint))
		}
	}
	if title := a.pinnedTitle(messages, loading); title != "" && a.pinnedList != nil {
		a.pinnedList.SetTitle(title)
	}
}

func (a *App) pinnedTitle(messages []telegram.Message, loading bool) string {
	if loading && len(messages) == 0 {
		return ""
	}
	return " " + i18n.Tf(i18n.KeyPinnedTitle, len(messages)) + " "
}

// applyPinnedMessages fills the open panel, or just records the result when the user has
// already closed it or moved to another chat.
func (a *App) applyPinnedMessages(peerKey string, messages []telegram.Message) {
	a.pinnedCache = messages
	a.pinnedCachePeer = peerKey
	if a.pinnedList == nil || a.pinnedPeer != peerKey {
		return
	}
	a.fillPinnedList(messages, false)
}

// jumpToMessageID selects the target if it is already loaded, otherwise it asks the backend
// for a window centred on it. Paging backwards from the viewport never arrived for targets
// far from the loaded range, so this is the single path all jumps use.
func (a *App) jumpToMessageID(id string) {
	if id == "" {
		return
	}
	if a.selectMessageByID(id) {
		a.setStatusMsg(i18n.KeyStatusJumpedToMessage)
		return
	}
	messageID, err := strconv.Atoi(id)
	if err != nil || messageID <= 0 {
		a.setStatusMsg(i18n.KeyStatusMessageNotFound)
		return
	}
	a.commands <- telegram.Command{Kind: telegram.CommandJumpToMessage, PeerKey: a.currentChat, MessageID: messageID}
	a.setStatusMsg(i18n.KeyStatusJumpingToMessage)
}

// pinnableMessage reports whether pinning is worth offering for a message.
//
// Only the server knows whether this account may pin in this chat, so this is deliberately a weak
// filter: it excludes what can never be pinned - service rows and local sends Telegram has never
// seen - and leaves the permission question to CHAT_ADMIN_REQUIRED.
func (a *App) pinnableMessage(msg telegram.Message) bool {
	if msg.ServiceKey != "" || msg.State != "synced" {
		return false
	}
	id, err := strconv.Atoi(msg.ID)
	return err == nil && id > 0
}

// messageIsPinned reports whether this message is among the chat's pinned ones.
//
// Read from the cached pinned list, which is what the banner and the pinned panel already show, so
// the action offered matches what the user can see rather than a second source of truth.
func (a *App) messageIsPinned(msg telegram.Message) bool {
	if a.pinnedCachePeer != msg.ChatID {
		return false
	}
	for _, pinned := range a.pinnedCache {
		if pinned.ID == msg.ID {
			return true
		}
	}
	return false
}

// requestPin pins or unpins a message.
func (a *App) requestPin(msg telegram.Message, unpin bool) {
	id, err := strconv.Atoi(msg.ID)
	if err != nil || id <= 0 {
		a.setStatusMsg(i18n.KeyStatusPinNotSynced)
		return
	}
	if a.commands == nil {
		return
	}
	a.commands <- telegram.Command{
		Kind:      telegram.CommandPinMessage,
		PeerKey:   msg.ChatID,
		MessageID: id,
		Unpin:     unpin,
	}
}
