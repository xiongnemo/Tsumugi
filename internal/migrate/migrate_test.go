package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
	"github.com/nemo/Tsumugi/internal/xdg"
)

// fixture is a fake home. Every path the migrator sees comes from the injected environment, so the
// real home is never looked at; the Windows layout is emulated on every host because it is the one
// where every item moves.
type fixture struct {
	t         *testing.T
	home      string
	cwd       string
	env       xdg.Env
	layout    xdg.Layout
	old       string // the legacy cache root: the database, sessions and media cache
	oldConfig string
	key       []byte
}

func newFixture(t *testing.T, goos string) *fixture {
	t.Helper()
	home := t.TempDir()
	vars := map[string]string{
		"APPDATA":      filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA": filepath.Join(home, "AppData", "Local"),
	}
	env := xdg.Env{GOOS: goos, Home: home, Getenv: func(k string) string { return vars[k] }}
	f := &fixture{
		t:      t,
		home:   home,
		cwd:    t.TempDir(),
		env:    env,
		layout: env.Layout(),
		key:    bytes.Repeat([]byte{3}, 32),
	}
	f.old = env.Legacy().Cache
	f.oldConfig = env.Legacy().Config
	return f
}

func (f *fixture) run(dry bool, opts ...func(*Options)) (int, string, string) {
	f.t.Helper()
	var out, errOut bytes.Buffer
	o := Options{
		Env:       f.env,
		Cwd:       f.cwd,
		DryRun:    dry,
		Out:       &out,
		Err:       &errOut,
		MasterKey: func([]byte) ([]byte, error) { return f.key, nil },
	}
	for _, fn := range opts {
		fn(&o)
	}
	code := Run(context.Background(), o)
	return code, out.String(), errOut.String()
}

// legacyInstall lays out what an older release left behind.
func (f *fixture) legacyInstall() {
	f.t.Helper()
	f.makeDB(f.old, filepath.Join(f.old, "media", "photo-1.jpg"))
	write(f.t, filepath.Join(f.old, "sessions", "user.json"), "session")
	write(f.t, filepath.Join(f.old, "media", "photo-1.jpg"), "photo")
	write(f.t, filepath.Join(f.old, "media", "clipboard-1.png"), "clip")
	mkdir(f.t, filepath.Join(f.old, "logs"))
	mkdir(f.t, filepath.Join(f.old, "cache"))
	mkdir(f.t, f.oldConfig)
	write(f.t, filepath.Join(f.cwd, xdg.DebugLogFile), "{}\n")
}

