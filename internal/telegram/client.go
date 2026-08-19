package telegram

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	mrand "math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/i18n"
	termmedia "github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/storage"
	"github.com/nemo/Tsumugi/internal/version"
)

type Client interface {
	Run(ctx context.Context, events chan<- Event, commands <-chan Command) error
	Capabilities() Capabilities
}

type GotdClient struct {
	cfg                  config.Config
	store                *storage.DB
	mtproto              *telegram.Client
	pendingMu            sync.Mutex
	pending              map[string][]pendingMessage
	peerHistoryMu        sync.Mutex
	peerHistoryLocks     map[string]*sync.Mutex
	fgLoadMu             sync.Mutex
	fgLoads              int
	olderLoadMu          sync.Mutex
	olderLoading         map[string]struct{}
	gapFillSessionMu     sync.Mutex
	gapFillSessions      map[string]struct{}
	focusMu              sync.Mutex
	focusPeer            string
	viewedMu             sync.Mutex
	viewedIDs            map[string]struct{}
	groupReadMu          sync.Mutex
	groupReadCounts      map[string]map[int]int
	groupReadUnsupported map[string]struct{}
	suggestMu            sync.Mutex
	mentionCache         map[string]mentionCacheEntry
	commandCache         map[string]commandCacheEntry
	inlineCache          map[string]inlineCacheEntry
	typingMu             sync.Mutex
	typingSentAt         map[string]time.Time
	// typingSend replaces the MessagesSetTyping call in tests.
	typingSend func(context.Context, *tg.MessagesSetTypingRequest) (bool, error)
	// forwardSend replaces the MessagesForwardMessages call in tests.
	forwardSend func(context.Context, *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error)
}

type pendingMessage struct {
	ID        int
	CreatedAt time.Time
}

func NewGotdClient(cfg config.Config, store *storage.DB) *GotdClient {
	return &GotdClient{
		cfg:                  cfg,
		store:                store,
		pending:              make(map[string][]pendingMessage),
		viewedIDs:            make(map[string]struct{}),
		olderLoading:         make(map[string]struct{}),
		groupReadCounts:      make(map[string]map[int]int),
		groupReadUnsupported: make(map[string]struct{}),
		mentionCache:         make(map[string]mentionCacheEntry),
		commandCache:         make(map[string]commandCacheEntry),
		inlineCache:          make(map[string]inlineCacheEntry),
		typingSentAt:         make(map[string]time.Time),
	}
}

func (c *GotdClient) Capabilities() Capabilities {
	if c.cfg.AuthMode == config.AuthBot {
		return BotCapabilities()
	}
	return UserCapabilities()
}

func (c *GotdClient) Run(ctx context.Context, events chan<- Event, commands <-chan Command) error {
	if !c.cfg.ReadyForTelegram() {
		sendEvent(ctx, events, Event{
			Kind:      EventStatus,
			StatusMsg: i18n.M(i18n.KeyStatusCredentialsMissing),
		})
		<-ctx.Done()
		return ctx.Err()
	}

	resolver, err := c.cfg.Proxy.Resolver()
	if err != nil {
		return err
	}

	sessionPath := c.sessionPath()
	var accountID string
	var api *tg.Client
	dispatcher := tg.NewUpdateDispatcher()
	c.registerUpdateHandlers(&dispatcher, &accountID, &api, events)
	// Registered here, not inside the Run callback: updateLoginToken arrives on the normal
	// update stream, and a handler added after Run has started never sees it — the QR flow then
	// hangs until expiry, forever.
	var qrLoggedIn qrlogin.LoggedIn
	if c.cfg.AuthMode == config.AuthUser && c.cfg.LoginMethod == config.LoginQR {
		qrLoggedIn = registerQRLogin(&dispatcher)
	}
	options := telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{Path: sessionPath},
		Device:         deviceConfig(),
		UpdateHandler:  dispatcher,
		AllowCDN:       true,
	}
	if resolver != nil {
		options.Resolver = resolver
	}

	client := telegram.NewClient(c.cfg.APIID, c.cfg.APIHash, options)
	api = client.API()
	c.mtproto = client

	return client.Run(ctx, func(ctx context.Context) error {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusConnecting)})
		if err := c.authenticate(ctx, client, events, qrLoggedIn); err != nil {
			return err
		}

		self, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("get self: %w", err)
		}
		sendEvent(ctx, events, Event{
			Kind: EventConnected,
			Self: Self{
				ID:       self.ID,
				Name:     displaySelfName(self),
				Username: self.Username,
				IsBot:    self.Bot,
			},
			StatusMsg: i18n.M(i18n.KeyStatusConnectedAs, displaySelfName(self)),
		})
		accountID = accountIDFromSelf(self)
		if c.store != nil {
			_ = c.store.SaveAccount(ctx, storage.Account{
				ID:          accountID,
				Mode:        string(c.cfg.AuthMode),
				DisplayName: displaySelfName(self),
				Username:    self.Username,
			})
		}

		if c.cfg.AuthMode == config.AuthUser {
			c.loadDialogFilters(ctx, accountID, api, events)
			c.loadDialogs(ctx, accountID, api, events)
			go c.startSyncWorkers(ctx, accountID, api, events)
		} else {
			sendEvent(ctx, events, Event{
				Kind: EventChats,
				Chats: []Chat{{
					ID:       "bot-updates",
					Title:    "Bot updates",
					Subtitle: "Bot mode only receives chats and updates allowed by Telegram bot permissions.",
				}},
			})
		}

		go c.consumeCommands(ctx, accountID, api, events, commands)
		<-ctx.Done()
		return ctx.Err()
	})
}

func (c *GotdClient) authenticate(ctx context.Context, client *telegram.Client, events chan<- Event, qrLoggedIn qrlogin.LoggedIn) error {
	if c.cfg.AuthMode == config.AuthBot {
		if _, err := client.Auth().Bot(ctx, c.cfg.BotToken); err != nil {
			return fmt.Errorf("bot auth: %w", err)
		}
		return nil
	}

	// An existing session short-circuits both methods, so the QR branch only runs when there is
	// actually something to authorise.
	if status, err := client.Auth().Status(ctx); err == nil && status.Authorized {
		return nil
	}
	if c.cfg.LoginMethod == config.LoginQR && qrLoggedIn != nil {
		return c.authenticateQR(ctx, client, events, qrLoggedIn)
	}

	flow := auth.NewFlow(newUIAuth(c.cfg.Phone, events), auth.SendCodeOptions{})
	if err := client.Auth().IfNecessary(ctx, flow); err != nil {
		return fmt.Errorf("user auth: %w", err)
	}
	return nil
}

func (c *GotdClient) loadDialogs(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	peers, chats, _, _, _, err := c.loadDialogBatch(ctx, accountID, api, dialogBatchRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("load dialogs: %w", err)})
		return
	}
	archivePeers, archiveChats, _, _, _, err := c.loadDialogBatch(ctx, accountID, api, dialogBatchRequest{
		FolderID:   1,
		UseFolder:  true,
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if err == nil {
		peers = append(peers, archivePeers...)
		chats = append(chats, archiveChats...)
	}
	peers = uniquePeers(peers)
	chats = uniqueChats(chats)
	sortChats(chats)
	if len(chats) == 0 {
		chats = append(chats, Chat{
			ID:       "empty",
			Title:    "No dialogs",
			Subtitle: "Telegram returned an empty dialog list.",
		})
	}
	if c.store != nil && len(peers) > 0 {
		for i := range peers {
			if existing, ok, err := c.store.Peer(ctx, accountID, peers[i].Key); err == nil && ok {
				// These peers came from messages.getDialogs, so their read state is
				// authoritative; mergePeerActivity would discard it.
				peers[i] = mergeDialogPeer(existing, peers[i])
			}
		}
		if err := c.store.SavePeers(ctx, peers); err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save peers: %w", err)})
		}
	}
	c.syncAllPinnedDialogs(ctx, accountID, api, events)
	c.refreshChatsFromStore(ctx, accountID, events)
}

type dialogBatchRequest struct {
	FolderID   int
	UseFolder  bool
	OffsetDate int
	OffsetID   int
	OffsetPeer tg.InputPeerClass
	Limit      int
	IndexBase  int
}

func (c *GotdClient) loadDialogBatch(ctx context.Context, accountID string, api *tg.Client, batch dialogBatchRequest) ([]storage.Peer, []Chat, storage.Peer, int, int, error) {
	if batch.OffsetPeer == nil {
		batch.OffsetPeer = &tg.InputPeerEmpty{}
	}
	if batch.Limit == 0 {
		batch.Limit = 100
	}
	request := &tg.MessagesGetDialogsRequest{
		OffsetDate: batch.OffsetDate,
		OffsetID:   batch.OffsetID,
		OffsetPeer: batch.OffsetPeer,
		Limit:      batch.Limit,
	}
	if batch.UseFolder {
		request.SetFolderID(batch.FolderID)
	}
	dialogs, err := api.MessagesGetDialogs(ctx, request)
	if err != nil {
		return nil, nil, storage.Peer{}, 0, 0, err
	}
	modified, ok := dialogs.AsModified()
	if !ok {
		return nil, nil, storage.Peer{}, 0, 0, nil
	}
	entities := dialogEntities(modified.GetUsers(), modified.GetChats())
	messageByID := make(map[int]tg.MessageClass)
	for _, item := range modified.GetMessages() {
		switch msg := item.(type) {
		case *tg.Message:
			messageByID[msg.ID] = msg
		case *tg.MessageService:
			messageByID[msg.ID] = msg
		}
	}
	peers := make([]storage.Peer, 0, len(modified.GetDialogs()))
	chats := make([]Chat, 0, len(modified.GetDialogs()))
	var drafts []storage.Draft
	var last storage.Peer
	for index, dialog := range modified.GetDialogs() {
		peer, chat, ok := normalizeDialog(accountID, dialog, messageByID, entities, batch.IndexBase+index)
		if !ok {
			continue
		}
		if batch.UseFolder {
			peer.FolderID = batch.FolderID
			chat.FolderID = batch.FolderID
		}
		peers = append(peers, peer)
		chats = append(chats, chat)
		last = peer
		if d, ok := dialog.(*tg.Dialog); ok {
			if draft, ok := draftFromDialog(accountID, peer.Key, d); ok {
				drafts = append(drafts, draft)
			}
		}
	}
	// Dialogs already carry drafts, so this costs no extra request. The dirty guard in
	// SaveServerDrafts is what keeps a locally typed draft from being clobbered here.
	if c.store != nil && len(drafts) > 0 {
		_ = c.store.SaveServerDrafts(ctx, drafts)
	}
	return peers, chats, last, len(modified.GetDialogs()), len(peers), nil
}

func (c *GotdClient) loadDialogFilters(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	filters, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusDialogFoldersUnavailable, err.Error())})
		return
	}
	stored := []storage.DialogFilter{
		{AccountID: accountID, ID: 0, Title: "All", Kind: "all"},
		{AccountID: accountID, ID: ArchiveFolderID, Title: "Archived chats", Kind: "archive", Archive: true},
	}
	for _, item := range filters.Filters {
		switch f := item.(type) {
		case *tg.DialogFilter:
			stored = append(stored, storageDialogFilter(accountID, f.ID, f.Title.Text, f))
		case *tg.DialogFilterChatlist:
			stored = append(stored, storageChatlistFilter(accountID, f.ID, f.Title.Text, f))
		}
	}
	if c.store != nil {
		_ = c.store.SaveDialogFilters(ctx, accountID, stored)
	}
}

