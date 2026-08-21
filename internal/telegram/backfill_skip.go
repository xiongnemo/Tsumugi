package telegram

import (
	"errors"
	"sync"

	"github.com/gotd/td/tgerr"
)

// The backfill used to retry a peer it could not read every few seconds, forever. One session logged
// 951 CHANNEL_INVALID errors across 50 peers - the same fifty, over and over - which burned network
// and CPU on work that could not succeed and buried every other error in the log.
//
// This is the same shape as the animation decode failures: an error that cannot be retried into
// success has to be remembered, or the loop that produced it becomes a busy loop.

// terminalBackfillErrors are the RPC error types that no amount of retrying will fix.
//
// Deliberately a short list of specific types rather than "any 400": a flood wait, a timeout or a
// server-side hiccup must all stay retryable, and being wrong in that direction silently stops
// syncing a chat that was only briefly unavailable.
var terminalBackfillErrors = map[string]struct{}{
	"CHANNEL_INVALID":        {},
	"CHANNEL_PRIVATE":        {},
	"CHAT_ID_INVALID":        {},
	"PEER_ID_INVALID":        {},
	"USER_BANNED_IN_CHANNEL": {},
	"CHAT_FORBIDDEN":         {},
	"CHANNEL_BANNED":         {},
}

// terminalBackfillError reports whether an error means "never for this peer" rather than "not now".
func terminalBackfillError(err error) bool {
	if err == nil {
		return false
	}
	var rpc *tgerr.Error
	if !errors.As(err, &rpc) {
		return false
	}
	_, terminal := terminalBackfillErrors[rpc.Type]
	return terminal
}

// backfillSkips remembers peers the backfill must stop asking about.
//
// For the session only, not persisted: access to a channel can come back - rejoining one, or an
// entity arriving that carries a usable hash - and a decision cached on disk would outlive its
// reason. A restart is cheap and is the natural moment to try again.
type backfillSkips struct {
	mu   sync.Mutex
	keys map[string]string
}

func (s *backfillSkips) skip(peerKey, reason string) {
	if peerKey == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys == nil {
		s.keys = make(map[string]string)
	}
	s.keys[peerKey] = reason
}

func (s *backfillSkips) skipped(peerKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.keys[peerKey]
	return ok
}

func (s *backfillSkips) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}