// makeDB creates a real database in dir with one attachment per path and the attach picker's last
// directory pointing at the first path's directory.
func (f *fixture) makeDB(dir string, localPaths ...string) {
	f.t.Helper()
	ctx := context.Background()
	mkdir(f.t, dir)
	db, _, err := storage.Open(ctx, dir)
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	cipher, err := secure.NewCipher(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	db.SetCipher(cipher)
	for i, p := range localPaths {
		media, _ := json.Marshal(map[string]any{"Kind": "photo", "LocalPath": p})
		if err := db.SaveMessages(ctx, []storage.Message{{AccountID: "acct", PeerKey: "user:1", ID: i + 1, MediaJSON: string(media), State: "synced"}}); err != nil {
			f.t.Fatal(err)
		}
	}
	if len(localPaths) > 0 {
		if err := db.SetSetting(ctx, "attach_dir", filepath.Dir(localPaths[0])); err != nil {
			f.t.Fatal(err)
		}
	}
}

// stored reads back the attachment paths and attach_dir of the database in dir.
func (f *fixture) stored(dir string) ([]string, string) {
	f.t.Helper()
	ctx := context.Background()
	db, _, err := storage.Open(ctx, dir)
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	cipher, _ := secure.NewCipher(f.key)
	db.SetCipher(cipher)
	msgs, err := db.MessagesForPeer(ctx, "acct", "user:1", 100)
	if err != nil {
		f.t.Fatal(err)
	}
	var paths []string
	for _, m := range msgs {
		var media struct{ LocalPath string }
		_ = json.Unmarshal([]byte(m.MediaJSON), &media)
		paths = append(paths, media.LocalPath)
	}
	attach, _, _ := db.GetSetting(ctx, "attach_dir")
	return paths, attach
}

func TestDryRunTouchesNothing(t *testing.T) {
	f := newFixture(t, "windows")
	f.legacyInstall()
	before := snapshot(t, f.home, f.cwd)

	code, out, errOut := f.run(true)
	t.Logf("plan:\n%s", out)
	if code != 0 {
		t.Fatalf("dry run exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if after := snapshot(t, f.home, f.cwd); !equalSnapshots(before, after) {
		t.Fatalf("dry run changed the filesystem\nbefore: %v\nafter:  %v", before, after)
	}
	for _, want := range []string{
		"dry run",
		filepath.Join(f.old, "tsumugi.db") + " -> " + filepath.Join(f.layout.Data, "tsumugi.db"),
		filepath.Join(f.old, "sessions") + " -> " + filepath.Join(f.layout.Data, "sessions"),
		filepath.Join(f.old, "media") + " -> " + filepath.Join(f.layout.Cache, "media"),
		filepath.Join(f.cwd, xdg.DebugLogFile) + " -> " + filepath.Join(f.layout.State, xdg.DebugLogFile),
		"skip     the memory diagnostics log: nothing at " + filepath.Join(f.cwd, xdg.MemoryLogFile),
		"empty directory " + filepath.Join(f.old, "logs"),
		"empty directory " + f.old,
		"empty directory " + f.oldConfig,
		"rewrite  paths inside " + filepath.Join(f.layout.Data, "tsumugi.db"),
		"current directory, " + f.cwd,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan is missing %q\n%s", want, out)
		}
	}
}

func TestPlainMoveThenNothingToDo(t *testing.T) {
	f := newFixture(t, "windows")
	f.legacyInstall()

	code, out, errOut := f.run(false)
	t.Logf("run:\n%s", out)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	expectContent(t, filepath.Join(f.layout.Data, "sessions", "user.json"), "session")
	expectContent(t, filepath.Join(f.layout.Cache, "media", "photo-1.jpg"), "photo")
	expectContent(t, filepath.Join(f.layout.Cache, "media", "clipboard-1.png"), "clip")
	expectContent(t, filepath.Join(f.layout.State, xdg.DebugLogFile), "{}\n")
	if _, err := os.Stat(filepath.Join(f.layout.Data, "tsumugi.db")); err != nil {
		t.Errorf("database not moved: %v", err)
	}
	for _, gone := range []string{f.old, f.oldConfig, filepath.Join(f.cwd, xdg.DebugLogFile)} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s is still there", gone)
		}
	}
	// Only Tsumugi's own directories go; their parents stay.
	for _, kept := range []string{filepath.Dir(f.old), filepath.Dir(f.oldConfig)} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("parent %s was removed: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.layout.State, pendingFile)); err == nil {
		t.Error("the pending rewrite record was left behind after a complete run")
	}

	code, out, errOut = f.run(false)
	if code != 0 || !strings.Contains(out, "nothing to migrate") {
		t.Fatalf("second run: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	for _, verb := range []string{"move ", "remove ", "rewrite ", "conflict "} {
		if strings.Contains(out, verb) {
			t.Errorf("second run still did %q:\n%s", verb, out)
		}
	}
}

func TestReferencesInsideTheDatabaseAreRewritten(t *testing.T) {
	f := newFixture(t, "windows")
	f.legacyInstall()
	if code, out, errOut := f.run(false); code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	paths, attach := f.stored(f.layout.Data)
	newMedia := filepath.Join(f.layout.Cache, "media")
	if len(paths) != 1 || paths[0] != filepath.Join(newMedia, "photo-1.jpg") {
		t.Errorf("attachment paths = %v, want under %s", paths, newMedia)
	}
	if attach != newMedia {
		t.Errorf("attach_dir = %q, want %q", attach, newMedia)
	}
}

