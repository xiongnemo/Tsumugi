package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/state"
	"github.com/nemo/Tsumugi/internal/storage"
	tgclient "github.com/nemo/Tsumugi/internal/telegram"
	"github.com/nemo/Tsumugi/internal/ui"
)

type App struct {
	cfg config.Config
}

func New(cfg config.Config) *App {
	return &App{cfg: cfg}
}

func (a *App) Run(ctx context.Context) error {
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

	clientEvents := make(chan tgclient.Event, 32)
	uiEvents := make(chan tgclient.Event, 32)
	commands := make(chan tgclient.Command, 32)
	store := state.NewStore()
	client := tgclient.NewGotdClient(a.cfg, db)

	go func() {
		if err := client.Run(ctx, clientEvents, commands); err != nil && ctx.Err() == nil {
			clientEvents <- tgclient.Event{Kind: tgclient.EventError, Error: err}
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-clientEvents:
				store.Apply(event)
				uiEvents <- event
			}
		}
	}()

	return ui.New(a.cfg, db, uiEvents, commands).Run(ctx)
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