func storageDialogFilter(accountID string, id int, title string, f *tg.DialogFilter) storage.DialogFilter {
	return storage.DialogFilter{
		AccountID:       accountID,
		ID:              id,
		Title:           title,
		Kind:            "telegram",
		Contacts:        f.Contacts,
		NonContacts:     f.NonContacts,
		Groups:          f.Groups,
		Broadcasts:      f.Broadcasts,
		Bots:            f.Bots,
		ExcludeMuted:    f.ExcludeMuted,
		ExcludeRead:     f.ExcludeRead,
		ExcludeArchived: f.ExcludeArchived,
		IncludePeers:    inputPeerKeys(f.IncludePeers),
		ExcludePeers:    inputPeerKeys(f.ExcludePeers),
		PinnedPeers:     inputPeerKeys(f.PinnedPeers),
	}
}

func storageChatlistFilter(accountID string, id int, title string, f *tg.DialogFilterChatlist) storage.DialogFilter {
	return storage.DialogFilter{
		AccountID:    accountID,
		ID:           id,
		Title:        title,
		Kind:         "telegram",
		IncludePeers: inputPeerKeys(f.IncludePeers),
		PinnedPeers:  inputPeerKeys(f.PinnedPeers),
	}
}

func inputPeerKeys(peers []tg.InputPeerClass) []string {
	out := make([]string, 0, len(peers))
	for _, peer := range peers {
		if key := inputPeerKey(peer); key != "" {
			out = append(out, key)
		}
	}
	return out
}

func inputPeerKey(peer tg.InputPeerClass) string {
	switch p := peer.(type) {
	case *tg.InputPeerSelf:
		return ""
	case *tg.InputPeerUser:
		return peerKey("user", p.UserID)
	case *tg.InputPeerChat:
		return peerKey("chat", p.ChatID)
	case *tg.InputPeerChannel:
		return peerKey("channel", p.ChannelID)
	default:
		return ""
	}
}

func (c *GotdClient) backgroundSync(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	if c.store == nil {
		return
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusSyncingBackground)})
	peers := c.syncDialogPages(ctx, accountID, api, events, 0, false)
	peers = append(peers, c.syncDialogPages(ctx, accountID, api, events, 1, true)...)
	if len(peers) == 0 {
		var err error
		peers, err = c.store.ListPeers(ctx, accountID)
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("list peers for sync: %w", err)})
			return
		}
	}
	sort.SliceStable(peers, func(i, j int) bool {
		if peers[i].LastMessageAt.Equal(peers[j].LastMessageAt) {
			return peers[i].TopMessageID > peers[j].TopMessageID
		}
		return peers[i].LastMessageAt.After(peers[j].LastMessageAt)
	})
	total := len(peers)
	for index, p := range peers {
		select {
		case <-ctx.Done():
			return
		default:
		}
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusSyncingProgress, index+1, total, p.Title)})
		c.syncPeerHistory(ctx, accountID, api, p, events, 1)
		time.Sleep(350 * time.Millisecond)
	}
	c.syncAllPinnedDialogs(ctx, accountID, api, events)
	c.refreshChatsFromStore(ctx, accountID, events)
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusSyncComplete, total)})
}

func (c *GotdClient) syncDialogPages(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, folderID int, useFolder bool) []storage.Peer {
	var allPeers []storage.Peer
	offsetPeer := tg.InputPeerClass(&tg.InputPeerEmpty{})
	offsetDate, offsetID := 0, 0
	for page := 0; page < 50; page++ {
		pagePeers, _, lastPeer, dialogCount, _, err := c.loadDialogBatch(ctx, accountID, api, dialogBatchRequest{
			FolderID:   folderID,
			UseFolder:  useFolder,
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      100,
			IndexBase:  page * 100,
		})
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("sync dialogs: %w", err)})
			return allPeers
		}
		if dialogCount == 0 {
			return allPeers
		}
		if len(pagePeers) > 0 {
			for i := range pagePeers {
				if existing, ok, err := c.store.Peer(ctx, accountID, pagePeers[i].Key); err == nil && ok {
					// Dialog-derived, so read state is authoritative here too.
					pagePeers[i] = mergeDialogPeer(existing, pagePeers[i])
				}
			}
			allPeers = append(allPeers, pagePeers...)
			if err := c.store.SavePeers(ctx, pagePeers); err != nil {
				sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save synced peers: %w", err)})
			}
			if peers, err := c.store.ListPeers(ctx, accountID); err == nil {
				sendEvent(ctx, events, Event{Kind: EventChats, Chats: peersToChats(peers)})
			}
		}
		if len(pagePeers) == 0 {
			return allPeers
		}
		input, err := inputPeer(lastPeer)
		if err != nil {
			return allPeers
		}
		offsetPeer = input
		offsetID = lastPeer.TopMessageID
		offsetDate = int(lastPeer.LastMessageAt.Unix())
		if dialogCount < 100 {
			return allPeers
		}
		time.Sleep(250 * time.Millisecond)
	}
	return allPeers
}

func (c *GotdClient) setFocusPeer(key string) {
	c.focusMu.Lock()
	c.focusPeer = key
	c.focusMu.Unlock()
}

func (c *GotdClient) focusedPeerKey() string {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	return c.focusPeer
}

func (c *GotdClient) isFocusedPeer(peerKey string) bool {
	return peerKey != "" && c.focusedPeerKey() == peerKey
}

func (c *GotdClient) sendFocusedEvent(ctx context.Context, events chan<- Event, peerKey string, event Event) {
	if !c.isFocusedPeer(peerKey) {
		return
	}
	if event.PeerKey == "" {
		event.PeerKey = peerKey
	}
	sendEvent(ctx, events, event)
}

func (c *GotdClient) messagesGetHistory(ctx context.Context, api *tg.Client, req *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	return retryFloodWait(ctx, defaultMaxFloodWaits, "loading history", func(ctx context.Context) (tg.MessagesMessagesClass, error) {
		return api.MessagesGetHistory(ctx, req)
	})
}

func (c *GotdClient) saveUpdateState(ctx context.Context, events chan<- Event, state storage.UpdateState) {
	if c.store == nil || state.AccountID == "" {
		return
	}
	if err := c.store.SaveUpdateState(ctx, state); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save update state: %w", err)})
	}
}

func (c *GotdClient) syncPeerHistory(ctx context.Context, accountID string, api *tg.Client, p storage.Peer, events chan<- Event, maxPages int) {
	c.peerHistoryLock(p.Key).Lock()
	defer c.peerHistoryLock(p.Key).Unlock()

	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	offsetID := p.HistoryMinID
	for page := 0; page < maxPages; page++ {
		select {
		case <-ctx.Done():
			return
		default:
		}
		history, err := c.messagesGetHistory(ctx, api, &tg.MessagesGetHistoryRequest{Peer: input, OffsetID: offsetID, Limit: 100})
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("sync history %s: %w", p.Title, err)})
			return
		}
		msgs := c.normalizeMessagesWithPreview(ctx, api, accountID, p.Key, history, false)
		if len(msgs) == 0 {
			return
		}
		if err := c.store.SaveMessages(ctx, msgs); err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save synced history: %w", err)})
			return
		}
		minID := minStorageMessageID(msgs)
		if minID == 0 || minID == offsetID {
			return
		}
		_ = c.store.UpdatePeerHistory(ctx, accountID, p.Key, minID, time.Now().UTC())
		offsetID = minID
		if len(msgs) < 100 {
			return
		}
		time.Sleep(350 * time.Millisecond)
	}
}

