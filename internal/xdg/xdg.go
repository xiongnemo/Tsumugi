// Package xdg decides where Tsumugi keeps its files.
//
// Every per-user file lives under the home directory in the XDG Base Directory layout, on Linux,
// macOS and Windows alike: one layout to document and one place to look, instead of %LOCALAPPDATA%
// on one machine and ~/Library/Caches on the next. Releases before this layout used
// os.UserConfigDir and os.UserCacheDir. Those are the legacy locations: still used when a file is
// found only there, and emptied by `tsumugi migrate`.
package xdg

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// App is Tsumugi's directory name under every base directory.
const App = "tsumugi"

// Item IDs. Stable: they are how the rest of the program and the tests refer to an item.
const (
	Database  = "database"
	Sessions  = "sessions"
	Media     = "media"
	DebugLog  = "debug-log"
	MemoryLog = "memory-log"
)

// File names, shared with the code that writes them.
const (
	DatabaseFile  = "tsumugi.db"
	DebugLogFile  = "debug-tsumugi.log"
	MemoryLogFile = "tsumugi-debug-mem.jsonl"
)

// Env is everything the layout depends on. Injectable so that tests never look at the real home.
type Env struct {
	GOOS   string
	Home   string
	Getenv func(string) string
}

// Current is the running process's environment.
func Current() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}, nil
}

func (e Env) getenv(name string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(name)
}

// base is $variable when it holds an absolute path, else ~/fallback. The XDG spec calls relative
// values invalid and says to ignore them; that holds on every OS, Windows included.
func (e Env) base(variable string, fallback ...string) string {
	if v := e.getenv(variable); v != "" && filepath.IsAbs(v) {
		return filepath.Clean(v)
	}
	return filepath.Join(append([]string{e.Home}, fallback...)...)
}

// Layout is Tsumugi's directory under each XDG base directory.
type Layout struct {
	Config string
	Data   string
	State  string
	Cache  string
}

// Layout resolves the base directories: $XDG_CONFIG_HOME, $XDG_DATA_HOME, $XDG_STATE_HOME and
// $XDG_CACHE_HOME when absolute, else ~/.config, ~/.local/share, ~/.local/state and ~/.cache.
func (e Env) Layout() Layout {
	return Layout{
		Config: filepath.Join(e.base("XDG_CONFIG_HOME", ".config"), App),
		Data:   filepath.Join(e.base("XDG_DATA_HOME", ".local", "share"), App),
		State:  filepath.Join(e.base("XDG_STATE_HOME", ".local", "state"), App),
		Cache:  filepath.Join(e.base("XDG_CACHE_HOME", ".cache"), App),
	}
}

// Legacy is where releases before the XDG layout kept files: os.UserConfigDir()/tsumugi, which
// they created and never wrote to, and os.UserCacheDir()/tsumugi, which held the database, the
// sessions and the media cache. An empty field means that function fails in this environment, so
// no release can have written there.
type Legacy struct {
	Config string
	Cache  string
}

// Legacy reproduces os.UserConfigDir and os.UserCacheDir for this environment. They are not
// called directly because they read the real process environment, and tests must not.
func (e Env) Legacy() Legacy {
	var config, cache string
	switch e.GOOS {
	case "windows":
		config = e.getenv("APPDATA")
		cache = e.getenv("LOCALAPPDATA")
	case "darwin", "ios":
		if e.Home != "" {
			config = filepath.Join(e.Home, "Library", "Application Support")
			cache = filepath.Join(e.Home, "Library", "Caches")
		}
	case "plan9":
		if e.Home != "" {
			config = filepath.Join(e.Home, "lib")
			cache = filepath.Join(e.Home, "lib", "cache")
		}
	default:
		config = e.legacyUnix("XDG_CONFIG_HOME", ".config")
		cache = e.legacyUnix("XDG_CACHE_HOME", ".cache")
	}
	l := Legacy{}
	if config != "" {
		l.Config = filepath.Join(config, App)
	}
	if cache != "" {
		l.Cache = filepath.Join(cache, App)
	}
	return l
}

// legacyUnix is os.UserConfigDir/os.UserCacheDir on Linux and the BSDs, which fail outright on a
// relative $XDG_*_HOME rather than ignoring it.
func (e Env) legacyUnix(variable, fallback string) string {
	if v := e.getenv(variable); v != "" {
		if !filepath.IsAbs(v) {
			return ""
		}
		return v
	}
	if e.Home == "" {
		return ""
	}
	return filepath.Join(e.Home, fallback)
}

// Item is one thing Tsumugi stores, resolved independently of every other item.
type Item struct {
	ID   string
	What string // for messages: "the database"
	Kind string // config, data, state or cache
	// Dir marks a directory of files rather than a single file.
	Dir    bool
	New    string
	Legacy []string
	// Unchanged is set when an old location is the new one, as the media cache's is on Linux.
	Unchanged bool
}

// Items lists everything Tsumugi stores. cwd is the directory the debug logs used to default to
// (the working directory); "" gives them no legacy location.
//
// The media cache keeps its media/ subdirectory under the cache root. On Linux that root was
// already ~/.cache/tsumugi, so the media cache is the one thing that does not move there.
func (e Env) Items(cwd string) []Item {
	l := e.Layout()
	old := e.Legacy()
	items := []Item{
		{ID: Database, What: "the database", Kind: "data", New: filepath.Join(l.Data, DatabaseFile), Legacy: under(old.Cache, DatabaseFile)},
		{ID: Sessions, What: "the Telegram sessions", Kind: "data", Dir: true, New: filepath.Join(l.Data, "sessions"), Legacy: under(old.Cache, "sessions")},
		{ID: Media, What: "the media cache", Kind: "cache", Dir: true, New: filepath.Join(l.Cache, "media"), Legacy: under(old.Cache, "media")},
		{ID: DebugLog, What: "the debug log", Kind: "state", New: filepath.Join(l.State, DebugLogFile), Legacy: under(cwd, DebugLogFile)},
		{ID: MemoryLog, What: "the memory diagnostics log", Kind: "state", New: filepath.Join(l.State, MemoryLogFile), Legacy: under(cwd, MemoryLogFile)},
	}
	for i := range items {
		var kept []string
		for _, p := range items[i].Legacy {
			if SamePath(p, items[i].New) {
				items[i].Unchanged = true
				continue
			}
			kept = append(kept, p)
		}
		items[i].Legacy = kept
	}
	return items
}

func under(dir, name string) []string {
	if dir == "" {
		return nil
	}
	return []string{filepath.Join(dir, name)}
}

// SamePath compares two paths as the same location.
func SamePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// Resolution is where an item lives for this run.
type Resolution struct {
	Path string
	// Legacy is set when the item was found only at an old location.
	Legacy bool
}

// Resolve applies the fallback: the new location if the item is there, else the first old
// location that has it, else the new location. Only the meaningful thing counts - the file, or a
// directory with a file in it - so an empty directory that a newer run created never hides old
// data. Nothing is created here; directories are made when something is first written.
func (it Item) Resolve() Resolution {
	if it.ExistsAt(it.New) {
		return Resolution{Path: it.New}
	}
	for _, old := range it.Legacy {
		if it.ExistsAt(old) {
			return Resolution{Path: old, Legacy: true}
		}
	}
	return Resolution{Path: it.New}
}

// ExistsAt reports whether the item is present at path.
func (it Item) ExistsAt(path string) bool {
	if it.Dir {
		return HasFiles(path)
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// HasFiles reports whether dir holds at least one regular file anywhere beneath it.
func HasFiles(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
