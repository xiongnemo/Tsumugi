package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
)

// archiveFolderID is Telegram's own archive folder. Only 0 and 1 exist, and 1 is the archive.
//
// Not to be confused with ArchiveFolderID (-10), which is Tsumugi's client-side folder id for showing
// the archive in the folder rail. Passing the wrong one gives FOLDER_ID_INVALID.
const archiveFolderID = 1

// pinDialog pins or unpins a chat in the list.
func (c *GotdClient) pinDialog(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	input, ok := c.inputPeerForCommand(ctx, accountID, events, command.PeerKey)
	if !ok {
		return
	}
	if _, err := retryFloodWait(ctx, defaultMaxFloodWaits, "pinning chat", func(ctx context.Context) (bool, error) {
		return api.MessagesToggleDialogPin(ctx, &tg.MessagesToggleDialogPinRequest{
			Peer:   &tg.InputDialogPeer{Peer: input},
			Pinned: !command.Unpin,
		})
	}); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: command.PeerKey, Error: rpcError("pin chat", err)})
		return
	}
	status := i18n.KeyStatusChatPinned
	if command.Unpin {
		status = i18n.KeyStatusChatUnpinned
	}
	c.afterChatOp(ctx, accountID, api, events, command.PeerKey, status)
}

// archiveDialog moves a chat into Telegram's archive folder, or back out of it.
func (c *GotdClient) archiveDialog(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	input, ok := c.inputPeerForCommand(ctx, accountID, events, command.PeerKey)
	if !ok {
		return
	}
	folder := archiveFolderID
	if command.Unpin {
		// Unpin doubles as "undo" for this command: the pair is archive/unarchive, and a second
		// boolean would be a second way to say the same thing.
		folder = 0
	}
	if _, err := retryFloodWait(ctx, defaultMaxFloodWaits, "archiving chat", func(ctx context.Context) (tg.UpdatesClass, error) {
		return api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{Peer: input, FolderID: folder}})
	}); err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: command.PeerKey, Error: rpcError("archive chat", err)})
		return
	}
	status := i18n.KeyStatusChatArchived
	if command.Unpin {
		status = i18n.KeyStatusChatUnarchived
	}
	c.afterChatOp(ctx, accountID, api, events, command.PeerKey, status)
}

// leaveChat leaves a group or channel.
//
// Two RPCs, because Telegram has two kinds of group: a supergroup or channel leaves with
// channels.leaveChannel, while a basic group is left by deleting yourself from it. Sending the wrong
// one is how the forward path first broke - a basic group that had been migrated still had a stale
// chat: row - so the peer goes through resolveInputPeer, which follows MigratedTo.
func (c *GotdClient) leaveChat(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, command.PeerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", command.PeerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := c.resolveInputPeer(ctx, api, p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}

	switch peer := input.(type) {
	case *tg.InputPeerChannel:
		_, err = retryFloodWait(ctx, defaultMaxFloodWaits, "leaving channel", func(ctx context.Context) (tg.UpdatesClass, error) {
			return api.ChannelsLeaveChannel(ctx, &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash})
		})
	case *tg.InputPeerChat:
		_, err = retryFloodWait(ctx, defaultMaxFloodWaits, "leaving group", func(ctx context.Context) (tg.UpdatesClass, error) {
			return api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
				ChatID: peer.ChatID,
				UserID: &tg.InputUserSelf{},
			})
		})
	default:
		// A private chat cannot be left; deleting the conversation is a different, destructive
		// action and is not offered here.
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusLeaveNotPossible)})
		return
	}
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: command.PeerKey, Error: rpcError("leave chat", err)})
		return
	}
	c.afterChatOp(ctx, accountID, api, events, command.PeerKey, i18n.KeyStatusChatLeft)
}

// inputPeerForCommand resolves a command's peer, reporting the failure itself.
func (c *GotdClient) inputPeerForCommand(ctx context.Context, accountID string, events chan<- Event, peerKey string) (tg.InputPeerClass, bool) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return nil, false
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return nil, false
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return nil, false
	}
	return input, true
}

// afterChatOp reports the result and refreshes the list from the server.
//
// A dialog resync rather than a local guess: pinned order, folder membership and whether the chat
// still exists are all server state, and writing what we asked for would show a list Telegram
// disagrees with the moment anything failed halfway.
func (c *GotdClient) afterChatOp(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey, statusKey string) {
	sendEvent(ctx, events, Event{Kind: EventStatus, PeerKey: peerKey, StatusMsg: i18n.M(statusKey)})
	go c.syncDialogMetadataOnly(ctx, accountID, api, events)
}

// LeavableChat reports whether leaving is meaningful for a peer key.
//
// Pure and exported so the UI can decide what to offer: a private chat has nothing to leave, and
// Saved Messages is stored as self:<id>.
func LeavableChat(peerKey string) bool {
	switch {
	case strings.HasPrefix(peerKey, "channel:"), strings.HasPrefix(peerKey, "chat:"):
		return true
	default:
		return false
	}
}