func (c *GotdClient) backfillHistory(ctx context.Context, accountID string, api *tg.Client, events chan<- Event) {
	if c.store == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		peers, err := c.store.ListPeers(ctx, accountID)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if len(peers) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		sort.SliceStable(peers, func(i, j int) bool {
			if peers[i].LastMessageAt.Equal(peers[j].LastMessageAt) {
				return peers[i].TopMessageID > peers[j].TopMessageID
			}
			return peers[i].LastMessageAt.After(peers[j].LastMessageAt)
		})
		c.focusMu.Lock()
		focused := c.focusPeer
		c.focusMu.Unlock()
		worked := false
		for _, p := range peers {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if focused != "" && focused == p.Key {
				continue
			}
			worked = true
			c.syncPeerHistory(ctx, accountID, api, p, events, 8)
			jitter := time.Duration(1000+mrand.Intn(2000)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}
		}
		if !worked {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (c *GotdClient) registerUpdateHandlers(dispatcher *tg.UpdateDispatcher, accountID *string, apiRef **tg.Client, events chan<- Event) {
	handle := func(ctx context.Context, e tg.Entities, msg *tg.Message, statusKey string) error {
		if accountID == nil || *accountID == "" || msg == nil {
			return nil
		}
		var api *tg.Client
		if apiRef != nil {
			api = *apiRef
		}
		peer, stMsg, ok := c.normalizeUpdateMessage(ctx, api, *accountID, msg, e)
		if !ok {
			return nil
		}
		if c.store != nil {
			if err := c.store.SavePeers(ctx, []storage.Peer{peer}); err != nil {
				return err
			}
			if err := c.store.SaveMessages(ctx, []storage.Message{stMsg}); err != nil {
				return err
			}
			chats, err := c.store.ListPeers(ctx, *accountID)
			if err == nil {
				sendEvent(ctx, events, Event{Kind: EventChats, Chats: peersToChats(chats)})
			}
		}
		removeIDs := c.takeMatchingPending(stMsg.PeerKey, stMsg.Text, stMsg.Outgoing)
		for _, id := range removeIDs {
			if c.store != nil {
				_ = c.store.DeleteMessage(ctx, *accountID, stMsg.PeerKey, id)
			}
		}
		sendEvent(ctx, events, Event{
			Kind:             EventMessages,
			PeerKey:          stMsg.PeerKey,
			Messages:         c.telegramMessages(ctx, *accountID, []storage.Message{stMsg}),
			Append:           true,
			RemoveMessageIDs: intIDsToStrings(removeIDs),
			StatusMsg:        i18n.M(statusKey),
		})
		if stMsg.Outgoing && peerSupportsGroupReadMarks(peer) {
			go c.refreshGroupReadMarks(ctx, *accountID, api, events, peer, []storage.Message{stMsg})
		}
		return nil
	}

	// Service messages carry no text or media, so they skip pending-send matching and
	// read-mark refresh; they only need to reach storage and the viewport.
	handleService := func(ctx context.Context, e tg.Entities, msg *tg.MessageService, statusKey string) error {
		if accountID == nil || *accountID == "" || msg == nil {
			return nil
		}
		peer, stMsg, ok := c.normalizeUpdateServiceMessage(ctx, *accountID, msg, e)
		if !ok {
			return nil
		}
		if c.store != nil {
			if err := c.store.SavePeers(ctx, []storage.Peer{peer}); err != nil {
				return err
			}
			if err := c.store.SaveMessages(ctx, []storage.Message{stMsg}); err != nil {
				return err
			}
			chats, err := c.store.ListPeers(ctx, *accountID)
			if err == nil {
				sendEvent(ctx, events, Event{Kind: EventChats, Chats: peersToChats(chats)})
			}
		}
		sendEvent(ctx, events, Event{
			Kind:      EventMessages,
			PeerKey:   stMsg.PeerKey,
			Messages:  c.telegramMessages(ctx, *accountID, []storage.Message{stMsg}),
			Append:    true,
			StatusMsg: i18n.M(statusKey),
		})
		return nil
	}

	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewMessage) error {
		if accountID != nil {
			c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		}
		switch msg := update.Message.(type) {
		case *tg.Message:
			return handle(ctx, e, msg, i18n.KeyStatusNewMessage)
		case *tg.MessageService:
			return handleService(ctx, e, msg, i18n.KeyStatusNewMessage)
		}
		return nil
	})
	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewChannelMessage) error {
		if accountID != nil {
			c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		}
		switch msg := update.Message.(type) {
		case *tg.Message:
			return handle(ctx, e, msg, i18n.KeyStatusNewChannelMessage)
		case *tg.MessageService:
			return handleService(ctx, e, msg, i18n.KeyStatusNewChannelMessage)
		}
		return nil
	})
	dispatcher.OnEditMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateEditMessage) error {
		msg, ok := update.Message.(*tg.Message)
		if !ok {
			return nil
		}
		if accountID != nil {
			c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		}
		return handle(ctx, e, msg, i18n.KeyStatusMessageEdited)
	})
	dispatcher.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateEditChannelMessage) error {
		msg, ok := update.Message.(*tg.Message)
		if !ok {
			return nil
		}
		if accountID != nil {
			c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		}
		return handle(ctx, e, msg, i18n.KeyStatusChannelMessageEdited)
	})
	dispatcher.OnDeleteChannelMessages(func(ctx context.Context, _ tg.Entities, update *tg.UpdateDeleteChannelMessages) error {
		pk := peerKey("channel", update.ChannelID)
		if c.store != nil && accountID != nil && *accountID != "" {
			_ = c.store.MarkMessagesDeleted(ctx, *accountID, pk, update.Messages)
			c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		}
		sendEvent(ctx, events, Event{Kind: EventMessages, PeerKey: pk, RemoveMessageIDs: intIDsToStrings(update.Messages), StatusMsg: i18n.M(i18n.KeyStatusChannelMessagesDeleted)})
		return nil
	})
	dispatcher.OnDraftMessage(func(ctx context.Context, _ tg.Entities, update *tg.UpdateDraftMessage) error {
		if accountID == nil || *accountID == "" {
			return nil
		}
		c.applyDraftUpdate(ctx, *accountID, events, update)
		return nil
	})
	dispatcher.OnUserTyping(func(ctx context.Context, e tg.Entities, update *tg.UpdateUserTyping) error {
		if accountID == nil || *accountID == "" {
			return nil
		}
		if topMsgID, ok := update.GetTopMsgID(); ok && topMsgID != 0 {
			return nil
		}
		entities := entitiesByID{users: e.Users, chats: e.Chats, channels: e.Channels}
		// In a private chat the peer and the typist are the same user.
		from := &tg.PeerUser{UserID: update.UserID}
		c.emitTyping(ctx, *accountID, events, peerKey("user", update.UserID), from, entities, update.Action)
		return nil
	})
	dispatcher.OnChatUserTyping(func(ctx context.Context, e tg.Entities, update *tg.UpdateChatUserTyping) error {
		if accountID == nil || *accountID == "" {
			return nil
		}
		entities := entitiesByID{users: e.Users, chats: e.Chats, channels: e.Channels}
		c.emitTyping(ctx, *accountID, events, peerKey("chat", update.ChatID), update.FromID, entities, update.Action)
		return nil
	})
	dispatcher.OnChannelUserTyping(func(ctx context.Context, e tg.Entities, update *tg.UpdateChannelUserTyping) error {
		if accountID == nil || *accountID == "" {
			return nil
		}
		if topMsgID, ok := update.GetTopMsgID(); ok && topMsgID != 0 {
			// Forum topic typing belongs to the topic, not the channel view; deferred.
			return nil
		}
		entities := entitiesByID{users: e.Users, chats: e.Chats, channels: e.Channels}
		c.emitTyping(ctx, *accountID, events, peerKey("channel", update.ChannelID), update.FromID, entities, update.Action)
		return nil
	})
	dispatcher.OnDeleteMessages(func(ctx context.Context, _ tg.Entities, update *tg.UpdateDeleteMessages) error {
		if c.store != nil && accountID != nil && *accountID != "" {
			if peers, err := c.store.PeerKeysForMessageIDs(ctx, *accountID, update.Messages); err == nil && len(peers) == 1 {
				_ = c.store.MarkMessagesDeleted(ctx, *accountID, peers[0], update.Messages)
				sendEvent(ctx, events, Event{
					Kind:             EventMessages,
					PeerKey:          peers[0],
					RemoveMessageIDs: intIDsToStrings(update.Messages),
					StatusMsg:        i18n.M(i18n.KeyStatusMessagesDeleted),
				})
			}
			c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		}
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusMessagesDeleted)})
		return nil
	})
	dispatcher.OnReadHistoryInbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadHistoryInbox) error {
		if accountID == nil || *accountID == "" || c.store == nil {
			return nil
		}
		if topMsgID, ok := update.GetTopMsgID(); ok && topMsgID != 0 {
			// Forum topic read state is a separate space; deferred.
			c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
			return nil
		}
		if key, ok := c.inboxPeerKey(ctx, *accountID, update.Peer); ok {
			c.applyReadInbox(ctx, *accountID, events, key, update.MaxID, update.StillUnreadCount)
		}
		c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		return nil
	})
	dispatcher.OnReadChannelInbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadChannelInbox) error {
		if accountID == nil || *accountID == "" || c.store == nil {
			return nil
		}
		c.applyReadInbox(ctx, *accountID, events, peerKey("channel", update.ChannelID), update.MaxID, update.StillUnreadCount)
		c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		return nil
	})
	dispatcher.OnReadHistoryOutbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadHistoryOutbox) error {
		if accountID == nil || *accountID == "" || c.store == nil {
			return nil
		}
		kind, id, ok := peerKindID(update.Peer)
		if !ok {
			return nil
		}
		peerKey := peerKey(kind, id)
		if err := c.store.UpdatePeerReadOutboxMaxID(ctx, *accountID, peerKey, update.MaxID); err != nil {
			return err
		}
		c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		sendEvent(ctx, events, Event{
			Kind:            EventReadOutbox,
			PeerKey:         peerKey,
			ReadOutboxMaxID: update.MaxID,
			StatusMsg:       i18n.M(i18n.KeyStatusMessageRead),
		})
		return nil
	})
	dispatcher.OnChannelMessageViews(func(ctx context.Context, _ tg.Entities, update *tg.UpdateChannelMessageViews) error {
		if accountID == nil || *accountID == "" || c.store == nil {
			return nil
		}
		pk := peerKey("channel", update.ChannelID)
		if err := c.store.UpdateMessageViews(ctx, *accountID, pk, update.ID, update.Views); err != nil {
			return err
		}
		patched, ok, err := c.store.MessageByID(ctx, *accountID, pk, update.ID)
		if err != nil || !ok {
			return err
		}
		sendEvent(ctx, events, Event{
			Kind:     EventMessages,
			PeerKey:  pk,
			Messages: c.telegramMessages(ctx, *accountID, []storage.Message{patched}),
			Patch:    true,
		})
		return nil
	})
	dispatcher.OnMessageReactions(func(ctx context.Context, e tg.Entities, update *tg.UpdateMessageReactions) error {
		if accountID == nil || *accountID == "" || c.store == nil {
			return nil
		}
		kind, id, ok := peerKindID(update.Peer)
		if !ok {
			return nil
		}
		pk := peerKey(kind, id)
		reactionsJSON := ReactionsJSON(ParseMessageReactions(&update.Reactions))
		if err := c.store.UpdateMessageReactions(ctx, *accountID, pk, update.MsgID, reactionsJSON); err != nil {
			return err
		}
		patched, ok, err := c.store.MessageByID(ctx, *accountID, pk, update.MsgID)
		if err != nil || !ok {
			return err
		}
		tgMsgs := c.telegramMessages(ctx, *accountID, []storage.Message{patched})
		if len(tgMsgs) > 0 {
			entities := entitiesByID{users: e.Users, chats: e.Chats, channels: e.Channels}
			tgMsgs[0].RecentReact = ParseRecentReactions(&update.Reactions, entities)
		}
		sendEvent(ctx, events, Event{
			Kind:     EventMessages,
			PeerKey:  pk,
			Messages: tgMsgs,
			Patch:    true,
		})
		return nil
	})
	dispatcher.OnFolderPeers(func(ctx context.Context, _ tg.Entities, update *tg.UpdateFolderPeers) error {
		if c.store == nil || accountID == nil || *accountID == "" {
			return nil
		}
		for _, folderPeer := range update.FolderPeers {
			kind, id, ok := peerKindID(folderPeer.Peer)
			if ok {
				_ = c.store.UpdatePeerFolder(ctx, *accountID, kind, id, folderPeer.FolderID)
			}
		}
		if peers, err := c.store.ListPeers(ctx, *accountID); err == nil {
			sendEvent(ctx, events, Event{Kind: EventChats, Chats: peersToChats(peers)})
		}
		c.saveUpdateState(ctx, events, storage.UpdateState{AccountID: *accountID, Pts: update.Pts})
		return nil
	})
}

func normalizeChat(accountID string, chat tg.ChatClass) (storage.Peer, Chat, bool) {
	switch c := chat.(type) {
	case *tg.Chat:
		return peer(accountID, "chat", c.ID, 0, c.Title, ""), Chat{ID: peerKey("chat", c.ID), Title: c.Title, Subtitle: "group"}, true
	case *tg.Channel:
		return peer(accountID, "channel", c.ID, c.AccessHash, c.Title, c.Username), Chat{ID: peerKey("channel", c.ID), Title: c.Title, Subtitle: "channel"}, true
	case *tg.ChatForbidden:
		return peer(accountID, "chat", c.ID, 0, c.Title, ""), Chat{ID: peerKey("chat", c.ID), Title: c.Title, Subtitle: "forbidden group"}, true
	case *tg.ChannelForbidden:
		return peer(accountID, "channel", c.ID, c.AccessHash, c.Title, ""), Chat{ID: peerKey("channel", c.ID), Title: c.Title, Subtitle: "forbidden channel"}, true
	default:
		return storage.Peer{}, Chat{}, false
	}
}

func normalizeUser(accountID string, user tg.UserClass) (storage.Peer, Chat, bool) {
	u, ok := user.(*tg.User)
	if !ok || u.Self {
		return storage.Peer{}, Chat{}, false
	}
	title := stringsJoin(u.FirstName, u.LastName)
	if title == "" && u.Username != "" {
		title = "@" + u.Username
	}
	if title == "" {
		title = fmt.Sprintf("user %d", u.ID)
	}
	p := peer(accountID, "user", u.ID, u.AccessHash, title, u.Username)
	p.Contact = u.Contact
	return p, Chat{ID: peerKey("user", u.ID), Title: title, Subtitle: "private", Contact: u.Contact}, true
}

type entitiesByID struct {
	users    map[int64]*tg.User
	chats    map[int64]*tg.Chat
	channels map[int64]*tg.Channel
}

func dialogEntities(users []tg.UserClass, chats []tg.ChatClass) entitiesByID {
	e := entitiesByID{
		users:    make(map[int64]*tg.User),
		chats:    make(map[int64]*tg.Chat),
		channels: make(map[int64]*tg.Channel),
	}
	for _, item := range users {
		if user, ok := item.(*tg.User); ok {
			e.users[user.ID] = user
		}
	}
	for _, item := range chats {
		switch chat := item.(type) {
		case *tg.Chat:
			e.chats[chat.ID] = chat
		case *tg.Channel:
			e.channels[chat.ID] = chat
		}
	}
	return e
}

