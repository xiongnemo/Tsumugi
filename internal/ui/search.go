package ui

import (
	"strconv"
	"strings"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// hitKey identifies a search hit across viewport replacements.
//
// The cursor is stored by identity rather than index because a jump replaces the message list
// wholesale: an index would silently come to mean a different hit, which is exactly how `n` used
// to lose its place.
func hitKey(hit telegram.SearchHit) string {
	return hit.PeerKey + "/" + strconv.Itoa(hit.MessageID)
}

// findChatMatches returns the chats matching a query, in chat-list order.
func findChatMatches(chats []telegram.Chat, query string) []telegram.Chat {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}
	var out []telegram.Chat
	for _, chat := range chats {
		haystack := strings.ToLower(chat.Title + " " + chat.Subtitle + " " + chat.LastPreview)
		if strings.Contains(haystack, needle) {
			out = append(out, chat)
		}
	}
	return out
}

// findMessageMatches returns the loaded messages matching a query, oldest first.
//
// Local-first is deliberate for in-chat search: the viewport caps at 1000 messages, so
// local-only would miss history, while server-only would spend a round trip on the common
// "find what I just read" case.
func findMessageMatches(messages []telegram.Message, query string) []telegram.SearchHit {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}
	var out []telegram.SearchHit
	for _, msg := range messages {
		haystack := strings.ToLower(msg.Author + " " + msg.Text + " " + msg.Media.Label)
		if !strings.Contains(haystack, needle) {
			continue
		}
		id, err := strconv.Atoi(msg.ID)
		if err != nil || id <= 0 {
			// A local pending send cannot be jumped to.
			continue
		}
		preview := strings.ReplaceAll(msg.Text, "\n", " ")
		if strings.TrimSpace(preview) == "" {
			preview = msg.Media.Label
		}
		out = append(out, telegram.SearchHit{
			PeerKey:   msg.ChatID,
			PeerTitle: msg.Author,
			MessageID: id,
			Preview:   preview,
			Date:      msg.CreatedAt,
			Outgoing:  msg.Outgoing,
		})
	}
	return out
}

// stepSearchCursor moves through hits by identity.
//
// current is a hitKey, or "" for "not started". delta is +1 or -1. Returns the new hit's key and
// whether the move wrapped around the end of the list.
//
// When the remembered hit is gone — deleted, or scrolled out of a replaced window — it restarts
// from the appropriate end rather than resetting to the top, so `n` after a jump continues in the
// direction the user was already going.
func stepSearchCursor(hits []telegram.SearchHit, current string, delta int) (string, bool) {
	if len(hits) == 0 {
		return "", false
	}
	index := -1
	for i, hit := range hits {
		if hitKey(hit) == current {
			index = i
			break
		}
	}
	if index < 0 {
		if delta < 0 {
			return hitKey(hits[len(hits)-1]), false
		}
		return hitKey(hits[0]), false
	}
	next := index + delta
	switch {
	case next >= len(hits):
		return hitKey(hits[0]), true
	case next < 0:
		return hitKey(hits[len(hits)-1]), true
	default:
		return hitKey(hits[next]), false
	}
}

// searchHitByKey looks a hit up by identity.
func searchHitByKey(hits []telegram.SearchHit, key string) (telegram.SearchHit, bool) {
	for _, hit := range hits {
		if hitKey(hit) == key {
			return hit, true
		}
	}
	return telegram.SearchHit{}, false
}

// folderContainingChat finds a folder that would show the given chat, so a chat-scope hit outside
// the current folder can switch to it instead of aborting.
func folderContainingChat(app *App, peerID string) (int, string, bool) {
	for _, folder := range app.allFolders {
		saved := app.currentFolder
		app.currentFolder = folder.ID
		visible := app.visibleChatsForFolder()
		app.currentFolder = saved
		if chatIndexByID(visible, peerID) >= 0 {
			return folder.ID, folder.Title, true
		}
	}
	return 0, "", false
}

// searchScopeValues and searchScopeLabels back the scope dropdown. Kept as parallel slices so the
// dropdown's index maps to a stable value that does not change with the interface language.
var searchScopeValues = []string{"messages", "chats", "global"}

func searchScopeLabels() []string {
	return []string{
		i18n.T(i18n.KeySearchScopeChat),
		i18n.T(i18n.KeySearchScopeChats),
		i18n.T(i18n.KeySearchScopeGlobal),
	}
}
