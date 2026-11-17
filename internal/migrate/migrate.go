// Package migrate moves Tsumugi's files from where older releases kept them to the XDG layout.
//
// Releases before the XDG layout kept the database, the Telegram sessions and the media cache under
// os.UserCacheDir()/tsumugi (%LOCALAPPDATA%\tsumugi, ~/Library/Caches/tsumugi, ~/.cache/tsumugi),
// created an empty os.UserConfigDir()/tsumugi, and wrote the debug logs into the working directory.
// See internal/xdg for where everything goes now.
//
// Nothing is ever overwritten. A file present on both sides with the same content loses its old
// copy; with different content both are kept, the conflict is reported, and the run exits non-zero
// once everything else is done.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
	"github.com/nemo/Tsumugi/internal/xdg"
)

// Options is one migrate run.
type Options struct {
	Env xdg.Env
	// Cwd is the working directory, which is where older releases wrote the debug logs by default.
	Cwd string
	// From replaces Cwd as the place to look for those logs: a directory, or one of the log files.
	From   string
	DryRun bool
	Out    io.Writer
	Err    io.Writer
	// MasterKey returns the database key, or nil when it is not available without asking. It is
	// needed to rewrite the paths stored inside sealed attachments. See KeyringOrPassphrase.
	MasterKey func(salt []byte) ([]byte, error)
}

// Run migrates and returns the process exit code: 0 when everything moved or there was nothing to
// do, 1 on a conflict, an error, or a running tsumugi, and 2 for a bad --from.
func Run(ctx context.Context, opts Options) int {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Err == nil {
		opts.Err = io.Discard
	}
	return newMigrator(opts, opts.DryRun).run(ctx)
}

func newMigrator(opts Options, dry bool) *migrator {
	return &migrator{
		opts:     opts,
		dry:      dry,
		gone:     map[string]bool{},
		goneDirs: map[string]bool{},
	}
}

type migrator struct {
	opts Options
	dry  bool

	acted     int
	conflicts int
	failures  int

	// gone and goneDirs are what the plan moves away or deletes. A real run can just look at the
	// disk afterwards; a dry run needs them to tell which old directories would end up empty.
	gone     map[string]bool
	goneDirs map[string]bool
	// rewrites are the directories that moved, for rewriting the paths stored inside the database.
	rewrites []storage.PathRewrite
	// dbTarget is where this run put the database, if it moved it.
	dbTarget string
}

func (m *migrator) run(ctx context.Context) int {
	items, where, err := m.items()
	if err != nil {
		fmt.Fprintf(m.opts.Err, "tsumugi migrate: %v\n", err)
		return 2
	}
	var db xdg.Item
	for _, it := range items {
		if it.ID == xdg.Database {
			db = it
		}
	}

	if m.dry {
		m.printf("dry run: nothing is changed; this is what tsumugi migrate would do")
		m.noteOpenDatabase(db)
	} else {
		// Plan silently first: with nothing to do there is no reason to look for a running tsumugi,
		// and a second run must say so and succeed even while one runs.
		quiet := m.opts
		quiet.Out, quiet.Err = io.Discard, io.Discard
		plan := newMigrator(quiet, true)
		plan.plan(ctx, items, db)
		if plan.acted == 0 && plan.conflicts == 0 && plan.failures == 0 {
			m.printf("nothing to migrate")
			if where != "" {
				m.printf("%s", where)
			}
			return 0
		}
		if m.refuseIfRunning(ctx, db) {
			return 1
		}
	}

	m.plan(ctx, items, db)
	if where != "" {
		m.printf("%s", where)
	}
	return m.finish()
}

