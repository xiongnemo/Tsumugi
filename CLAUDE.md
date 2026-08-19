# CLAUDE.md

Read [AGENTS.md](AGENTS.md) first. It is the single source of truth for this repository's
conventions: Go/dependency rules, TUI conventions (including the `SetInputCapture` keyboard
routing rule), single-file builds and packaging, the version string format, test expectations,
and documentation hygiene. Do not restate or fork those rules here — if a convention needs to
change, change `AGENTS.md`.

This file only records what is specific to running Claude Code on this repo.

## Shell

The default shell here is Git Bash on Windows. Use POSIX syntax, and set the module proxy —
the network needs it:

```bash
export GOPROXY='https://goproxy.cn,direct'
go test ./...
./build-dev.sh          # builds ./Tsumugi.exe and prints the version
```

`build-dev.sh` derives the version the way `release.yml` does — patch number is
the nearest exact semver tag's patch plus the commits since that tag. Passing
`-ldflags` by hand is how a build ends up mislabelled `v0.1.0` forever.

`AGENTS.md` also lists the PowerShell equivalents, which is what the user runs interactively.

## Local files to leave alone

- `nemo.secret` — real Telegram credentials for manual testing. Read-only when a manual test
  needs it, never echo it into output, logs, or status text. Gitignored via `*.secret`.
- `Tsumugi.exe` at the repo root and `dist/` — local build artifacts, gitignored.
- `debug-*.log` — NDJSON diagnostics from `internal/debuglog`, gitignored. Turn it on with
  `TSUMUGI_DEBUG=1` (override the path with `TSUMUGI_DEBUG_LOG`). Every error that reaches the
  UI is written there in full, which is the only way to read one: the status bar has a single
  truncated column.

Manual Telegram tests are env-gated: `TSUMUGI_MANUAL_SECRET=1` for read-only, and
`TSUMUGI_MANUAL_SEND=1` additionally required before anything is sent.

## Debug output

Never write debug output to stdout or stderr while the TUI is running — it lands in the same
terminal screen tview draws to and corrupts the UI. Diagnostics go to a file
(`internal/debuglog`, `TSUMUGI_DEBUG_MEM=1`, `TSUMUGI_PPROF_ADDR`), and are scaffolding to
remove once the bug they were added for is confirmed fixed.

## Docs and strings

User-visible changes touch four files, not one: `README.md`, `README.zh-CN.md`, and both
`internal/i18n/locales/en.json` + `zh.json` (key sets must match; there is a parity test).
Backend code emits `i18n.Msg{Key, Args}`, never display text — see `internal/i18n/README.md`.