// Without the key, migrate records the rewrite for tsumugi's next start, which finishes it.
func TestRewriteWithoutTheKeyIsFinishedByTsumugi(t *testing.T) {
	f := newFixture(t, "windows")
	f.legacyInstall()
	noKey := func(o *Options) { o.MasterKey = func([]byte) ([]byte, error) { return nil, nil } }

	code, out, errOut := f.run(false, noKey)
	if code != 0 || !strings.Contains(out, "TSUMUGI_PASSPHRASE") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if paths, _ := f.stored(f.layout.Data); len(paths) != 1 || !strings.HasPrefix(paths[0], f.old) {
		t.Fatalf("paths changed without a key: %v", paths)
	}

	ctx := context.Background()
	db, _, err := storage.Open(ctx, f.layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := secure.NewCipher(f.key)
	db.SetCipher(cipher)
	res, done, err := FinishPending(ctx, db, filepath.Join(f.layout.Data, "tsumugi.db"), f.layout.State)
	_ = db.Close()
	if err != nil || !done || res.Media != 1 || res.Settings != 1 {
		t.Fatalf("FinishPending = %+v, %v, %v", res, done, err)
	}
	if paths, _ := f.stored(f.layout.Data); paths[0] != filepath.Join(f.layout.Cache, "media", "photo-1.jpg") {
		t.Fatalf("paths after FinishPending: %v", paths)
	}
	if code, out, _ := f.run(false, noKey); code != 0 || !strings.Contains(out, "nothing to migrate") {
		t.Fatalf("run after FinishPending: exit %d\n%s", code, out)
	}
}

// A different database - a --config-dir run - must not receive the default layout's rewrites.
func TestFinishPendingIgnoresAnotherDatabase(t *testing.T) {
	state := t.TempDir()
	if err := writePending(state, pending{Database: filepath.Join(t.TempDir(), "tsumugi.db"), Rewrites: []storage.PathRewrite{{From: "a", To: "b"}}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, _, err := storage.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, done, err := FinishPending(context.Background(), db, filepath.Join(dir, "tsumugi.db"), state); err != nil || done {
		t.Fatalf("FinishPending on another database: done=%v err=%v", done, err)
	}
	if _, err := os.Stat(filepath.Join(state, pendingFile)); err != nil {
		t.Fatal("the record for the other database was removed")
	}
}

func TestDirectoryMerge(t *testing.T) {
	f := newFixture(t, "windows")
	write(t, filepath.Join(f.old, "media", "a.jpg"), "a")
	write(t, filepath.Join(f.layout.Cache, "media", "b.jpg"), "b")

	if code, out, errOut := f.run(false); code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	expectContent(t, filepath.Join(f.layout.Cache, "media", "a.jpg"), "a")
	expectContent(t, filepath.Join(f.layout.Cache, "media", "b.jpg"), "b")
	if _, err := os.Stat(f.old); err == nil {
		t.Errorf("emptied %s was left behind", f.old)
	}
}

func TestConflictKeepsBothAndExitsNonZero(t *testing.T) {
	f := newFixture(t, "windows")
	write(t, filepath.Join(f.old, "sessions", "user.json"), "old session")
	write(t, filepath.Join(f.layout.Data, "sessions", "user.json"), "new session")
	write(t, filepath.Join(f.old, "media", "a.jpg"), "a")

	code, out, errOut := f.run(false)
	if code == 0 {
		t.Fatalf("a conflict exited 0\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	expectContent(t, filepath.Join(f.old, "sessions", "user.json"), "old session")
	expectContent(t, filepath.Join(f.layout.Data, "sessions", "user.json"), "new session")
	// Everything else still moved.
	expectContent(t, filepath.Join(f.layout.Cache, "media", "a.jpg"), "a")
	if !strings.Contains(out, "conflict") || !strings.Contains(errOut, "1 conflicts") {
		t.Errorf("conflict not reported\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if !strings.Contains(out, "leave    "+f.old) {
		t.Errorf("the non-empty old directory is not reported as left in place:\n%s", out)
	}
	// The unresolved conflict is still there on the next run.
	if code, _, _ := f.run(false); code == 0 {
		t.Error("a run with an unresolved conflict exited 0")
	}
}

func TestIdenticalContentRemovesTheLegacyCopy(t *testing.T) {
	f := newFixture(t, "windows")
	write(t, filepath.Join(f.old, "sessions", "user.json"), "same")
	write(t, filepath.Join(f.layout.Data, "sessions", "user.json"), "same")

	code, out, errOut := f.run(false)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	expectContent(t, filepath.Join(f.layout.Data, "sessions", "user.json"), "same")
	if _, err := os.Stat(filepath.Join(f.old, "sessions", "user.json")); err == nil {
		t.Error("the identical legacy copy was kept")
	}
	if !strings.Contains(out, "the same file is already at") {
		t.Errorf("removal not reported:\n%s", out)
	}
}

func TestRefusesWhileTsumugiRuns(t *testing.T) {
	f := newFixture(t, "windows")
	f.legacyInstall()
	running, _, err := storage.Open(context.Background(), f.old)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	before := snapshot(t, f.home, f.cwd)

	code, out, errOut := f.run(false)
	if code == 0 || !strings.Contains(errOut, "tsumugi is running") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	// The running instance's own -wal/-shm may change; nothing else may.
	after := snapshot(t, f.home, f.cwd)
	for p := range before {
		if strings.Contains(p, "tsumugi.db") {
			delete(before, p)
			delete(after, p)
		}
	}
	for p := range after {
		if strings.Contains(p, "tsumugi.db") {
			delete(after, p)
		}
	}
	if !equalSnapshots(before, after) {
		t.Fatalf("migrate changed files while tsumugi was running\nbefore: %v\nafter:  %v", before, after)
	}
}

func TestCrossVolumeMoveCopiesVerifiesAndKeepsModeAndMtime(t *testing.T) {
	f := newFixture(t, "windows")
	src := filepath.Join(f.old, "media", "a.jpg")
	write(t, src, "a")
	write(t, filepath.Join(f.old, "sessions", "nested", "user.json"), "s")
	stamp := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(src, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}

	renames := 0
	rename = func(oldpath, newpath string) error {
		renames++
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errCrossDevice}
	}
	t.Cleanup(func() { rename = os.Rename })

	if code, out, errOut := f.run(false); code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if renames == 0 {
		t.Fatal("the rename hook was never called")
	}
	dst := filepath.Join(f.layout.Cache, "media", "a.jpg")
	expectContent(t, dst, "a")
	expectContent(t, filepath.Join(f.layout.Data, "sessions", "nested", "user.json"), "s")
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(stamp) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), stamp)
	}
	if info.Mode() != srcInfo.Mode() {
		t.Errorf("mode = %v, want %v", info.Mode(), srcInfo.Mode())
	}
	if _, err := os.Stat(f.old); err == nil {
		t.Error("the copied source was not removed")
	}
}

// On Linux the old cache root is the new one: the database and sessions move to ~/.local/share,
// the media cache stays, and the never-used logs/ and cache/ directories go.
func TestLinuxLayout(t *testing.T) {
	f := newFixture(t, "linux")
	f.makeDB(f.old, filepath.Join(f.old, "media", "photo-1.jpg"))
	write(t, filepath.Join(f.old, "sessions", "user.json"), "session")
	write(t, filepath.Join(f.old, "media", "photo-1.jpg"), "photo")
	mkdir(t, filepath.Join(f.old, "logs"))
	mkdir(t, filepath.Join(f.old, "cache"))
	mkdir(t, f.oldConfig)

	code, out, errOut := f.run(false)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".local", "share", "tsumugi", "tsumugi.db")); err != nil {
		t.Errorf("database not moved: %v", err)
	}
	expectContent(t, filepath.Join(f.home, ".local", "share", "tsumugi", "sessions", "user.json"), "session")
	expectContent(t, filepath.Join(f.home, ".cache", "tsumugi", "media", "photo-1.jpg"), "photo")
	for _, gone := range []string{filepath.Join(f.old, "logs"), filepath.Join(f.old, "cache"), filepath.Join(f.old, "tsumugi.db")} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s is still there", gone)
		}
	}
	// ~/.config/tsumugi is the new config directory as well as the old one.
	if _, err := os.Stat(f.oldConfig); err != nil {
		t.Errorf("the config directory was removed: %v", err)
	}
	if paths, _ := f.stored(f.layout.Data); len(paths) != 1 || paths[0] != filepath.Join(f.old, "media", "photo-1.jpg") {
		t.Errorf("media paths changed although the media cache did not move: %v", paths)
	}
}