// plan works through every item, then the emptied old directories, then the paths stored inside the
// database. A dry run prints it; a real run performs each step as it prints it.
func (m *migrator) plan(ctx context.Context, items []xdg.Item, db xdg.Item) {
	layout := m.opts.Env.Layout()
	pend, hasPend, err := readPending(layout.State)
	if err != nil {
		m.fail("read %s: %v", filepath.Join(layout.State, pendingFile), err)
	}
	for _, item := range items {
		m.migrateItem(item)
	}
	m.cleanup(layout, m.opts.Env.Legacy(), items)
	m.rewriteReferences(ctx, db, layout.State, pend, hasPend)
}

// items lists what to migrate, with --from applied to the logs that used to default to the working
// directory. The second result says where those were looked for.
func (m *migrator) items() ([]xdg.Item, string, error) {
	env := m.opts.Env
	if m.opts.From == "" {
		if m.opts.Cwd == "" {
			return env.Items(""), "could not read the current directory, so old debug logs were not looked for; use --from", nil
		}
		return env.Items(m.opts.Cwd), fmt.Sprintf("looked for old debug logs in the current directory, %s; use --from to look somewhere else", m.opts.Cwd), nil
	}
	from, err := filepath.Abs(m.opts.From)
	if err != nil {
		return nil, "", fmt.Errorf("--from: %w", err)
	}
	info, err := os.Stat(from)
	if err != nil {
		return nil, "", fmt.Errorf("--from: %w", err)
	}
	if info.IsDir() {
		return env.Items(from), fmt.Sprintf("looked for old debug logs in %s", from), nil
	}
	items := env.Items(filepath.Dir(from))
	matched := false
	for i := range items {
		if items[i].ID != xdg.DebugLog && items[i].ID != xdg.MemoryLog {
			continue
		}
		if filepath.Base(items[i].New) == filepath.Base(from) {
			matched = true
			continue
		}
		items[i].Legacy = nil // not the file --from named
	}
	if !matched {
		return nil, "", fmt.Errorf("--from: %s is not a file tsumugi writes (%s or %s)", from, xdg.DebugLogFile, xdg.MemoryLogFile)
	}
	return items, "", nil
}

// noteOpenDatabase is the dry run's stand-in for the probe, which it cannot run because the probe
// may checkpoint a leftover WAL: it points out the files SQLite keeps beside an open database.
func (m *migrator) noteOpenDatabase(db xdg.Item) {
	for _, p := range append([]string{db.New}, db.Legacy...) {
		for _, suffix := range []string{"-wal", "-shm"} {
			if exists(p + suffix) {
				m.printf("note: %s exists, so tsumugi is running or did not exit cleanly; a real run checks, and refuses while tsumugi runs", p+suffix)
				return
			}
		}
	}
}

// refuseIfRunning probes every database file, old and new, for a running tsumugi.
func (m *migrator) refuseIfRunning(ctx context.Context, db xdg.Item) bool {
	for _, p := range append([]string{db.New}, db.Legacy...) {
		if !db.ExistsAt(p) {
			continue
		}
		err := storage.ProbeInUse(ctx, p)
		if err == nil {
			continue
		}
		if errors.Is(err, storage.ErrInUse) {
			fmt.Fprintf(m.opts.Err, "tsumugi migrate: tsumugi is running (it has %s open); quit it and run tsumugi migrate again. Nothing was changed.\n", p)
			return true
		}
		// SQLite cannot open it, so no tsumugi can be using it either.
		m.printf("note: %s does not open as a database (%v); it is moved as it is", p, err)
	}
	return false
}

func (m *migrator) migrateItem(it xdg.Item) {
	if len(it.Legacy) == 0 {
		if it.Unchanged {
			m.skip("%s: already at %s, where older releases kept it too", it.What, it.New)
		}
		return
	}
	for _, old := range it.Legacy {
		if !it.ExistsAt(old) {
			m.skip("%s: nothing at %s", it.What, old)
			continue
		}
		switch {
		case it.ID == xdg.Database:
			m.moveDatabase(it, old)
		case it.Dir:
			m.moveDirItem(it, old)
		default:
			m.moveFileItem(it, old, it.New)
		}
	}
}

