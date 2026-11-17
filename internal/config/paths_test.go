package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nemo/Tsumugi/internal/xdg"
)

// --config-dir used to redirect only the config directory while the database, sessions and media
// cache stayed under the user cache dir. Running against an "empty" directory therefore still opened
// the real database and connected as the real account, which makes isolated testing impossible and
// the flag actively dangerous.
func TestConfigDirOverridesTheDataRootToo(t *testing.T) {
	override := t.TempDir()
	paths, err := defaultPaths(override)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(override, "tsumugi")
	for name, path := range map[string]string{
		"ConfigDir":  paths.ConfigDir,
		"DataDir":    paths.DataDir,
		"StateDir":   paths.StateDir,
		"CacheDir":   paths.CacheDir,
		"SessionDir": paths.SessionDir,
		"MediaDir":   paths.MediaDir,
		"DebugLog":   paths.DebugLog,
		"MemoryLog":  paths.MemoryLog,
	} {
		if path == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
			t.Errorf("%s = %q escaped the --config-dir override %q", name, path, root)
		}
	}
	// The override's layout is what existing --config-dir users already have on disk.
	if paths.DataDir != root || paths.SessionDir != filepath.Join(root, "sessions") || paths.MediaDir != filepath.Join(root, "media") {
		t.Fatalf("override layout changed: %+v", paths)
	}
}

// The override wins over everything: even with a legacy database sitting in the default location,
// --config-dir must neither use it nor mention it.
func TestConfigDirIgnoresLegacyLocations(t *testing.T) {
	home := isolateHome(t)
	writeFile(t, filepath.Join(home, "legacy-cache", "tsumugi", "tsumugi.db"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "legacy-cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "legacy-cache"))

	override := t.TempDir()
	cfg, err := Load(CLIFlags{ConfigPath: override})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.DataDir != filepath.Join(override, "tsumugi") || len(cfg.Paths.Legacy) != 0 {
		t.Fatalf("override consulted a legacy location: %+v", cfg.Paths)
	}
}

// Load goes through the real environment; with HOME and the rest pointed at a temp directory, a
// fresh install lands entirely in the XDG layout under that home and touches no legacy location.
func TestLoadUsesTheXDGLayoutUnderHome(t *testing.T) {
	home := isolateHome(t)
	cfg, err := Load(CLIFlags{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DataDir":    filepath.Join(home, ".local", "share", "tsumugi"),
		"SessionDir": filepath.Join(home, ".local", "share", "tsumugi", "sessions"),
		"MediaDir":   filepath.Join(home, ".cache", "tsumugi", "media"),
		"StateDir":   filepath.Join(home, ".local", "state", "tsumugi"),
		"ConfigDir":  filepath.Join(home, ".config", "tsumugi"),
	}
	got := map[string]string{
		"DataDir":    cfg.Paths.DataDir,
		"SessionDir": cfg.Paths.SessionDir,
		"MediaDir":   cfg.Paths.MediaDir,
		"StateDir":   cfg.Paths.StateDir,
		"ConfigDir":  cfg.Paths.ConfigDir,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(cfg.Paths.Legacy) != 0 {
		t.Errorf("fresh install reported legacy items: %+v", cfg.Paths.Legacy)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Load created %v under the home; directories are made on first write", entries)
	}
}

func TestEnsureDirsCreatesOnlyWhatStartupWrites(t *testing.T) {
	isolateHome(t)
	cfg, err := Load(CLIFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{cfg.Paths.DataDir, cfg.Paths.SessionDir} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s was not created: %v", dir, err)
		}
	}
	for _, dir := range []string{cfg.Paths.ConfigDir, cfg.Paths.CacheDir, cfg.Paths.StateDir} {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%s was created before anything was written to it", dir)
		}
	}
}

// Each item falls back on its own, and only items this run uses produce a notice: an old debug log
// in the working directory is not mentioned while debug logging is off.
func TestResolvePathsPerItemWithNotices(t *testing.T) {
	home := t.TempDir()
	env := xdg.Env{GOOS: "windows", Home: home, Getenv: mapEnv(map[string]string{
		"LOCALAPPDATA": filepath.Join(home, "AppData", "Local"),
		"APPDATA":      filepath.Join(home, "AppData", "Roaming"),
	})}
	legacyRoot := filepath.Join(home, "AppData", "Local", "tsumugi")
	writeFile(t, filepath.Join(legacyRoot, "tsumugi.db"))
	writeFile(t, filepath.Join(home, ".local", "share", "tsumugi", "sessions", "user.json"))

	p := resolvePaths(env)
	if p.DataDir != legacyRoot {
		t.Errorf("DataDir = %q, want the legacy %q", p.DataDir, legacyRoot)
	}
	if want := filepath.Join(home, ".local", "share", "tsumugi", "sessions"); p.SessionDir != want {
		t.Errorf("SessionDir = %q, want %q", p.SessionDir, want)
	}
	if want := filepath.Join(home, ".cache", "tsumugi", "media"); p.MediaDir != want {
		t.Errorf("MediaDir = %q, want %q", p.MediaDir, want)
	}
	if ids := legacyIDs(p.Legacy); strings.Join(ids, ",") != xdg.Database {
		t.Errorf("legacy notices = %v, want only the database", ids)
	}

	notice := p.Legacy[0].Notice()
	for _, want := range []string{"tsumugi:", filepath.Join(legacyRoot, "tsumugi.db"), `"tsumugi migrate"`, filepath.Join(home, ".local", "share", "tsumugi", "tsumugi.db")} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q is missing %q", notice, want)
		}
	}
	if strings.Contains(notice, "\n") {
		t.Errorf("notice must be one line: %q", notice)
	}
}