func normalizeDialog(accountID string, item tg.DialogClass, messageByID map[int]tg.MessageClass, entities entitiesByID, index int) (storage.Peer, Chat, bool) {
	d, ok := item.(*tg.Dialog)
	if !ok {
		return storage.Peer{}, Chat{}, false
	}
	p, ok := peerFromRef(accountID, d.Peer, entities)
	if !ok {
		return storage.Peer{}, Chat{}, false
	}
	switch last := messageByID[d.TopMessage].(type) {
	case *tg.Message:
		p.LastMessageAt = time.Unix(int64(last.Date), 0).UTC()
		messagePreview(last).applyTo(&p)
		p.ThumbCacheKey = thumbCacheKey(last)
	case *tg.MessageService:
		p.LastMessageAt = time.Unix(int64(last.Date), 0).UTC()
		serviceMessagePreview(last, entities).applyTo(&p)
	}
	p.TopMessageID = d.TopMessage
	p.Unread = d.UnreadCount
	p.ReadOutboxMaxID = d.ReadOutboxMaxID
	p.ReadInboxMaxID = d.ReadInboxMaxID
	p.Pinned = d.Pinned
	if p.Pinned {
		p.PinnedOrder = index + 1
	}
	if folderID, ok := d.GetFolderID(); ok {
		p.FolderID = folderID
	}
	if p.Subtitle == "" {
		p.Subtitle = p.Kind
	}
	chat := Chat{
		ID:            p.Key,
		Title:         p.Title,
		Subtitle:      chatSubtitle(p),
		Kind:          p.Kind,
		Contact:       p.Contact,
		FolderID:      p.FolderID,
		Pinned:        p.Pinned,
		PinnedOrder:   p.PinnedOrder,
		Unread:        p.Unread,
		LastPreview:   p.LastPreview,
		PreviewKey:    p.LastPreviewKey,
		PreviewArg:    p.LastPreviewArg,
		LastMessageAt: p.LastMessageAt,
		TopMessageID:  p.TopMessageID,
	}
	return p, chat, true
}

func peerFromRef(accountID string, ref tg.PeerClass, entities entitiesByID) (storage.Peer, bool) {
	switch p := ref.(type) {
	case *tg.PeerUser:
		user := entities.users[p.UserID]
		if user == nil {
			return peer(accountID, "user", p.UserID, 0, fmt.Sprintf("user %d", p.UserID), ""), true
		}
		title := userTitle(user)
		subtitle := "private"
		if user.Self {
			title = "Saved Messages"
			subtitle = "saved messages"
			out := peer(accountID, "self", user.ID, 0, title, user.Username)
			out.Subtitle = subtitle
			return out, true
		} else if user.Bot {
			subtitle = "bot"
		}
		out := peer(accountID, "user", user.ID, user.AccessHash, title, user.Username)
		out.Subtitle = subtitle
		out.Contact = user.Contact
		return out, true
	case *tg.PeerChat:
		chat := entities.chats[p.ChatID]
		title := fmt.Sprintf("group %d", p.ChatID)
		if chat != nil {
			title = chat.Title
		}
		out := peer(accountID, "chat", p.ChatID, 0, title, "")
		out.Subtitle = "group"
		return out, true
	case *tg.PeerChannel:
		channel := entities.channels[p.ChannelID]
		title := fmt.Sprintf("channel %d", p.ChannelID)
		username := ""
		accessHash := int64(0)
		subtitle := "channel"
		if channel != nil {
			title = channel.Title
			username = channel.Username
			accessHash = channel.AccessHash
			if channel.Megagroup {
				subtitle = "group"
			}
		}
		out := peer(accountID, "channel", p.ChannelID, accessHash, title, username)
		out.Subtitle = subtitle
		return out, true
	default:
		return storage.Peer{}, false
	}
}

func (c *GotdClient) normalizeUpdateMessage(ctx context.Context, api *tg.Client, accountID string, msg *tg.Message, e tg.Entities) (storage.Peer, storage.Message, bool) {
	entities := entitiesByID{users: e.Users, chats: e.Chats, channels: e.Channels}
	p, ok := peerFromRef(accountID, messagePeer(msg), entities)
	if !ok {
		return storage.Peer{}, storage.Message{}, false
	}
	p.LastMessageAt = time.Unix(int64(msg.Date), 0).UTC()
	messagePreview(msg).applyTo(&p)
	p.TopMessageID = msg.ID
	p.ThumbCacheKey = thumbCacheKey(msg)
	p.UpdatedAt = time.Now().UTC()
	if c.store != nil {
		if existing, ok, err := c.store.Peer(ctx, accountID, p.Key); err == nil && ok {
			p = mergePeerActivity(existing, p)
		}
	}
	stMsg := c.normalizeTGMessageWithPreview(ctx, api, accountID, p.Key, msg, entities, c.isFocusedPeer(p.Key))
	return p, stMsg, true
}

func messagePeer(msg *tg.Message) tg.PeerClass {
	if msg == nil {
		return nil
	}
	if msg.PeerID != nil {
		return msg.PeerID
	}
	if from, ok := msg.GetFromID(); ok {
		return from
	}
	return nil
}

func (c *GotdClient) normalizeTGMessage(ctx context.Context, api *tg.Client, accountID, peerKey string, msg *tg.Message, entities entitiesByID) storage.Message {
	return c.normalizeTGMessageWithPreview(ctx, api, accountID, peerKey, msg, entities, true)
}

func (c *GotdClient) normalizeTGMessageWithPreview(ctx context.Context, api *tg.Client, accountID, peerKey string, msg *tg.Message, entities entitiesByID, preview bool) storage.Message {
	senderKind, senderID := peerRefParts(msg.FromID)
	senderName := senderDisplayName(msg.FromID, entities)
	media := classifyMessageMedia(msg.Media)
	if preview {
		media = c.enrichMediaPreview(ctx, api, media)
	} else if media.Kind != "" {
		media.PreviewText = mediaFallbackText(media)
		media = resolveAnimatedLocalPath(c.cfg.Paths.MediaDir, media)
	}
	mediaJSON := ""
	if media.Kind != "" {
		if raw, err := json.Marshal(media); err == nil {
			mediaJSON = string(raw)
		}
	}
	replyID := 0
	if reply, ok := msg.GetReplyTo(); ok {
		if header, ok := reply.(*tg.MessageReplyHeader); ok {
			replyID = header.ReplyToMsgID
		}
	}
	forward := ""
	if fwd, ok := msg.GetFwdFrom(); ok {
		forward = forwardSource(fwd)
	}
	views := 0
	if v, ok := msg.GetViews(); ok {
		views = v
	}
	forwards := 0
	if f, ok := msg.GetForwards(); ok {
		forwards = f
	}
	reactionsJSON := ""
	if reactions, ok := msg.GetReactions(); ok {
		reactionsJSON = ReactionsJSON(ParseMessageReactions(&reactions))
	}
	viaBot := viaBotUsername(msg, entities)
	return storage.Message{
		AccountID:      accountID,
		PeerKey:        peerKey,
		ID:             msg.ID,
		Date:           time.Unix(int64(msg.Date), 0).UTC(),
		Sender:         senderName,
		SenderKind:     senderKind,
		SenderID:       senderID,
		SenderName:     senderName,
		SenderColor:    senderColor(senderKind, senderID, senderName),
		Outgoing:       msg.Out,
		Text:           msg.Message,
		MediaJSON:      mediaJSON,
		MediaKind:      media.Kind,
		ForwardSource:  forward,
		ReplyToID:      replyID,
		State:          "synced",
		Views:          views,
		Forwards:       forwards,
		ReactionsJSON:  reactionsJSON,
		ViaBotUsername: viaBot,
	}
}

func viaBotUsername(msg *tg.Message, entities entitiesByID) string {
	if msg == nil {
		return ""
	}
	botID, ok := msg.GetViaBotID()
	if !ok || botID == 0 {
		return ""
	}
	if u := entities.users[botID]; u != nil && u.Username != "" {
		return u.Username
	}
	return ""
}

// chatSubtitle is the chat kind only ("private"/"group"/"channel"/...). It must stay a bare
// kind token: i18n.ChatKind translates it by exact match, and folder rules compare it with
// ==, so appending anything here silently breaks both.
func chatSubtitle(p storage.Peer) string {
	if p.Subtitle != "" {
		return p.Subtitle
	}
	return p.Kind
}

func userTitle(u *tg.User) string {
	title := stringsJoin(u.FirstName, u.LastName)
	if title == "" && u.Username != "" {
		title = "@" + u.Username
	}
	if title == "" {
		title = fmt.Sprintf("user %d", u.ID)
	}
	return title
}

func (c *GotdClient) consumeCommands(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, commands <-chan Command) {
	for {
		select {
		case <-ctx.Done():
			return
		case command, ok := <-commands:
			if !ok {
				return
			}
			switch command.Kind {
			case CommandFocusChat:
				c.setFocusPeer(command.PeerKey)
			case CommandOpenChat:
				peerKey := command.PeerKey
				jumpToUnread := command.JumpToUnread
				go c.openChat(ctx, accountID, api, events, peerKey, jumpToUnread)
			case CommandSendText:
				c.sendText(ctx, accountID, api, events, command.PeerKey, command.Text, command.ReplyToID, command.MentionEntities)
			case CommandRetrySend:
				c.retrySendText(ctx, accountID, api, events, command.PeerKey, command.MessageID)
			case CommandLoadOlder:
				peerKey := command.PeerKey
				beforeID := command.MessageID
				go func() {
					if !c.isFocusedPeer(peerKey) {
						return
					}
					c.loadOlder(ctx, accountID, api, events, peerKey, beforeID)
				}()
			case CommandFillHistoryGap:
				peerKey := command.PeerKey
				offsetID := command.MessageID
				go func() {
					if !c.isFocusedPeer(peerKey) {
						return
					}
					c.fillHistoryGap(ctx, accountID, api, events, peerKey, offsetID)
				}()
			case CommandDeleteMessage:
				c.deleteMessage(ctx, accountID, api, events, command.PeerKey, command.MessageID)
			case CommandDownloadMedia:
				c.downloadMedia(ctx, api, events, command.Media)
			case CommandSendReaction:
				c.sendReaction(ctx, accountID, api, events, command.PeerKey, command.MessageID, command.Reaction)
			case CommandMarkViewed:
				c.markMessageViewed(ctx, accountID, api, events, command.PeerKey, command.MessageID)
			case CommandSearchMentions:
				go c.searchMentions(ctx, accountID, api, events, command)
			case CommandLoadBotCommands:
				go c.loadBotCommands(ctx, accountID, api, events, command)
			case CommandQueryInlineBot:
				go c.queryInlineBot(ctx, accountID, api, events, command)
			case CommandSendInlineResult:
				c.sendInlineResult(ctx, accountID, api, events, command)
			case CommandLoadPinned:
				go c.loadPinnedMessageList(ctx, accountID, api, events, command.PeerKey)
			case CommandJumpToMessage:
				go c.jumpToMessage(ctx, accountID, api, events, command.PeerKey, command.MessageID)
			case CommandFetchInlineThumb:
				go c.fetchInlineThumb(ctx, api, events, command)
			case CommandSaveDraft:
				go c.saveDraft(ctx, accountID, api, events, command)
			case CommandSetTyping:
				go c.setTyping(ctx, accountID, api, command.PeerKey, command.Typing)
			case CommandSearchMessages:
				go c.searchMessages(ctx, accountID, api, events, command)
			case CommandForwardMessages:
				// Must be `go`: forwarding is a network round trip and the command loop
				// serves every other interaction.
				go c.forwardMessages(ctx, accountID, api, events, command)
			case CommandMarkRead:
				// Must be `go`: a synchronous read mark would stall the command loop on
				// every scroll.
				go c.markRead(ctx, accountID, api, events, command)
			}
		}
	}
}

