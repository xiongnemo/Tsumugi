package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

const (
	// searchPageLimit is Telegram's page size for one search request.
	searchPageLimit = 40
	// searchMaxPages bounds how far a single search walks. Three pages is enough to answer
	// "where did I see this" without turning one keystroke into an unbounded crawl.
	searchMaxPages = 3
)

// SearchScope selects where a query runs.
type SearchScope string

const (
	// SearchScopeChat searches inside one peer via messages.search.
	SearchScopeChat SearchScope = "chat"
	// SearchScopeGlobal searches every dialog via messages.searchGlobal.
	SearchScopeGlobal SearchScope = "global"
)

// SearchHit is one search result, carrying enough to render a row and to jump to it.
type SearchHit struct {
	PeerKey   string
	PeerTitle string
	MessageID int
	Preview   string
	Date      time.Time
	Outgoing  bool
}

// searchMessages runs a message search and returns the hits to the UI.
//
// User-initiated only — never on a keystroke — because each call is up to three round trips.
func (c *GotdClient) searchMessages(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	if c.store == nil || api == nil {
		return
	}
	query := strings.TrimSpace(cmd.Query)
	if query == "" {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusSearchEmpty)})
		return
	}

	c.beginForegroundLoad()
	defer c.endForegroundLoad()
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusSearching)})

	var (
		hits []SearchHit
		err  error
	)
	if SearchScope(cmd.SearchScope) == SearchScopeGlobal {
		hits, err = c.searchGlobal(ctx, accountID, api, query)
	} else {
		hits, err = c.searchInPeer(ctx, accountID, api, cmd.PeerKey, query)
	}
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: rpcError("search", err), RequestID: cmd.RequestID})
		return
	}
	sendEvent(ctx, events, Event{
		Kind:       EventSearchResults,
		Query:      query,
		SearchHits: hits,
		RequestID:  cmd.RequestID,
		StatusMsg:  i18n.M(i18n.KeyStatusSearchResults, len(hits)),
	})
}

// searchInPeer walks messages.search for one peer.
func (c *GotdClient) searchInPeer(ctx context.Context, accountID string, api *tg.Client, peerKey, query string) ([]SearchHit, error) {
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		return nil, err
	}
	input, err := inputPeer(p)
	if err != nil {
		return nil, err
	}

	var hits []SearchHit
	offsetID := 0
	for page := 0; page < searchMaxPages; page++ {
		res, err := retryFloodWait(ctx, defaultMaxFloodWaits, "searching messages", func(ctx context.Context) (tg.MessagesMessagesClass, error) {
			return api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
				Peer:     input,
				Q:        query,
				Filter:   &tg.InputMessagesFilterEmpty{},
				Limit:    searchPageLimit,
				OffsetID: offsetID,
			})
		})
		if err != nil {
			return hits, err
		}
		pageHits, lowest := searchHitsFromMessages(res, p.Title, peerKey)
		hits = append(hits, pageHits...)
		if len(pageHits) < searchPageLimit || lowest <= 0 {
			break
		}
		offsetID = lowest
	}
	return hits, nil
}

