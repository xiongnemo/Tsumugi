package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	sqlite "modernc.org/sqlite"
)

// ErrInUse means another connection, which is to say a running Tsumugi, has the database open.
var ErrInUse = errors.New("the database is open in another process")

// ErrUndecryptable means not one sealed attachment opened with the key: it is the wrong key.
var ErrUndecryptable = errors.New("no attachment in the database opens with this key")

// ProbeInUse reports whether any other connection has the database at path open.
//
// It asks SQLite for an exclusive lock on the file. Every Tsumugi already holds a share of that
// lock: in WAL mode a connection keeps a SHARED lock on the database file for as long as it is open,
// and SQLite itself relies on that to know which connection closes last. So the probe works against
// any release, including the ones that predate it, and needs no lock file of its own.
//
// mode=rw makes a missing file an error rather than a new empty database. Closing the probe
// checkpoints a leftover WAL into the database, as the last connection to close always does.
func ProbeInUse(ctx context.Context, path string) error {
	dsn := sqliteURI(path) + "?mode=rw&_pragma=busy_timeout(0)&_pragma=locking_mode(EXCLUSIVE)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer raw.Close()
	var n int
	err = raw.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&n)
	if isBusy(err) {
		return fmt.Errorf("%s: %w", path, ErrInUse)
	}
	return err
}

// sqliteURI turns a filesystem path into a file: URI, so that query parameters can ride along and
// characters such as '?' and '#' in the path stay part of it.
func sqliteURI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x becomes /C:/x, which SQLite reads as a Windows drive path
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// isBusy matches SQLITE_BUSY and SQLITE_LOCKED, including their extended codes.
func isBusy(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() & 0xff {
	case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
		return true
	}
	return false
}

// PathRewrite maps a directory that moved to where it went.
type PathRewrite struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Apply returns path with the From directory replaced by To, and whether it was under From.
func (r PathRewrite) Apply(path string) (string, bool) {
	if r.From == "" {
		return path, false
	}
	if path == r.From {
		return r.To, true
	}
	if rest, ok := strings.CutPrefix(path, r.From+string(filepath.Separator)); ok {
		return filepath.Join(r.To, rest), true
	}
	return path, false
}

func applyRewrites(path string, rewrites []PathRewrite) (string, bool) {
	for _, r := range rewrites {
		if out, ok := r.Apply(path); ok {
			return out, true
		}
	}
	return path, false
}

// RewriteResult counts what RewritePaths changed.
type RewriteResult struct {
	// Media is the number of message attachments whose LocalPath was rewritten.
	Media int
	// Settings is the number of settings whose value was rewritten.
	Settings int
	// Unreadable is the number of attachments that did not decrypt and were left alone.
	Unreadable int
}

// RewritePaths rewrites the absolute paths the database stores that point into a directory that
// has moved: the LocalPath of message attachments, which are sealed and so need the cipher, and
// plain settings whose value is such a path, like the attach picker's last directory.
//
// Everything is read and decrypted before anything is written, and the writes are one transaction.
// If not one attachment decrypts, the key is wrong and nothing is changed.
func (db *DB) RewritePaths(ctx context.Context, rewrites []PathRewrite) (RewriteResult, error) {
	var res RewriteResult
	if len(rewrites) == 0 {
		return res, nil
	}
	if db.cipher == nil {
		return res, errors.New("rewriting paths needs the database key")
	}

	type mediaUpdate struct {
		accountID string
		peerKey   string
		id        int
		blob      []byte
	}
	var media []mediaUpdate
	opened := 0
	// Read everything first: the pool has one connection, so nothing can be written while these rows
	// are open.
	rows, err := db.sql.QueryContext(ctx, `SELECT account_id, peer_key, message_id, media_blob FROM messages WHERE media_blob IS NOT NULL AND length(media_blob) > 0`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var msg Message
		var blob []byte
		if err := rows.Scan(&msg.AccountID, &msg.PeerKey, &msg.ID, &blob); err != nil {
			_ = rows.Close()
			return res, err
		}
		rowID := messageRowID(msg)
		plain, err := db.open("messages", rowID, "media", blob)
		if err != nil {
			res.Unreadable++
			continue
		}
		opened++
		rewritten, ok := rewriteLocalPath(plain, rewrites)
		if !ok {
			continue
		}
		sealed, err := db.seal("messages", rowID, "media", rewritten)
		if err != nil {
			_ = rows.Close()
			return res, err
		}
		media = append(media, mediaUpdate{accountID: msg.AccountID, peerKey: msg.PeerKey, id: msg.ID, blob: sealed})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return res, err
	}
	_ = rows.Close()
	if opened == 0 && res.Unreadable > 0 {
		return res, ErrUndecryptable
	}

	type settingUpdate struct{ key, value string }
	var settings []settingUpdate
	rows, err = db.sql.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return res, err
		}
		// Sealed settings are base64 and never start with an absolute path, so only plain path
		// values can match.
		if out, ok := applyRewrites(value, rewrites); ok {
			settings = append(settings, settingUpdate{key: key, value: out})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return res, err
	}
	_ = rows.Close()

	if len(media) == 0 && len(settings) == 0 {
		return res, nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	for _, u := range media {
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET media_blob = ? WHERE account_id = ? AND peer_key = ? AND message_id = ?`, u.blob, u.accountID, u.peerKey, u.id); err != nil {
			_ = tx.Rollback()
			return res, err
		}
	}
	for _, u := range settings {
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = ?`, u.value, u.key); err != nil {
			_ = tx.Rollback()
			return res, err
		}
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.Media = len(media)
	res.Settings = len(settings)
	return res, nil
}

// rewriteLocalPath rewrites the LocalPath field of an attachment's JSON. Every other value is
// carried over as raw JSON, so 64-bit ids and access hashes never pass through a float. The
// attachment type lives in the telegram package, which imports this one, hence the raw handling.
func rewriteLocalPath(plain []byte, rewrites []PathRewrite) ([]byte, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(plain, &fields); err != nil {
		return nil, false
	}
	raw, ok := fields["LocalPath"]
	if !ok {
		return nil, false
	}
	var path string
	if err := json.Unmarshal(raw, &path); err != nil || path == "" {
		return nil, false
	}
	out, ok := applyRewrites(path, rewrites)
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	fields["LocalPath"] = encoded
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return rewritten, true
}
