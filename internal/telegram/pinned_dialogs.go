package telegram

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

func (c *GotdClient) syncPinnedDialogs(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, folderID int) {
	if c.store == nil {
		return
	}
	res, err := api.MessagesGetPinnedDialogs(ctx, folderID)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusPinnedDialogsError, folderID, err)})
		return
	}
	entities := dialogEntities(res.GetUsers(), res.GetChats())
	messageByID := make(map[int]tg.MessageClass)
	for _, item := range res.GetMessages() {
		switch msg := item.(type) {
		case *tg.Message:
			messageByID[msg.ID] = msg
		case *tg.MessageService:
			messageByID[msg.ID] = msg
		}
	}

	pinnedKeys := make([]string, 0, len(res.GetDialogs()))
	peers := make([]storage.Peer, 0, len(res.GetDialogs()))
	for index, dialog := range res.GetDialogs() {
		d, ok := dialog.(*tg.Dialog)
		if !ok {
			continue
		}
		peer, _, ok := normalizeDialog(accountID, dialog, messageByID, entities, index)
		if !ok {
			if key, ok := peerKeyFromDialog(accountID, d, entities); ok {
				pinnedKeys = append(pinnedKeys, key)
			}
			continue
		}
		peer.Pinned = true
		peer.PinnedOrder = index + 1
		if existing, ok, err := c.store.Peer(ctx, accountID, peer.Key); err == nil && ok {
			peer = mergeDialogPeer(existing, peer)
			peer.Pinned = true
			peer.PinnedOrder = index + 1
		}
		if folderID == 0 {
			peer.FolderID = 0
			peer.FolderTitle = ""
		}
		pinnedKeys = append(pinnedKeys, peer.Key)
		peers = append(peers, peer)
	}

	if folderID == 0 {
		dialogCount := len(res.GetDialogs())
		if dialogCount > 0 && len(pinnedKeys) == 0 {
			sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusPinnedDialogsError, folderID, fmt.Errorf("no pinned peers parsed"))})
			return
		}
		if err := c.store.ApplyGlobalPins(ctx, accountID, pinnedKeys); err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("apply global pins: %w", err)})
			return
		}
		if len(peers) > 0 {
			if err := c.store.SavePeers(ctx, peers); err != nil {
				sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save pinned dialogs: %w", err)})
				return
			}
		}
		return
	}

	if err := c.store.UpdateDialogFilterPinnedPeers(ctx, accountID, folderID, pinnedKeys); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save folder pins: %w", err)})
		return
	}
	if len(peers) > 0 {
		if err := c.store.SavePeers(ctx, peers); err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save pinned folder peers: %w", err)})
		}
	}
}

func (c *GotdClient) syncAllPinnedDialogs(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	c.syncPinnedDialogs(ctx, accountID, api, events, 0)
	if c.store == nil {
		return
	}
	filters, err := c.store.ListDialogFilters(ctx, accountID)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("list filters for pins: %w", err)})
		return
	}
	for _, filter := range filters {
		if filter.Kind != "telegram" || filter.ID <= 0 || filter.Archive {
			continue
		}
		c.syncPinnedDialogs(ctx, accountID, api, events, filter.ID)
	}
}

func (c *GotdClient) refreshChatsFromStore(ctx context.Context, accountID string, events chan<- Event) {
	if c.store == nil {
		return
	}
	peers, err := c.store.ListPeers(ctx, accountID)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("list peers: %w", err)})
		return
	}
	folders := withArchiveFolder([]Folder{{ID: 0, Title: "All", Kind: "all"}})
	if saved, err := c.store.ListDialogFilters(ctx, accountID); err == nil && len(saved) > 0 {
		folders = withArchiveFolder(storageFiltersToFolders(saved))
	}
	sendEvent(ctx, events, Event{Kind: EventChats, Chats: c.applyMutes(ctx, accountID, peersToChats(peers)), Folders: folders})
}

func uniquePeers(peers []storage.Peer) []storage.Peer {
	if len(peers) == 0 {
		return peers
	}
	byKey := make(map[string]storage.Peer, len(peers))
	order := make([]string, 0, len(peers))
	for _, peer := range peers {
		existing, ok := byKey[peer.Key]
		if !ok {
			byKey[peer.Key] = peer
			order = append(order, peer.Key)
			continue
		}
		if preferPeer(existing, peer) {
			continue
		}
		byKey[peer.Key] = peer
	}
	out := make([]storage.Peer, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out
}

func preferPeer(existing, incoming storage.Peer) bool {
	if existing.FolderID != 1 && incoming.FolderID == 1 {
		return true
	}
	if existing.FolderID == 1 && incoming.FolderID != 1 {
		return false
	}
	if existing.Pinned && !incoming.Pinned {
		return true
	}
	return false
}

func peerKeyFromDialog(accountID string, dialog *tg.Dialog, entities entitiesByID) (string, bool) {
	if dialog == nil {
		return "", false
	}
	peer, ok := peerFromRef(accountID, dialog.Peer, entities)
	if !ok {
		return "", false
	}
	return peer.Key, true
}
