package telegram

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	termmedia "github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/storage"
)

const composeSuggestLimit = 5

type mentionCacheEntry struct {
	expires time.Time
	items   []MentionSuggestion
	hasMore bool
}

type commandCacheEntry struct {
	expires time.Time
	items   []BotCommandSuggestion
}

type inlineCacheEntry struct {
	expires     time.Time
	items       []InlineResultSuggestion
	nextOffset  string
	hasMore     bool
	placeholder string
}

func (c *GotdClient) searchMentions(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	key := cmd.PeerKey + "\x00" + strings.ToLower(cmd.Query)
	c.suggestMu.Lock()
	if cached, ok := c.mentionCache[key]; ok && time.Now().Before(cached.expires) {
		c.suggestMu.Unlock()
		sendEvent(ctx, events, Event{Kind: EventMentionSuggestions, PeerKey: cmd.PeerKey, RequestID: cmd.RequestID, Query: cmd.Query, Mentions: cached.items, HasMore: cached.hasMore})
		return
	}
	c.suggestMu.Unlock()

	items, hasMore, err := c.loadMentionSuggestions(ctx, accountID, api, cmd.PeerKey, cmd.Query)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: fmt.Errorf("load mention suggestions: %w", err)})
	}
	c.suggestMu.Lock()
	c.mentionCache[key] = mentionCacheEntry{expires: time.Now().Add(20 * time.Second), items: items, hasMore: hasMore}
	c.suggestMu.Unlock()
	sendEvent(ctx, events, Event{Kind: EventMentionSuggestions, PeerKey: cmd.PeerKey, RequestID: cmd.RequestID, Query: cmd.Query, Mentions: items, HasMore: hasMore})
}

func (c *GotdClient) loadMentionSuggestions(ctx context.Context, accountID string, api *tg.Client, peerKey, query string) ([]MentionSuggestion, bool, error) {
	if c.store == nil {
		return nil, false, fmt.Errorf("storage is unavailable")
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		return nil, false, err
	}
	var out []MentionSuggestion
	var firstErr error
	switch p.Kind {
	case "channel":
		out, _, firstErr = c.channelMentionSuggestions(ctx, api, p, query, &tg.ChannelParticipantsMentions{})
		if len(out) < composeSuggestLimit {
			fallback, _, err := c.channelMentionSuggestions(ctx, api, p, query, &tg.ChannelParticipantsSearch{Q: query})
			if err == nil {
				out = mergeMentionSuggestions(out, fallback)
			} else if firstErr == nil {
				firstErr = err
			}
		}
	case "chat":
		out, firstErr = c.basicGroupMentionSuggestions(ctx, api, p, query)
	}
	if len(out) < composeSuggestLimit && strings.TrimSpace(query) != "" {
		global, err := c.globalMentionSuggestions(ctx, api, query)
		if err == nil {
			out = mergeMentionSuggestions(out, global)
		} else if firstErr == nil {
			firstErr = err
		}
	}
	sortMentionSuggestions(out)
	hasMore := len(out) > composeSuggestLimit
	if len(out) > composeSuggestLimit {
		out = out[:composeSuggestLimit]
	}
	return out, hasMore, firstErr
}

func (c *GotdClient) channelMentionSuggestions(ctx context.Context, api *tg.Client, p storage.Peer, query string, filter tg.ChannelParticipantsFilterClass) ([]MentionSuggestion, bool, error) {
	if f, ok := filter.(*tg.ChannelParticipantsMentions); ok {
		f.SetQ(query)
	}
	res, err := api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
		Channel: &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash},
		Filter:  filter,
		Limit:   composeSuggestLimit + 1,
	})
	if err != nil {
		return nil, false, err
	}
	modified, ok := res.AsModified()
	if !ok {
		return nil, false, nil
	}
	items := mentionSuggestionsFromUsers(modified.Users, "chat")
	return items, len(items) > composeSuggestLimit || modified.Count > composeSuggestLimit, nil
}

