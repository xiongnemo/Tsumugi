package xdg

import (
	"os"
	"path/filepath"
	"testing"
)

func envFor(goos, home string, vars map[string]string) Env {
	return Env{GOOS: goos, Home: home, Getenv: func(k string) string { return vars[k] }}
}

func TestLayoutHonorsOnlyAbsoluteXDGVariables(t *testing.T) {
	home := t.TempDir()
	custom := t.TempDir()
	for _, goos := range []string{"linux", "darwin", "windows", "freebsd"} {
		t.Run(goos+"/unset", func(t *testing.T) {
			l := envFor(goos, home, nil).Layout()
			want := Layout{
				Config: filepath.Join(home, ".config", "tsumugi"),
				Data:   filepath.Join(home, ".local", "share", "tsumugi"),
				State:  filepath.Join(home, ".local", "state", "tsumugi"),
				Cache:  filepath.Join(home, ".cache", "tsumugi"),
			}
			if l != want {
				t.Fatalf("Layout() = %+v, want %+v", l, want)
			}
		})
		t.Run(goos+"/absolute", func(t *testing.T) {
			l := envFor(goos, home, map[string]string{
				"XDG_CONFIG_HOME": filepath.Join(custom, "c"),
				"XDG_DATA_HOME":   filepath.Join(custom, "d"),
				"XDG_STATE_HOME":  filepath.Join(custom, "s"),
				"XDG_CACHE_HOME":  filepath.Join(custom, "k"),
			}).Layout()
			want := Layout{
				Config: filepath.Join(custom, "c", "tsumugi"),
				Data:   filepath.Join(custom, "d", "tsumugi"),
				State:  filepath.Join(custom, "s", "tsumugi"),
				Cache:  filepath.Join(custom, "k", "tsumugi"),
			}
			if l != want {
				t.Fatalf("Layout() = %+v, want %+v", l, want)
			}
		})
		t.Run(goos+"/relative", func(t *testing.T) {
			l := envFor(goos, home, map[string]string{
				"XDG_CONFIG_HOME": "rel/c",
				"XDG_DATA_HOME":   "rel/d",
				"XDG_STATE_HOME":  ".",
				"XDG_CACHE_HOME":  "rel",
			}).Layout()
			if l != envFor(goos, home, nil).Layout() {
				t.Fatalf("relative XDG variables were not ignored: %+v", l)
			}
		})
	}
}

