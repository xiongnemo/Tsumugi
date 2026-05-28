package telegram

import (
	"context"
	"strconv"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/nemo/Tsumugi/internal/storage"
)

const (
	groupReadRecentLimit = 20
	groupReadMaxAge      = 7 * 24 * time.Hour
)

func peerSupportsGroupReadMarks(peer storage.Peer) bool {
	return peer.Kind == "chat" || (peer.Kind == "channel" && peer.Subtitle == "group")
}

func selectRecentOutgoingGroupReadMessages(messages []storage.Message, now time.Time, limit int) []storage.Message {
	if limit <= 0 {
		limit = groupReadRecentLimit
	}
	out := make([]storage.Message, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		msg := messages[i]
		if !msg.Outgoing || msg.State != "synced" || msg.ID <= 0 {
			continue
		}
		if !msg.Date.IsZero() && now.Sub(msg.Date) > groupReadMaxAge {
			continue
		}
		out = append(out, msg)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (c *GotdClient) hasGroupReadUnsupported(peerKey string) bool {
	c.groupReadMu.Lock()
	defer c.groupReadMu.Unlock()
	_, ok := c.groupReadUnsupported[peerKey]
	return ok
}

func (c *GotdClient) markGroupReadUnsupported(peerKey string) {
	c.groupReadMu.Lock()
	if c.groupReadUnsupported == nil {
		c.groupReadUnsupported = make(map[string]struct{})
	}
	c.groupReadUnsupported[peerKey] = struct{}{}
	c.groupReadMu.Unlock()
}

func (c *GotdClient) setGroupReadCount(peerKey string, messageID int, count int) bool {
	if peerKey == "" || messageID <= 0 || count <= 0 {
		return false
	}
	c.groupReadMu.Lock()
	defer c.groupReadMu.Unlock()
	if c.groupReadCounts == nil {
		c.groupReadCounts = make(map[string]map[int]int)
	}
	byID := c.groupReadCounts[peerKey]
	if byID == nil {
		byID = make(map[int]int)
		c.groupReadCounts[peerKey] = byID
	}
	if byID[messageID] == count {
		return false
	}
	byID[messageID] = count
	return true
}

func (c *GotdClient) applyGroupReadCounts(messages []Message, peer storage.Peer) {
	if !peerSupportsGroupReadMarks(peer) {
		return
	}
	c.groupReadMu.Lock()
	counts := c.groupReadCounts[peer.Key]
	if len(counts) == 0 {
		c.groupReadMu.Unlock()
		return
	}
	copyCounts := make(map[int]int, len(counts))
	for id, count := range counts {
		copyCounts[id] = count
	}
	c.groupReadMu.Unlock()
	for i := range messages {
		if !messages[i].Outgoing || messages[i].State != "synced" {
			continue
		}
		id, err := strconv.Atoi(messages[i].ID)
		if err != nil || id <= 0 {
			continue
		}
		messages[i].GroupReadCount = copyCounts[id]
	}
}

func groupReadUnsupportedError(err error) bool {
	return tgerr.Is(err, "CHAT_TOO_BIG", "PEER_ID_INVALID")
}

func groupReadSkippableError(err error) bool {
	return tgerr.Is(err, "MSG_TOO_OLD", "MSG_ID_INVALID") || groupReadUnsupportedError(err)
}

func (c *GotdClient) refreshGroupReadMarks(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peer storage.Peer, messages []storage.Message) {
	if api == nil || c.store == nil || len(messages) == 0 || !peerSupportsGroupReadMarks(peer) || c.hasGroupReadUnsupported(peer.Key) {
		return
	}
	input, err := inputPeer(peer)
	if err != nil {
		return
	}
	candidates := selectRecentOutgoingGroupReadMessages(messages, time.Now(), groupReadRecentLimit)
	if len(candidates) == 0 {
		return
	}
	patches := make([]storage.Message, 0, len(candidates))
	for _, msg := range candidates {
		if !c.isFocusedPeer(peer.Key) {
			return
		}
		participants, err := api.MessagesGetMessageReadParticipants(ctx, &tg.MessagesGetMessageReadParticipantsRequest{
			Peer:  input,
			MsgID: msg.ID,
		})
		if err != nil {
			if groupReadUnsupportedError(err) {
				c.markGroupReadUnsupported(peer.Key)
				return
			}
			if groupReadSkippableError(err) {
				continue
			}
			continue
		}
		if c.setGroupReadCount(peer.Key, msg.ID, len(participants)) {
			patches = append(patches, msg)
		}
	}
	if len(patches) == 0 || !c.isFocusedPeer(peer.Key) {
		return
	}
	c.sendFocusedEvent(ctx, events, peer.Key, Event{
		Kind:     EventMessages,
		PeerKey:  peer.Key,
		Messages: c.telegramMessages(ctx, accountID, patches),
		Patch:    true,
	})
}
