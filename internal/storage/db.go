package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nemo/Tsumugi/internal/network"
	"github.com/nemo/Tsumugi/internal/secure"
)

type DB struct {
	sql    *sql.DB
	cipher *secure.Cipher
}

func Open(ctx context.Context, dataDir string) (*DB, []byte, error) {
	path := filepath.Join(dataDir, "tsumugi.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, nil, err
	}
	raw.SetMaxOpenConns(1)

	db := &DB{sql: raw}
	if err := db.migrate(ctx); err != nil {
		_ = raw.Close()
		return nil, nil, err
	}
	salt, err := db.salt(ctx)
	if err != nil {
		_ = raw.Close()
		return nil, nil, err
	}
	return db, salt, nil
}

func (db *DB) Close() error {
	if db == nil || db.sql == nil {
		return nil
	}
	return db.sql.Close()
}

func (db *DB) SetCipher(cipher *secure.Cipher) {
	db.cipher = cipher
}

func (db *DB) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value BLOB NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS accounts (
			id TEXT PRIMARY KEY,
			mode TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS peers (
			account_id TEXT NOT NULL,
			key TEXT NOT NULL,
			kind TEXT NOT NULL,
			telegram_id INTEGER NOT NULL,
			access_hash INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			subtitle TEXT NOT NULL DEFAULT '',
			contact INTEGER NOT NULL DEFAULT 0,
			last_preview TEXT NOT NULL DEFAULT '',
			last_message_at TEXT NOT NULL DEFAULT '',
			top_message_id INTEGER NOT NULL DEFAULT 0,
			folder_id INTEGER NOT NULL DEFAULT 0,
			folder_title TEXT NOT NULL DEFAULT '',
			pinned INTEGER NOT NULL DEFAULT 0,
			pinned_order INTEGER NOT NULL DEFAULT 0,
			unread INTEGER NOT NULL DEFAULT 0,
			history_min_id INTEGER NOT NULL DEFAULT 0,
			history_loaded_until TEXT NOT NULL DEFAULT '',
			thumb_cache_key TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(account_id, key)
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			account_id TEXT NOT NULL,
			peer_key TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			date TEXT NOT NULL,
			sender TEXT NOT NULL DEFAULT '',
			sender_kind TEXT NOT NULL DEFAULT '',
			sender_id INTEGER NOT NULL DEFAULT 0,
			sender_name TEXT NOT NULL DEFAULT '',
			sender_color INTEGER NOT NULL DEFAULT 0,
			outgoing INTEGER NOT NULL DEFAULT 0,
			text_blob BLOB,
			media_blob BLOB,
			media_kind TEXT NOT NULL DEFAULT '',
			forward_source TEXT NOT NULL DEFAULT '',
			reply_to_id INTEGER NOT NULL DEFAULT 0,
			state TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(account_id, peer_key, message_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_peer_date ON messages(account_id, peer_key, date);`,
		`CREATE TABLE IF NOT EXISTS dialog_filters (
			account_id TEXT NOT NULL,
			id INTEGER NOT NULL,
			title TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT '',
			archive INTEGER NOT NULL DEFAULT 0,
			contacts INTEGER NOT NULL DEFAULT 0,
			non_contacts INTEGER NOT NULL DEFAULT 0,
			groups INTEGER NOT NULL DEFAULT 0,
			broadcasts INTEGER NOT NULL DEFAULT 0,
			bots INTEGER NOT NULL DEFAULT 0,
			exclude_muted INTEGER NOT NULL DEFAULT 0,
			exclude_read INTEGER NOT NULL DEFAULT 0,
			exclude_archived INTEGER NOT NULL DEFAULT 0,
			include_peers TEXT NOT NULL DEFAULT '[]',
			exclude_peers TEXT NOT NULL DEFAULT '[]',
			pinned_peers TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(account_id, id)
		);`,
		`CREATE TABLE IF NOT EXISTS sync_state (
			account_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(account_id, key)
		);`,
		`CREATE TABLE IF NOT EXISTS proxy_profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			source TEXT NOT NULL,
			address TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			password_blob BLOB,
			secret_blob BLOB,
			env_var TEXT NOT NULL DEFAULT '',
			active INTEGER NOT NULL DEFAULT 0,
			read_only INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS drafts (
			account_id TEXT NOT NULL,
			peer_key TEXT NOT NULL,
			text_blob BLOB,
			reply_to_id INTEGER NOT NULL DEFAULT 0,
			entities_json TEXT NOT NULL DEFAULT '',
			server_date INTEGER NOT NULL DEFAULT 0,
			dirty INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(account_id, peer_key)
		);`,
		`CREATE TABLE IF NOT EXISTS peer_mutes (
			account_id TEXT NOT NULL,
			peer_key TEXT NOT NULL,
			mute_until INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(account_id, peer_key)
		);`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, datetime('now'));`,
	}

	for _, stmt := range statements {
		if _, err := db.sql.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	for _, col := range []struct {
		table string
		name  string
		def   string
	}{
		{"peers", "subtitle", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "contact", "INTEGER NOT NULL DEFAULT 0"},
		{"peers", "last_preview", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "last_message_at", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "top_message_id", "INTEGER NOT NULL DEFAULT 0"},
		{"peers", "folder_id", "INTEGER NOT NULL DEFAULT 0"},
		{"peers", "folder_title", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "pinned_order", "INTEGER NOT NULL DEFAULT 0"},
		{"peers", "read_outbox_max_id", "INTEGER NOT NULL DEFAULT 0"},
		{"peers", "history_min_id", "INTEGER NOT NULL DEFAULT 0"},
		{"peers", "history_loaded_until", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "thumb_cache_key", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "last_preview_key", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "last_preview_arg", "TEXT NOT NULL DEFAULT ''"},
		{"peers", "read_inbox_max_id", "INTEGER NOT NULL DEFAULT 0"},
		{"messages", "sender_kind", "TEXT NOT NULL DEFAULT ''"},
		{"messages", "sender_id", "INTEGER NOT NULL DEFAULT 0"},
		{"messages", "sender_name", "TEXT NOT NULL DEFAULT ''"},
		{"messages", "sender_color", "INTEGER NOT NULL DEFAULT 0"},
		{"messages", "forward_source", "TEXT NOT NULL DEFAULT ''"},
		{"messages", "reply_to_id", "INTEGER NOT NULL DEFAULT 0"},
		{"messages", "views", "INTEGER NOT NULL DEFAULT 0"},
		{"messages", "forwards", "INTEGER NOT NULL DEFAULT 0"},
		{"messages", "reactions_json", "TEXT NOT NULL DEFAULT ''"},
		{"messages", "via_bot_username", "TEXT NOT NULL DEFAULT ''"},
		{"messages", "service_key", "TEXT NOT NULL DEFAULT ''"},
		{"messages", "service_arg", "TEXT NOT NULL DEFAULT ''"},
		{"dialog_filters", "archive", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "contacts", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "non_contacts", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "groups", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "broadcasts", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "bots", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "exclude_muted", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "exclude_read", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "exclude_archived", "INTEGER NOT NULL DEFAULT 0"},
		{"dialog_filters", "include_peers", "TEXT NOT NULL DEFAULT '[]'"},
		{"dialog_filters", "exclude_peers", "TEXT NOT NULL DEFAULT '[]'"},
		{"dialog_filters", "pinned_peers", "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := db.ensureColumn(ctx, col.table, col.name, col.def); err != nil {
			return err
		}
	}
	// RecentPeersForBackfill orders without the pinned prefix, so idx_peers_dialog_order cannot
	// serve it and SQLite would scan and sort the whole table on every backfill round.
	if _, err := db.sql.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_peers_recent ON peers(account_id, last_message_at DESC, top_message_id DESC)`); err != nil {
		return err
	}
	if _, err := db.sql.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_peers_dialog_order ON peers(account_id, pinned DESC, pinned_order ASC, last_message_at DESC, top_message_id DESC)`); err != nil {
		return err
	}
	return nil
}

func (db *DB) ensureColumn(ctx context.Context, table, name, definition string) error {
	_, err := db.sql.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, definition))
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return nil
	}
	return err
}

