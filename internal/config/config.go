package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nemo/Tsumugi/internal/network"
	"github.com/nemo/Tsumugi/internal/xdg"
)

type AuthMode string

const (
	AuthUser AuthMode = "user"
	AuthBot  AuthMode = "bot"
)

// LoginMethod selects how a user session is established.
//
// Deliberately a separate axis from AuthMode rather than a third mode. AuthMode is a capability
// discriminator: it drives Capabilities(), the dialogs-versus-bot-console branch,
// ReadyForTelegram, saveTelegramConfig, the footer and sessionPath. A QR session is an ordinary
// user session with identical capabilities, so a third mode would force every `== AuthUser` check
// in the codebase to become `!= AuthBot` for no semantic gain.
type LoginMethod string

const (
	LoginPhone LoginMethod = "phone"
	LoginQR    LoginMethod = "qr"
)

func ParseLoginMethod(value string) LoginMethod {
	if strings.EqualFold(strings.TrimSpace(value), string(LoginQR)) {
		return LoginQR
	}
	return LoginPhone
}

type CLIFlags struct {
	ConfigPath string
	AuthMode   string
	APIID      int
	APIHash    string
	BotToken   string
	Phone      string
	Proxy      string
}

// Paths is where this run reads and writes. See internal/xdg for the layout and the fallback to
// the locations older releases used.
type Paths struct {
	// ConfigDir is reserved: Tsumugi keeps its settings in the database and writes nothing here.
	ConfigDir string
	// DataDir holds the database.
	DataDir    string
	StateDir   string
	CacheDir   string
	SessionDir string
	MediaDir   string
	DebugLog   string
	MemoryLog  string
	// Legacy lists the items in use this run that were found only at an old location, so that
	// startup can say so once, before the TUI takes the screen.
	Legacy []LegacyItem
}

// LegacyItem is an item read from an old location.
type LegacyItem struct {
	ID   string
	What string
	Old  string
	New  string
}

// Notice is the one line printed to stderr for an item still at an old location.
func (l LegacyItem) Notice() string {
	return fmt.Sprintf("tsumugi: using %s from the old location %s; run \"tsumugi migrate\" to move it to %s", l.What, l.Old, l.New)
}

type SyncMode string

const (
	SyncLazy SyncMode = "lazy"
	SyncFull SyncMode = "full"
)

type Config struct {
	AuthMode        AuthMode
	LoginMethod     LoginMethod
	SyncMode        SyncMode
	APIID           int
	APIHash         string
	BotToken        string
	Phone           string
	Proxy           network.ProxyConfig
	Paths           Paths
	AuthModeFromEnv bool
	APIIDFromEnv    bool
	APIHashFromEnv  bool
	BotTokenFromEnv bool
	PhoneFromEnv    bool
}