// moveFileItem moves one file, or settles it against the copy already at dst.
func (m *migrator) moveFileItem(it xdg.Item, src, dst string) {
	if exists(dst) {
		m.settle(it, src, dst)
		return
	}
	m.act("move", "%s: %s -> %s", it.What, src, dst)
	if !m.dry {
		if err := moveFile(src, dst); err != nil {
			m.fail("%s: %v", it.What, err)
			return
		}
	}
	m.gone[src] = true
}

// settle handles a file that exists on both sides: the old copy goes if it is the same, and both
// stay if not.
func (m *migrator) settle(it xdg.Item, src, dst string) {
	same, err := sameContent(src, dst)
	if err != nil {
		m.fail("%s: compare %s with %s: %v", it.What, src, dst, err)
		return
	}
	if !same {
		m.conflict(it, src, dst)
		return
	}
	m.act("remove", "%s: %s (the same file is already at %s)", it.What, src, dst)
	if !m.dry {
		if err := os.Remove(src); err != nil {
			m.fail("%s: %v", it.What, err)
			return
		}
	}
	m.gone[src] = true
}

func (m *migrator) conflict(it xdg.Item, old, current string) {
	m.conflicts++
	m.printf("%-8s %s: %s and %s differ; both are kept, and tsumugi uses %s", "conflict", it.What, old, current, current)
}

// moveDirItem moves a directory whole when the new one does not exist yet, and merges it file by
// file when it does.
func (m *migrator) moveDirItem(it xdg.Item, src string) {
	m.rewrites = append(m.rewrites, storage.PathRewrite{From: src, To: it.New})
	info, err := os.Stat(it.New)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		m.act("move", "%s: %s -> %s (%d files)", it.What, src, it.New, countFiles(src))
		if !m.dry {
			if err := moveDir(src, it.New); err != nil {
				m.fail("%s: %v", it.What, err)
				return
			}
		}
		m.goneDirs[src] = true
		return
	case err != nil:
		m.fail("%s: %v", it.What, err)
		return
	case !info.IsDir():
		m.conflict(it, src, it.New)
		return
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			m.fail("%s: %v", it.What, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			m.printf("%-8s %s: %s is not a regular file", "leave", it.What, path)
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			m.fail("%s: %v", it.What, err)
			return nil
		}
		m.moveFileItem(it, path, filepath.Join(it.New, rel))
		return nil
	})
	if err != nil {
		m.fail("%s: %v", it.What, err)
	}
}

// moveDatabase moves tsumugi.db with what SQLite keeps beside it. A WAL or rollback journal belongs
// to the database file next to it - applied to any other database it corrupts it - so it only
// ever travels with its own file, and the move is refused if a stray one waits at the destination.
// The -shm file is an index SQLite rebuilds on open; it is deleted rather than moved.
func (m *migrator) moveDatabase(it xdg.Item, src string) {
	dst := it.New
	if exists(dst) {
		for _, s := range []string{"-wal", "-journal"} {
			if exists(src + s) {
				m.conflict(it, src, dst)
				return
			}
		}
		same, err := sameContent(src, dst)
		if err != nil {
			m.fail("%s: compare %s with %s: %v", it.What, src, dst, err)
			return
		}
		if !same {
			m.conflict(it, src, dst)
			return
		}
		m.act("remove", "%s: %s (the same file is already at %s)", it.What, src, dst)
		if !m.dry {
			if err := os.Remove(src); err != nil {
				m.fail("%s: %v", it.What, err)
				return
			}
		}
		m.gone[src] = true
		m.removeIndex(it, src)
		return
	}
	for _, s := range []string{"-wal", "-journal"} {
		if exists(dst + s) {
			m.failures++
			m.printf("%-8s %s: %s is at the new location without its database, and SQLite would apply it to the one moved there; nothing was moved. Delete it and run tsumugi migrate again", "error", it.What, dst+s)
			return
		}
	}
	m.act("move", "%s: %s -> %s", it.What, src, dst)
	if !m.dry {
		if err := moveFile(src, dst); err != nil {
			m.fail("%s: %v", it.What, err)
			return
		}
	}
	m.gone[src] = true
	m.dbTarget = dst
	for _, s := range []string{"-wal", "-journal"} {
		if exists(src + s) {
			m.moveFileItem(it, src+s, dst+s)
		}
	}
	m.removeIndex(it, src)
}