// openChat loads a chat's history and emits it.
//
// jumpToUnread asks for the window around the first unread message instead of the newest one.
// It is a parameter rather than a second command on purpose: two commands would dispatch two
// concurrent viewport replacements, and whichever landed second would win nondeterministically.
func (c *GotdClient) openChat(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, jumpToUnread bool) {
	if c.store == nil || !c.isFocusedPeer(peerKey) {
		return
	}
	// Decided up front so an unread jump does not first flash the newest cached window and then
	// replace it with one from the middle of the history.
	wantUnreadJump := false
	if jumpToUnread {
		if p, ok, err := c.store.Peer(ctx, accountID, peerKey); err == nil && ok {
			wantUnreadJump = p.ReadInboxMaxID > 0 && p.Unread > 0
		}
	}
	cached, err := c.store.MessagesForPeer(ctx, accountID, peerKey, 100)
	cachedCount := 0
	if err == nil && len(cached) > 0 {
		cachedCount = len(cached)
		if !wantUnreadJump {
			converted := c.telegramMessages(ctx, accountID, cached)
			c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventMessages, PeerKey: peerKey, Messages: converted})
		}
		if c.isFocusedPeer(peerKey) {
			toEnrich := append([]storage.Message(nil), cached...)
			go c.enrichPeerMessagePreviews(ctx, accountID, api, events, peerKey, toEnrich)
			if peer, ok, err := c.store.Peer(ctx, accountID, peerKey); err == nil && ok {
				toRead := append([]storage.Message(nil), cached...)
				go c.refreshGroupReadMarks(ctx, accountID, api, events, peer, toRead)
			}
		}
		go c.loadPeerPinnedMessage(ctx, accountID, api, events, peerKey)
	}
	c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusLoadingHistory)})

	c.beginForegroundLoad()
	defer c.endForegroundLoad()

	var p storage.Peer
	var msgs []storage.Message
	var skipReplace bool
	func() {
		if !c.isFocusedPeer(peerKey) {
			return
		}
		c.peerHistoryLock(peerKey).Lock()
		defer c.peerHistoryLock(peerKey).Unlock()

		var ok bool
		var err error
		p, ok, err = c.store.Peer(ctx, accountID, peerKey)
		if err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("peer %s not found", peerKey)
			}
			c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventError, Error: err})
			return
		}
		input, err := inputPeer(p)
		if err != nil {
			c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventError, Error: err})
			return
		}
		if !c.isFocusedPeer(peerKey) {
			return
		}
		// Anchor on the first unread rather than the tail when asked, using the same
		// centred-window trick as jumpToMessage: OffsetID one past the read watermark with
		// AddOffset pulled back half the window, so the target arrives with context on both
		// sides in one round trip.
		windowed := jumpToUnread && p.ReadInboxMaxID > 0 && p.Unread > 0
		req := &tg.MessagesGetHistoryRequest{Peer: input, Limit: 50}
		if windowed {
			req.OffsetID = p.ReadInboxMaxID + 1
			req.AddOffset = -jumpWindowLimit / 2
			req.Limit = jumpWindowLimit
		}
		history, err := c.messagesGetHistory(ctx, api, req)
		if err != nil {
			c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventError, Error: fmt.Errorf("load history: %w", err)})
			return
		}
		msgs = c.normalizeMessagesWithPreview(ctx, api, accountID, peerKey, history, false)
		if err := c.store.SaveMessages(ctx, msgs); err != nil {
			c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventError, Error: fmt.Errorf("save history: %w", err)})
			return
		}
		if len(msgs) > 0 && !windowed {
			// Deliberately skipped for a window: lowering history_min_id to a disjoint
			// window's minimum makes the backfiller believe it holds history it does not,
			// and it then chases a hole that is not there. jump.go avoids this the same way.
			_ = c.store.UpdatePeerHistory(ctx, accountID, peerKey, minStorageMessageID(msgs), time.Now().UTC())
		}
		// The shortcut compares against the newest-window cache, so it is meaningless for a
		// window anchored elsewhere. Gated on wantUnreadJump rather than windowed because that
		// is what suppressed the cached emit: if the two ever disagreed, skipping the replace
		// would leave the pane empty.
		skipReplace = !wantUnreadJump && !windowed && cachedCount > 0 && sameStorageMessageSet(cached, msgs)
		switch {
		case skipReplace:
			c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusHistoryLoaded, len(cached))})
		case windowed:
			// Resolve the landing spot from what actually came back. The id one past the
			// watermark may be deleted, a service message, or outside the window, so
			// asserting it exists would fail on ordinary chats.
			target := firstUnreadStorageID(msgs, p.ReadInboxMaxID)
			event := Event{
				Kind:            EventMessages,
				PeerKey:         peerKey,
				Messages:        c.telegramMessages(ctx, accountID, msgs),
				WindowedHistory: true,
				StatusMsg:       i18n.M(i18n.KeyStatusHistoryLoaded, len(msgs)),
			}
			if target > 0 {
				event.SelectMessageID = strconv.Itoa(target)
				event.FirstUnreadID = strconv.Itoa(target)
			}
			c.sendFocusedEvent(ctx, events, peerKey, event)
		default:
			c.sendFocusedEvent(ctx, events, peerKey, Event{Kind: EventMessages, PeerKey: peerKey, Messages: c.telegramMessages(ctx, accountID, msgs), StatusMsg: i18n.M(i18n.KeyStatusHistoryLoaded, len(msgs))})
		}
		if c.isFocusedPeer(peerKey) {
			c.refreshChannelViews(ctx, accountID, api, events, p, msgs)
			toRead := append([]storage.Message(nil), msgs...)
			go c.refreshGroupReadMarks(ctx, accountID, api, events, p, toRead)
			toEnrich := append([]storage.Message(nil), msgs...)
			go c.enrichPeerMessagePreviews(ctx, accountID, api, events, peerKey, toEnrich)
		}
	}()
	go c.loadPeerPinnedMessage(ctx, accountID, api, events, peerKey)
	c.sendPeerDraft(ctx, accountID, events, peerKey)
	go func() {
		if !c.isFocusedPeer(peerKey) {
			return
		}
		c.fillKnownGapsForPeer(ctx, accountID, api, events, peerKey)
	}()
}

func (c *GotdClient) loadOlder(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, beforeID int) {
	if c.store == nil {
		return
	}
	if !c.tryBeginOlderLoad(peerKey, beforeID) {
		return
	}
	defer c.endOlderLoad(peerKey, beforeID)

	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusLoadingOlderMessages)})

	c.beginForegroundLoad()
	defer c.endForegroundLoad()

	cacheShown := false
	var enrichBatch []storage.Message
	var p storage.Peer
	func() {
		c.peerHistoryLock(peerKey).Lock()
		defer c.peerHistoryLock(peerKey).Unlock()

		var ok bool
		var err error
		p, ok, err = c.store.Peer(ctx, accountID, peerKey)
		if err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("peer %s not found", peerKey)
			}
			sendEvent(ctx, events, Event{Kind: EventError, Error: err})
			return
		}
		input, err := inputPeer(p)
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: err})
			return
		}

		if beforeID > 0 {
			cached, err := c.store.OlderMessagesForPeer(ctx, accountID, peerKey, beforeID, 50)
			if err != nil {
				sendEvent(ctx, events, Event{Kind: EventError, Error: err})
				return
			}
			if len(cached) > 0 && olderCacheIsAdjacent(cached, beforeID) {
				cacheShown = true
				enrichBatch = append(enrichBatch, cached...)
				sendEvent(ctx, events, Event{
					Kind:             EventMessages,
					PeerKey:          peerKey,
					Messages:         c.telegramMessages(ctx, accountID, cached),
					Merge:            true,
					PreserveViewport: true,
					StatusMsg:        i18n.M(i18n.KeyStatusOlderCachedLoaded),
				})
			}
		}

		offsetID := beforeID
		if offsetID == 0 {
			offsetID = p.HistoryMinID
		}
		if offsetID == 0 {
			cached, err := c.store.MessagesForPeer(ctx, accountID, peerKey, 100)
			if err == nil && len(cached) > 0 {
				offsetID = minStorageMessageID(cached)
			}
		}
		if cacheShown {
			sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusFetchingOlderNetwork)})
		}
		history, err := c.messagesGetHistory(ctx, api, &tg.MessagesGetHistoryRequest{Peer: input, OffsetID: offsetID, Limit: 50})
		if err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("load older history: %w", err)})
			return
		}
		msgs := c.normalizeMessagesWithPreview(ctx, api, accountID, peerKey, history, false)
		if len(msgs) == 0 {
			if cacheShown {
				sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusOlderCacheOnlyGapFilled)})
			} else {
				sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusNoOlderMessages)})
			}
			return
		}
		if err := c.store.SaveMessages(ctx, msgs); err != nil {
			sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save older history: %w", err)})
			return
		}
		_ = c.store.UpdatePeerHistory(ctx, accountID, peerKey, minStorageMessageID(msgs), time.Now().UTC())
		enrichBatch = append(enrichBatch, msgs...)
		sendEvent(ctx, events, Event{
			Kind:             EventMessages,
			PeerKey:          peerKey,
			Messages:         c.telegramMessages(ctx, accountID, msgs),
			Merge:            true,
			PreserveViewport: true,
			StatusMsg:        i18n.M(i18n.KeyStatusOlderMessagesLoaded),
		})
		c.refreshChannelViews(ctx, accountID, api, events, p, msgs)
		if c.isFocusedPeer(peerKey) {
			toRead := append([]storage.Message(nil), msgs...)
			go c.refreshGroupReadMarks(ctx, accountID, api, events, p, toRead)
		}
	}()
	if len(enrichBatch) > 0 && c.isFocusedPeer(peerKey) {
		toEnrich := append([]storage.Message(nil), enrichBatch...)
		go c.enrichPeerMessagePreviews(ctx, accountID, api, events, peerKey, toEnrich)
	}
	c.fillKnownGapsForPeer(ctx, accountID, api, events, peerKey)
}

func (c *GotdClient) tryBeginOlderLoad(peerKey string, beforeID int) bool {
	key := fmt.Sprintf("%s:%d", peerKey, beforeID)
	c.olderLoadMu.Lock()
	defer c.olderLoadMu.Unlock()
	if _, ok := c.olderLoading[key]; ok {
		return false
	}
	c.olderLoading[key] = struct{}{}
	return true
}

func (c *GotdClient) endOlderLoad(peerKey string, beforeID int) {
	key := fmt.Sprintf("%s:%d", peerKey, beforeID)
	c.olderLoadMu.Lock()
	delete(c.olderLoading, key)
	c.olderLoadMu.Unlock()
}

func (c *GotdClient) tryBeginGapFillSession(peerKey string) bool {
	c.gapFillSessionMu.Lock()
	defer c.gapFillSessionMu.Unlock()
	if c.gapFillSessions == nil {
		c.gapFillSessions = make(map[string]struct{})
	}
	if _, ok := c.gapFillSessions[peerKey]; ok {
		return false
	}
	c.gapFillSessions[peerKey] = struct{}{}
	return true
}

func (c *GotdClient) endGapFillSession(peerKey string) {
	c.gapFillSessionMu.Lock()
	delete(c.gapFillSessions, peerKey)
	c.gapFillSessionMu.Unlock()
}

