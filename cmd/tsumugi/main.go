package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"

	"github.com/nemo/Tsumugi/internal/app"
	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/debuglog"
	"github.com/nemo/Tsumugi/internal/migrate"
	"github.com/nemo/Tsumugi/internal/version"
	"github.com/nemo/Tsumugi/internal/xdg"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrate(os.Args[2:], os.Stdout, os.Stderr))
	}
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
	flag.StringVar(&flags.ConfigPath, "config-dir", "", "keep every file under <dir>/tsumugi (database, sessions, media cache, logs) instead of the default locations")
	flag.StringVar(&flags.APIHash, "api-hash", "", "Telegram API hash")
	flag.StringVar(&flags.BotToken, "bot-token", "", "Telegram bot token")
	flag.StringVar(&flags.Phone, "phone", "", "Telegram phone number for user login")
	flag.StringVar(&flags.Proxy, "proxy", "", "proxy URL: socks5://, http://, mtproxy://")
	flag.IntVar(&flags.APIID, "api-id", 0, "Telegram API ID")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "Usage:")
		fmt.Fprintln(out, "  tsumugi [flags]")
		fmt.Fprintln(out, "  tsumugi migrate [--dry-run] [--from path]   move files from the locations older releases used")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Flags:")
		flag.PrintDefaults()
	}
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
	// Before the TUI takes the screen: printed any later, it would land in the frame tview draws.
	for _, item := range cfg.Paths.Legacy {
		fmt.Fprintln(os.Stderr, item.Notice())
	}
	debuglog.SetPath(cfg.Paths.DebugLog)
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

const migrateHelp = `Usage: tsumugi migrate [--dry-run] [--from path]

Moves Tsumugi's files from the locations older releases used to the XDG layout
under your home directory, on every OS:

  database, Telegram sessions   $XDG_DATA_HOME/tsumugi   (~/.local/share/tsumugi)
  media cache                   $XDG_CACHE_HOME/tsumugi  (~/.cache/tsumugi)
  debug logs                    $XDG_STATE_HOME/tsumugi  (~/.local/state/tsumugi)

Older releases kept the database, sessions and media cache in os.UserCacheDir:
%LOCALAPPDATA%\tsumugi on Windows, ~/Library/Caches/tsumugi on macOS,
~/.cache/tsumugi on Linux. The debug logs went to the working directory, so
migrate looks for them in the current directory unless --from says otherwise.

Nothing is overwritten. When the same file is already at the new location, the
old copy is deleted; when the content differs, both are kept, the conflict is
reported, and migrate exits 1 once everything else is done. Old directories left
empty are removed. Absolute paths stored inside the database are rewritten; that
needs the database key, and if it is in neither the OS keyring nor
TSUMUGI_PASSPHRASE, tsumugi finishes the rewrite the next time it starts.

migrate refuses while tsumugi is running: it asks SQLite for an exclusive lock
on the database, which a running tsumugi holds open.

Flags:
`

func runMigrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "print the plan, including what is skipped and why, and change nothing")
	from := fs.String("from", "", "where to look for old debug logs: a directory, or one of debug-tsumugi.log and tsumugi-debug-mem.jsonl (default: the current directory)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), migrateHelp)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tsumugi migrate: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
	env, err := xdg.Current()
	if err != nil {
		fmt.Fprintf(stderr, "tsumugi migrate: %v\n", err)
		return 1
	}
	cwd, _ := os.Getwd()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return migrate.Run(ctx, migrate.Options{
		Env:       env,
		Cwd:       cwd,
		From:      *from,
		DryRun:    *dryRun,
		Out:       stdout,
		Err:       stderr,
		MasterKey: migrate.KeyringOrPassphrase,
	})
}