func TestFromPointsAtTheOldLogs(t *testing.T) {
	f := newFixture(t, "windows")
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, xdg.DebugLogFile), "debug")
	write(t, filepath.Join(elsewhere, xdg.MemoryLogFile), "mem")

	// A file moves only itself.
	code, out, errOut := f.run(false, func(o *Options) { o.From = filepath.Join(elsewhere, xdg.MemoryLogFile) })
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	expectContent(t, filepath.Join(f.layout.State, xdg.MemoryLogFile), "mem")
	expectContent(t, filepath.Join(elsewhere, xdg.DebugLogFile), "debug")

	// A directory is searched for both.
	if code, out, errOut := f.run(false, func(o *Options) { o.From = elsewhere }); code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	expectContent(t, filepath.Join(f.layout.State, xdg.DebugLogFile), "debug")

	other := filepath.Join(elsewhere, "notes.txt")
	write(t, other, "x")
	if code, _, errOut := f.run(false, func(o *Options) { o.From = other }); code != 2 || !strings.Contains(errOut, "--from") {
		t.Fatalf("--from with a stranger's file: exit %d, %s", code, errOut)
	}
	expectContent(t, other, "x")
}

// A WAL at the destination without its database would be applied to the moved database and corrupt
// it, so the database stays where it is.
func TestStrayWALAtTheDestinationBlocksTheDatabase(t *testing.T) {
	f := newFixture(t, "windows")
	f.makeDB(f.old)
	write(t, filepath.Join(f.layout.Data, "tsumugi.db-wal"), "stray")

	code, out, errOut := f.run(false)
	if code == 0 {
		t.Fatalf("exit 0 despite a stray WAL\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if _, err := os.Stat(filepath.Join(f.old, "tsumugi.db")); err != nil {
		t.Errorf("database left its old location: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.layout.Data, "tsumugi.db")); err == nil {
		t.Error("database was moved next to a stray WAL")
	}
}

// A database must never end up apart from its journals. This calls moveDatabase directly: through
// Run, the probe's checkpoint would usually have removed the WAL first, and the guarantee must not
// depend on that.
func TestFailedJournalMoveMovesTheDatabaseBack(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "old", "tsumugi.db")
	dst := filepath.Join(root, "new", "tsumugi.db")
	write(t, src, "db")
	write(t, src+"-wal", "wal")
	write(t, src+"-journal", "journal")

	injectRename(t, func(oldpath, newpath string) bool { return oldpath == src+"-journal" })
	var out, errOut bytes.Buffer
	m := newMigrator(Options{Out: &out, Err: &errOut}, false)
	m.moveDatabase(xdg.Item{ID: xdg.Database, What: "the database", New: dst}, src)

	if m.failures != 1 || !strings.Contains(errOut.String(), "moved back") {
		t.Fatalf("failures = %d\nstdout:\n%s\nstderr:\n%s", m.failures, out.String(), errOut.String())
	}
	expectContent(t, src, "db")
	expectContent(t, src+"-wal", "wal")
	expectContent(t, src+"-journal", "journal")
	for _, p := range []string{dst, dst + "-wal", dst + "-journal"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was left at the new location", p)
		}
	}
	if m.dbTarget != "" {
		t.Errorf("dbTarget = %q after a failed move", m.dbTarget)
	}
}

