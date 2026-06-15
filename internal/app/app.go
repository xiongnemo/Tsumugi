package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/term"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
	tgclient "github.com/nemo/Tsumugi/internal/telegram"
	"github.com/nemo/Tsumugi/internal/ui"
)

type App struct {
	cfg config.Config
}

const (
	settingTelegramAuthMode = "telegram.auth_mode"
	settingTelegramAPIID    = "telegram.api_id"
	settingTelegramAPIHash  = "telegram.api_hash"
	settingTelegramPhone    = "telegram.phone"
	settingTelegramBotToken = "telegram.bot_token"
)

func New(cfg config.Config) *App {
	return &App{cfg: cfg}
}

func (a *App) Run(ctx context.Context) error {
	startPprofIfEnabled()

	db, salt, err := storage.Open(ctx, a.cfg.Paths.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	key, _, err := secure.ResolveMasterKey(salt)
	if err != nil {
		if !errors.Is(err, secure.ErrPassphraseRequired) {
			return err
		}
		passphrase, promptErr := promptPassphrase()
		if promptErr != nil {
			return fmt.Errorf("%w; set TSUMUGI_PASSPHRASE or enable the OS keyring", err)
		}
		key = secure.DeriveKey(passphrase, salt)
	}
	cipher, err := secure.NewCipher(key)
	if err != nil {
		return err
	}
	db.SetCipher(cipher)
	if err := db.UpsertEnvironmentProxy(ctx, a.cfg.Proxy); err != nil {
		return err
	}
	if err := applyStoredTelegramConfig(ctx, db, &a.cfg); err != nil {
		return err
	}

	clientEvents := make(chan tgclient.Event, 32)
	uiEvents := make(chan tgclient.Event, 32)
	commands := make(chan tgclient.Command, 32)
	control := make(chan ui.ControlEvent, 8)

	var clientCancel context.CancelFunc
	startClient := func(cfg config.Config) {
		if clientCancel != nil {
			clientCancel()
		}
		clientCtx, cancel := context.WithCancel(ctx)
		clientCancel = cancel
		client := tgclient.NewGotdClient(cfg, db)
		go func() {
			if err := client.Run(clientCtx, clientEvents, commands); err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				sendClientEvent(ctx, clientEvents, tgclient.Event{Kind: tgclient.EventError, Error: err})
			}
		}()
	}
	stopClient := func() {
		if clientCancel != nil {
			clientCancel()
			clientCancel = nil
		}
	}
	defer stopClient()

	if a.cfg.ReadyForTelegram() && !a.cfg.NeedsOnboarding() {
		startClient(a.cfg)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-clientEvents:
				select {
				case uiEvents <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-control:
				switch event.Kind {
				case ui.ControlOnboardingComplete:
					if err := saveTelegramConfig(ctx, db, event.Config); err != nil {
						replyControl(event, err)
						continue
					}
					a.cfg = event.Config
					if a.cfg.ReadyForTelegram() {
						startClient(a.cfg)
					}
					replyControl(event, nil)
				case ui.ControlLogout:
					stopClient()
					if err := clearTelegramAuth(ctx, db, a.cfg); err != nil {
						replyControl(event, err)
						continue
					}
					a.cfg = a.cfg.WithoutTelegramAuth()
					replyControl(event, nil)
				default:
					replyControl(event, fmt.Errorf("unknown UI control event %q", event.Kind))
				}
			}
		}
	}()

	return ui.New(a.cfg, db, uiEvents, commands, control).Run(ctx)
}

func sendClientEvent(ctx context.Context, events chan<- tgclient.Event, event tgclient.Event) {
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

func replyControl(event ui.ControlEvent, err error) {
	if event.Reply == nil {
		return
	}
	select {
	case event.Reply <- err:
	default:
	}
}

func applyStoredTelegramConfig(ctx context.Context, db *storage.DB, cfg *config.Config) error {
	if db == nil || cfg == nil {
		return nil
	}
	if !cfg.AuthModeFromEnv {
		if value, ok, err := db.GetSetting(ctx, settingTelegramAuthMode); err != nil {
			return err
		} else if ok {
			mode, err := config.ParseAuthMode(value)
			if err != nil {
				return err
			}
			cfg.AuthMode = mode
		}
	}
	if !cfg.APIIDFromEnv {
		if value, ok, err := db.GetSetting(ctx, settingTelegramAPIID); err != nil {
			return err
		} else if ok && value != "" {
			apiID, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("parse saved Telegram API ID: %w", err)
			}
			cfg.APIID = apiID
		}
	}
	if !cfg.APIHashFromEnv {
		if value, ok, err := db.GetSecretSetting(ctx, settingTelegramAPIHash); err != nil {
			return err
		} else if ok {
			cfg.APIHash = value
		}
	}
	if !cfg.PhoneFromEnv {
		if value, ok, err := db.GetSecretSetting(ctx, settingTelegramPhone); err != nil {
			return err
		} else if ok {
			cfg.Phone = value
		}
	}
	if !cfg.BotTokenFromEnv {
		if value, ok, err := db.GetSecretSetting(ctx, settingTelegramBotToken); err != nil {
			return err
		} else if ok {
			cfg.BotToken = value
		}
	}
	return nil
}

func saveTelegramConfig(ctx context.Context, db *storage.DB, cfg config.Config) error {
	if db == nil {
		return nil
	}
	if err := db.SetSetting(ctx, settingTelegramAuthMode, string(cfg.AuthMode)); err != nil {
		return err
	}
	if err := db.SetSetting(ctx, settingTelegramAPIID, strconv.Itoa(cfg.APIID)); err != nil {
		return err
	}
	if err := db.SetSecretSetting(ctx, settingTelegramAPIHash, cfg.APIHash); err != nil {
		return err
	}
	if cfg.AuthMode == config.AuthBot {
		if err := db.SetSecretSetting(ctx, settingTelegramBotToken, cfg.BotToken); err != nil {
			return err
		}
		return db.DeleteSettings(ctx, settingTelegramPhone)
	}
	if err := db.SetSecretSetting(ctx, settingTelegramPhone, cfg.Phone); err != nil {
		return err
	}
	return db.DeleteSettings(ctx, settingTelegramBotToken)
}

func clearTelegramAuth(ctx context.Context, db *storage.DB, cfg config.Config) error {
	if err := clearTelegramSessionFiles(cfg.Paths.SessionDir); err != nil {
		return err
	}
	if db == nil {
		return nil
	}
	return db.DeleteSettings(ctx,
		settingTelegramAuthMode,
		settingTelegramAPIID,
		settingTelegramAPIHash,
		settingTelegramPhone,
		settingTelegramBotToken,
	)
}

func clearTelegramSessionFiles(sessionDir string) error {
	entries, err := os.ReadDir(sessionDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(sessionDir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func promptPassphrase() (string, error) {
	fmt.Fprint(os.Stderr, "Tsumugi local database passphrase: ")
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(value) == 0 {
		return "", fmt.Errorf("empty passphrase")
	}
	return string(value), nil
}