func (c *GotdClient) deleteMessage(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, messageID int) {
	if messageID == 0 {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusNoMessageSelected)})
		return
	}
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	if p.Kind == "channel" {
		_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash},
			ID:      []int{messageID},
		})
	} else {
		_, err = api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{Revoke: true, ID: []int{messageID}})
	}
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("delete message: %w", err)})
		return
	}
	deleted, ok, loadErr := c.store.MessageByID(ctx, accountID, peerKey, messageID)
	if loadErr != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: loadErr})
		return
	}
	if !ok {
		deleted = storage.Message{AccountID: accountID, PeerKey: peerKey, ID: messageID, Date: time.Now().UTC(), State: "deleted"}
	}
	deleted.State = "deleted"
	deleted.Text = ""
	deleted.MediaJSON = ""
	deleted.MediaKind = ""
	_ = c.store.MarkMessagesDeleted(ctx, accountID, peerKey, []int{messageID})
	sendEvent(ctx, events, Event{
		Kind:      EventMessages,
		PeerKey:   peerKey,
		Messages:  c.telegramMessages(ctx, accountID, []storage.Message{deleted}),
		Append:    true,
		StatusMsg: i18n.M(i18n.KeyStatusMessageDeleted),
	})
}

func (c *GotdClient) downloadMedia(ctx context.Context, api *tg.Client, events chan<- Event, media MediaAttachment) {
	if media.LocalPath != "" {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusMediaCachedAt, media.LocalPath)})
		return
	}
	if api == nil || media.DownloadKey == "" {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusNoDownloadableMedia)})
		return
	}
	path, err := c.ensureMediaPreview(ctx, api, media)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("download media preview: %w", err)})
		return
	}
	if path == "" {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusNoTerminalPreview)})
		return
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusMediaCachedAt, path)})
}

func (c *GotdClient) sendText(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey, text string, replyToID int, mentionEntities []MessageEntityMentionName) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	randomID, err := randomInt64()
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	pendingID := -int(randomID & 0x7fffffff)
	pending := storage.Message{
		AccountID:   accountID,
		PeerKey:     peerKey,
		ID:          pendingID,
		Date:        time.Now().UTC(),
		Sender:      "me",
		SenderName:  "me",
		SenderColor: senderColor("self", 0, "me"),
		Outgoing:    true,
		Text:        text,
		ReplyToID:   replyToID,
		State:       "pending",
	}
	_ = c.store.SaveMessages(ctx, []storage.Message{pending})
	c.rememberPending(peerKey, text, pendingID)
	sendEvent(ctx, events, Event{Kind: EventMessages, PeerKey: peerKey, Messages: c.telegramMessages(ctx, accountID, []storage.Message{pending}), Append: true, StatusMsg: i18n.M(i18n.KeyStatusSending)})

	request := &tg.MessagesSendMessageRequest{
		Peer:     input,
		Message:  text,
		RandomID: randomID,
	}
	if replyToID != 0 {
		request.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: replyToID}
	}
	if entities := buildMentionNameEntities(mentionEntities); len(entities) > 0 {
		request.SetEntities(entities)
	}
	updates, err := api.MessagesSendMessage(ctx, request)
	if err != nil {
		c.forgetPending(peerKey, text, pendingID)
		pending.State = "failed"
		_ = c.store.SaveMessages(ctx, []storage.Message{pending})
		sendEvent(ctx, events, Event{Kind: EventMessages, PeerKey: peerKey, Messages: c.telegramMessages(ctx, accountID, []storage.Message{pending}), Append: true})
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("send message: %w", err)})
		return
	}
	serverMessages := c.messagesFromSendUpdates(ctx, api, accountID, peerKey, text, replyToID, updates)
	if len(serverMessages) > 0 {
		c.forgetPending(peerKey, text, pendingID)
		removeIDs := []int{pendingID}
		_ = c.store.DeleteMessage(ctx, accountID, peerKey, pendingID)
		_ = c.store.SaveMessages(ctx, serverMessages)
		sendEvent(ctx, events, Event{
			Kind:             EventMessages,
			PeerKey:          peerKey,
			Messages:         c.telegramMessages(ctx, accountID, serverMessages),
			Append:           true,
			RemoveMessageIDs: intIDsToStrings(removeIDs),
			StatusMsg:        i18n.M(i18n.KeyStatusMessageSent),
		})
		// Only now that the server has the message. retrySendText deliberately does not
		// do this: a failed row still holds the text the draft was covering for.
		c.clearDraftAfterSend(ctx, accountID, api, peerKey)
		return
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusMessageSubmitted)})
}

func (c *GotdClient) retrySendText(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, failedID int) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	if failedID >= 0 {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusOnlyLocalFailedRetry)})
		return
	}
	st, ok, err := c.store.MessageByID(ctx, accountID, peerKey, failedID)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	if !ok {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusMessageNotFound)})
		return
	}
	if st.State != "failed" || !st.Outgoing || strings.TrimSpace(st.Text) == "" {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusOnlyFailedTextRetry)})
		return
	}
	p, peerOK, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !peerOK {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	randomID, err := randomInt64()
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	text := st.Text
	replyToID := st.ReplyToID
	pendingID := st.ID

	st.State = "pending"
	st.Date = time.Now().UTC()
	if err := c.store.SaveMessages(ctx, []storage.Message{st}); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("save pending: %w", err)})
		return
	}
	c.rememberPending(peerKey, text, pendingID)
	sendEvent(ctx, events, Event{Kind: EventMessages, PeerKey: peerKey, Messages: c.telegramMessages(ctx, accountID, []storage.Message{st}), Append: true, StatusMsg: i18n.M(i18n.KeyStatusRetryingSend)})

	request := &tg.MessagesSendMessageRequest{
		Peer:     input,
		Message:  text,
		RandomID: randomID,
	}
	if replyToID != 0 {
		request.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: replyToID}
	}
	updates, err := api.MessagesSendMessage(ctx, request)
	if err != nil {
		c.forgetPending(peerKey, text, pendingID)
		st.State = "failed"
		_ = c.store.SaveMessages(ctx, []storage.Message{st})
		sendEvent(ctx, events, Event{Kind: EventMessages, PeerKey: peerKey, Messages: c.telegramMessages(ctx, accountID, []storage.Message{st}), Append: true})
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("send message: %w", err)})
		return
	}
	serverMessages := c.messagesFromSendUpdates(ctx, api, accountID, peerKey, text, replyToID, updates)
	if len(serverMessages) > 0 {
		c.forgetPending(peerKey, text, pendingID)
		removeIDs := []int{pendingID}
		_ = c.store.DeleteMessage(ctx, accountID, peerKey, pendingID)
		_ = c.store.SaveMessages(ctx, serverMessages)
		sendEvent(ctx, events, Event{
			Kind:             EventMessages,
			PeerKey:          peerKey,
			Messages:         c.telegramMessages(ctx, accountID, serverMessages),
			Append:           true,
			RemoveMessageIDs: intIDsToStrings(removeIDs),
			StatusMsg:        i18n.M(i18n.KeyStatusMessageSent),
		})
		return
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusMessageSubmitted)})
}