// If moving back fails as well, the error must say where the files are and what to do.
func TestFailedRollbackSaysWhatToMoveByHand(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "old", "tsumugi.db")
	dst := filepath.Join(root, "new", "tsumugi.db")
	write(t, src, "db")
	write(t, src+"-journal", "journal")

	injectRename(t, func(oldpath, newpath string) bool { return oldpath == src+"-journal" || oldpath == dst })
	var errOut bytes.Buffer
	m := newMigrator(Options{Err: &errOut}, false)
	m.moveDatabase(xdg.Item{ID: xdg.Database, What: "the database", New: dst}, src)

	if m.failures != 1 {
		t.Fatalf("failures = %d\n%s", m.failures, errOut.String())
	}
	for _, want := range []string{"by hand", dst, src} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("error is missing %q:\n%s", want, errOut.String())
		}
	}
}

// injectRename makes rename fail, with an error that is not a cross-volume one, for the moves fail
// picks; every other rename goes through.
func injectRename(t *testing.T, fail func(oldpath, newpath string) bool) {
	t.Helper()
	rename = func(oldpath, newpath string) error {
		if fail(oldpath, newpath) {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errors.New("injected failure")}
		}
		return os.Rename(oldpath, newpath)
	}
	t.Cleanup(func() { rename = os.Rename })
}

type entry struct {
	dir   bool
	mode  fs.FileMode
	mtime time.Time
	sum   string
}

func snapshot(t *testing.T, roots ...string) map[string]entry {
	t.Helper()
	out := map[string]entry{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			e := entry{dir: d.IsDir(), mode: info.Mode()}
			if !d.IsDir() {
				e.mtime = info.ModTime()
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				s := sha256.Sum256(raw)
				e.sum = hex.EncodeToString(s[:])
			}
			out[path] = e
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func equalSnapshots(a, b map[string]entry) bool {
	if len(a) != len(b) {
		return false
	}
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		x, ok := b[k]
		if !ok || x.dir != a[k].dir || x.mode != a[k].mode || !x.mtime.Equal(a[k].mtime) || x.sum != a[k].sum {
			return false
		}
	}
	return true
}

func write(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func expectContent(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	if string(raw) != want {
		t.Errorf("%s = %q, want %q", path, raw, want)
	}
}
