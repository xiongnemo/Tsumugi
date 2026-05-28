package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"github.com/nemo/Tsumugi/internal/app"
	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "tsumugi: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var flags config.CLIFlags
	var showVersion bool

	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&flags.AuthMode, "login", "", "login mode: user or bot")
	flag.StringVar(&flags.ConfigPath, "config-dir", "", "override config directory")
	flag.StringVar(&flags.APIHash, "api-hash", "", "Telegram API hash")
	flag.StringVar(&flags.BotToken, "bot-token", "", "Telegram bot token")
	flag.StringVar(&flags.Phone, "phone", "", "Telegram phone number for user login")
	flag.StringVar(&flags.Proxy, "proxy", "", "proxy URL: socks5://, http://, mtproxy://")
	flag.IntVar(&flags.APIID, "api-id", 0, "Telegram API ID")
	flag.Parse()

	if showVersion {
		fmt.Println(version.String())
		return nil
	}

	if envID := os.Getenv("TSUMUGI_API_ID"); flags.APIID == 0 && envID != "" {
		id, err := strconv.Atoi(envID)
		if err != nil {
			return fmt.Errorf("parse TSUMUGI_API_ID: %w", err)
		}
		flags.APIID = id
	}

	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := app.New(cfg).Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
