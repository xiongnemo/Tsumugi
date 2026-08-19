package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/debuglog"
	"github.com/nemo/Tsumugi/internal/storage"
)

// resolveInputPeer builds an input peer, following a basic group that has since become a
// supergroup.
//
// Telegram rejects the old chat:<id> form with PEER_ID_INVALID once a group has migrated, and it
// says nothing about why. Tsumugi renders the migration service message but never acted on it, so
// a stale chat row sat in storage and in the chat list indefinitely — every RPC against it failed.
//
// gotd models this explicitly as peers.Chat.MigratedTo, which is the long-term home for all of
// this: telegram/peers is a peer manager that owns access hashes, migrations and resolution. It is
// marked experimental, so this handles the one case that actually bites rather than adopting it
// mid-debug.
func (c *GotdClient) resolveInputPeer(ctx context.Context, api *tg.Client, p storage.Peer) (tg.InputPeerClass, error) {
	if p.Kind != "chat" || api == nil {
		return inputPeer(p)
	}
	migrated, ok := c.migratedChannel(ctx, api, p)
	if !ok {
		return inputPeer(p)
	}
	return migrated, nil
}

// migratedChannel asks Telegram whether a basic group has become a supergroup, and records the
// answer so the lookup is not repeated.
func (c *GotdClient) migratedChannel(ctx context.Context, api *tg.Client, p storage.Peer) (tg.InputPeerClass, bool) {
	res, err := api.MessagesGetChats(ctx, []int64{p.ID})
	if err != nil {
		debuglog.Error("migrated_chat_lookup", err, map[string]any{"peer_key": p.Key})
		return nil, false
	}
	for _, item := range res.GetChats() {
		chat, ok := item.(*tg.Chat)
		if !ok || chat.ID != p.ID {
			continue
		}
		raw, ok := chat.GetMigratedTo()
		if !ok {
			return nil, false
		}
		channel, ok := raw.(*tg.InputChannel)
		if !ok {
			return nil, false
		}
		debuglog.Log("chat_migrated", map[string]any{
			"from":       p.Key,
			"to_channel": channel.ChannelID,
		})
		// Persist it so the chat list and later sends stop using the dead id.
		if c.store != nil {
			replacement := peer(p.AccountID, "channel", channel.ChannelID, channel.AccessHash, p.Title, p.Username)
			replacement.Subtitle = "group"
			replacement.LastMessageAt = p.LastMessageAt
			_ = c.store.SavePeers(ctx, []storage.Peer{replacement})
		}
		return &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, true
	}
	return nil, false
}

// describeInputPeer renders an input peer for the debug log.
//
// The access hash is reported as present or absent rather than logged: it is a credential, and the
// log is a plain file in the working directory.
func describeInputPeer(p tg.InputPeerClass) map[string]any {
	switch v := p.(type) {
	case *tg.InputPeerSelf:
		return map[string]any{"type": "self"}
	case *tg.InputPeerUser:
		return map[string]any{"type": "user", "id": v.UserID, "has_hash": v.AccessHash != 0}
	case *tg.InputPeerChat:
		return map[string]any{"type": "chat", "id": v.ChatID}
	case *tg.InputPeerChannel:
		return map[string]any{"type": "channel", "id": v.ChannelID, "has_hash": v.AccessHash != 0}
	case nil:
		return map[string]any{"type": "nil"}
	default:
		return map[string]any{"type": fmt.Sprintf("%T", p)}
	}
}

// storageKeyForTarget maps a forward destination to the peer key its messages are stored under.
//
// SavedMessagesTarget is a sentinel: it resolves to InputPeerSelf for the RPC, which needs no
// stored peer. Storage and the UI are keyed by peer, though, so an echo into Saved Messages needs
// the real self:<id> key or it is never written down.
func (c *GotdClient) storageKeyForTarget(ctx context.Context, accountID, target string) string {
	if target != SavedMessagesTarget {
		return target
	}
	id := telegramIDFromAccountID(accountID)
	if id == 0 {
		return ""
	}
	if c.store != nil {
		if key, ok, err := c.store.PeerKeyForTelegramID(ctx, accountID, id, "self"); err == nil && ok {
			return key
		}
	}
	// Not synced yet; the key is deterministic, so the row can be created now and matched later.
	return peerKey("self", id)
}

// telegramIDFromAccountID pulls the numeric id out of an account id such as "user:123".
func telegramIDFromAccountID(accountID string) int64 {
	idx := strings.LastIndexByte(accountID, ':')
	if idx < 0 {
		return 0
	}
	id, err := strconv.ParseInt(accountID[idx+1:], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
