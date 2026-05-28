# Repository Instructions

This repository is now Tsumugi, a Telegram TUI client written in Go. It uses
`gotd/td` as the Telegram MTProto backend and `tview`/`tcell` for the terminal
UI. Keep it friendly to future agents: prefer simple defaults, single-file
release artifacts, explicit versioning, and testable UI behavior.

## Quick Commands

Use the China-friendly Go module proxy when running Go commands in constrained
networks:

```pwsh
$env:GOPROXY = 'https://goproxy.cn,direct'
```

For one-off PowerShell commands:

```pwsh
$env:GOPROXY='https://goproxy.cn,direct'; go test ./...
$env:GOPROXY='https://goproxy.cn,direct'; New-Item -ItemType Directory -Force -Path .\dist | Out-Null; go build -trimpath -o .\dist\app.exe .
$env:GOPROXY='https://goproxy.cn,direct'; New-Item -ItemType Directory -Force -Path .\dist | Out-Null; go build -trimpath -ldflags="-s -w" -o .\dist\app.exe .
```

Bash equivalents:

```bash
export GOPROXY='https://goproxy.cn,direct'
go test ./...
mkdir -p ./dist && go build -trimpath -o ./dist/app .
mkdir -p ./dist && go build -trimpath -ldflags="-s -w" -o ./dist/app .
```

Before relying on a command, adjust `app`, module paths, package paths, and
artifact names to match the concrete repository created from this template.

## Go And Dependencies

Follow `go.mod` as the source of truth for the Go version and dependencies.
Do not copy Go versions, module paths, or dependency constraints from other
projects unless they are intentionally part of this template.

Prefer pure Go dependencies and `CGO_ENABLED=0` for the default build so the
application can ship as one executable without runtime sidecars. If a feature
requires cgo, native tools, or bundled native assets, document that exception,
fail fast when the required compiler/toolchain is missing, and keep the default
path as portable as possible.

Avoid adding runtime data files beside the executable. If static assets are
needed, prefer `go:embed` behind clear package boundaries or build tags.

## TUI Conventions

Centralize colors and styles instead of scattering ad hoc `tcell.Style` values
through the codebase. For `tview`, use `tview.Styles`, `tcell.GetColor`, or a
small local theme helper consistently.

For `tview` applications, do not mutate UI primitives directly from background
goroutines. Send work back through `Application.QueueUpdateDraw` or an
equivalent event channel owned by the UI loop.

Treat keyboard, mouse, resize, and focus behavior as part of the product
contract. When enabling mouse or focus events, handle resize synchronization
and document visible keybindings/mouse behavior in `README.md`.

Keep rendering helpers pure where practical. Formatting rows, truncation,
column-width calculations, footer text, key encoding, and state transitions
should be testable without opening a real terminal.

When raw `tcell` drawing may display CJK or other wide Unicode text, use
display-width-aware helpers such as `uniseg.StringWidth` rather than byte
length or rune count.

## Single-File Builds And Packaging

The default release target is one executable. Sidecar files are allowed only
when explicitly documented as a feature requirement.

Use release builds like:

```bash
mkdir -p dist
go build -trimpath -ldflags="-s -w ${VERSION_LDFLAGS}" -o "dist/${APP_NAME}" ./cmd/${APP_NAME}
```

Keep generated artifacts under `dist/`. Include checksums for release
artifacts and archive outputs predictably, for example `.tar.gz` on Unix-like
targets and `.zip` or `.tar.gz` on Windows according to the repository's
documented convention.

In CI, use `actions/setup-go` with `go-version-file: go.mod`, run
`go test ./...` before release builds, and use `fetch-depth: 0` whenever
versioning depends on git tags or commit counts.

Keep cross-build matrices opt-in. Do not blindly build every target from
`go tool dist list`; new Go releases may add targets that the repository has
not validated.

## Versioning

Use this version format for build and release artifacts:

```text
v{major}.{minor}.{patch}-{branch}-{commit12}[-dirty]
```

Derive `major.minor.patch` from the nearest exact semver tag matching
`vMAJOR.MINOR.PATCH`, plus the number of commits since that tag. Ignore
prerelease/dev tags such as `v0.1.2-dev-abcdef123456` for this calculation.

For a brand-new project, use `v0.0.1` as the local fallback base before any
exact semver tag exists. This fallback is only for local/dev builds, not for a
package-manager update channel.

Once the project has reached an MVP that the user is satisfied with, and
especially before adding CI prereleases, package-manager manifests, or Scoop
update support, ask the user whether to start the formal version line. If
approved, create the first exact semver tag, usually `v0.0.1` for early
prereleases or `v0.1.0` for an MVP baseline.

After the first exact semver tag exists, CI prereleases must use the normal
version format and advance the patch number from the nearest exact semver tag
plus commits since that tag. This keeps prerelease builds detectable by package
managers such as Scoop. Dev prerelease tags like `v0.0.2-dev-abcdef123456`
must not be used as the base for future patch calculations.

To bump major/minor or reset the patch base, create and push a new exact semver
tag on the desired base commit, for example `v0.2.0`. That tagged commit builds
as `v0.2.0-...`; the next commit builds as `v0.2.1-...`.

Sanitize branch names before placing them in version strings or artifact names.
Use a 12-character commit hash and append `-dirty` when uncommitted changes are
part of the build.

Inject build metadata with `-ldflags -X` into a small package such as
`internal/version` or `internal/buildinfo`. That package should fall back to
`debug.ReadBuildInfo()` when ldflags are not provided so `go run` and local
builds still report useful VCS metadata.

Expose the build version through a `version` command or `--version` flag before
launching the TUI.

## Tests

Run `go test ./...` before handing off substantive changes.

Prefer focused tests around pure helpers and visible behavior:

- formatting, truncation, column widths, keybinding labels, and footer text
- version formatting, branch sanitization, dirty flag parsing, and build-info
  fallbacks
- command help, flags, and Cobra completion behavior when the app uses Cobra
- headless TUI behavior through `tcell.NewSimulationScreen`, synthetic keys,
  synthetic mouse events, or `tview.Application` tests where appropriate

Do not require a real terminal for ordinary unit tests.

## Documentation And Change Hygiene

After changing commands, flags, keybindings, visible TUI workflows, build
scripts, release packaging, or versioning behavior, check whether `README.md`
or other user-facing docs need to be updated.

Keep comments that explain intent. If code changes make a comment stale,
rewrite the comment instead of deleting useful context.

Keep unrelated edits out of each change. When the user asks for a commit, commit
only the logical unit of work and avoid unrelated dirty files.

Do not copy product-specific names or behavior from source projects into repos
created from this template. Replace module paths, env prefixes, binary names,
artifact names, and domain-specific rules with names that belong to the new
repository.
