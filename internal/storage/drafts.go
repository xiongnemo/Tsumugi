package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func draftRowID(accountID, peerKey string) string {
	return accountID + "/" + peerKey
}

// SaveLocalDraft records a draft the user is typing. It marks the row dirty so a later dialog
// sync cannot overwrite it with the server's older copy.
func (db *DB) SaveLocalDraft(ctx context.Context, draft Draft) error {
	if draft.Text == "" && draft.ReplyToID == 0 {
		return db.DeleteDraft(ctx, draft.AccountID, draft.PeerKey)
	}
	sealed, err := db.seal("drafts", draftRowID(draft.AccountID, draft.PeerKey), "text", []byte(draft.Text))
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx, `
		INSERT INTO drafts(account_id, peer_key, text_blob, reply_to_id, entities_json, server_date, dirty, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(account_id, peer_key) DO UPDATE SET
			text_blob = excluded.text_blob,
			reply_to_id = excluded.reply_to_id,
			entities_json = excluded.entities_json,
			dirty = 1,
			updated_at = excluded.updated_at
	`, draft.AccountID, draft.PeerKey, sealed, draft.ReplyToID, draft.EntitiesJSON, draft.ServerDate, formatTime(time.Now().UTC()))
	return err
}

// MarkDraftSynced clears the dirty flag once the server has accepted our copy.
func (db *DB) MarkDraftSynced(ctx context.Context, accountID, peerKey string, serverDate int) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE drafts SET dirty = 0, server_date = ?, updated_at = ?
		WHERE account_id = ? AND peer_key = ?
	`, serverDate, formatTime(time.Now().UTC()), accountID, peerKey)
	return err
}

// SaveServerDrafts applies drafts learned from Telegram.
//
// The WHERE clause on the upsert is the whole point: a draft the user is still typing (dirty)
// or one newer than the incoming copy must survive. Without it the repeating background dialog
// sweep would clobber in-progress text every time it came around.
func (db *DB) SaveServerDrafts(ctx context.Context, drafts []Draft) error {
	if len(drafts) == 0 {
		return nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := formatTime(time.Now().UTC())
	for _, draft := range drafts {
		if draft.Text == "" && draft.ReplyToID == 0 {
			// An empty server draft means "cleared elsewhere", but a local edit still wins.
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM drafts WHERE account_id = ? AND peer_key = ? AND dirty = 0
			`, draft.AccountID, draft.PeerKey); err != nil {
				return err
			}
			continue
		}
		sealed, err := db.seal("drafts", draftRowID(draft.AccountID, draft.PeerKey), "text", []byte(draft.Text))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drafts(account_id, peer_key, text_blob, reply_to_id, entities_json, server_date, dirty, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, 0, ?)
			ON CONFLICT(account_id, peer_key) DO UPDATE SET
				text_blob = excluded.text_blob,
				reply_to_id = excluded.reply_to_id,
				entities_json = excluded.entities_json,
				server_date = excluded.server_date,
				dirty = 0,
				updated_at = excluded.updated_at
			WHERE drafts.dirty = 0 AND excluded.server_date >= drafts.server_date
		`, draft.AccountID, draft.PeerKey, sealed, draft.ReplyToID, draft.EntitiesJSON, draft.ServerDate, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) Draft(ctx context.Context, accountID, peerKey string) (Draft, bool, error) {
	var (
		draft     Draft
		textBlob  []byte
		dirty     int
		updatedAt string
	)
	err := db.sql.QueryRowContext(ctx, `
		SELECT text_blob, reply_to_id, entities_json, server_date, dirty, updated_at
		FROM drafts WHERE account_id = ? AND peer_key = ?
	`, accountID, peerKey).Scan(&textBlob, &draft.ReplyToID, &draft.EntitiesJSON, &draft.ServerDate, &dirty, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	text, err := db.open("drafts", draftRowID(accountID, peerKey), "text", textBlob)
	if err != nil {
		return Draft{}, false, err
	}
	draft.AccountID = accountID
	draft.PeerKey = peerKey
	draft.Text = string(text)
	draft.Dirty = dirty != 0
	draft.UpdatedAt = parseTime(updatedAt)
	return draft, true, nil
}

func (db *DB) DeleteDraft(ctx context.Context, accountID, peerKey string) error {
	_, err := db.sql.ExecContext(ctx, `
		DELETE FROM drafts WHERE account_id = ? AND peer_key = ?
	`, accountID, peerKey)
	return err
}