// Old debug logs in the working directory are migrate's business only: a run started there still
// writes new logs to the state directory, and says nothing about the old ones.
func TestDebugLogsNeverFallBackToTheWorkingDirectory(t *testing.T) {
	home := isolateHome(t)
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, xdg.DebugLogFile))
	writeFile(t, filepath.Join(cwd, xdg.MemoryLogFile))
	t.Chdir(cwd)
	t.Setenv("TSUMUGI_DEBUG", "1")
	t.Setenv("TSUMUGI_DEBUG_MEM", "1")

	cfg, err := Load(CLIFlags{})
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".local", "state", "tsumugi")
	if cfg.Paths.DebugLog != filepath.Join(state, xdg.DebugLogFile) {
		t.Errorf("DebugLog = %q, want it in %s", cfg.Paths.DebugLog, state)
	}
	if cfg.Paths.MemoryLog != filepath.Join(state, xdg.MemoryLogFile) {
		t.Errorf("MemoryLog = %q, want it in %s", cfg.Paths.MemoryLog, state)
	}
	if len(cfg.Paths.Legacy) != 0 {
		t.Errorf("old logs produced notices: %+v", cfg.Paths.Legacy)
	}

	override := filepath.Join(t.TempDir(), "x.log")
	t.Setenv("TSUMUGI_DEBUG_LOG", override)
	for _, flags := range []CLIFlags{{}, {ConfigPath: t.TempDir()}} {
		cfg, err := Load(flags)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Paths.DebugLog != override {
			t.Errorf("with %+v, DebugLog = %q, want the TSUMUGI_DEBUG_LOG %q", flags, cfg.Paths.DebugLog, override)
		}
	}
}

// isolateHome points every variable the resolver or os.UserHomeDir reads at a temp directory, so
// nothing here can see the real home.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "TSUMUGI_DEBUG", "TSUMUGI_DEBUG_LOG", "TSUMUGI_DEBUG_MEM"} {
		t.Setenv(v, "")
	}
	return home
}

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
		t.Fatal(err)
	}
}

func legacyIDs(items []LegacyItem) []string {
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}
