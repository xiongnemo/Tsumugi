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

// SetPeerMute records a peer's mute deadline. Zero means not muted.
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

// PeerMuted reports whether one peer is muted right now.
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