func (m *migrator) removeIndex(it xdg.Item, db string) {
	shm := db + "-shm"
	if !exists(shm) {
		return
	}
	m.act("remove", "%s: %s (SQLite's index, rebuilt when the database opens)", it.What, shm)
	if !m.dry {
		if err := os.Remove(shm); err != nil {
			m.fail("%s: %v", it.What, err)
			return
		}
	}
	m.gone[shm] = true
}

// cleanup removes the old directories this run emptied. Only Tsumugi's own directories are
// candidates, never a parent such as %LOCALAPPDATA%, and never one that is, or holds, a directory
// of the new layout - on Linux the old cache root is the new one.
func (m *migrator) cleanup(layout xdg.Layout, legacy xdg.Legacy, items []xdg.Item) {
	keep := []string{layout.Config, layout.Data, layout.State, layout.Cache}
	var dirs []string
	for _, it := range items {
		keep = append(keep, it.New)
		if it.Dir {
			dirs = append(dirs, it.Legacy...)
		}
	}
	if legacy.Cache != "" {
		// logs/ and cache/ were created by every older release and never written to.
		dirs = append(dirs, filepath.Join(legacy.Cache, "logs"), filepath.Join(legacy.Cache, "cache"), legacy.Cache)
	}
	if legacy.Config != "" {
		dirs = append(dirs, legacy.Config)
	}
	for _, dir := range dirs {
		if holdsNew(dir, keep) || m.goneDirs[dir] || !exists(dir) {
			continue
		}
		if !m.prune(dir) {
			m.printf("%-8s %s: not empty", "leave", dir)
		}
	}
}

func holdsNew(dir string, keep []string) bool {
	for _, k := range keep {
		if xdg.SamePath(dir, k) || isUnder(k, dir) {
			return true
		}
	}
	return false
}

// isUnder reports whether path is inside dir.
func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// prune removes dir if nothing but empty directories is left in it once this run is done.
func (m *migrator) prune(dir string) bool {
	if m.goneDirs[dir] {
		return true
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		m.fail("%v", err)
		return false
	}
	empty := true
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if !m.prune(p) {
				empty = false
			}
			continue
		}
		if m.dry && m.gone[p] {
			continue
		}
		empty = false
	}
	if !empty {
		return false
	}
	m.act("remove", "empty directory %s", dir)
	if !m.dry {
		if err := os.Remove(dir); err != nil {
			m.fail("%v", err)
			return false
		}
	}
	m.goneDirs[dir] = true
	return true
}

