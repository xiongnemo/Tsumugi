package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
	"github.com/nemo/Tsumugi/internal/xdg"
)

// pendingFile records path rewrites inside the database that are still to be done. Attachments are
// sealed, so rewriting their paths needs the database key; when migrate cannot get it without
// asking, Tsumugi finishes the job on its next start, where it has the key anyway.
const pendingFile = "migrate-pending.json"

type pending struct {
	Database string                `json:"database"`
	Rewrites []storage.PathRewrite `json:"rewrites"`
}

func readPending(stateDir string) (pending, bool, error) {
	raw, err := os.ReadFile(filepath.Join(stateDir, pendingFile))
	if errors.Is(err, fs.ErrNotExist) {
		return pending{}, false, nil
	}
	if err != nil {
		return pending{}, false, err
	}
	var p pending
	if err := json.Unmarshal(raw, &p); err != nil {
		return pending{}, false, err
	}
	return p, true, nil
}

func writePending(stateDir string, p pending) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, pendingFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(stateDir, pendingFile))
}

func removePending(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, pendingFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// FinishPending completes the path rewrites a migrate run left for want of the database key. Tsumugi
// calls it at startup with the database it opened and keyed. It does nothing unless the record is
// for that same database file, so a --config-dir run never applies the default layout's rewrites.
// done reports whether a record was applied and removed.
func FinishPending(ctx context.Context, db *storage.DB, dbPath, stateDir string) (res storage.RewriteResult, done bool, err error) {
	p, ok, err := readPending(stateDir)
	if err != nil || !ok {
		return res, false, err
	}
	if !xdg.SamePath(p.Database, dbPath) {
		return res, false, nil
	}
	res, err = db.RewritePaths(ctx, p.Rewrites)
	if err != nil {
		return res, false, err
	}
	return res, true, removePending(stateDir)
}

// KeyringOrPassphrase is the database key as Tsumugi finds it - the OS keyring, then
// TSUMUGI_PASSPHRASE - or nil when neither has it. It never creates a key or asks for one.
func KeyringOrPassphrase(salt []byte) ([]byte, error) {
	key, _, err := secure.LookupMasterKey(salt)
	if errors.Is(err, secure.ErrPassphraseRequired) {
		return nil, nil
	}
	return key, err
}