func (c *GotdClient) messagesFromSendUpdates(ctx context.Context, api *tg.Client, accountID, peerKey, sentText string, replyToID int, updates tg.UpdatesClass) []storage.Message {
	var out []storage.Message
	switch u := updates.(type) {
	case *tg.UpdateShortSentMessage:
		out = append(out, storage.Message{
			AccountID:   accountID,
			PeerKey:     peerKey,
			ID:          u.ID,
			Date:        time.Unix(int64(u.Date), 0).UTC(),
			Sender:      "me",
			SenderName:  "me",
			SenderColor: senderColor("self", 0, "me"),
			Outgoing:    true,
			Text:        sentText,
			ReplyToID:   replyToID,
			State:       "synced",
		})
	case *tg.Updates:
		entities := dialogEntities(u.Users, u.Chats)
		for _, update := range u.Updates {
			switch item := update.(type) {
			case *tg.UpdateNewMessage:
				out = append(out, c.normalizeUpdateMessageClass(ctx, api, accountID, peerKey, item.Message, entities)...)
			case *tg.UpdateNewChannelMessage:
				out = append(out, c.normalizeUpdateMessageClass(ctx, api, accountID, peerKey, item.Message, entities)...)
			}
		}
	case *tg.UpdatesCombined:
		entities := dialogEntities(u.Users, u.Chats)
		for _, update := range u.Updates {
			switch item := update.(type) {
			case *tg.UpdateNewMessage:
				out = append(out, c.normalizeUpdateMessageClass(ctx, api, accountID, peerKey, item.Message, entities)...)
			case *tg.UpdateNewChannelMessage:
				out = append(out, c.normalizeUpdateMessageClass(ctx, api, accountID, peerKey, item.Message, entities)...)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func minStorageMessageID(messages []storage.Message) int {
	minID := 0
	for _, msg := range messages {
		if msg.ID <= 0 {
			continue
		}
		if minID == 0 || msg.ID < minID {
			minID = msg.ID
		}
	}
	return minID
}

func sameStorageMessageSet(a, b []storage.Message) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	seen := make(map[int]struct{}, len(a))
	for _, msg := range a {
		seen[msg.ID] = struct{}{}
	}
	for _, msg := range b {
		if _, ok := seen[msg.ID]; !ok {
			return false
		}
	}
	return true
}

func (c *GotdClient) rememberPending(peerKey, text string, id int) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	key := pendingKey(peerKey, text)
	c.pending[key] = append(c.pending[key], pendingMessage{ID: id, CreatedAt: time.Now().UTC()})
}

func (c *GotdClient) takeMatchingPending(peerKey, text string, outgoing bool) []int {
	if !outgoing || text == "" {
		return nil
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	key := pendingKey(peerKey, text)
	pending := c.pending[key]
	for len(pending) > 0 && time.Since(pending[0].CreatedAt) > 5*time.Minute {
		pending = pending[1:]
	}
	if len(pending) == 0 {
		delete(c.pending, key)
		return nil
	}
	id := pending[0].ID
	pending = pending[1:]
	if len(pending) == 0 {
		delete(c.pending, key)
	} else {
		c.pending[key] = pending
	}
	return []int{id}
}

func (c *GotdClient) forgetPending(peerKey, text string, id int) {
	if text == "" {
		return
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	key := pendingKey(peerKey, text)
	pending := c.pending[key]
	for i, p := range pending {
		if p.ID == id {
			pending = append(pending[:i], pending[i+1:]...)
			break
		}
	}
	if len(pending) == 0 {
		delete(c.pending, key)
	} else {
		c.pending[key] = pending
	}
}

func pendingKey(peerKey, text string) string {
	return peerKey + "\x00" + text
}

func (c *GotdClient) normalizeMessages(ctx context.Context, api *tg.Client, accountID, peerKey string, history tg.MessagesMessagesClass) []storage.Message {
	return c.normalizeMessagesWithPreview(ctx, api, accountID, peerKey, history, true)
}

func (c *GotdClient) normalizeMessagesWithPreview(ctx context.Context, api *tg.Client, accountID, peerKey string, history tg.MessagesMessagesClass, preview bool) []storage.Message {
	modified, ok := history.AsModified()
	if !ok {
		return nil
	}
	entities := dialogEntities(modified.GetUsers(), modified.GetChats())
	out := make([]storage.Message, 0, len(modified.GetMessages()))
	for _, item := range modified.GetMessages() {
		switch msg := item.(type) {
		case *tg.Message:
			out = append(out, c.normalizeTGMessageWithPreview(ctx, api, accountID, peerKey, msg, entities, preview))
		case *tg.MessageService:
			if stMsg, ok := c.normalizeTGServiceMessage(accountID, peerKey, msg, entities); ok {
				out = append(out, stMsg)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].ID < out[j].ID
		}
		return out[i].Date.Before(out[j].Date)
	})
	return out
}

// normalizeUpdateMessageClass converts a message carried by an update into storage rows,
// covering both regular and service messages. Service messages occupy real message IDs, so
// dropping them would leave gaps that the history gap detector then tries to refill forever.
func (c *GotdClient) normalizeUpdateMessageClass(ctx context.Context, api *tg.Client, accountID, peerKey string, item tg.MessageClass, entities entitiesByID) []storage.Message {
	switch msg := item.(type) {
	case *tg.Message:
		return []storage.Message{c.normalizeTGMessage(ctx, api, accountID, peerKey, msg, entities)}
	case *tg.MessageService:
		if stMsg, ok := c.normalizeTGServiceMessage(accountID, peerKey, msg, entities); ok {
			return []storage.Message{stMsg}
		}
	}
	return nil
}

func toTelegramMessages(messages []storage.Message) []Message {
	senderByID := make(map[int]string, len(messages))
	for _, m := range messages {
		if m.SenderName != "" {
			senderByID[m.ID] = m.SenderName
		}
	}
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		media := MediaAttachment{}
		if msg.MediaJSON != "" {
			_ = json.Unmarshal([]byte(msg.MediaJSON), &media)
			media = LocalizeMediaAttachment(media)
		}
		replyAuthor := ""
		if msg.ReplyToID != 0 {
			replyAuthor = senderByID[msg.ReplyToID]
		}
		out = append(out, Message{
			ID:             fmt.Sprintf("%d", msg.ID),
			ChatID:         msg.PeerKey,
			Author:         msg.SenderName,
			AuthorColor:    msg.SenderColor,
			Text:           msg.Text,
			Outgoing:       msg.Outgoing,
			CreatedAt:      msg.Date,
			Media:          media,
			ForwardSource:  msg.ForwardSource,
			ReplyToID:      intString(msg.ReplyToID),
			ReplyToAuthor:  replyAuthor,
			State:          msg.State,
			Views:          msg.Views,
			Forwards:       msg.Forwards,
			Reactions:      ReactionsFromJSON(msg.ReactionsJSON),
			ViaBotUsername: msg.ViaBotUsername,
			ServiceKey:     msg.ServiceKey,
			ServiceArg:     msg.ServiceArg,
		})
	}
	return out
}

func (c *GotdClient) enrichReplyAuthorFromStore(ctx context.Context, accountID string, m *Message) {
	if c.store == nil || m == nil || m.ReplyToID == "" || m.ReplyToAuthor != "" {
		return
	}
	replyID, err := strconv.Atoi(m.ReplyToID)
	if err != nil || replyID <= 0 {
		return
	}
	name, ok, err := c.store.MessageSenderName(ctx, accountID, m.ChatID, replyID)
	if err == nil && ok && name != "" {
		m.ReplyToAuthor = name
	}
}

func (c *GotdClient) telegramMessages(ctx context.Context, accountID string, messages []storage.Message) []Message {
	out := toTelegramMessages(messages)
	for i := range out {
		out[i].Media = resolveAnimatedLocalPath(c.cfg.Paths.MediaDir, out[i].Media)
		c.enrichReplyAuthorFromStore(ctx, accountID, &out[i])
	}
	if len(messages) > 0 && c.store != nil {
		if peer, ok, err := c.store.Peer(ctx, accountID, messages[0].PeerKey); err == nil && ok {
			applyOutboxRead(out, peer)
			c.applyGroupReadCounts(out, peer)
		}
	}
	return out
}

func inputPeer(p storage.Peer) (tg.InputPeerClass, error) {
	switch p.Kind {
	case "self":
		return &tg.InputPeerSelf{}, nil
	case "user":
		return &tg.InputPeerUser{UserID: p.ID, AccessHash: p.AccessHash}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: p.ID}, nil
	case "channel":
		return &tg.InputPeerChannel{ChannelID: p.ID, AccessHash: p.AccessHash}, nil
	default:
		return nil, fmt.Errorf("unsupported peer kind %q", p.Kind)
	}
}

func peer(accountID, kind string, id, accessHash int64, title, username string) storage.Peer {
	return storage.Peer{
		AccountID:  accountID,
		Key:        peerKey(kind, id),
		Kind:       kind,
		ID:         id,
		AccessHash: accessHash,
		Title:      title,
		Username:   username,
	}
}

func peerKey(kind string, id int64) string {
	return fmt.Sprintf("%s:%d", kind, id)
}

func peerRefParts(ref tg.PeerClass) (string, int64) {
	switch p := ref.(type) {
	case *tg.PeerUser:
		return "user", p.UserID
	case *tg.PeerChat:
		return "chat", p.ChatID
	case *tg.PeerChannel:
		return "channel", p.ChannelID
	default:
		return "", 0
	}
}

func peerKindID(ref tg.PeerClass) (string, int64, bool) {
	kind, id := peerRefParts(ref)
	if kind == "" || id == 0 {
		return "", 0, false
	}
	return kind, id, true
}

func senderDisplayName(ref tg.PeerClass, entities entitiesByID) string {
	switch p := ref.(type) {
	case *tg.PeerUser:
		if u := entities.users[p.UserID]; u != nil {
			return userTitle(u)
		}
		return ""
	case *tg.PeerChat:
		if c := entities.chats[p.ChatID]; c != nil {
			return c.Title
		}
		return ""
	case *tg.PeerChannel:
		if c := entities.channels[p.ChannelID]; c != nil {
			return c.Title
		}
		return ""
	default:
		return ""
	}
}

func senderColor(kind string, id int64, fallback string) int {
	seed := uint32(2166136261)
	for _, r := range kind + fallback {
		seed ^= uint32(r)
		seed *= 16777619
	}
	seed ^= uint32(id)
	return int(seed % 12)
}

func forwardSource(header tg.MessageFwdHeader) string {
	if header.PostAuthor != "" {
		return header.PostAuthor
	}
	if header.FromName != "" {
		return header.FromName
	}
	return "forwarded"
}

func (c *GotdClient) enrichMediaPreview(ctx context.Context, api *tg.Client, media MediaAttachment) MediaAttachment {
	media, _ = c.enrichMediaPreviewAttempt(ctx, api, media)
	return media
}

func (c *GotdClient) ensureMediaPreview(ctx context.Context, api *tg.Client, media MediaAttachment) (string, error) {
	if err := os.MkdirAll(c.cfg.Paths.MediaDir, 0o700); err != nil {
		return "", err
	}
	if media.Kind == "gif" || media.Kind == "video_sticker" {
		if strings.HasPrefix(media.DownloadKey, "document:") {
			return c.ensureAnimatedDocument(ctx, api, media)
		}
	}
	thumb := media.ThumbSize
	location := tg.InputFileLocationClass(nil)
	previewName := safeFileName(media.DownloadKey)
	if strings.HasPrefix(media.DownloadKey, "photo:") {
		if thumb == "" {
			return "", nil
		}
		location = &tg.InputPhotoFileLocation{
			ID:            media.DocumentID,
			AccessHash:    media.AccessHash,
			FileReference: media.FileReference,
			ThumbSize:     thumb,
		}
		previewName += "-" + safeFileName(thumb) + ".jpg"
	} else {
		location = &tg.InputDocumentFileLocation{
			ID:            media.DocumentID,
			AccessHash:    media.AccessHash,
			FileReference: media.FileReference,
			ThumbSize:     thumb,
		}
		if thumb != "" {
			previewName += "-" + safeFileName(thumb) + ".jpg"
		} else if media.Kind == "sticker" || media.Kind == "animated_sticker" || strings.HasPrefix(media.MimeType, "image/") {
			previewName += mediaExtension(media)
		} else {
			return "", nil
		}
	}
	path := filepath.Join(c.cfg.Paths.MediaDir, previewName)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, nil
	}
	file, err := uploadGetFile(ctx, api, &tg.UploadGetFileRequest{
		Location: location,
		Offset:   0,
		Limit:    512 * 1024,
	})
	if err != nil {
		return "", err
	}
	uploaded, ok := file.(*tg.UploadFile)
	if !ok || len(uploaded.Bytes) == 0 {
		return "", nil
	}
	if err := os.WriteFile(path, uploaded.Bytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (c *GotdClient) ensureAnimatedDocument(ctx context.Context, api *tg.Client, media MediaAttachment) (string, error) {
	ext := animatedDocumentExt(media)
	name := safeFileName(media.DownloadKey) + ext
	path := filepath.Join(c.cfg.Paths.MediaDir, name)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		if media.Size <= 0 || info.Size() >= media.Size {
			return path, nil
		}
	}
	if c.mtproto == nil {
		return c.ensureDocumentThumb(ctx, api, media)
	}
	location := &tg.InputDocumentFileLocation{
		ID:            media.DocumentID,
		AccessHash:    media.AccessHash,
		FileReference: media.FileReference,
	}
	if _, err := c.mtproto.Download(location).ToPath(ctx, path); err != nil {
		return c.ensureDocumentThumb(ctx, api, media)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		_ = os.Remove(path)
		return c.ensureDocumentThumb(ctx, api, media)
	}
	return path, nil
}

func (c *GotdClient) ensureDocumentThumb(ctx context.Context, api *tg.Client, media MediaAttachment) (string, error) {
	if media.ThumbSize == "" {
		return "", nil
	}
	if err := os.MkdirAll(c.cfg.Paths.MediaDir, 0o700); err != nil {
		return "", err
	}
	previewName := safeFileName(media.DownloadKey) + "-" + safeFileName(media.ThumbSize) + ".jpg"
	path := filepath.Join(c.cfg.Paths.MediaDir, previewName)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, nil
	}
	location := &tg.InputDocumentFileLocation{
		ID:            media.DocumentID,
		AccessHash:    media.AccessHash,
		FileReference: media.FileReference,
		ThumbSize:     media.ThumbSize,
	}
	file, err := uploadGetFile(ctx, api, &tg.UploadGetFileRequest{
		Location: location,
		Offset:   0,
		Limit:    512 * 1024,
	})
	if err != nil {
		return "", err
	}
	uploaded, ok := file.(*tg.UploadFile)
	if !ok || len(uploaded.Bytes) == 0 {
		return "", nil
	}
	if err := os.WriteFile(path, uploaded.Bytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func animatedDocumentExt(media MediaAttachment) string {
	if media.Kind == "video_sticker" {
		return ".webm"
	}
	return mediaExtension(media)
}

func uploadGetFile(ctx context.Context, api *tg.Client, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	return retryFloodWait(ctx, defaultMaxFloodWaits, "downloading media", func(ctx context.Context) (tg.UploadFileClass, error) {
		return api.UploadGetFile(ctx, req)
	})
}

var (
	renderMediaPreview = func(path string) string {
		return termmedia.RasterPreviewANSIToTview(path, termmedia.RasterPreviewOptions{
			MaxCols: termmedia.PreviewMaxCols,
			MaxRows: termmedia.PreviewMaxRows,
		})
	}
	renderVideoStillPreview = termmedia.StillPreviewANSIToTview
)

func mediaFallbackText(media MediaAttachment) string {
	label := media.Label
	if label == "" {
		label = "[Media]"
	}
	if media.FileName != "" {
		label += " " + media.FileName
	}
	return label
}

func mediaExtension(media MediaAttachment) string {
	switch media.MimeType {
	case "image/webp":
		return ".webp"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "application/x-tgsticker":
		return ".tgs"
	default:
		if media.Kind == "gif" {
			return ".mp4"
		}
		return ".bin"
	}
}

func resolveAnimatedLocalPath(mediaDir string, media MediaAttachment) MediaAttachment {
	if media.Kind != "gif" && media.Kind != "video_sticker" {
		return media
	}
	if media.DownloadKey == "" || strings.HasPrefix(media.DownloadKey, "photo:") {
		return media
	}
	name := safeFileName(media.DownloadKey) + animatedDocumentExt(media)
	path := filepath.Join(mediaDir, name)
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		return media
	}
	media.LocalPath = path
	return media
}

func safeFileName(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "media"
	}
	return b.String()
}