func Load(flags CLIFlags) (Config, error) {
	paths, err := defaultPaths(flags.ConfigPath)
	if err != nil {
		return Config{}, err
	}
	paths = withDebugLogOverride(paths, os.Getenv)
	envAPIID, err := envInt("TSUMUGI_API_ID", "API_ID")
	if err != nil {
		return Config{}, err
	}

	envAPIHash := firstString(os.Getenv("TSUMUGI_API_HASH"), os.Getenv("API_HASH"))
	envBotToken := os.Getenv("TSUMUGI_BOT_TOKEN")
	envPhone := firstString(os.Getenv("TSUMUGI_PHONE"), os.Getenv("TG_PHONE"))

	cfg := Config{
		AuthMode:        AuthUser,
		LoginMethod:     LoginPhone,
		SyncMode:        SyncLazy,
		APIID:           firstInt(flags.APIID, envAPIID),
		APIHash:         firstString(flags.APIHash, envAPIHash),
		BotToken:        firstString(flags.BotToken, envBotToken),
		Phone:           firstString(flags.Phone, envPhone),
		Paths:           paths,
		APIIDFromEnv:    flags.APIID != 0 || envAPIID != 0,
		APIHashFromEnv:  strings.TrimSpace(flags.APIHash) != "" || envAPIHash != "",
		BotTokenFromEnv: strings.TrimSpace(flags.BotToken) != "" || strings.TrimSpace(envBotToken) != "",
		PhoneFromEnv:    strings.TrimSpace(flags.Phone) != "" || envPhone != "",
	}

	mode := firstString(flags.AuthMode, os.Getenv("TSUMUGI_AUTH_MODE"))
	if mode != "" {
		parsed, err := ParseAuthMode(mode)
		if err != nil {
			return Config{}, err
		}
		cfg.AuthMode = parsed
		cfg.AuthModeFromEnv = true
	}
	if cfg.AuthMode == AuthBot && cfg.BotToken == "" {
		cfg.BotToken = os.Getenv("BOT_TOKEN")
		cfg.BotTokenFromEnv = strings.TrimSpace(cfg.BotToken) != ""
	}

	if method := strings.TrimSpace(os.Getenv("TSUMUGI_LOGIN_METHOD")); method != "" {
		cfg.LoginMethod = ParseLoginMethod(method)
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
	return true
}

func (c Config) NeedsOnboarding() bool {
	if c.APIID == 0 || c.APIHash == "" {
		return true
	}
	if c.AuthMode == AuthBot {
		return c.BotToken == ""
	}
	// QR login has nothing to type: the code is scanned instead of a number being entered.
	if c.LoginMethod == LoginQR {
		return false
	}
	return c.Phone == ""
}

func (c Config) WithoutTelegramAuth() Config {
	c.AuthMode = AuthUser
	c.LoginMethod = LoginPhone
	c.APIID = 0
	c.APIHash = ""
	c.BotToken = ""
	c.Phone = ""
	c.AuthModeFromEnv = false
	c.APIIDFromEnv = false
	c.APIHashFromEnv = false
	c.BotTokenFromEnv = false
	c.PhoneFromEnv = false
	return c
}

// EnsureDirs creates the directories written to at startup: the database's, which is opened
// straight away, and the sessions', because gotd writes a session file without creating its parent.
// Everything else is created when something is first written there, so a run never leaves empty
// directories behind.
func (c Config) EnsureDirs() error {
	for _, dir := range []string{
		c.Paths.DataDir,
		c.Paths.SessionDir,
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

// defaultPaths resolves where Tsumugi keeps its files.
//
// configPath (--config-dir) puts *everything* under <configPath>/tsumugi and consults no other
// location. It used to redirect only the config directory while the database, sessions and media
// cache stayed under os.UserCacheDir, which made the flag a trap: running with --config-dir pointed
// at an empty directory still opened the real database, read the real stored credentials, and
// connected as the real account. There is no use for a config directory that is isolated from the
// data it describes, so the override also wins over the XDG layout and over every legacy location.
func defaultPaths(configPath string) (Paths, error) {
	if configPath != "" {
		return overridePaths(configPath), nil
	}
	env, err := xdg.Current()
	if err != nil {
		return Paths{}, err
	}
	return resolvePaths(env), nil
}

// overridePaths is the --config-dir layout, unchanged from before the XDG move apart from the state
// directory, which holds the debug logs that used to default to the working directory.
func overridePaths(configPath string) Paths {
	root := filepath.Join(configPath, xdg.App)
	state := filepath.Join(root, "state")
	return Paths{
		ConfigDir:  root,
		DataDir:    root,
		StateDir:   state,
		CacheDir:   filepath.Join(root, "cache"),
		SessionDir: filepath.Join(root, "sessions"),
		MediaDir:   filepath.Join(root, "media"),
		DebugLog:   filepath.Join(state, xdg.DebugLogFile),
		MemoryLog:  filepath.Join(state, xdg.MemoryLogFile),
	}
}

// resolvePaths places each item on its own: an old database does not drag the sessions or the media
// cache back to the old location with it, nor the other way round.
//
// The debug logs have no fallback. Older releases wrote them to the working directory, but a run
// must not append to whatever log happens to sit in the directory it was started from: new logs
// always go to the state directory, and `tsumugi migrate` moves old ones there.
func resolvePaths(env xdg.Env) Paths {
	layout := env.Layout()
	p := Paths{ConfigDir: layout.Config, StateDir: layout.State, CacheDir: layout.Cache}
	for _, item := range env.Items("") {
		r := item.Resolve()
		switch item.ID {
		case xdg.Database:
			p.DataDir = filepath.Dir(r.Path)
		case xdg.Sessions:
			p.SessionDir = r.Path
		case xdg.Media:
			p.MediaDir = r.Path
		case xdg.DebugLog:
			p.DebugLog = r.Path
		case xdg.MemoryLog:
			p.MemoryLog = r.Path
		}
		if r.Legacy {
			p.Legacy = append(p.Legacy, LegacyItem{ID: item.ID, What: item.What, Old: r.Path, New: item.New})
		}
	}
	return p
}

// withDebugLogOverride applies TSUMUGI_DEBUG_LOG, which still wins for the debug log over both the
// state directory and --config-dir.
func withDebugLogOverride(p Paths, getenv func(string) string) Paths {
	if override := strings.TrimSpace(getenv("TSUMUGI_DEBUG_LOG")); override != "" {
		p.DebugLog = override
	}
	return p
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
