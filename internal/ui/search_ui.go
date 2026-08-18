package ui

import (
	"fmt"
	"strconv"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// runSearch dispatches a query to the right scope.
func (a *App) runSearch(scope, query string) {
	a.searchQuery = query
	a.searchScope = scope
	switch scope {
	case "chats":
		a.searchChats(query)
	case "global":
		a.requestServerSearch(telegram.SearchScopeGlobal, "", query)
	default:
		a.searchCurrentChat(query)
	}
}

// searchChats highlights the first matching chat, switching folders if the match is not in the
// current one.
//
// Previously this aborted with "match is outside the current folder", which told the user their
// search had failed when it had in fact succeeded.
func (a *App) searchChats(query string) {
	matches := findChatMatches(a.allChats, query)
	if len(matches) == 0 {
		a.setStatusMsg(i18n.KeyStatusNoMatchingChat)
		return
	}
	chat := matches[0]
	visible := a.visibleChatsForFolder()
	if idx := chatIndexByID(visible, chat.ID); idx >= 0 {
		a.chats.SetCurrentItem(idx)
		a.chatsHighlightPeer = chat.ID
		a.setStatusMsg(i18n.KeyStatusFoundChat, chat.Title)
		return
	}
	if folderID, folderTitle, ok := folderContainingChat(a, chat.ID); ok {
		a.currentFolder = folderID
		a.refreshChats()
		if idx := chatIndexByID(a.visibleChatsForFolder(), chat.ID); idx >= 0 {
			a.chats.SetCurrentItem(idx)
			a.chatsHighlightPeer = chat.ID
		}
		a.setStatusMsg(i18n.KeyStatusSwitchedFolder, folderTitle)
		return
	}
	a.setStatusMsg(i18n.KeyStatusChatOutsideFolder)
}

// searchCurrentChat searches the loaded window first and falls back to the server on no hits.
func (a *App) searchCurrentChat(query string) {
	if !a.draftablePeer(a.currentChat) {
		a.setStatusMsg(i18n.KeyStatusNoChatSelected)
		return
	}
	if hits := findMessageMatches(a.messages.Messages(), query); len(hits) > 0 {
		a.applySearchHits(hits, query)
		return
	}
	a.requestServerSearch(telegram.SearchScopeChat, a.currentChat, query)
}

// requestServerSearch asks the backend for results.
//
// RequestID gates the reply so a slow earlier search cannot overwrite a newer one's results.
func (a *App) requestServerSearch(scope telegram.SearchScope, peerKey, query string) {
	if a.commands == nil {
		return
	}
	a.searchRequestID++
	a.commands <- telegram.Command{
		Kind:        telegram.CommandSearchMessages,
		PeerKey:     peerKey,
		Query:       query,
		SearchScope: string(scope),
		RequestID:   a.searchRequestID,
	}
}

// applySearchResultsEvent takes a completed server search.
func (a *App) applySearchResultsEvent(event telegram.Event) {
	if event.RequestID != 0 && event.RequestID != a.searchRequestID {
		// A stale page from a search the user has already replaced.
		return
	}
	if len(event.SearchHits) == 0 {
		a.setStatusMsg(i18n.KeyStatusSearchNoResults)
		return
	}
	a.applySearchHits(event.SearchHits, event.Query)
}

// applySearchHits stores a result set and opens the overlay.
func (a *App) applySearchHits(hits []telegram.SearchHit, query string) {
	a.searchHits = hits
	a.searchQuery = query
	a.searchCursor = ""
	a.openSearchResults()
}

// openSearchResults shows the hits in an overlay.
//
// An overlay rather than the message viewport: appendMessage drops anything whose ChatID is not
// the open chat, and a synthetic non-contiguous set would trip gap detection — the same false
// adjacency hazard jump.go documents.
func (a *App) openSearchResults() {
	list := tview.NewList().ShowSecondaryText(true)
	hint := tview.NewTextView().SetDynamicColors(true).SetText("[gray]" + i18n.T(i18n.KeySearchResultsHint))
	a.searchList = list

	for _, hit := range a.searchHits {
		primary := fmt.Sprintf("%s · %s", hit.PeerTitle, hit.Date.Local().Format("2006-01-02 15:04"))
		list.AddItem(render.Truncate(primary, 70), render.Truncate(hit.Preview, 70), 0, nil)
	}
	list.SetSelectedFunc(func(index int, _, _ string, _ rune) {
		if index >= 0 && index < len(a.searchHits) {
			a.jumpToSearchHit(a.searchHits[index])
		}
	})

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(list, 0, 1, true).
		AddItem(hint, 1, 0, false)
	title := fmt.Sprintf(" %s (%d) ", i18n.T(i18n.KeySearchResultsTitle), len(a.searchHits))
	layout.SetBorder(true).SetTitle(title)
	// Esc is handled in App.capture, which runs first and would swallow it here anyway.

	a.app.SetRoot(layout, true)
	a.app.SetFocus(list)
}

func (a *App) closeSearchResults() {
	a.searchList = nil
	a.restoreMessageFocus()
}

// jumpToSearchHit opens the hit's chat and jumps to the message.
//
// Deliberately no CommandOpenChat alongside the jump: both dispatch with `go`, so their
// EventMessages ordering is nondeterministic and openChat's window could land after the jump and
// destroy the selection. jumpToMessage loads the pinned banner itself to make up for it.
func (a *App) jumpToSearchHit(hit telegram.SearchHit) {
	a.searchCursor = hitKey(hit)
	if a.searchList != nil {
		a.closeSearchResults()
	}
	if hit.PeerKey != a.currentChat {
		a.leaveChatForDraft()
		a.currentChat = hit.PeerKey
		a.currentTitle = hit.PeerTitle
		if idx := chatIndexByID(a.allChats, hit.PeerKey); idx >= 0 {
			a.currentTitle = a.allChats[idx].Title
		}
		a.commands <- telegram.Command{Kind: telegram.CommandFocusChat, PeerKey: hit.PeerKey}
		a.applyMessagesPaneTitle()
	}
	a.commands <- telegram.Command{
		Kind:      telegram.CommandJumpToMessage,
		PeerKey:   hit.PeerKey,
		MessageID: hit.MessageID,
	}
}

// stepSearch moves to the next or previous hit. Bound to n and N.
func (a *App) stepSearch(delta int) {
	if len(a.searchHits) == 0 {
		a.setStatusMsg(i18n.KeyStatusSearchNavHint)
		return
	}
	next, wrapped := stepSearchCursor(a.searchHits, a.searchCursor, delta)
	if next == "" {
		a.setStatusMsg(i18n.KeyStatusSearchNoMore)
		return
	}
	hit, ok := searchHitByKey(a.searchHits, next)
	if !ok {
		a.setStatusMsg(i18n.KeyStatusSearchNoMore)
		return
	}
	if wrapped {
		edge := i18n.T(i18n.KeySearchFirst)
		if delta < 0 {
			edge = i18n.T(i18n.KeySearchLast)
		}
		a.setStatusMsg(i18n.KeyStatusSearchWrapped, edge)
	}
	// A hit inside the open chat and already loaded needs no round trip.
	if hit.PeerKey == a.currentChat && a.selectMessageByID(strconv.Itoa(hit.MessageID)) {
		a.searchCursor = next
		return
	}
	a.jumpToSearchHit(hit)
}
