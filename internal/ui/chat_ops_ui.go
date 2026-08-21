package ui

import (
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// chatOp is one row of the chat actions overlay.
type chatOp struct {
	ID    string
	Label string
}

// openChatOps lists what can be done to a whole chat.
//
// One overlay on `m` rather than four global keys. Mute, pin, archive and leave are all rare and all
// need confirming by reading a label - spending four of the remaining letters on them would be a bad
// trade, and a list is where a user looks for "what can I do to this chat".
func (a *App) openChatOps() {
	peerKey, title := a.chatOpsTarget()
	if peerKey == "" {
		a.setStatusMsg(i18n.KeyStatusNoChatSelected)
		return
	}

	muted := a.chatIsMuted(peerKey)
	pinned := a.chatIsPinned(peerKey)
	archived := a.chatIsArchived(peerKey)

	ops := []chatOp{}
	if muted {
		ops = append(ops, chatOp{ID: "unmute", Label: i18n.T(i18n.KeyChatOpsUnmute)})
	} else {
		ops = append(ops, chatOp{ID: "mute", Label: i18n.T(i18n.KeyChatOpsMute)})
	}
	if pinned {
		ops = append(ops, chatOp{ID: "unpin", Label: i18n.T(i18n.KeyChatOpsUnpin)})
	} else {
		ops = append(ops, chatOp{ID: "pin", Label: i18n.T(i18n.KeyChatOpsPin)})
	}
	if archived {
		ops = append(ops, chatOp{ID: "unarchive", Label: i18n.T(i18n.KeyChatOpsUnarchive)})
	} else {
		ops = append(ops, chatOp{ID: "archive", Label: i18n.T(i18n.KeyChatOpsArchive)})
	}
	// Leaving is only offered where it means something: a private chat has nothing to leave.
	if telegram.LeavableChat(peerKey) {
		ops = append(ops, chatOp{ID: "leave", Label: i18n.T(i18n.KeyChatOpsLeave)})
	}

	list := tview.NewList().ShowSecondaryText(false)
	for _, op := range ops {
		list.AddItem(op.Label, "", 0, nil)
	}
	hint := tview.NewTextView().SetDynamicColors(true).SetText("[gray]" + i18n.T(i18n.KeyChatOpsHint))

	a.chatOpsList = list
	a.chatOpsRows = ops
	a.chatOpsPeer = peerKey

	list.SetSelectedFunc(func(index int, _, _ string, _ rune) { a.commitChatOp(index) })

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(list, 0, 1, true).
		AddItem(hint, 1, 0, false)
	layout.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyChatOpsTitle) + " · " + render.Truncate(title, 40) + " ")

	a.app.SetRoot(layout, true)
	a.app.SetFocus(list)
}

// chatOpsTarget is the chat the actions apply to: the highlighted row when the chat list has focus,
// otherwise the open conversation.
func (a *App) chatOpsTarget() (string, string) {
	if a.chats != nil && a.app != nil && a.app.GetFocus() == a.chats {
		// Through the same mapping the selection callback uses: the List index is not the index into
		// the folder's chats once pinned rows and folder filters are applied.
		visible := a.visibleChatsForFolder()
		if idx := a.chatsVisibleIndexFromListIndex(a.chats.GetCurrentItem()); idx >= 0 && idx < len(visible) {
			return visible[idx].ID, visible[idx].Title
		}
	}
	if a.draftablePeer(a.currentChat) {
		return a.currentChat, a.currentTitle
	}
	return "", ""
}

func (a *App) closeChatOps() {
	a.chatOpsList = nil
	a.chatOpsRows = nil
	a.chatOpsPeer = ""
	a.app.SetRoot(a.root, true)
	a.app.SetFocus(a.chats)
	a.updateFocusStyle()
}

// commitChatOp runs the highlighted action.
func (a *App) commitChatOp(index int) {
	if index < 0 || index >= len(a.chatOpsRows) || a.commands == nil {
		return
	}
	op := a.chatOpsRows[index]
	peerKey := a.chatOpsPeer
	if op.ID == "leave" {
		// The only one of these that cannot be undone from here, so it asks first.
		a.confirmLeaveChat(peerKey)
		return
	}
	command := telegram.Command{PeerKey: peerKey}
	switch op.ID {
	case "mute":
		command.Kind, command.Mute = telegram.CommandMutePeer, true
	case "unmute":
		command.Kind, command.Mute = telegram.CommandMutePeer, false
	case "pin":
		command.Kind = telegram.CommandPinDialog
	case "unpin":
		command.Kind, command.Unpin = telegram.CommandPinDialog, true
	case "archive":
		command.Kind = telegram.CommandArchiveDialog
	case "unarchive":
		command.Kind, command.Unpin = telegram.CommandArchiveDialog, true
	default:
		return
	}
	a.closeChatOps()
	a.commands <- command
}

// confirmLeaveChat asks before leaving, because rejoining may not be possible.
func (a *App) confirmLeaveChat(peerKey string) {
	title := a.chatTitleFor(peerKey)
	modal := tview.NewModal().
		SetText(i18n.Tf(i18n.KeyChatOpsLeaveBody, title)).
		AddButtons([]string{i18n.T(i18n.KeyChatOpsLeaveConfirm), i18n.T(i18n.KeyActionCancel)}).
		SetDoneFunc(func(_ int, label string) {
			confirmed := label == i18n.T(i18n.KeyChatOpsLeaveConfirm)
			a.closeChatOps()
			if confirmed && a.commands != nil {
				a.commands <- telegram.Command{Kind: telegram.CommandLeaveChat, PeerKey: peerKey}
			}
		})
	a.app.SetRoot(modal, true)
	a.app.SetFocus(modal)
}

func (a *App) chatIsPinned(peerKey string) bool {
	if idx := chatIndexByID(a.allChats, peerKey); idx >= 0 {
		return a.allChats[idx].Pinned
	}
	return false
}

// chatIsArchived reads Telegram's own folder id, not Tsumugi's client-side archive folder.
func (a *App) chatIsArchived(peerKey string) bool {
	if idx := chatIndexByID(a.allChats, peerKey); idx >= 0 {
		return a.allChats[idx].FolderID == 1
	}
	return false
}
