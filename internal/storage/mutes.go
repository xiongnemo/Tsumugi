package storage

import (
	"context"
	"time"
)

// Mute state lives in its own table rather than as a column on peers.
//
// SavePeers is a full-column upsert called from every update handler, and only a dialog sync carries
// notify settings - an incoming message does not. A peers column would therefore be blanked by the
// next message to arrive, and defending it would need a "value unknown" sentinel threaded through
// every peer constructor. The drafts table exists for exactly this reason; this is the same shape.

// MuteInherit marks a peer that carries no notification setting of its own.
//
// Telegram distinguishes "muted until 0" from "no value": the first is an explicit unmute, the second
// means the peer follows the default for its type. Collapsing the two into 0 is what made a client
// full of muted groups ring for all of them - only 19 of 773 dialogs here carry a per-chat mute,
// because the rest are muted by the group default.
const MuteInherit = -1

// NotifyScope is one of Telegram's three notification scopes. Every dialog belongs to exactly one,
// and inherits that scope's default unless it has a setting of its own.
type NotifyScope string

const (
	NotifyScopeUsers      NotifyScope = "users"
	NotifyScopeChats      NotifyScope = "chats"
	NotifyScopeBroadcasts NotifyScope = "broadcasts"
)

// PeerNotifyScope is the scope a stored peer belongs to.
//
// Telegram's "chats" scope covers basic groups and supergroups alike, while Tsumugi stores a
// supergroup as kind "channel" - so the subtitle is what separates a supergroup from a broadcast,
// the same way ChatIsBroadcast reads it.
func PeerNotifyScope(kind, subtitle string) NotifyScope {
	switch kind {
	case "user", "self":
		return NotifyScopeUsers
	case "chat":
		return NotifyScopeChats
	default:
		if subtitle == "channel" {
			return NotifyScopeBroadcasts
		}
		return NotifyScopeChats
	}
}

// SetNotifyDefault records the default mute deadline for a scope.
func (db *DB) SetNotifyDefault(ctx context.Context, accountID string, scope NotifyScope, muteUntil int) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO notify_defaults(account_id, scope, mute_until, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(account_id, scope) DO UPDATE SET
			mute_until = excluded.mute_until,
			updated_at = excluded.updated_at
	`, accountID, string(scope), muteUntil, formatTime(time.Now().UTC()))
	return err
}

// NotifyDefaults returns the per-scope defaults for an account.
func (db *DB) NotifyDefaults(ctx context.Context, accountID string) (map[NotifyScope]int, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT scope, mute_until FROM notify_defaults WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[NotifyScope]int, 3)
	for rows.Next() {
		var scope string
		var until int
		if err := rows.Scan(&scope, &until); err != nil {
			return nil, err
		}
		out[NotifyScope(scope)] = until
	}
	return out, rows.Err()
}

// SetPeerMute records a peer's mute deadline. Zero means explicitly not muted; MuteInherit means the
// peer has no setting of its own and follows its scope's default.
func (db *DB) SetPeerMute(ctx context.Context, accountID, peerKey string, muteUntil int) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO peer_mutes(account_id, peer_key, mute_until, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(account_id, peer_key) DO UPDATE SET
			mute_until = excluded.mute_until,
			updated_at = excluded.updated_at
	`, accountID, peerKey, muteUntil, formatTime(time.Now().UTC()))
	return err
}

// PeerMutes returns every stored mute deadline for an account, keyed by peer.
//
// Loaded whole rather than per peer: the chat list needs all of them at once to draw its markers, and
// there is one row per muted dialog - a few hundred at most, against the ~1.4MB the chat list itself
// costs to build.
func (db *DB) PeerMutes(ctx context.Context, accountID string) (map[string]int, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT peer_key, mute_until FROM peer_mutes WHERE account_id = ? AND mute_until != 0
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var key string
		var until int
		if err := rows.Scan(&key, &until); err != nil {
			return nil, err
		}
		out[key] = until
	}
	return out, rows.Err()
}

// PeerMuted reports whether one peer is muted right now, resolving an inherited setting against its
// scope default.
func (db *DB) PeerMuted(ctx context.Context, accountID, peerKey string) bool {
	var until int
	err := db.sql.QueryRowContext(ctx, `
		SELECT mute_until FROM peer_mutes WHERE account_id = ? AND peer_key = ?
	`, accountID, peerKey).Scan(&until)
	if err != nil {
		return false
	}
	return MuteActive(until, time.Now())
}

// ResolveMute picks the deadline that applies to a peer.
//
// explicit is the peer's own value and whether it had one at all; an absent row and MuteInherit mean
// the same thing, because a dialog written before inheritance was understood stored 0 for both.
func ResolveMute(explicit int, hasExplicit bool, scopeDefault int) int {
	if !hasExplicit || explicit == MuteInherit {
		return scopeDefault
	}
	return explicit
}

// MuteActive reports whether a mute deadline is still in force.
//
// Telegram uses a unix deadline, with a very distant one standing in for "forever": muting from a
// phone writes something like the year 2038, so this must not be read as "expired long ago" or as a
// literal date to show anywhere.
func MuteActive(muteUntil int, now time.Time) bool {
	if muteUntil <= 0 {
		return false
	}
	return int64(muteUntil) > now.Unix()
}