func (c *GotdClient) basicGroupMentionSuggestions(ctx context.Context, api *tg.Client, p storage.Peer, query string) ([]MentionSuggestion, error) {
	full, err := api.MessagesGetFullChat(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	users := usersByID(full.Users)
	allowed := map[int64]bool{}
	if chatFull, ok := full.FullChat.(*tg.ChatFull); ok {
		if participants, ok := chatFull.Participants.(*tg.ChatParticipants); ok {
			for _, participant := range participants.Participants {
				if id := chatParticipantUserID(participant); id != 0 {
					allowed[id] = true
				}
			}
		}
	}
	var out []MentionSuggestion
	for id, user := range users {
		if len(allowed) > 0 && !allowed[id] {
			continue
		}
		if mentionMatches(user, query) {
			out = append(out, mentionSuggestionFromUser(user, "chat"))
		}
	}
	return out, nil
}

func (c *GotdClient) globalMentionSuggestions(ctx context.Context, api *tg.Client, query string) ([]MentionSuggestion, error) {
	found, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: query, Limit: composeSuggestLimit + 1})
	if err != nil {
		return nil, err
	}
	return mentionSuggestionsFromUsers(found.Users, "global"), nil
}

func usersByID(items []tg.UserClass) map[int64]*tg.User {
	out := make(map[int64]*tg.User, len(items))
	for _, item := range items {
		if user, ok := item.(*tg.User); ok {
			out[user.ID] = user
		}
	}
	return out
}

func chatParticipantUserID(participant tg.ChatParticipantClass) int64 {
	switch p := participant.(type) {
	case *tg.ChatParticipant:
		return p.UserID
	case *tg.ChatParticipantAdmin:
		return p.UserID
	case *tg.ChatParticipantCreator:
		return p.UserID
	default:
		return 0
	}
}

func mentionSuggestionsFromUsers(users []tg.UserClass, source string) []MentionSuggestion {
	out := make([]MentionSuggestion, 0, len(users))
	for _, item := range users {
		user, ok := item.(*tg.User)
		if !ok || user.Self || user.Deleted {
			continue
		}
		out = append(out, mentionSuggestionFromUser(user, source))
	}
	return out
}

func mentionSuggestionFromUser(user *tg.User, source string) MentionSuggestion {
	accessHash, _ := user.GetAccessHash()
	username, _ := user.GetUsername()
	return MentionSuggestion{
		UserID:     user.ID,
		AccessHash: accessHash,
		Name:       userTitle(user),
		Username:   username,
		IsBot:      user.Bot,
		Source:     source,
	}
}

func mentionMatches(user *tg.User, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	username, _ := user.GetUsername()
	value := strings.ToLower(userTitle(user) + " " + username)
	return strings.Contains(value, query)
}

func mergeMentionSuggestions(primary, extra []MentionSuggestion) []MentionSuggestion {
	seen := make(map[int64]bool, len(primary)+len(extra))
	out := make([]MentionSuggestion, 0, len(primary)+len(extra))
	for _, item := range append(primary, extra...) {
		if item.UserID == 0 || seen[item.UserID] {
			continue
		}
		seen[item.UserID] = true
		out = append(out, item)
	}
	return out
}

func sortMentionSuggestions(items []MentionSuggestion) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Username != "" && items[j].Username == "" {
			return true
		}
		if items[i].Username == "" && items[j].Username != "" {
			return false
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
}

func (c *GotdClient) loadBotCommands(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	c.suggestMu.Lock()
	if cached, ok := c.commandCache[cmd.PeerKey]; ok && time.Now().Before(cached.expires) {
		c.suggestMu.Unlock()
		sendEvent(ctx, events, Event{Kind: EventBotCommandSuggestions, PeerKey: cmd.PeerKey, RequestID: cmd.RequestID, Query: cmd.Query, BotCommands: filterBotCommands(cached.items, cmd.Query)})
		return
	}
	c.suggestMu.Unlock()

	items, err := c.loadBotCommandSuggestions(ctx, accountID, api, cmd.PeerKey)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: fmt.Errorf("load bot commands: %w", err)})
	}
	c.suggestMu.Lock()
	c.commandCache[cmd.PeerKey] = commandCacheEntry{expires: time.Now().Add(5 * time.Minute), items: items}
	c.suggestMu.Unlock()
	sendEvent(ctx, events, Event{Kind: EventBotCommandSuggestions, PeerKey: cmd.PeerKey, RequestID: cmd.RequestID, Query: cmd.Query, BotCommands: filterBotCommands(items, cmd.Query)})
}