// searchGlobal walks messages.searchGlobal across every dialog.
//
// Result peers are saved even though the messages are not: a jump resolves its destination from
// storage, so a hit in a chat that has never been synced would otherwise fail with "peer not
// found". The messages themselves are deliberately never saved — a global result set is
// non-contiguous, and writing it would feed the history gap detector a hole that is not real.
//
// FolderID is never set from a Tsumugi folder. Tsumugi folders are client-side filters with
// arbitrary ids, while Telegram's FolderID is a peer folder with only 0 and 1; passing a filter
// id yields 400 FOLDER_ID_INVALID.
func (c *GotdClient) searchGlobal(ctx context.Context, accountID string, api *tg.Client, query string) ([]SearchHit, error) {
	var hits []SearchHit
	var (
		offsetRate int
		offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
		offsetID   int
	)
	for page := 0; page < searchMaxPages; page++ {
		res, err := retryFloodWait(ctx, defaultMaxFloodWaits, "searching all chats", func(ctx context.Context) (tg.MessagesMessagesClass, error) {
			return api.MessagesSearchGlobal(ctx, &tg.MessagesSearchGlobalRequest{
				Q:          query,
				Filter:     &tg.InputMessagesFilterEmpty{},
				Limit:      searchPageLimit,
				OffsetRate: offsetRate,
				OffsetPeer: offsetPeer,
				OffsetID:   offsetID,
			})
		})
		if err != nil {
			return hits, err
		}
		modified, ok := res.AsModified()
		if !ok {
			break
		}
		entities := dialogEntities(modified.GetUsers(), modified.GetChats())
		var peers []storage.Peer
		var lastPeerRef tg.PeerClass
		count := 0
		for _, item := range modified.GetMessages() {
			hit, peerRef, ok := globalSearchHit(item, entities)
			if !ok {
				continue
			}
			hits = append(hits, hit)
			lastPeerRef = peerRef
			offsetID = hit.MessageID
			count++
			if p, ok := peerFromRef(accountID, peerRef, entities); ok {
				peers = append(peers, p)
			}
		}
		if len(peers) > 0 {
			// Merged rather than overwritten: these peers carry no read state or pin flags, and
			// SavePeers is a full-column upsert.
			c.savePeersPreservingLocal(ctx, accountID, peers)
		}
		if count < searchPageLimit {
			break
		}
		if next, ok := res.(*tg.MessagesMessagesSlice); ok {
			offsetRate = next.NextRate
		}
		if lastPeerRef != nil {
			if p, ok := peerFromRef(accountID, lastPeerRef, entities); ok {
				if in, err := inputPeer(p); err == nil {
					offsetPeer = in
				}
			}
		}
	}
	return hits, nil
}

// savePeersPreservingLocal writes peers learned from a search without clobbering local state.
func (c *GotdClient) savePeersPreservingLocal(ctx context.Context, accountID string, peers []storage.Peer) {
	merged := make([]storage.Peer, 0, len(peers))
	for _, p := range peers {
		if existing, ok, err := c.store.Peer(ctx, accountID, p.Key); err == nil && ok {
			merged = append(merged, mergePeerActivity(existing, p))
			continue
		}
		merged = append(merged, p)
	}
	_ = c.store.SavePeers(ctx, merged)
}

// globalSearchHit converts one global-search message into a hit.
func globalSearchHit(item tg.MessageClass, entities entitiesByID) (SearchHit, tg.PeerClass, bool) {
	switch msg := item.(type) {
	case *tg.Message:
		kind, id := peerRefParts(msg.PeerID)
		if kind == "" || id == 0 {
			return SearchHit{}, nil, false
		}
		title := senderDisplayName(msg.PeerID, entities)
		if title == "" {
			title = peerKey(kind, id)
		}
		return SearchHit{
			PeerKey:   peerKey(kind, id),
			PeerTitle: title,
			MessageID: msg.ID,
			Preview:   searchPreview(msg),
			Date:      time.Unix(int64(msg.Date), 0).UTC(),
			Outgoing:  msg.Out,
		}, msg.PeerID, true
	default:
		return SearchHit{}, nil, false
	}
}

// searchHitsFromMessages converts an in-peer search page, also returning the lowest id seen so
// the next page can continue from it.
func searchHitsFromMessages(res tg.MessagesMessagesClass, peerTitle, peerKey string) ([]SearchHit, int) {
	modified, ok := res.AsModified()
	if !ok {
		return nil, 0
	}
	var hits []SearchHit
	lowest := 0
	for _, item := range modified.GetMessages() {
		msg, ok := item.(*tg.Message)
		if !ok {
			continue
		}
		hits = append(hits, SearchHit{
			PeerKey:   peerKey,
			PeerTitle: peerTitle,
			MessageID: msg.ID,
			Preview:   searchPreview(msg),
			Date:      time.Unix(int64(msg.Date), 0).UTC(),
			Outgoing:  msg.Out,
		})
		if lowest == 0 || msg.ID < lowest {
			lowest = msg.ID
		}
	}
	return hits, lowest
}

// searchPreview builds a one-line preview for a result row.
//
// Falls back to the media placeholder key's own text rather than leaving the row blank, because a
// search for a caption legitimately matches a photo.
func searchPreview(msg *tg.Message) string {
	text := strings.TrimSpace(strings.ReplaceAll(msg.Message, "\n", " "))
	if text != "" {
		return text
	}
	if msg.Media != nil {
		if label := classifyMessageMedia(msg.Media).LabelKey; label != "" {
			return i18n.T(label)
		}
	}
	return i18n.T(i18n.KeyMessageEmpty)
}
