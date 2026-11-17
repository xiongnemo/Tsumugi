package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nemo/Tsumugi/internal/secure"
)

// The probe is what makes `tsumugi migrate` refuse while tsumugi runs: an open connection, like a
// running tsumugi's, must make it report ErrInUse, and a closed one must not.
func TestProbeInUseSeesAnOpenDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, _, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tsumugi.db")
	if err := ProbeInUse(ctx, path); !errors.Is(err, ErrInUse) {
		_ = db.Close()
		t.Fatalf("ProbeInUse with the database open = %v, want ErrInUse", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ProbeInUse(ctx, path); err != nil {
		t.Fatalf("ProbeInUse after close = %v, want nil", err)
	}
	// The probe left the database usable.
	db, _, err = Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen after probe: %v", err)
	}
	_ = db.Close()
}

// The path travels inside a file: URI, so characters that mean something there must survive.
func TestProbeInUseNeverCreatesTheFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "odd #name %20 dir")
	path := filepath.Join(dir, "tsumugi.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ProbeInUse(context.Background(), path); err == nil || errors.Is(err, ErrInUse) {
		t.Fatalf("ProbeInUse on a missing file = %v, want an open error", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("ProbeInUse created the database it was asked about")
	}
	db, _, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ProbeInUse(context.Background(), path); !errors.Is(err, ErrInUse) {
		t.Errorf("ProbeInUse on an open database under an odd path = %v, want ErrInUse", err)
	}
	_ = db.Close()
	if err := ProbeInUse(context.Background(), path); err != nil {
		t.Errorf("ProbeInUse on a closed database under an odd path = %v", err)
	}
}

func TestRewritePathsRewritesAttachmentsAndSettings(t *testing.T) {
	ctx := context.Background()
	db := openKeyed(t, t.TempDir(), 7)
	oldDir := filepath.Join(t.TempDir(), "old", "media")
	newDir := filepath.Join(t.TempDir(), "new", "media")
	elsewhere := filepath.Join(t.TempDir(), "Pictures", "shot.png")

	save := func(id int, localPath string) {
		t.Helper()
		media, err := json.Marshal(map[string]any{"Kind": "photo", "DocumentID": int64(9007199254740993), "LocalPath": localPath})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveMessages(ctx, []Message{{AccountID: "acct", PeerKey: "user:1", ID: id, MediaJSON: string(media), State: "synced"}}); err != nil {
			t.Fatal(err)
		}
	}
	save(1, filepath.Join(oldDir, "photo-1.jpg"))
	save(2, elsewhere)
	save(3, "")
	if err := db.SetSetting(ctx, "attach_dir", oldDir); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, "locale", "zh"); err != nil {
		t.Fatal(err)
	}

	res, err := db.RewritePaths(ctx, []PathRewrite{{From: oldDir, To: newDir}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Media != 1 || res.Settings != 1 || res.Unreadable != 0 {
		t.Fatalf("RewritePaths = %+v, want one attachment and one setting", res)
	}

	got := localPaths(t, db)
	if got[1] != filepath.Join(newDir, "photo-1.jpg") {
		t.Errorf("message 1 LocalPath = %q", got[1])
	}
	if got[2] != elsewhere {
		t.Errorf("a path outside the moved directory changed: %q", got[2])
	}
	if got[3] != "" {
		t.Errorf("an empty path changed: %q", got[3])
	}
	if v, _, _ := db.GetSetting(ctx, "attach_dir"); v != newDir {
		t.Errorf("attach_dir = %q, want %q", v, newDir)
	}
	if v, _, _ := db.GetSetting(ctx, "locale"); v != "zh" {
		t.Errorf("locale = %q", v)
	}
	// 64-bit ids must survive the rewrite exactly.
	msgs, err := db.MessagesForPeer(ctx, "acct", "user:1", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.ID == 1 && !bytes.Contains([]byte(m.MediaJSON), []byte("9007199254740993")) {
			t.Errorf("DocumentID lost precision: %s", m.MediaJSON)
		}
	}
}

func TestRewritePathsWithTheWrongKeyChangesNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openKeyed(t, dir, 7)
	oldDir := filepath.Join(dir, "old")
	media, _ := json.Marshal(map[string]any{"LocalPath": filepath.Join(oldDir, "a.jpg")})
	if err := db.SaveMessages(ctx, []Message{{AccountID: "acct", PeerKey: "user:1", ID: 1, MediaJSON: string(media), State: "synced"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, "attach_dir", oldDir); err != nil {
		t.Fatal(err)
	}
	wrong, err := secure.NewCipher(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(wrong)
	if _, err := db.RewritePaths(ctx, []PathRewrite{{From: oldDir, To: filepath.Join(dir, "new")}}); !errors.Is(err, ErrUndecryptable) {
		t.Fatalf("RewritePaths with the wrong key = %v, want ErrUndecryptable", err)
	}
	if v, _, _ := db.GetSetting(ctx, "attach_dir"); v != oldDir {
		t.Errorf("attach_dir changed to %q despite the wrong key", v)
	}
}

func openKeyed(t *testing.T, dir string, keyByte byte) *DB {
	t.Helper()
	db, _, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{keyByte}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(cipher)
	return db
}

func localPaths(t *testing.T, db *DB) map[int]string {
	t.Helper()
	msgs, err := db.MessagesForPeer(context.Background(), "acct", "user:1", 10)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]string{}
	for _, m := range msgs {
		var media struct{ LocalPath string }
		if m.MediaJSON != "" {
			if err := json.Unmarshal([]byte(m.MediaJSON), &media); err != nil {
				t.Fatal(err)
			}
		}
		out[m.ID] = media.LocalPath
	}
	return out
}