func (c *GotdClient) loadBotCommandSuggestions(ctx context.Context, accountID string, api *tg.Client, peerKey string) ([]BotCommandSuggestion, error) {
	if c.store == nil {
		return nil, fmt.Errorf("storage is unavailable")
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		return nil, err
	}
	var infos []tg.BotInfo
	usernames := map[int64]string{}
	forceBotSuffix := false
	switch p.Kind {
	case "user":
		full, err := api.UsersGetFullUser(ctx, &tg.InputUser{UserID: p.ID, AccessHash: p.AccessHash})
		if err != nil {
			return nil, err
		}
		for _, item := range full.Users {
			if user, ok := item.(*tg.User); ok {
				username, _ := user.GetUsername()
				usernames[user.ID] = username
			}
		}
		if info, ok := full.FullUser.GetBotInfo(); ok {
			infos = append(infos, info)
			if usernames[info.UserID] == "" {
				usernames[info.UserID] = p.Username
			}
		}
	case "chat":
		forceBotSuffix = true
		full, err := api.MessagesGetFullChat(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		usernames = usernamesFromUsers(full.Users)
		if chatFull, ok := full.FullChat.(*tg.ChatFull); ok {
			if botInfo, ok := chatFull.GetBotInfo(); ok {
				infos = append(infos, botInfo...)
			}
		}
	case "channel":
		forceBotSuffix = true
		full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash})
		if err != nil {
			return nil, err
		}
		usernames = usernamesFromUsers(full.Users)
		if channelFull, ok := full.FullChat.(*tg.ChannelFull); ok {
			infos = append(infos, channelFull.BotInfo...)
		}
	}
	return botCommandsFromInfo(infos, usernames, forceBotSuffix), nil
}

func usernamesFromUsers(users []tg.UserClass) map[int64]string {
	out := make(map[int64]string, len(users))
	for _, item := range users {
		if user, ok := item.(*tg.User); ok {
			username, _ := user.GetUsername()
			out[user.ID] = username
		}
	}
	return out
}

func botCommandsFromInfo(infos []tg.BotInfo, usernames map[int64]string, forceBotSuffix bool) []BotCommandSuggestion {
	var out []BotCommandSuggestion
	counts := map[string]int{}
	for _, info := range infos {
		commands, ok := info.GetCommands()
		if !ok {
			continue
		}
		botUsername := usernames[info.UserID]
		for _, command := range commands {
			if command.Command == "" {
				continue
			}
			item := BotCommandSuggestion{
				Command:     command.Command,
				Description: command.Description,
				BotUsername: botUsername,
			}
			out = append(out, item)
			counts[item.Command]++
		}
	}
	for i := range out {
		out[i].NeedsBotSuffix = out[i].BotUsername != "" && (forceBotSuffix || counts[out[i].Command] > 1)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Command < out[j].Command
	})
	return out
}

func filterBotCommands(items []BotCommandSuggestion, query string) []BotCommandSuggestion {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]BotCommandSuggestion, 0, composeSuggestLimit)
	for _, item := range items {
		if query != "" && !strings.HasPrefix(strings.ToLower(item.Command), query) {
			continue
		}
		out = append(out, item)
		if len(out) >= composeSuggestLimit {
			break
		}
	}
	return out
}

func (c *GotdClient) queryInlineBot(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	key := strings.ToLower(cmd.BotUsername) + "\x00" + cmd.PeerKey + "\x00" + cmd.Query + "\x00" + cmd.Offset
	c.suggestMu.Lock()
	if cached, ok := c.inlineCache[key]; ok && time.Now().Before(cached.expires) {
		c.suggestMu.Unlock()
		sendEvent(ctx, events, Event{Kind: EventInlineResultSuggestions, PeerKey: cmd.PeerKey, RequestID: cmd.RequestID, Query: cmd.Query, BotUsername: cmd.BotUsername, InlineResults: cached.items, NextOffset: cached.nextOffset, HasMore: cached.hasMore, Placeholder: cached.placeholder})
		return
	}
	c.suggestMu.Unlock()

	items, nextOffset, hasMore, placeholder, cacheFor, err := c.loadInlineResults(ctx, accountID, api, cmd.PeerKey, cmd.BotUsername, cmd.Query, cmd.Offset)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: fmt.Errorf("load inline results: %w", err)})
	}
	c.suggestMu.Lock()
	c.inlineCache[key] = inlineCacheEntry{expires: time.Now().Add(cacheFor), items: items, nextOffset: nextOffset, hasMore: hasMore, placeholder: placeholder}
	c.suggestMu.Unlock()
	sendEvent(ctx, events, Event{Kind: EventInlineResultSuggestions, PeerKey: cmd.PeerKey, RequestID: cmd.RequestID, Query: cmd.Query, BotUsername: cmd.BotUsername, InlineResults: items, NextOffset: nextOffset, HasMore: hasMore, Placeholder: placeholder})
}

