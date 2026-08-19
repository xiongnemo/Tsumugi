package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PrunablePeers returns peers holding messages older than cutoff, most-stale first.
//
// Bounded so a prune round is a fixed amount of work regardless of how many dialogs exist.
func (db *DB) PrunablePeers(ctx context.Context, accountID string, cutoff time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT peer_key, count(*) AS stale
		FROM messages
		WHERE account_id = ? AND date < ? AND state NOT IN ('pending', 'failed')
		GROUP BY peer_key
		ORDER BY stale DESC
		LIMIT ?
	`, accountID, formatTime(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var key string
		var stale int64
		if err := rows.Scan(&key, &stale); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

// PruneMessagesForPeer deletes messages older than cutoff for one peer, never dropping the newest
// keepNewest and never deleting more than limit rows in one call. It returns how many it removed.
//
// Two guards matter more than the deleting.
//
// keepNewest means an inactive chat can never be emptied: opening it offline still shows something,
// however old that chat is. Retention that can leave a conversation blank is worse than a large
// database.
//
// Local sends are excluded. A pending or failed row is the only copy of text the user typed and has
// not successfully sent, so age is not a reason to destroy it.
//
// Batched rather than one statement because the table can hold millions of rows: a single delete
// would take a long write lock and inflate the WAL by roughly the size of what it removed.
func (db *DB) PruneMessagesForPeer(ctx context.Context, accountID, peerKey string, cutoff time.Time, keepNewest, limit int) (int64, error) {
	if limit <= 0 || keepNewest < 0 {
		return 0, nil
	}
	bound := formatTime(cutoff)
	if keepNewest > 0 {
		// The date of the keepNewest-th newest message, so deleting strictly older than it keeps
		// exactly keepNewest. OFFSET keepNewest would name the one after that and keep one too
		// many.
		var floor string
		err := db.sql.QueryRowContext(ctx, `
			SELECT date FROM messages
			WHERE account_id = ? AND peer_key = ?
			ORDER BY date DESC
			LIMIT 1 OFFSET ?
		`, accountID, peerKey, keepNewest-1).Scan(&floor)
		if errors.Is(err, sql.ErrNoRows) {
			// Fewer than keepNewest messages stored, so the whole history is protected.
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		// Both bounds apply, so the effective one is whichever is earlier.
		if floor < bound {
			bound = floor
		}
	}

	res, err := db.sql.ExecContext(ctx, `
		DELETE FROM messages
		WHERE account_id = ? AND peer_key = ? AND message_id IN (
			SELECT message_id FROM messages
			WHERE account_id = ? AND peer_key = ? AND date < ?
				AND state NOT IN ('pending', 'failed')
			ORDER BY date ASC
			LIMIT ?
		)
	`, accountID, peerKey, accountID, peerKey, bound, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DatabaseFileBytes is the size of the database as SQLite sees it.
//
// Reported rather than acted upon: deleting rows leaves free pages that SQLite reuses for new
// inserts, so pruning stops the file growing but does not shrink it. Shrinking needs VACUUM, which
// rewrites the whole database and wants room for a second copy — not something to start behind a
// user's back on a multi-gigabyte file.
func (db *DB) DatabaseFileBytes(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := db.sql.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := db.sql.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}

// FormatBytes renders a byte count the way a person reads it.
//
// Lives next to DatabaseFileBytes rather than in the UI because both the backend (inside an
// i18n.Msg argument) and the settings panel show sizes, and the display layer imports telegram, so
// the shared helper cannot live there. Units are left untranslated on purpose: KB/MB/GB read the
// same in every locale Tsumugi ships.
//
// render.HumanSize is the same idea for message media and stays separate: it cannot be reached from
// the backend (render imports telegram, so the dependency only goes one way), and it drops the space
// and the decimal to fit a media caption, where this has a settings field to itself.
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"KB", "MB", "GB"} {
		value /= unit
		if value < unit || suffix == "GB" {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f GB", value)
}

// Compact rewrites the database to release free pages back to the filesystem.
//
// Explicitly invoked, never automatic: VACUUM rewrites everything, needs temporary room for a
// second copy, and blocks for as long as that takes — on a 3.7GB file that is a long freeze and a
// disk-space risk, which is not an acceptable surprise at startup.
func (db *DB) Compact(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `VACUUM`)
	return err
}
