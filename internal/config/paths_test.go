package config

import (
	"strings"
	"testing"
)

// --config-dir used to redirect only the config directory while the database, sessions and media
// cache stayed under the user cache dir. Running against an "empty" directory therefore still opened
// the real database and connected as the real account, which makes isolated testing impossible and
// the flag actively dangerous.
func TestConfigDirOverridesTheDataRootToo(t *testing.T) {
	paths, err := defaultPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	root := paths.ConfigDir
	for name, dir := range map[string]string{
		"DataDir":    paths.DataDir,
		"CacheDir":   paths.CacheDir,
		"SessionDir": paths.SessionDir,
		"MediaDir":   paths.MediaDir,
		"LogDir":     paths.LogDir,
	} {
		if dir == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		// Both roots end in "tsumugi" under the override, so comparing against the override's
		// parent is what proves nothing escaped to the real cache directory.
		if !strings.HasPrefix(dir, strings.TrimSuffix(root, "tsumugi")) {
			t.Errorf("%s = %q escaped the --config-dir override %q", name, dir, root)
		}
	}
}

// Without the flag, config and data keep their separate platform locations.
func TestDefaultPathsKeepPlatformSeparation(t *testing.T) {
	paths, err := defaultPaths("")
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigDir == "" || paths.DataDir == "" {
		t.Fatal("both roots should be resolved")
	}
}
