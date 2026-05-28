package telegram

import "sort"

// PinStateForFolder returns whether chat should appear pinned in folder and its sort order.
func PinStateForFolder(chat Chat, folder Folder) (pinned bool, order int) {
	switch {
	case folder.ID == 0 || folder.Kind == "all":
		if chat.Pinned {
			order = chat.PinnedOrder
			if order == 0 {
				order = 1
			}
			return true, order
		}
	case folder.Kind == "telegram" && len(folder.Rules.PinnedPeers) > 0:
		for i, id := range folder.Rules.PinnedPeers {
			if id == chat.ID {
				return true, i + 1
			}
		}
		return false, 0
	default:
		if chat.Pinned {
			order = chat.PinnedOrder
			if order == 0 {
				order = 1
			}
			return true, order
		}
	}
	return false, 0
}

// ChatForFolderDisplay returns a copy of chat with Pinned/PinnedOrder set for list rendering.
func ChatForFolderDisplay(chat Chat, folder Folder) Chat {
	out := chat
	if pinned, order := PinStateForFolder(chat, folder); pinned {
		out.Pinned = true
		out.PinnedOrder = order
	} else if folder.Kind == "telegram" && folder.ID != 0 && len(folder.Rules.PinnedPeers) > 0 {
		out.Pinned = false
		out.PinnedOrder = 0
	}
	return out
}

// SortChatsForFolder orders visible chats for the active folder view.
func SortChatsForFolder(chats []Chat, folder Folder) {
	sort.SliceStable(chats, func(i, j int) bool {
		pi, oi := PinStateForFolder(chats[i], folder)
		pj, oj := PinStateForFolder(chats[j], folder)
		if pi != pj {
			return pi
		}
		if pi && pj && oi != oj {
			return oi < oj
		}
		if !chats[i].LastMessageAt.Equal(chats[j].LastMessageAt) {
			return chats[i].LastMessageAt.After(chats[j].LastMessageAt)
		}
		if chats[i].TopMessageID != chats[j].TopMessageID {
			return chats[i].TopMessageID > chats[j].TopMessageID
		}
		return chats[i].Title < chats[j].Title
	})
}