func bestPhotoSize(sizes []tg.PhotoSizeClass) string {
	bestType := ""
	bestArea := 0
	for _, size := range sizes {
		switch s := size.(type) {
		case *tg.PhotoSize:
			area := s.W * s.H
			if area > bestArea {
				bestType = s.Type
				bestArea = area
			}
		case *tg.PhotoSizeProgressive:
			area := s.W * s.H
			if area > bestArea {
				bestType = s.Type
				bestArea = area
			}
		case *tg.PhotoCachedSize:
			area := s.W * s.H
			if area > bestArea {
				bestType = s.Type
				bestArea = area
			}
		}
	}
	return bestType
}

// peerPreview is the chat-list preview for a message. Key is empty when Text is
// user-authored message text, which is never translated; otherwise Key/Arg regenerate
// Text on a locale change so the list does not need reverse string lookup.
type peerPreview struct {
	Text string
	Key  string
	Arg  string
}

func (p peerPreview) applyTo(peer *storage.Peer) {
	peer.LastPreview = p.Text
	peer.LastPreviewKey = p.Key
	peer.LastPreviewArg = p.Arg
}

func messagePreview(msg *tg.Message) peerPreview {
	if msg == nil {
		return peerPreview{}
	}
	if msg.Message != "" {
		return peerPreview{Text: msg.Message}
	}
	if media := classifyMessageMedia(msg.Media); media.Label != "" {
		return peerPreview{Text: media.Label, Key: media.LabelKey, Arg: media.Alt}
	}
	return peerPreview{Text: i18n.T(i18n.KeyMessageEmpty), Key: i18n.KeyMessageEmpty}
}

func thumbCacheKey(msg *tg.Message) string {
	if media := classifyMessageMedia(msg.Media); media.DownloadKey != "" {
		return media.DownloadKey
	}
	return ""
}

func sortChats(chats []Chat) {
	sort.SliceStable(chats, func(i, j int) bool {
		if chats[i].Pinned != chats[j].Pinned {
			return chats[i].Pinned
		}
		if chats[i].Pinned && chats[j].Pinned && chats[i].PinnedOrder != chats[j].PinnedOrder {
			return chats[i].PinnedOrder < chats[j].PinnedOrder
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

// mergePeerActivity keeps dialog metadata when applying a message-driven peer update.
func mergePeerActivity(existing, activity storage.Peer) storage.Peer {
	out := activity
	if isPlaceholderPeerTitle(out.Title, out.ID) && !isPlaceholderPeerTitle(existing.Title, existing.ID) {
		out.Title = existing.Title
		out.Username = existing.Username
		out.Subtitle = existing.Subtitle
		if out.AccessHash == 0 {
			out.AccessHash = existing.AccessHash
		}
		out.Contact = existing.Contact
	}
	out.Pinned = existing.Pinned
	out.PinnedOrder = existing.PinnedOrder
	out.Unread = existing.Unread
	out.ReadOutboxMaxID = existing.ReadOutboxMaxID
	out.ReadInboxMaxID = existing.ReadInboxMaxID
	if out.FolderID == 0 {
		out.FolderID = existing.FolderID
		out.FolderTitle = existing.FolderTitle
	}
	if out.HistoryMinID == 0 {
		out.HistoryMinID = existing.HistoryMinID
	}
	if out.HistoryLoadedUntil.IsZero() {
		out.HistoryLoadedUntil = existing.HistoryLoadedUntil
	}
	return out
}

// mergeDialogPeer keeps local-only peer metadata when applying a dialog-sync update.
//
// It differs from mergePeerActivity in exactly one respect, and that difference matters:
// read state comes from the dialog. messages.getDialogs is the authoritative source for
// unread counts and read watermarks, whereas a message-driven update carries none and must
// preserve whatever is stored. The dialog sync loop used to call mergePeerActivity, which
// pinned the unread badge to whatever the very first sync happened to see — it then never
// refreshed, and once read state starts driving the unread jump it would freeze that too.
//
// A local mark-read can briefly disagree with a dialog sync that the server has not yet
// processed, which shows up as the badge bouncing back for one sync. The server's own
// UpdateReadHistoryInbox echo carries StillUnreadCount and corrects it, and trusting the
// dialog is still much better than never refreshing at all.
func mergeDialogPeer(existing, dialog storage.Peer) storage.Peer {
	out := mergePeerActivity(existing, dialog)
	out.Unread = dialog.Unread
	out.ReadOutboxMaxID = dialog.ReadOutboxMaxID
	// Never let a dialog sync walk the read pointer backwards. SavePeers guards this too, but
	// callers also compare the merged value against the stored one to decide whether to jump.
	if dialog.ReadInboxMaxID > existing.ReadInboxMaxID {
		out.ReadInboxMaxID = dialog.ReadInboxMaxID
	}
	return out
}

func isPlaceholderPeerTitle(title string, telegramID int64) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return true
	}
	if telegramID == 0 {
		return false
	}
	switch title {
	case fmt.Sprintf("user %d", telegramID),
		fmt.Sprintf("group %d", telegramID),
		fmt.Sprintf("channel %d", telegramID):
		return true
	default:
		return false
	}
}

func fallbackFolders(chats []Chat) []Folder {
	seen := map[int]bool{0: true}
	folders := []Folder{{ID: 0, Title: "All", Kind: "all"}}
	kinds := map[string]Folder{
		"bot":     {ID: -1, Title: "Bots", Kind: "bot"},
		"group":   {ID: -2, Title: "Groups", Kind: "group"},
		"channel": {ID: -3, Title: "Channels", Kind: "channel"},
	}
	for _, chat := range chats {
		if chat.FolderID > 0 && !seen[chat.FolderID] {
			seen[chat.FolderID] = true
			folders = append(folders, Folder{ID: chat.FolderID, Title: fmt.Sprintf("Folder %d", chat.FolderID), Kind: "telegram"})
		}
		kind := chat.Kind
		if chat.Subtitle == "bot" {
			kind = "bot"
		} else if chat.Subtitle == "group" {
			kind = "group"
		}
		if folder, ok := kinds[kind]; ok && !seen[folder.ID] {
			seen[folder.ID] = true
			folders = append(folders, folder)
		}
	}
	return folders
}

func peersToChats(peers []storage.Peer) []Chat {
	chats := make([]Chat, 0, len(peers))
	for _, p := range peers {
		chats = append(chats, Chat{
			ID:            p.Key,
			Title:         p.Title,
			Subtitle:      chatSubtitle(p),
			Kind:          p.Kind,
			Contact:       p.Contact,
			FolderID:      p.FolderID,
			Pinned:        p.Pinned,
			PinnedOrder:   p.PinnedOrder,
			Unread:        p.Unread,
			LastPreview:   p.LastPreview,
			PreviewKey:    p.LastPreviewKey,
			PreviewArg:    p.LastPreviewArg,
			LastMessageAt: p.LastMessageAt,
			TopMessageID:  p.TopMessageID,
		})
	}
	sortChats(chats)
	return chats
}

func storageFiltersToFolders(filters []storage.DialogFilter) []Folder {
	out := make([]Folder, 0, len(filters))
	for _, filter := range filters {
		out = append(out, storageFilterToFolder(filter))
	}
	return out
}

func storageFilterToFolder(filter storage.DialogFilter) Folder {
	return Folder{
		ID:      filter.ID,
		Title:   filter.Title,
		Kind:    filter.Kind,
		Archive: filter.Archive,
		Rules: FolderRules{
			Contacts:        filter.Contacts,
			NonContacts:     filter.NonContacts,
			Groups:          filter.Groups,
			Broadcasts:      filter.Broadcasts,
			Bots:            filter.Bots,
			ExcludeMuted:    filter.ExcludeMuted,
			ExcludeRead:     filter.ExcludeRead,
			ExcludeArchived: filter.ExcludeArchived,
			IncludePeers:    append([]string(nil), filter.IncludePeers...),
			ExcludePeers:    append([]string(nil), filter.ExcludePeers...),
			PinnedPeers:     append([]string(nil), filter.PinnedPeers...),
		},
	}
}

func withArchiveFolder(folders []Folder) []Folder {
	out := make([]Folder, 0, len(folders)+1)
	seenArchive := false
	for _, folder := range folders {
		out = append(out, folder)
		if folder.Archive {
			seenArchive = true
		}
	}
	if !seenArchive {
		insertAt := len(out)
		if len(out) > 0 && out[0].ID == 0 {
			insertAt = 1
		}
		archive := Folder{ID: ArchiveFolderID, Title: "Archived chats", Kind: "archive", Archive: true}
		out = append(out, Folder{})
		copy(out[insertAt+1:], out[insertAt:])
		out[insertAt] = archive
	}
	return out
}

func hasOfficialFolders(folders []Folder) bool {
	for _, folder := range folders {
		if folder.Kind == "telegram" {
			return true
		}
	}
	return false
}

func uniqueChats(chats []Chat) []Chat {
	out := make([]Chat, 0, len(chats))
	seen := make(map[string]bool, len(chats))
	for _, chat := range chats {
		if seen[chat.ID] {
			continue
		}
		seen[chat.ID] = true
		out = append(out, chat)
	}
	return out
}

func mergeFolders(primary, fallback []Folder) []Folder {
	seen := make(map[int]bool, len(primary)+len(fallback))
	out := make([]Folder, 0, len(primary)+len(fallback))
	for _, folder := range append(primary, fallback...) {
		if seen[folder.ID] {
			continue
		}
		seen[folder.ID] = true
		out = append(out, folder)
	}
	return out
}

func intString(value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

func intIDsToStrings(ids []int) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, fmt.Sprintf("%d", id))
	}
	return out
}

func accountIDFromSelf(self *tg.User) string {
	if self == nil {
		return "unknown"
	}
	if self.Bot {
		return fmt.Sprintf("bot:%d", self.ID)
	}
	return fmt.Sprintf("user:%d", self.ID)
}

func randomInt64() (int64, error) {
	n, err := crand.Int(crand.Reader, big.NewInt(1<<62))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}

func deviceConfig() telegram.DeviceConfig {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown-host"
	}
	return telegram.DeviceConfig{
		DeviceModel:    "Tsumugi (" + hostname + ")",
		SystemVersion:  runtime.GOOS + "/" + runtime.GOARCH,
		AppVersion:     version.String(),
		SystemLangCode: "en",
		LangCode:       "en",
	}
}

func (c *GotdClient) sessionPath() string {
	identity := c.cfg.Phone
	if c.cfg.AuthMode == config.AuthBot {
		identity = "bot"
	} else if c.cfg.LoginMethod == config.LoginQR && identity == "" {
		// Its own identity, so a QR session does not land on the phone-less user.json fallback
		// and get mistaken for a phone login's session.
		identity = "qr"
	}
	return filepath.Clean(c.cfg.SessionPath(identity))
}

func displaySelfName(self *tg.User) string {
	if self == nil {
		return "unknown"
	}
	if self.Username != "" {
		return "@" + self.Username
	}
	if self.FirstName != "" || self.LastName != "" {
		return stringsJoin(self.FirstName, self.LastName)
	}
	return fmt.Sprintf("user %d", self.ID)
}

func stringsJoin(parts ...string) string {
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if out != "" {
			out += " "
		}
		out += part
	}
	return out
}

func sendEvent(ctx context.Context, events chan<- Event, event Event) {
	if event.Kind == "" {
		event.Kind = EventStatus
	}
	if event.StatusMsg.IsZero() && event.Error != nil {
		event.StatusMsg = i18n.M(i18n.KeyStatusError, event.Error.Error())
	}

	select {
	case events <- event:
	case <-ctx.Done():
	}
}