func TestLegacyMatchesWhatOlderReleasesUsed(t *testing.T) {
	home := t.TempDir()
	xdgCache := filepath.Join(home, "xc")
	cases := []struct {
		name string
		env  Env
		want Legacy
	}{
		{"windows", envFor("windows", home, map[string]string{
			"APPDATA":      filepath.Join(home, "AppData", "Roaming"),
			"LOCALAPPDATA": filepath.Join(home, "AppData", "Local"),
		}), Legacy{
			Config: filepath.Join(home, "AppData", "Roaming", "tsumugi"),
			Cache:  filepath.Join(home, "AppData", "Local", "tsumugi"),
		}},
		{"windows without the variables", envFor("windows", home, nil), Legacy{}},
		{"darwin ignores XDG", envFor("darwin", home, map[string]string{"XDG_CACHE_HOME": xdgCache}), Legacy{
			Config: filepath.Join(home, "Library", "Application Support", "tsumugi"),
			Cache:  filepath.Join(home, "Library", "Caches", "tsumugi"),
		}},
		{"linux defaults", envFor("linux", home, nil), Legacy{
			Config: filepath.Join(home, ".config", "tsumugi"),
			Cache:  filepath.Join(home, ".cache", "tsumugi"),
		}},
		{"linux absolute XDG", envFor("linux", home, map[string]string{"XDG_CACHE_HOME": xdgCache}), Legacy{
			Config: filepath.Join(home, ".config", "tsumugi"),
			Cache:  filepath.Join(xdgCache, "tsumugi"),
		}},
		// os.UserCacheDir fails on a relative value, so no release could have written anywhere.
		{"linux relative XDG", envFor("linux", home, map[string]string{"XDG_CACHE_HOME": "rel"}), Legacy{
			Config: filepath.Join(home, ".config", "tsumugi"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.env.Legacy(); got != tc.want {
				t.Fatalf("Legacy() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveLegacyNewBothNeither(t *testing.T) {
	for _, dir := range []bool{false, true} {
		name := map[bool]string{false: "file", true: "dir"}[dir]
		write := func(t *testing.T, path string) {
			if dir {
				path = filepath.Join(path, "inside")
			}
			mustWrite(t, path, "x")
		}
		t.Run(name+"/legacy only", func(t *testing.T) {
			root := t.TempDir()
			it := Item{Dir: dir, New: filepath.Join(root, "new"), Legacy: []string{filepath.Join(root, "old")}}
			write(t, it.Legacy[0])
			if r := it.Resolve(); r.Path != it.Legacy[0] || !r.Legacy {
				t.Fatalf("Resolve() = %+v, want the legacy path", r)
			}
		})
		t.Run(name+"/new only", func(t *testing.T) {
			root := t.TempDir()
			it := Item{Dir: dir, New: filepath.Join(root, "new"), Legacy: []string{filepath.Join(root, "old")}}
			write(t, it.New)
			if r := it.Resolve(); r.Path != it.New || r.Legacy {
				t.Fatalf("Resolve() = %+v, want the new path", r)
			}
		})
		t.Run(name+"/both", func(t *testing.T) {
			root := t.TempDir()
			it := Item{Dir: dir, New: filepath.Join(root, "new"), Legacy: []string{filepath.Join(root, "old")}}
			write(t, it.New)
			write(t, it.Legacy[0])
			if r := it.Resolve(); r.Path != it.New || r.Legacy {
				t.Fatalf("Resolve() = %+v, want the new path", r)
			}
		})
		t.Run(name+"/neither", func(t *testing.T) {
			root := t.TempDir()
			it := Item{Dir: dir, New: filepath.Join(root, "new"), Legacy: []string{filepath.Join(root, "old")}}
			if r := it.Resolve(); r.Path != it.New || r.Legacy {
				t.Fatalf("Resolve() = %+v, want the new path", r)
			}
			if _, err := os.Stat(it.New); err == nil {
				t.Fatal("Resolve created the new location; that waits for the first write")
			}
		})
	}
}

// An empty directory a newer run created must not hide data still at the old location.
func TestEmptyNewDirectoryDoesNotHideLegacy(t *testing.T) {
	root := t.TempDir()
	it := Item{Dir: true, New: filepath.Join(root, "new", "sessions"), Legacy: []string{filepath.Join(root, "old", "sessions")}}
	if err := os.MkdirAll(filepath.Join(it.New, "empty-subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(it.Legacy[0], "user.json"), "session")
	if r := it.Resolve(); r.Path != it.Legacy[0] || !r.Legacy {
		t.Fatalf("Resolve() = %+v, want the legacy sessions", r)
	}
}

// A database at the old location does not drag the sessions or the media cache along with it.
func TestItemsResolveIndependently(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	env := envFor("windows", home, map[string]string{"LOCALAPPDATA": filepath.Join(home, "AppData", "Local")})
	old := filepath.Join(home, "AppData", "Local", "tsumugi")
	mustWrite(t, filepath.Join(old, DatabaseFile), "db")
	mustWrite(t, filepath.Join(home, ".local", "share", "tsumugi", "sessions", "user.json"), "s")
	mustWrite(t, filepath.Join(old, "media", "a.jpg"), "a")
	mustWrite(t, filepath.Join(home, ".cache", "tsumugi", "media", "b.jpg"), "b")
	mustWrite(t, filepath.Join(cwd, MemoryLogFile), "m")

	got := map[string]Resolution{}
	for _, it := range env.Items(cwd) {
		got[it.ID] = it.Resolve()
	}
	want := map[string]Resolution{
		Database:  {Path: filepath.Join(old, DatabaseFile), Legacy: true},
		Sessions:  {Path: filepath.Join(home, ".local", "share", "tsumugi", "sessions")},
		Media:     {Path: filepath.Join(home, ".cache", "tsumugi", "media")},
		DebugLog:  {Path: filepath.Join(home, ".local", "state", "tsumugi", DebugLogFile)},
		MemoryLog: {Path: filepath.Join(cwd, MemoryLogFile), Legacy: true},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s resolved to %+v, want %+v", id, got[id], w)
		}
	}
}

// On Linux the old cache root is the new one, so the media cache has nowhere to move from.
func TestLinuxMediaCacheStaysPut(t *testing.T) {
	home := t.TempDir()
	for _, it := range envFor("linux", home, nil).Items("") {
		switch it.ID {
		case Media:
			if len(it.Legacy) != 0 || !it.Unchanged {
				t.Fatalf("media item = %+v, want no legacy location and Unchanged", it)
			}
		case Database:
			if len(it.Legacy) != 1 || it.Legacy[0] != filepath.Join(home, ".cache", "tsumugi", DatabaseFile) {
				t.Fatalf("database legacy = %v", it.Legacy)
			}
			if it.New != filepath.Join(home, ".local", "share", "tsumugi", DatabaseFile) {
				t.Fatalf("database new = %v", it.New)
			}
		case DebugLog, MemoryLog:
			if len(it.Legacy) != 0 {
				t.Fatalf("%s has a legacy location without a working directory: %v", it.ID, it.Legacy)
			}
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