func (c *GotdClient) loadInlineResults(ctx context.Context, accountID string, api *tg.Client, peerKey, botUsername, query, offset string) ([]InlineResultSuggestion, string, bool, string, time.Duration, error) {
	if c.store == nil {
		return nil, "", false, "", 30 * time.Second, fmt.Errorf("storage is unavailable")
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		return nil, "", false, "", 30 * time.Second, err
	}
	peer, err := inputPeer(p)
	if err != nil {
		return nil, "", false, "", 30 * time.Second, err
	}
	bot, placeholder, err := c.resolveInlineBot(ctx, api, botUsername)
	if err != nil {
		return nil, "", false, "", 30 * time.Second, err
	}
	results, err := api.MessagesGetInlineBotResults(ctx, &tg.MessagesGetInlineBotResultsRequest{
		Bot:    bot,
		Peer:   peer,
		Query:  query,
		Offset: offset,
	})
	if err != nil {
		return nil, "", false, placeholder, 30 * time.Second, err
	}
	nextOffset, hasNext := results.GetNextOffset()
	items := inlineResultSuggestions(results.QueryID, results.Results)
	hasMore := hasNext && nextOffset != ""
	if len(items) > composeSuggestLimit {
		hasMore = true
		items = items[:composeSuggestLimit]
	}
	cacheFor := time.Duration(results.CacheTime) * time.Second
	if cacheFor <= 0 {
		cacheFor = 30 * time.Second
	}
	return items, nextOffset, hasMore, placeholder, cacheFor, nil
}

func (c *GotdClient) resolveInlineBot(ctx context.Context, api *tg.Client, username string) (tg.InputUserClass, string, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return nil, "", fmt.Errorf("inline bot username is empty")
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, "", err
	}
	for _, item := range resolved.Users {
		user, ok := item.(*tg.User)
		if !ok {
			continue
		}
		foundUsername, _ := user.GetUsername()
		if strings.EqualFold(foundUsername, username) {
			if user.BotInlineGeo {
				return nil, "", fmt.Errorf("@%s requires location for inline mode, unsupported in this MVP", username)
			}
			placeholder, _ := user.GetBotInlinePlaceholder()
			accessHash, _ := user.GetAccessHash()
			return &tg.InputUser{UserID: user.ID, AccessHash: accessHash}, placeholder, nil
		}
	}
	return nil, "", fmt.Errorf("inline bot @%s not found", username)
}

func inlineResultSuggestions(queryID int64, results []tg.BotInlineResultClass) []InlineResultSuggestion {
	out := make([]InlineResultSuggestion, 0, len(results))
	for _, result := range results {
		switch item := result.(type) {
		case *tg.BotInlineResult:
			title, _ := item.GetTitle()
			description, _ := item.GetDescription()
			out = append(out, InlineResultSuggestion{ID: item.ID, QueryID: queryID, Title: title, Description: description, Type: item.Type})
		case *tg.BotInlineMediaResult:
			title, _ := item.GetTitle()
			description, _ := item.GetDescription()
			suggestion := InlineResultSuggestion{ID: item.ID, QueryID: queryID, Title: title, Description: description, Type: item.Type}
			if doc, ok := item.GetDocument(); ok {
				applyInlineResultDocument(&suggestion, doc)
			}
			out = append(out, suggestion)
		}
	}
	return out
}

