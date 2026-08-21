package telegram

import (
	"context"
	"sync"
	"time"
)

// chatListRefreshInterval is the shortest gap between two chat-list rebuilds.
//
// Every arriving message used to rebuild the entire list: ListPeers plus peersToChats for every
// dialog on the account. That is 0.2ms and 24KB at a handful of peers, and 37ms and 11MB at five
// thousand — per message. A few busy groups were enough to spend most of a core turning the same
// 852 rows into garbage.
//
// 750ms matches what the UI already does: App.setChats skips the re-render if it ran less than
// 750ms ago, so anything faster than this was work whose result was thrown away before it was
// drawn.
const chatListRefreshInterval = 750 * time.Millisecond

// chatListRefresher coalesces chat-list rebuilds: the first arrival refreshes immediately, the rest
// of the window is covered by one trailing refresh.
type chatListRefresher struct {
	mu    sync.Mutex
	last  time.Time
	timer *time.Timer
}

// scheduleChatListRefresh emits the chat list, at most once per window.
//
// Leading plus trailing rather than a plain throttle: the leading edge keeps the list responsive
// when a message arrives into an idle session, and the trailing edge is what guarantees the *last*
// message of a burst is reflected. Dropping the trailing refresh would leave the sidebar showing a
// stale preview until the next unrelated update.
func (c *GotdClient) scheduleChatListRefresh(ctx context.Context, accountID string, events chan<- Event) {
	if c.store == nil {
		return
	}
	r := &c.chatRefresh
	r.mu.Lock()
	if time.Since(r.last) >= chatListRefreshInterval {
		r.last = time.Now()
		r.mu.Unlock()
		c.emitChatList(ctx, accountID, events)
		return
	}
	if r.timer != nil {
		// A trailing refresh is already pending and will pick this arrival up too.
		r.mu.Unlock()
		return
	}
	delay := chatListRefreshInterval - time.Since(r.last)
	r.timer = time.AfterFunc(delay, func() {
		r.mu.Lock()
		r.timer = nil
		r.last = time.Now()
		r.mu.Unlock()
		// A cancelled context makes this a no-op rather than an error: sendEvent and the query
		// both honour it, so a shutdown mid-window costs nothing.
		c.emitChatList(ctx, accountID, events)
	})
	r.mu.Unlock()
}

// emitChatList rebuilds the chat list from storage and sends it.
func (c *GotdClient) emitChatList(ctx context.Context, accountID string, events chan<- Event) {
	if c.store == nil {
		return
	}
	peers, err := c.store.ListPeers(ctx, accountID)
	if err != nil {
		// Deliberately silent: this runs on every incoming message, and a transient query error
		// here is not worth a red status line the user cannot act on. The next message retries.
		return
	}
	sendEvent(ctx, events, Event{Kind: EventChats, Chats: c.applyMutes(ctx, accountID, peersToChats(peers))})
}