// rewriteReferences rewrites the absolute paths the database stores into directories that moved:
// attachment LocalPaths (sealed) and plain settings such as the attach picker's last directory. The
// work is recorded first, so that when the key is not available without asking, or the run is
// interrupted, tsumugi finishes it on its next start.
func (m *migrator) rewriteReferences(ctx context.Context, db xdg.Item, stateDir string, pend pending, hasPend bool) {
	rewrites := m.rewrites
	if hasPend {
		rewrites = mergeRewrites(pend.Rewrites, rewrites)
	}
	if len(rewrites) == 0 {
		return
	}
	dbPath := m.dbTarget
	if dbPath == "" {
		dbPath = db.Resolve().Path
		if !db.ExistsAt(dbPath) {
			// No database anywhere, so nothing inside one points at the old directories.
			if hasPend && !m.dry {
				if err := removePending(stateDir); err != nil {
					m.fail("%v", err)
				}
			}
			return
		}
	}
	for _, r := range rewrites {
		m.act("rewrite", "paths inside %s: %s -> %s", dbPath, r.From, r.To)
	}
	if m.dry {
		return
	}
	if err := writePending(stateDir, pending{Database: dbPath, Rewrites: rewrites}); err != nil {
		m.fail("record the path rewrites: %v", err)
		return
	}
	store, salt, err := storage.Open(ctx, filepath.Dir(dbPath))
	if err != nil {
		m.fail("open %s: %v", dbPath, err)
		return
	}
	defer store.Close()
	var key []byte
	if m.opts.MasterKey != nil {
		key, err = m.opts.MasterKey(salt)
		if err != nil {
			m.fail("read the database key: %v", err)
			return
		}
	}
	if key == nil {
		m.printf("note: the database key is in neither the OS keyring nor TSUMUGI_PASSPHRASE, so the paths inside %s are not rewritten yet. tsumugi rewrites them itself the next time it starts; or set TSUMUGI_PASSPHRASE and run tsumugi migrate again.", dbPath)
		return
	}
	cipher, err := secure.NewCipher(key)
	if err != nil {
		m.fail("%v", err)
		return
	}
	store.SetCipher(cipher)
	res, err := store.RewritePaths(ctx, rewrites)
	if errors.Is(err, storage.ErrUndecryptable) {
		m.fail("the database key does not open %s, so nothing inside it was changed", dbPath)
		return
	}
	if err != nil {
		m.fail("rewrite paths inside %s: %v", dbPath, err)
		return
	}
	m.printf("rewrote  %d attachment paths and %d settings inside %s", res.Media, res.Settings, dbPath)
	if res.Unreadable > 0 {
		m.printf("note: %d attachments did not decrypt and were left as they were", res.Unreadable)
	}
	if err := removePending(stateDir); err != nil {
		m.fail("%v", err)
	}
}

func mergeRewrites(a, b []storage.PathRewrite) []storage.PathRewrite {
	out := append([]storage.PathRewrite(nil), a...)
	for _, r := range b {
		dup := false
		for _, have := range out {
			if have.From == r.From {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, r)
		}
	}
	return out
}

func (m *migrator) finish() int {
	if m.acted == 0 && m.conflicts == 0 && m.failures == 0 {
		m.printf("nothing to migrate")
		return 0
	}
	if m.conflicts > 0 {
		verb := "were"
		if m.dry {
			verb = "would be"
		}
		fmt.Fprintf(m.opts.Err, "tsumugi migrate: %d conflicts: both copies %s kept. tsumugi uses the one at the new location; compare them, delete the copy you do not want, and run tsumugi migrate again.\n", m.conflicts, verb)
	}
	if m.failures > 0 {
		fmt.Fprintf(m.opts.Err, "tsumugi migrate: %d errors, listed above\n", m.failures)
	}
	if m.conflicts > 0 || m.failures > 0 {
		return 1
	}
	if m.dry {
		m.printf("run tsumugi migrate without --dry-run to do this")
	} else {
		m.printf("done")
	}
	return 0
}

func (m *migrator) printf(format string, args ...any) {
	fmt.Fprintf(m.opts.Out, format+"\n", args...)
}

// act prints one step of the plan, which a real run has just performed.
func (m *migrator) act(verb, format string, args ...any) {
	m.acted++
	fmt.Fprintf(m.opts.Out, "%-8s "+format+"\n", append([]any{verb}, args...)...)
}

// skip prints what the plan leaves alone and why. Only a dry run lists these: a real run prints
// what it does.
func (m *migrator) skip(format string, args ...any) {
	if m.dry {
		fmt.Fprintf(m.opts.Out, "%-8s "+format+"\n", append([]any{"skip"}, args...)...)
	}
}

func (m *migrator) fail(format string, args ...any) {
	m.failures++
	fmt.Fprintf(m.opts.Err, "error    "+format+"\n", args...)
}

func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}