// applyInlineResultDocument copies the size/dimension metadata a GIF or video inline result
// carries. These bots usually omit Title and Description, so this is the only thing that
// distinguishes one row from the next.
func applyInlineResultDocument(out *InlineResultSuggestion, doc tg.DocumentClass) {
	d, ok := doc.(*tg.Document)
	if !ok {
		return
	}
	out.Size = d.Size
	out.MimeType = d.MimeType
	// Thumb-only attachment: the grid renders from the JPEG thumbnail, never the full
	// animation, so a panel of results costs a handful of small downloads.
	if thumb := bestPhotoSize(d.Thumbs); thumb != "" {
		out.Thumb = MediaAttachment{
			Kind:          "photo",
			DownloadKey:   fmt.Sprintf("document:%d", d.ID),
			DocumentID:    d.ID,
			AccessHash:    d.AccessHash,
			FileReference: append([]byte(nil), d.FileReference...),
			ThumbSize:     thumb,
		}
	}
	for _, attr := range d.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeVideo:
			out.Width = a.W
			out.Height = a.H
			out.Duration = int(a.Duration)
		case *tg.DocumentAttributeImageSize:
			if out.Width == 0 && out.Height == 0 {
				out.Width = a.W
				out.Height = a.H
			}
		}
	}
}

// InlineThumbCols/InlineThumbRows are the cell size of one inline-result thumbnail. The
// backend renders at this size and the grid lays out cells to match, so both sides agree
// without the UI having to send dimensions with every request.
const (
	InlineThumbCols = 16
	InlineThumbRows = 6
)

// fetchInlineThumb downloads and renders one inline result's thumbnail. Only cells the grid
// actually shows are requested, and stale replies are dropped by RequestID on the UI side.
func (c *GotdClient) fetchInlineThumb(ctx context.Context, api *tg.Client, events chan<- Event, cmd Command) {
	if api == nil || cmd.Media.DocumentID == 0 || cmd.Media.ThumbSize == "" {
		return
	}
	path, err := c.ensureDocumentThumb(ctx, api, cmd.Media)
	if err != nil || path == "" {
		return
	}
	preview := termmedia.RenderTerminalPreview(path, InlineThumbCols, InlineThumbRows)
	if preview == "" {
		return
	}
	sendEvent(ctx, events, Event{
		Kind:         EventInlineResultThumb,
		RequestID:    cmd.RequestID,
		ResultID:     cmd.ResultID,
		ThumbPreview: preview,
	})
}

func (c *GotdClient) sendInlineResult(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, cmd Command) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, cmd.PeerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", cmd.PeerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	peer, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	randomID, err := randomInt64()
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	req := &tg.MessagesSendInlineBotResultRequest{
		Peer:     peer,
		RandomID: randomID,
		QueryID:  cmd.QueryID,
		ID:       cmd.ResultID,
	}
	if cmd.ReplyToID != 0 {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: cmd.ReplyToID})
	}
	updates, err := api.MessagesSendInlineBotResult(ctx, req)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: cmd.PeerKey, Error: fmt.Errorf("send inline result: %w", err)})
		return
	}
	serverMessages := c.messagesFromSendUpdates(ctx, api, accountID, cmd.PeerKey, "", cmd.ReplyToID, updates)
	for i := range serverMessages {
		if serverMessages[i].ViaBotUsername == "" && cmd.BotUsername != "" {
			serverMessages[i].ViaBotUsername = cmd.BotUsername
		}
	}
	if len(serverMessages) > 0 {
		_ = c.store.SaveMessages(ctx, serverMessages)
		sendEvent(ctx, events, Event{
			Kind:      EventMessages,
			PeerKey:   cmd.PeerKey,
			Messages:  c.telegramMessages(ctx, accountID, serverMessages),
			Append:    true,
			StatusMsg: i18n.M(i18n.KeyStatusMessageSent),
		})
		return
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: cmd.PeerKey, StatusMsg: i18n.M(i18n.KeyStatusMessageSubmitted)})
}

func buildMentionNameEntities(items []MessageEntityMentionName) []tg.MessageEntityClass {
	out := make([]tg.MessageEntityClass, 0, len(items))
	for _, item := range items {
		if item.Length <= 0 || item.UserID == 0 {
			continue
		}
		out = append(out, &tg.InputMessageEntityMentionName{
			Offset: item.Offset,
			Length: item.Length,
			UserID: &tg.InputUser{UserID: item.UserID, AccessHash: item.AccessHash},
		})
	}
	return out
}