func (db *DB) salt(ctx context.Context) ([]byte, error) {
	var encoded []byte
	err := db.sql.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = 'db_salt'`).Scan(&encoded)
	if err == nil {
		return hex.DecodeString(string(encoded))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	salt, err := secure.RandomBytes(16)
	if err != nil {
		return nil, err
	}
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO metadata(key, value) VALUES('db_salt', ?)`, []byte(hex.EncodeToString(salt))); err != nil {
		return nil, err
	}
	return salt, nil
}

func (db *DB) SaveAccount(ctx context.Context, account Account) error {
	now := time.Now().UTC()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO accounts(id, mode, display_name, username, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			mode = excluded.mode,
			display_name = excluded.display_name,
			username = excluded.username,
			updated_at = excluded.updated_at
	`, account.ID, account.Mode, account.DisplayName, account.Username, formatTime(account.CreatedAt), formatTime(account.UpdatedAt))
	return err
}

func (db *DB) SavePeers(ctx context.Context, peers []Peer) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	for _, peer := range peers {
		if peer.UpdatedAt.IsZero() {
			peer.UpdatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO peers(
				account_id, key, kind, telegram_id, access_hash, title, username, subtitle, contact, last_preview,
				last_message_at, top_message_id, folder_id, folder_title, pinned, pinned_order, unread,
				read_outbox_max_id, history_min_id, history_loaded_until, thumb_cache_key, updated_at,
				last_preview_key, last_preview_arg, read_inbox_max_id
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(account_id, key) DO UPDATE SET
				kind = excluded.kind,
				telegram_id = excluded.telegram_id,
				-- Never overwritten with zero. An access hash cannot be re-derived locally, so a
				-- peer built from an update that carried no entity for it would otherwise wipe
				-- the stored credential and make every later RPC on that peer fail with
				-- PEER_ID_INVALID.
				access_hash = CASE WHEN excluded.access_hash != 0 THEN excluded.access_hash ELSE peers.access_hash END,
				title = excluded.title,
				username = excluded.username,
				subtitle = excluded.subtitle,
				contact = excluded.contact,
				last_preview = excluded.last_preview,
				last_message_at = excluded.last_message_at,
				top_message_id = excluded.top_message_id,
				folder_id = CASE
					WHEN excluded.pinned != 0 AND excluded.folder_id = 0 THEN 0
					WHEN excluded.folder_id != 0 OR peers.folder_id = 0 THEN excluded.folder_id
					ELSE peers.folder_id
				END,
				folder_title = excluded.folder_title,
				pinned = excluded.pinned,
				pinned_order = excluded.pinned_order,
				unread = excluded.unread,
				read_outbox_max_id = CASE WHEN excluded.read_outbox_max_id > peers.read_outbox_max_id THEN excluded.read_outbox_max_id ELSE peers.read_outbox_max_id END,
				read_inbox_max_id = CASE WHEN excluded.read_inbox_max_id > peers.read_inbox_max_id THEN excluded.read_inbox_max_id ELSE peers.read_inbox_max_id END,
				history_min_id = CASE WHEN excluded.history_min_id != 0 THEN excluded.history_min_id ELSE peers.history_min_id END,
				history_loaded_until = CASE WHEN excluded.history_loaded_until != '' THEN excluded.history_loaded_until ELSE peers.history_loaded_until END,
				thumb_cache_key = excluded.thumb_cache_key,
				last_preview_key = excluded.last_preview_key,
				last_preview_arg = excluded.last_preview_arg,
				updated_at = excluded.updated_at
		`, peer.AccountID, peer.Key, peer.Kind, peer.ID, peer.AccessHash, peer.Title, peer.Username, peer.Subtitle, boolInt(peer.Contact), peer.LastPreview,
			nullableTime(peer.LastMessageAt), peer.TopMessageID, peer.FolderID, peer.FolderTitle, boolInt(peer.Pinned), peer.PinnedOrder, peer.Unread,
			peer.ReadOutboxMaxID, peer.HistoryMinID, nullableTime(peer.HistoryLoadedUntil), peer.ThumbCacheKey, formatTime(peer.UpdatedAt),
			peer.LastPreviewKey, peer.LastPreviewArg, peer.ReadInboxMaxID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const peerColumns = `key, kind, telegram_id, access_hash, title, username, subtitle, contact, last_preview, last_message_at,
			top_message_id, folder_id, folder_title, pinned, pinned_order, unread, read_outbox_max_id, history_min_id,
			history_loaded_until, thumb_cache_key, updated_at, last_preview_key, last_preview_arg, read_inbox_max_id`

func scanPeerRows(rows *sql.Rows, accountID string, capacity int) ([]Peer, error) {
	peers := make([]Peer, 0, capacity)
	for rows.Next() {
		var p Peer
		var pinned, contact int
		var lastMessageAt, historyLoadedUntil, updated string
		p.AccountID = accountID
		if err := rows.Scan(&p.Key, &p.Kind, &p.ID, &p.AccessHash, &p.Title, &p.Username, &p.Subtitle, &contact, &p.LastPreview, &lastMessageAt,
			&p.TopMessageID, &p.FolderID, &p.FolderTitle, &pinned, &p.PinnedOrder, &p.Unread, &p.ReadOutboxMaxID, &p.HistoryMinID,
			&historyLoadedUntil, &p.ThumbCacheKey, &updated, &p.LastPreviewKey, &p.LastPreviewArg, &p.ReadInboxMaxID); err != nil {
			return nil, err
		}
		p.Pinned = pinned != 0
		p.Contact = contact != 0
		p.LastMessageAt = parseTime(lastMessageAt)
		p.HistoryLoadedUntil = parseTime(historyLoadedUntil)
		p.UpdatedAt = parseTime(updated)
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

func (db *DB) ListPeers(ctx context.Context, accountID string) ([]Peer, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+peerColumns+`
		FROM peers WHERE account_id = ?
		ORDER BY pinned DESC, pinned_order ASC, last_message_at DESC, top_message_id DESC, title COLLATE NOCASE
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	peers, err := scanPeerRows(rows, accountID, 64)
	if err != nil {
		return nil, err
	}
	// Filled here rather than at each caller. Four separate places emit a chat list, and when this
	// was applied by the callers instead, two of them were missed - so the startup sync published a
	// list with every chat unmuted and the notification bell rang for muted groups.
	return db.withMutes(ctx, accountID, peers)
}

// withMutes fills MuteUntil on a list of peers.
//
// A second small query rather than a join: mute lives in its own table precisely to stay out of the
// peers upsert, and peerColumns is shared by several statements that would all have to grow an alias.
// There is one row per muted dialog, against the megabyte the peer list itself costs.
func (db *DB) withMutes(ctx context.Context, accountID string, peers []Peer) ([]Peer, error) {
	if len(peers) == 0 {
		return peers, nil
	}
	mutes, err := db.PeerMutes(ctx, accountID)
	if err != nil || len(mutes) == 0 {
		// A failure here is not worth losing the peer list over: an unmuted-looking chat is a worse
		// outcome than no chat list at all only if you squint.
		return peers, nil
	}
	for i := range peers {
		if until, ok := mutes[peers[i].Key]; ok {
			peers[i].MuteUntil = until
		}
	}
	return peers, nil
}

// RecentPeersForBackfill returns at most limit peers that are active since cutoff and do not yet
// hold history reaching back to horizon, newest first.
//
// Two problems this query exists to solve.
//
// The backfill used to call ListPeers every round and throw almost all of it away: on an account
// with a few thousand dialogs that is tens of megabytes of garbage every few seconds to choose
// twenty rows. Filtering and limiting in SQL makes a round a bounded read.
//
// More importantly it now has a stopping condition. The backfill had no notion of far enough, so it
// walked every recent peer's history backwards a page at a time forever: on a real account that
// produced six million messages across eight hundred dialogs and a 3.7GB database in about a day,
// almost none of which will ever be read. The horizon predicate is "we have not yet stored anything
// older than this", so a peer drops out of the candidate set the moment its stored history reaches
// back far enough, and a steady state does no work at all.
//
// last_message_at is RFC3339Nano text, which is not perfectly ordered as a string because trailing
// zeros in the fraction are dropped. That only mis-ranks peers within the same second, which does
// not matter for choosing what to backfill next — but it is why this is not the query to reach for
// if exact ordering ever does matter.
func (db *DB) RecentPeersForBackfill(ctx context.Context, accountID string, cutoff, horizon time.Time, limit int) ([]Peer, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+peerColumns+`
		FROM peers p
		WHERE p.account_id = ? AND p.last_message_at != '' AND p.last_message_at >= ?
			AND NOT EXISTS (
				SELECT 1 FROM messages m
				WHERE m.account_id = p.account_id AND m.peer_key = p.key AND m.date <= ?
			)
		ORDER BY p.last_message_at DESC, p.top_message_id DESC
		LIMIT ?
	`, accountID, formatTime(cutoff), formatTime(horizon), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPeerRows(rows, accountID, limit)
}

func (db *DB) Peer(ctx context.Context, accountID, key string) (Peer, bool, error) {
	var p Peer
	var pinned, contact int
	var lastMessageAt, historyLoadedUntil, updated string
	err := db.sql.QueryRowContext(ctx, `
		SELECT kind, telegram_id, access_hash, title, username, subtitle, contact, last_preview, last_message_at,
			top_message_id, folder_id, folder_title, pinned, pinned_order, unread, read_outbox_max_id, history_min_id,
			history_loaded_until, thumb_cache_key, updated_at, last_preview_key, last_preview_arg, read_inbox_max_id
		FROM peers WHERE account_id = ? AND key = ?
	`, accountID, key).Scan(&p.Kind, &p.ID, &p.AccessHash, &p.Title, &p.Username, &p.Subtitle, &contact, &p.LastPreview, &lastMessageAt,
		&p.TopMessageID, &p.FolderID, &p.FolderTitle, &pinned, &p.PinnedOrder, &p.Unread, &p.ReadOutboxMaxID, &p.HistoryMinID,
		&historyLoadedUntil, &p.ThumbCacheKey, &updated, &p.LastPreviewKey, &p.LastPreviewArg, &p.ReadInboxMaxID)
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, false, nil
	}
	if err != nil {
		return Peer{}, false, err
	}
	p.AccountID = accountID
	p.Key = key
	p.Pinned = pinned != 0
	p.Contact = contact != 0
	p.LastMessageAt = parseTime(lastMessageAt)
	p.HistoryLoadedUntil = parseTime(historyLoadedUntil)
	p.UpdatedAt = parseTime(updated)
	return p, true, nil
}

func (db *DB) SaveMessages(ctx context.Context, messages []Message) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, msg := range messages {
		rowID := messageRowID(msg)
		textBlob, err := db.seal("messages", rowID, "text", []byte(msg.Text))
		if err != nil {
			return err
		}
		mediaBlob, err := db.seal("messages", rowID, "media", []byte(msg.MediaJSON))
		if err != nil {
			return err
		}
		if msg.Date.IsZero() {
			msg.Date = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages(
				account_id, peer_key, message_id, date, sender, sender_kind, sender_id, sender_name, sender_color,
				outgoing, text_blob, media_blob, media_kind, forward_source, reply_to_id, state,
				views, forwards, reactions_json, via_bot_username, service_key, service_arg
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(account_id, peer_key, message_id) DO UPDATE SET
				date = excluded.date,
				sender = excluded.sender,
				sender_kind = excluded.sender_kind,
				sender_id = excluded.sender_id,
				sender_name = excluded.sender_name,
				sender_color = excluded.sender_color,
				outgoing = excluded.outgoing,
				text_blob = excluded.text_blob,
				media_blob = excluded.media_blob,
				media_kind = excluded.media_kind,
				forward_source = excluded.forward_source,
				reply_to_id = excluded.reply_to_id,
				state = excluded.state,
				views = excluded.views,
				forwards = excluded.forwards,
				reactions_json = excluded.reactions_json,
				via_bot_username = excluded.via_bot_username,
				service_key = excluded.service_key,
				service_arg = excluded.service_arg
		`, msg.AccountID, msg.PeerKey, msg.ID, formatTime(msg.Date), msg.Sender, msg.SenderKind, msg.SenderID, msg.SenderName, msg.SenderColor,
			boolInt(msg.Outgoing), textBlob, mediaBlob, msg.MediaKind, msg.ForwardSource, msg.ReplyToID, msg.State,
			msg.Views, msg.Forwards, msg.ReactionsJSON, msg.ViaBotUsername, msg.ServiceKey, msg.ServiceArg); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) MessagesForPeer(ctx context.Context, accountID, peerKey string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT message_id, date, sender, sender_kind, sender_id, sender_name, sender_color, outgoing,
			text_blob, media_blob, media_kind, forward_source, reply_to_id, state, views, forwards, reactions_json, via_bot_username, service_key, service_arg
		FROM messages WHERE account_id = ? AND peer_key = ?
		ORDER BY date DESC, message_id DESC LIMIT ?
	`, accountID, peerKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reversed []Message
	for rows.Next() {
		msg, err := db.scanMessage(rows, accountID, peerKey)
		if err != nil {
			return nil, err
		}
		reversed = append(reversed, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}

// MessageSenderName returns the sender_name of a stored message for reply previews.
func (db *DB) MessageSenderName(ctx context.Context, accountID, peerKey string, messageID int) (string, bool, error) {
	var name string
	err := db.sql.QueryRowContext(ctx, `
		SELECT sender_name FROM messages
		WHERE account_id = ? AND peer_key = ? AND message_id = ? AND state != 'deleted'
	`, accountID, peerKey, messageID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

func (db *DB) UpdateMessageViews(ctx context.Context, accountID, peerKey string, messageID, views int) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE messages SET views = ? WHERE account_id = ? AND peer_key = ? AND message_id = ?
	`, views, accountID, peerKey, messageID)
	return err
}

func (db *DB) UpdateMessageReactions(ctx context.Context, accountID, peerKey string, messageID int, reactionsJSON string) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE messages SET reactions_json = ? WHERE account_id = ? AND peer_key = ? AND message_id = ?
	`, reactionsJSON, accountID, peerKey, messageID)
	return err
}

func (db *DB) OlderMessagesForPeer(ctx context.Context, accountID, peerKey string, beforeID, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT message_id, date, sender, sender_kind, sender_id, sender_name, sender_color, outgoing,
			text_blob, media_blob, media_kind, forward_source, reply_to_id, state, views, forwards, reactions_json, via_bot_username, service_key, service_arg
		FROM messages WHERE account_id = ? AND peer_key = ? AND message_id > 0 AND message_id < ?
		ORDER BY date DESC, message_id DESC LIMIT ?
	`, accountID, peerKey, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reversed []Message
	for rows.Next() {
		msg, err := db.scanMessage(rows, accountID, peerKey)
		if err != nil {
			return nil, err
		}
		reversed = append(reversed, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}

func (db *DB) MarkMessagesDeleted(ctx context.Context, accountID, peerKey string, ids []int) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages SET state = 'deleted', text_blob = NULL, media_blob = NULL, media_kind = ''
			WHERE account_id = ? AND peer_key = ? AND message_id = ?
		`, accountID, peerKey, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) DeleteMessage(ctx context.Context, accountID, peerKey string, id int) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM messages WHERE account_id = ? AND peer_key = ? AND message_id = ?`, accountID, peerKey, id)
	return err
}

func (db *DB) PeerKeysForMessageIDs(ctx context.Context, accountID string, ids []int) ([]string, error) {
	seen := make(map[string]struct{})
	for _, id := range ids {
		rows, err := db.sql.QueryContext(ctx, `
			SELECT DISTINCT peer_key FROM messages
			WHERE account_id = ? AND message_id = ? AND state != 'deleted'
		`, accountID, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var peerKey string
			if err := rows.Scan(&peerKey); err != nil {
				_ = rows.Close()
				return nil, err
			}
			seen[peerKey] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(seen))
	for peerKey := range seen {
		out = append(out, peerKey)
	}
	sort.Strings(out)
	return out, nil
}

// MessageByID loads a single message row for the peer, if present.
func (db *DB) MessageByID(ctx context.Context, accountID, peerKey string, messageID int) (Message, bool, error) {
	row := db.sql.QueryRowContext(ctx, `
		SELECT message_id, date, sender, sender_kind, sender_id, sender_name, sender_color, outgoing,
			text_blob, media_blob, media_kind, forward_source, reply_to_id, state, views, forwards, reactions_json, via_bot_username, service_key, service_arg
		FROM messages WHERE account_id = ? AND peer_key = ? AND message_id = ?
	`, accountID, peerKey, messageID)
	msg, err := db.scanMessage(row, accountID, peerKey)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	return msg, true, nil
}

func (db *DB) UpdatePeerHistory(ctx context.Context, accountID, peerKey string, minID int, loadedUntil time.Time) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE peers SET
			history_min_id = CASE WHEN ? != 0 AND (history_min_id = 0 OR ? < history_min_id) THEN ? ELSE history_min_id END,
			history_loaded_until = ?,
			updated_at = ?
		WHERE account_id = ? AND key = ?
	`, minID, minID, minID, nullableTime(loadedUntil), formatTime(time.Now().UTC()), accountID, peerKey)
	return err
}

func (db *DB) UpdatePeerFolder(ctx context.Context, accountID, kind string, id int64, folderID int) error {
	switch kind {
	case "user":
		_, err := db.sql.ExecContext(ctx, `
			UPDATE peers SET folder_id = ?, updated_at = ?
			WHERE account_id = ? AND telegram_id = ? AND kind IN ('user', 'self')
		`, folderID, formatTime(time.Now().UTC()), accountID, id)
		return err
	default:
		_, err := db.sql.ExecContext(ctx, `
			UPDATE peers SET folder_id = ?, updated_at = ?
			WHERE account_id = ? AND telegram_id = ? AND kind = ?
		`, folderID, formatTime(time.Now().UTC()), accountID, id, kind)
		return err
	}
}

func (db *DB) UpdatePeerReadOutboxMaxID(ctx context.Context, accountID, peerKey string, maxID int) error {
	if maxID <= 0 {
		return nil
	}
	_, err := db.sql.ExecContext(ctx, `
		UPDATE peers SET read_outbox_max_id = CASE WHEN read_outbox_max_id > ? THEN read_outbox_max_id ELSE ? END,
			updated_at = ?
		WHERE account_id = ? AND key = ?
	`, maxID, maxID, formatTime(time.Now().UTC()), accountID, peerKey)
	return err
}

// UpdatePeerReadInbox advances the read-inbox watermark and replaces the unread count.
//
// The watermark is monotonic like read_outbox_max_id, because reads only ever move forward and
// updates can arrive out of order. The unread count is not: StillUnreadCount from the server is
// authoritative and legitimately goes both up and down. Pass unread < 0 to leave it alone.
func (db *DB) UpdatePeerReadInbox(ctx context.Context, accountID, peerKey string, maxID, unread int) error {
	if maxID <= 0 && unread < 0 {
		return nil
	}
	if unread < 0 {
		_, err := db.sql.ExecContext(ctx, `
			UPDATE peers SET read_inbox_max_id = CASE WHEN read_inbox_max_id > ? THEN read_inbox_max_id ELSE ? END,
				updated_at = ?
			WHERE account_id = ? AND key = ?
		`, maxID, maxID, formatTime(time.Now().UTC()), accountID, peerKey)
		return err
	}
	_, err := db.sql.ExecContext(ctx, `
		UPDATE peers SET read_inbox_max_id = CASE WHEN read_inbox_max_id > ? THEN read_inbox_max_id ELSE ? END,
			unread = ?, updated_at = ?
		WHERE account_id = ? AND key = ?
	`, maxID, maxID, unread, formatTime(time.Now().UTC()), accountID, peerKey)
	return err
}

// PeerKeyForTelegramID resolves a stored peer key from a Telegram id, restricted to the given
// kinds.
//
// Needed because a read update for Saved Messages arrives as a PeerUser while the row is stored
// as self:<id>, so building the key from the update's type alone would miss it. The kinds must
// be passed explicitly rather than searched across all of them: Telegram ids are only unique
// within a peer type, so a user and a chat can share one.
func (db *DB) PeerKeyForTelegramID(ctx context.Context, accountID string, telegramID int64, kinds ...string) (string, bool, error) {
	if len(kinds) == 0 {
		return "", false, nil
	}
	args := []any{accountID, telegramID}
	placeholders := make([]string, len(kinds))
	for i, kind := range kinds {
		placeholders[i] = "?"
		args = append(args, kind)
	}
	var key string
	err := db.sql.QueryRowContext(ctx, `
		SELECT key FROM peers
		WHERE account_id = ? AND telegram_id = ? AND kind IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY kind
		LIMIT 1
	`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}

func (db *DB) ClearGlobalPins(ctx context.Context, accountID string) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE peers SET pinned = 0, pinned_order = 0, updated_at = ?
		WHERE account_id = ? AND folder_id != 1
	`, formatTime(time.Now().UTC()), accountID)
	return err
}

