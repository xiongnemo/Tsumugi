package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nemo/Tsumugi/internal/network"
)

type AuthMode string

const (
	AuthUser AuthMode = "user"
	AuthBot  AuthMode = "bot"
)

type CLIFlags struct {
	ConfigPath string
	AuthMode   string
	APIID      int
	APIHash    string
	BotToken   string
	Phone      string
	Proxy      string
}

type Paths struct {
	ConfigDir  string
	DataDir    string
	CacheDir   string
	SessionDir string
	LogDir     string
	MediaDir   string
}

type SyncMode string

const (
	SyncLazy SyncMode = "lazy"
	SyncFull SyncMode = "full"
)

type Config struct {
	AuthMode AuthMode
	SyncMode SyncMode
	APIID    int
	APIHash  string
	BotToken string
	Phone    string
	Proxy    network.ProxyConfig
	Paths    Paths
}

func Load(flags CLIFlags) (Config, error) {
	paths, err := defaultPaths(flags.ConfigPath)
	if err != nil {
		return Config{}, err
	}
	envAPIID, err := envInt("TSUMUGI_API_ID", "API_ID")
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		AuthMode: AuthUser,
		SyncMode: SyncLazy,
		APIID:    firstInt(flags.APIID, envAPIID),
		APIHash:  firstString(flags.APIHash, os.Getenv("TSUMUGI_API_HASH"), os.Getenv("API_HASH")),
		BotToken: firstString(flags.BotToken, os.Getenv("TSUMUGI_BOT_TOKEN")),
		Phone:    firstString(flags.Phone, os.Getenv("TSUMUGI_PHONE"), os.Getenv("TG_PHONE")),
		Paths:    paths,
	}

	mode := firstString(flags.AuthMode, os.Getenv("TSUMUGI_AUTH_MODE"))
	if mode != "" {
		parsed, err := ParseAuthMode(mode)
		if err != nil {
			return Config{}, err
		}
		cfg.AuthMode = parsed
	}
	if cfg.AuthMode == AuthBot && cfg.BotToken == "" {
		cfg.BotToken = os.Getenv("BOT_TOKEN")
	}

	syncMode := firstString(os.Getenv("TSUMUGI_SYNC_MODE"))
	switch strings.ToLower(strings.TrimSpace(syncMode)) {
	case "full":
		cfg.SyncMode = SyncFull
	case "", "lazy":
		cfg.SyncMode = SyncLazy
	default:
		return Config{}, fmt.Errorf("unsupported TSUMUGI_SYNC_MODE %q (use lazy or full)", syncMode)
	}

	proxyCfg, err := network.ResolveProxy(flags.Proxy, os.Environ())
	if err != nil {
		return Config{}, err
	}
	cfg.Proxy = proxyCfg

	return cfg, nil
}

func ParseAuthMode(value string) (AuthMode, error) {
	switch AuthMode(strings.ToLower(strings.TrimSpace(value))) {
	case "", AuthUser:
		return AuthUser, nil
	case AuthBot:
		return AuthBot, nil
	default:
		return "", fmt.Errorf("unknown auth mode %q", value)
	}
}

func (c Config) ReadyForTelegram() bool {
	if c.APIID == 0 || c.APIHash == "" {
		return false
	}
	if c.AuthMode == AuthBot {
		return c.BotToken != ""
	}
	return c.Phone != ""
}

func (c Config) EnsureDirs() error {
	for _, dir := range []string{
		c.Paths.ConfigDir,
		c.Paths.DataDir,
		c.Paths.CacheDir,
		c.Paths.SessionDir,
		c.Paths.LogDir,
		c.Paths.MediaDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

func (c Config) SessionPath(identity string) string {
	safe := sanitizePathPart(identity)
	if safe == "" {
		safe = string(c.AuthMode)
	}
	return filepath.Join(c.Paths.SessionDir, safe+".json")
}

func defaultPaths(configPath string) (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	dataDir, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, err
	}

	if configPath != "" {
		configDir = configPath
	}

	baseConfig := filepath.Join(configDir, "tsumugi")
	baseData := filepath.Join(dataDir, "tsumugi")
	return Paths{
		ConfigDir:  baseConfig,
		DataDir:    baseData,
		CacheDir:   filepath.Join(baseData, "cache"),
		SessionDir: filepath.Join(baseData, "sessions"),
		LogDir:     filepath.Join(baseData, "logs"),
		MediaDir:   filepath.Join(baseData, "media"),
	}, nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func envInt(names ...string) (int, error) {
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", name, err)
		}
		return n, nil
	}
	return 0, nil
}

func sanitizePathPart(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}