// ApplyGlobalPins replaces main-folder pin flags using stable peer keys in order.
func (db *DB) ApplyGlobalPins(ctx context.Context, accountID string, orderedKeys []string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := formatTime(time.Now().UTC())
	if _, err := tx.ExecContext(ctx, `
		UPDATE peers SET pinned = 0, pinned_order = 0, updated_at = ?
		WHERE account_id = ? AND folder_id != 1
	`, now, accountID); err != nil {
		return err
	}
	for index, key := range orderedKeys {
		if key == "" {
			continue
		}
		pinned := false
		for _, alias := range peerKeyAliases(key) {
			res, err := tx.ExecContext(ctx, `
				UPDATE peers SET pinned = 1, pinned_order = ?, updated_at = ?
				WHERE account_id = ? AND key = ? AND folder_id != 1
			`, index+1, now, accountID, alias)
			if err != nil {
				return err
			}
			rows, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if rows > 0 {
				pinned = true
				break
			}
		}
		if pinned {
			continue
		}
		kind, id, ok := parsePeerKey(key)
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE peers SET pinned = 1, pinned_order = ?, updated_at = ?
			WHERE account_id = ? AND kind = ? AND telegram_id = ? AND folder_id != 1
		`, index+1, now, accountID, kind, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func peerKeyAliases(key string) []string {
	if key == "" {
		return nil
	}
	keys := []string{key}
	const selfPrefix = "self:"
	const userPrefix = "user:"
	if strings.HasPrefix(key, selfPrefix) {
		keys = append(keys, userPrefix+strings.TrimPrefix(key, selfPrefix))
	} else if strings.HasPrefix(key, userPrefix) {
		keys = append(keys, selfPrefix+strings.TrimPrefix(key, userPrefix))
	}
	return keys
}

func parsePeerKey(key string) (kind string, id int64, ok bool) {
	before, after, found := strings.Cut(key, ":")
	if !found || before == "" || after == "" {
		return "", 0, false
	}
	parsed, err := strconv.ParseInt(after, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return before, parsed, true
}

func (db *DB) UpdateDialogFilterPinnedPeers(ctx context.Context, accountID string, filterID int, peers []string) error {
	data, err := jsonString(peers)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx, `
		UPDATE dialog_filters SET pinned_peers = ?, updated_at = ?
		WHERE account_id = ? AND id = ?
	`, data, formatTime(time.Now().UTC()), accountID, filterID)
	return err
}

func (db *DB) SaveDialogFilters(ctx context.Context, accountID string, filters []DialogFilter) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM dialog_filters WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	now := formatTime(time.Now().UTC())
	for _, filter := range filters {
		includePeers, err := jsonString(filter.IncludePeers)
		if err != nil {
			return err
		}
		excludePeers, err := jsonString(filter.ExcludePeers)
		if err != nil {
			return err
		}
		pinnedPeers, err := jsonString(filter.PinnedPeers)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dialog_filters(
				account_id, id, title, kind, archive, contacts, non_contacts, groups, broadcasts, bots,
				exclude_muted, exclude_read, exclude_archived, include_peers, exclude_peers, pinned_peers, updated_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, accountID, filter.ID, filter.Title, filter.Kind, boolInt(filter.Archive), boolInt(filter.Contacts), boolInt(filter.NonContacts),
			boolInt(filter.Groups), boolInt(filter.Broadcasts), boolInt(filter.Bots), boolInt(filter.ExcludeMuted), boolInt(filter.ExcludeRead),
			boolInt(filter.ExcludeArchived), includePeers, excludePeers, pinnedPeers, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) ListDialogFilters(ctx context.Context, accountID string) ([]DialogFilter, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, title, kind, archive, contacts, non_contacts, groups, broadcasts, bots,
			exclude_muted, exclude_read, exclude_archived, include_peers, exclude_peers, pinned_peers
		FROM dialog_filters WHERE account_id = ?
		ORDER BY CASE WHEN id = 0 THEN 0 WHEN archive != 0 THEN 1 ELSE 2 END, id
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var filters []DialogFilter
	for rows.Next() {
		var f DialogFilter
		var archive, contacts, nonContacts, groups, broadcasts, bots, excludeMuted, excludeRead, excludeArchived int
		var includePeers, excludePeers, pinnedPeers string
		f.AccountID = accountID
		if err := rows.Scan(&f.ID, &f.Title, &f.Kind, &archive, &contacts, &nonContacts, &groups, &broadcasts, &bots,
			&excludeMuted, &excludeRead, &excludeArchived, &includePeers, &excludePeers, &pinnedPeers); err != nil {
			return nil, err
		}
		f.Archive = archive != 0
		f.Contacts = contacts != 0
		f.NonContacts = nonContacts != 0
		f.Groups = groups != 0
		f.Broadcasts = broadcasts != 0
		f.Bots = bots != 0
		f.ExcludeMuted = excludeMuted != 0
		f.ExcludeRead = excludeRead != 0
		f.ExcludeArchived = excludeArchived != 0
		f.IncludePeers = stringSliceFromJSON(includePeers)
		f.ExcludePeers = stringSliceFromJSON(excludePeers)
		f.PinnedPeers = stringSliceFromJSON(pinnedPeers)
		filters = append(filters, f)
	}
	return filters, rows.Err()
}

func (db *DB) SaveUpdateState(ctx context.Context, state UpdateState) error {
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	for key, value := range map[string]int{
		"pts":  state.Pts,
		"qts":  state.Qts,
		"date": state.Date,
		"seq":  state.Seq,
	} {
		if value == 0 {
			continue
		}
		if _, err := db.sql.ExecContext(ctx, `
			INSERT INTO sync_state(account_id, key, value, updated_at) VALUES(?, ?, ?, ?)
			ON CONFLICT(account_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, state.AccountID, key, strconv.Itoa(value), formatTime(state.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) SaveProxyProfile(ctx context.Context, profile ProxyProfile) (int64, error) {
	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	if profile.Active {
		if _, err := db.sql.ExecContext(ctx, `UPDATE proxy_profiles SET active = 0 WHERE read_only = 0`); err != nil {
			return 0, err
		}
	}
	if profile.ID == 0 {
		res, err := db.sql.ExecContext(ctx, `
			INSERT INTO proxy_profiles(name, kind, source, address, username, env_var, active, read_only, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, profile.Name, profile.Config.Kind, profile.Config.Source, profile.Config.Address, profile.Config.Username, profile.Config.EnvVar, boolInt(profile.Active), boolInt(profile.ReadOnly), formatTime(profile.CreatedAt), formatTime(profile.UpdatedAt))
		if err != nil {
			return 0, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		profile.ID = id
	}
	rowID := strconv.FormatInt(profile.ID, 10)
	password, err := db.seal("proxy_profiles", rowID, "password", []byte(profile.Config.Password))
	if err != nil {
		return 0, err
	}
	secret, err := db.seal("proxy_profiles", rowID, "secret", []byte(profile.Config.Secret))
	if err != nil {
		return 0, err
	}
	_, err = db.sql.ExecContext(ctx, `
		UPDATE proxy_profiles SET
			name = ?, kind = ?, source = ?, address = ?, username = ?, password_blob = ?, secret_blob = ?, env_var = ?,
			active = ?, read_only = ?, updated_at = ?
		WHERE id = ?
	`, profile.Name, profile.Config.Kind, profile.Config.Source, profile.Config.Address, profile.Config.Username, password, secret, profile.Config.EnvVar, boolInt(profile.Active), boolInt(profile.ReadOnly), formatTime(profile.UpdatedAt), profile.ID)
	return profile.ID, err
}

func (db *DB) ListProxyProfiles(ctx context.Context) ([]ProxyProfile, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, name, kind, source, address, username, password_blob, secret_blob, env_var, active, read_only, created_at, updated_at
		FROM proxy_profiles ORDER BY read_only DESC, active DESC, name COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []ProxyProfile
	for rows.Next() {
		var p ProxyProfile
		var kind, source string
		var active, readOnly int
		var passwordBlob, secretBlob []byte
		var created, updated string
		if err := rows.Scan(&p.ID, &p.Name, &kind, &source, &p.Config.Address, &p.Config.Username, &passwordBlob, &secretBlob, &p.Config.EnvVar, &active, &readOnly, &created, &updated); err != nil {
			return nil, err
		}
		p.Config.Kind = network.ProxyKind(kind)
		p.Config.Source = network.ProxySource(source)
		p.Active = active != 0
		p.ReadOnly = readOnly != 0
		p.CreatedAt = parseTime(created)
		p.UpdatedAt = parseTime(updated)
		password, err := db.open("proxy_profiles", strconv.FormatInt(p.ID, 10), "password", passwordBlob)
		if err != nil {
			return nil, err
		}
		secret, err := db.open("proxy_profiles", strconv.FormatInt(p.ID, 10), "secret", secretBlob)
		if err != nil {
			return nil, err
		}
		p.Config.Password = string(password)
		p.Config.Secret = string(secret)
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func (db *DB) DeleteProxyProfile(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM proxy_profiles WHERE id = ? AND read_only = 0`, id)
	return err
}

func (db *DB) ActivateProxyProfile(ctx context.Context, id int64) error {
	if _, err := db.sql.ExecContext(ctx, `UPDATE proxy_profiles SET active = 0 WHERE read_only = 0`); err != nil {
		return err
	}
	_, err := db.sql.ExecContext(ctx, `UPDATE proxy_profiles SET active = 1 WHERE id = ? AND read_only = 0`, id)
	return err
}

func (db *DB) UpsertEnvironmentProxy(ctx context.Context, cfg network.ProxyConfig) error {
	if cfg.Source != network.SourceEnvironment {
		return nil
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM proxy_profiles WHERE read_only = 1 AND source = ?`, network.SourceEnvironment); err != nil {
		return err
	}
	_, err := db.SaveProxyProfile(ctx, ProxyProfile{
		Name:     "Environment: " + cfg.EnvVar,
		Config:   cfg,
		Active:   true,
		ReadOnly: true,
	})
	return err
}

func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO settings(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, formatTime(time.Now().UTC()))
	return err
}

func (db *DB) SetSecretSetting(ctx context.Context, key, value string) error {
	sealed, err := db.seal("settings", key, "value", []byte(value))
	if err != nil {
		return err
	}
	return db.SetSetting(ctx, key, base64.StdEncoding.EncodeToString(sealed))
}

func (db *DB) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := db.sql.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (db *DB) GetSecretSetting(ctx context.Context, key string) (string, bool, error) {
	value, ok, err := db.GetSetting(ctx, key)
	if err != nil || !ok {
		return "", ok, err
	}
	sealed, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", false, err
	}
	opened, err := db.open("settings", key, "value", sealed)
	if err != nil {
		return "", false, err
	}
	return string(opened), true, nil
}

func (db *DB) DeleteSettings(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if _, err := db.sql.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) seal(table, rowID, field string, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	return db.cipher.Seal(table, rowID, field, plaintext)
}

func (db *DB) open(table, rowID, field string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	return db.cipher.Open(table, rowID, field, ciphertext)
}

type messageScanner interface {
	Scan(dest ...any) error
}

func (db *DB) scanMessage(scanner messageScanner, accountID, peerKey string) (Message, error) {
	var msg Message
	var outgoing int
	var date string
	var textBlob, mediaBlob []byte
	msg.AccountID = accountID
	msg.PeerKey = peerKey
	if err := scanner.Scan(&msg.ID, &date, &msg.Sender, &msg.SenderKind, &msg.SenderID, &msg.SenderName, &msg.SenderColor, &outgoing,
		&textBlob, &mediaBlob, &msg.MediaKind, &msg.ForwardSource, &msg.ReplyToID, &msg.State,
		&msg.Views, &msg.Forwards, &msg.ReactionsJSON, &msg.ViaBotUsername, &msg.ServiceKey, &msg.ServiceArg); err != nil {
		return Message{}, err
	}
	msg.Date = parseTime(date)
	msg.Outgoing = outgoing != 0
	text, err := db.open("messages", messageRowID(msg), "text", textBlob)
	if err != nil {
		return Message{}, err
	}
	media, err := db.open("messages", messageRowID(msg), "media", mediaBlob)
	if err != nil {
		return Message{}, err
	}
	msg.Text = string(text)
	msg.MediaJSON = string(media)
	return msg, nil
}

func messageRowID(msg Message) string {
	return fmt.Sprintf("%s/%s/%d", msg.AccountID, msg.PeerKey, msg.ID)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func jsonString(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func stringSliceFromJSON(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil
	}
	return out
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}

func parseTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}
