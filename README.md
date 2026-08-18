# Tsumugi

**English** · [简体中文](README.zh-CN.md)

Tsumugi is a Telegram client that runs in your terminal.

It uses `gotd/td` as a pure Go MTProto backend and `tview`/`tcell` for the
terminal UI. User login and bot login use different authentication flows, then
share the same Telegram API adapter after authentication. Everything ships as one
static executable with no runtime dependencies — `ffmpeg` is optional and only
affects inline video animation.

## Install

### Scoop (Windows)

```pwsh
scoop bucket add nemo https://github.com/xiongnemo/windows-binaries-scoop-bucket
scoop install nemo/tsumugi-nightly
```

This tracks `dev` prereleases, which is currently the only release channel.

### Prebuilt binaries

Download an archive from [Releases](https://github.com/xiongnemo/Tsumugi/releases)
and extract the single executable. Assets are named
`tsumugi_<version>_<os>_<arch>` (`.zip` for Windows, `.tar.gz` elsewhere) and each
release ships a `checksums_<version>.txt`. Builds are published for Windows,
Linux, macOS, FreeBSD, and OpenBSD on `amd64` and `arm64`.

### From source

Requires Go 1.26 or newer:

```bash
git clone https://github.com/xiongnemo/Tsumugi
cd Tsumugi
go build -trimpath -o tsumugi ./cmd/tsumugi
```

`go install` from a module path does not work yet: the module is declared as
`github.com/nemo/Tsumugi` while the repository lives at
`github.com/xiongnemo/Tsumugi`, so Go cannot resolve it. Clone and build instead.

## Quick start

1. Create Telegram API credentials at <https://my.telegram.org/apps>. You need
   the **API ID** and **API hash**.
2. Run `tsumugi`. With no credentials saved, the first-run wizard asks for your
   language, login mode, API ID/hash, and phone number, then hands off to
   Telegram's code and 2FA prompts.
3. `Tab` cycles panes: folders → chats → messages → composer. `Enter` opens the
   highlighted chat, `i` jumps to the composer, `?` opens settings, `q` quits.
4. Credentials are saved locally, encrypted, so later starts go straight to the
   chat list.

The full key list is under [Keybindings](#keybindings). Nothing needs to be
configured up front — the sections below are for proxies, scripted setups, and
behaviour you may want to change.

## Features

- User login with phone/code/2FA through `gotd/td`.
- Bot login with a bot token through the same backend.
- QR login as an alternative to phone + code: pick **QR code** as the login method in
  the first-run wizard (or set `TSUMUGI_LOGIN_METHOD=qr`), then scan the code with
  Telegram on your phone under Settings → Devices → Link Desktop Device. The code
  refreshes itself, `y` copies the raw `tg://login` URL if your terminal renders it badly,
  and `p` falls back to phone login. Accounts with two-step verification are prompted for
  the cloud password after scanning.
- Shared event pipeline for connection status, dialogs, and normalized app
  state.
- SQLite-backed local storage for accounts, peers, messages, sync metadata,
  proxy profiles, and settings; media preview files are cached on disk.
- Application-layer encryption for sensitive local data. Tsumugi stores the
  master key in the OS keyring when possible; if the keyring is unavailable,
  set `TSUMUGI_PASSPHRASE` so a key can be derived with Argon2id.
- TUI shell with folder rail, chat list, selectable message pane, composer,
  status/footer, keybindings, a settings hub, and a proxy settings modal.
- Global pinned chats in the **All** folder (Saved Messages and other main-list
  pins), synced from Telegram and shown at the top of the chat list with a pin
  marker.
- Telegram service (system) messages — joins and leaves, title/photo changes,
  pins, history clears, calls, auto-delete timers and so on — rendered inline as
  dim single-line entries and shown in chat-list previews. Actions Tsumugi has no
  dedicated wording for still appear as a generic system message rather than
  silently disappearing.
- First-run onboarding wizard for language, Telegram login mode, API
  credentials, and phone/bot token setup when credentials are incomplete.
- Proxy support for SOCKS5, HTTP CONNECT, and Telegram MTProxy.
- Proxy profile management in the TUI. Environment proxy entries are shown as
  read-only profiles so you can tell when connectivity comes from environment
  variables instead of the local database.
- Chat history loading with a DB-first path, Telegram `messages.getHistory`
  refresh when a chat is opened, automatic gap fill for discontinuous cached
  history, and lazy startup sync (dialog metadata plus optional recent-peer
  backfill). Set `TSUMUGI_SYNC_MODE=full` to restore the older all-peer history
  sweep on startup.
- Single-line status bar: connection (left), foreground network activity
  (center), background tasks (right, only when running). Chat summary and
  history gap hints appear in the message pane title.
- Text message sending for the selected peer, including reply targets from the
  message action menu.
- Per-chat drafts, synced with Telegram. Unsent text is saved as you type, restored
  when you reopen the chat, and cleared once the message goes out. Drafts are stored
  encrypted locally so they survive a quit, and a draft changed in another client is
  picked up without ever overwriting text you are currently typing.
- Unread handling: opening a chat marks it read and lands on the first unread message,
  with a `-- Unread messages` rule above it. **`End`** or **`G`** returns to the latest
  messages; the pane title says so while you are looking at older history. Turn the jump
  off in Settings → General, or with `TSUMUGI_JUMP_UNREAD=0`.
- Typing indicators both ways. Typing in the composer notifies the peer, and someone
  typing, recording audio, or uploading a file in the open chat is shown in the
  message pane title next to the chat name.
- Forward messages. **`v`** marks the selected message (a `✓` appears in the gutter),
  **`f`** opens a destination picker, and **`F`** forwards without the original author.
  With nothing marked, `f` forwards the message under the cursor. Saved Messages is the
  first destination in the list. Marks are dropped when they scroll out of the loaded
  window, and Tsumugi says how many — you can never forward a message you cannot see.
- Compose suggestions for `@` mentions, `@inline_bot query` inline results, and
  `/` bot commands in the current chat. Inline bot results render as a grid of
  thumbnails rather than a list.
- Message search in three scopes, picked from a dropdown in the search form: the open
  chat, the chat list, or **all chats** (**`G`**). In-chat search looks at the loaded
  window first and only asks Telegram when that finds nothing. Results open in a list;
  **`Enter`** jumps to one, and **`n`** / **`N`** step forward and backward through them
  with wrap-around. A chat-list match outside the current folder switches to the folder
  that contains it instead of refusing.
- Reactions: existing reactions appear under a message, and **`R`** opens a panel
  showing counts and recent reactors with number-key quick picks.
- Read state per peer type: `✓`/`✓✓` for private chats, a read count for groups,
  and view counts (👁) for broadcast channel posts.
- Pinned messages: the newest pin shows as a banner above the conversation, and
  **`#`** expands it into the full pinned list with jump-to-message.
- Jump to a message by loading a window centred on it, used by the pinned list and
  by "jump to replied message". Targets far outside the loaded history arrive in
  one request rather than by paging backwards.
- Switchable outgoing message layout (**`L`**): `transcript` (single column) or
  `im` (incoming left, outgoing right).
- Media classification for emoji-preserving text, static stickers, animated
  stickers, video stickers, GIF animations, videos, photos, and documents.
- Media cache/opening boundaries and a pure Go terminal half-block renderer for
  image/sticker thumbnails (no external image-to-terminal CLI).
- Bilingual interface (English and 简体中文), switchable at runtime, with
  translations in editable JSON files.

Inline previews use Unicode half-blocks with true-color ANSI, converted for
`tview`. Message detail opens a right-column preview sized from the layout after
the modal is drawn. Supported still formats match the Go decoder path (PNG,
JPEG, GIF, WebP). Video containers are not decoded inline in the terminal.
Enable **inline GIF animation** in Settings (`?` → General) to animate cached
GIF/MP4/WebM previews in the message list (Telegram GIFs and video stickers).
MP4 and WebM decoding uses **ffmpeg** in your `PATH`. This redraws periodically
and can use more CPU on busy chats. Without ffmpeg you still get a picture: GIFs
and video stickers fall back to rendering Telegram's own JPEG thumbnail, so they
appear as a static image rather than a text label. Only the animation needs
ffmpeg. Decoded animation frames are kept in a shared
32 MB in-memory LRU cache; set `TSUMUGI_INLINE_ANIM_CACHE_MB` to a positive
integer to raise or lower that budget. You can also set `TSUMUGI_INLINE_ANIM=1`
before first launch when the setting is not yet stored in SQLite.

Media-only messages (polls, contacts, locations, and other attachment types) show
localized placeholders in the chat list and message view instead of `(empty message)`.

## Settings

Press **`?`** to open the settings hub. The first release includes:

- **General**: interface language (`en` / `zh`), inline GIF animation, and outgoing message layout (`transcript` / `im`)
- **Account**: current login mode and local Telegram logout
- **Network**: proxy profile management (same UI as **`P`**)

Settings are stored in the local SQLite `settings` table and apply immediately
for locale and inline animation. Telegram credentials collected by onboarding
are stored locally, with secrets protected by the same application-layer
encryption used for message content. Proxy changes still require reconnecting
when the active profile changes.

The **Account** page includes a non-destructive Telegram logout. Logout clears
only the saved Telegram login credentials and local Telegram session files, then
returns to onboarding/login. Local SQLite history, media cache, settings, and
proxy profiles are kept.

Translations live in `internal/i18n/locales/` (`en.json`, `zh.json`). Contributors
can add or update strings in those JSON files without changing Go code; run
`go test ./internal/i18n/...` to verify key parity between locales. See
[`internal/i18n/README.md`](internal/i18n/README.md) for naming conventions and
how to add a language.

Environment fallbacks apply only when a value is not yet saved in SQLite:

```pwsh
$env:TSUMUGI_LOCALE = 'zh'
$env:TSUMUGI_INLINE_ANIM = '1'
$env:TSUMUGI_OUTGOING_LAYOUT = 'im'
```

In the message pane, press **`L`** to toggle outgoing layout (`transcript` = left-aligned with `>` prefix, `im` = incoming left / outgoing right). Press **`R`** on a selected message to view reactions and send a quick emoji (keys `1`–`8`). Press **`#`** to expand the pinned banner into the full list of pinned messages for the chat; `Enter` jumps to one, `Esc` closes. Broadcast channel posts show view counts (👁) instead of private-chat read receipts.

Telegram API credentials, phone numbers, and bot tokens can be entered in the
first-run onboarding wizard or supplied through env/CLI. The database passphrase
remains env/prompt-only and is not exposed in the settings UI.

## Configuration

Create Telegram API credentials at <https://my.telegram.org/apps>. If required
Telegram credentials are missing, Tsumugi opens an onboarding wizard in the TUI
and delays the Telegram connection until setup is complete.

Common environment variables still work and override saved onboarding values:

```pwsh
$env:TSUMUGI_API_ID = '123456'
$env:TSUMUGI_API_HASH = 'your_api_hash'
$env:TSUMUGI_AUTH_MODE = 'user'
$env:TSUMUGI_PHONE = '+15551234567'
```

If your OS keyring is unavailable, Tsumugi prompts for a local database
passphrase before starting the TUI. You can also provide it ahead of time:

```pwsh
$env:TSUMUGI_PASSPHRASE = 'choose-a-long-local-passphrase'
```

Memory diagnostics are opt-in and write JSON lines to `tsumugi-debug-mem.jsonl`
in the working directory, keeping the TUI stderr clean. Use the pprof address
when you need heap/profile verification alongside the periodic counters:

```pwsh
$env:TSUMUGI_DEBUG_MEM = '1'       # periodic MemStats, viewport, cache, and gap-fill counts
$env:TSUMUGI_PPROF_ADDR = ':6060'  # enable net/http/pprof on this address
$env:TSUMUGI_INLINE_ANIM_CACHE_MB = '64'
```

For bot mode:

```pwsh
$env:TSUMUGI_AUTH_MODE = 'bot'
$env:TSUMUGI_BOT_TOKEN = '123456:bot-token'
```

CLI flags can also provide the same values:

```pwsh
go run ./cmd/tsumugi --login=user --api-id=123456 --api-hash=your_api_hash --phone=+15551234567
go run ./cmd/tsumugi --login=bot --api-id=123456 --api-hash=your_api_hash --bot-token=123456:bot-token
```

Use `--config-dir=<path>` to override where Tsumugi stores config-derived
directories. Tsumugi does not read a config file yet.

Bot mode is a bot console, not a full personal Telegram client. Telegram only
sends chats and updates that the bot is allowed to receive.

## Proxy

Proxy can be configured with `--proxy` or environment variables:

```pwsh
go run ./cmd/tsumugi --proxy=socks5://user:pass@127.0.0.1:1080
go run ./cmd/tsumugi --proxy=http://user:pass@127.0.0.1:8080
go run ./cmd/tsumugi --proxy=mtproxy://0123456789abcdef0123456789abcdef@proxy.example:443
```

Environment lookup order:

```text
TSUMUGI_PROXY
TSUMUGI_SOCKS5_PROXY
TSUMUGI_HTTP_PROXY
TSUMUGI_MTPROXY
HTTPS_PROXY
HTTP_PROXY
ALL_PROXY
```

When an environment proxy is active, the proxy settings view shows a special
read-only `Environment` entry, for example
`Environment: ALL_PROXY -> SOCKS5 127.0.0.1:11085`. Credentials are masked in
UI labels. Editable profiles are stored in the local SQLite database.

## Raw Linux console

Tsumugi is meant to be usable on a bare Linux text-mode console (`Ctrl+Alt+F3`,
no terminal emulator), not just inside one. What that costs and what Tsumugi does
about it:

- **Keys.** `Ctrl+J` sends, because it is a control character every terminal can
  emit. `Ctrl+Enter` and `Alt+Enter` are aliases that only work where the
  terminal can encode them.
- **No mouse.** Every overlay and the inline result grid are fully
  keyboard-reachable.
- **Glyphs.** The console font has no emoji and no CJK, so message text in those
  scripts renders as blanks — that cannot be fixed from inside the program.
  Tsumugi's own markers (pins, view counts, read receipts, selection marks) fall
  back to ASCII automatically on `TERM=linux` or in a non-UTF-8 locale. Force it
  anywhere with `TSUMUGI_ASCII=1`. Half-block glyphs are in the standard console
  font, so image previews and the login QR code still work.
- **80x25.** Below roughly 90 columns the folder rail is hidden (still reachable
  with `Tab`), and below about 70 the chat list narrows too, so the message pane
  keeps at least 40 columns. The inline result grid drops to one thumbnail per
  row.
- **~16 colors.** Previews emit 24-bit ANSI and `tcell` downsamples; output is
  rougher but correct.

## Keybindings

- `Tab`: switch focus between folders, chat list, message view, and composer
- `Shift+Tab`: move focus to the previous pane
- `j`/`k` or arrow keys in message view: select messages
- `Enter`: open chat, send composer text, or open the selected message action
  menu
- `PgUp` / `PgDn` in message view: page the scroll; the highlight moves to the
  **first** visible message after scrolling up, or the **last** visible message
  after scrolling down (same for the mouse wheel). At the very top, `PgUp` also
  loads older history. Inline media preview height is stabilized so the composer
  and scroll position do not jump while previews load.
- `End`: jump to the latest messages, move the highlight to the last line, and
  clear the "new below" indicator
- When you are scrolled up, new incoming messages are counted in the message
  pane title (for example `Chat · 3 new`) and a short status line; go to the
  bottom to clear them
- In the message list, timestamps from **today** show `HH:MM` only; older
  messages include the calendar date (`YYYY-MM-DD HH:MM`) so mixed-day threads
  stay readable
- `i`: focus composer
- In the composer, `Enter` inserts a new line and **`Ctrl+J`** sends. `Ctrl+Enter`
  and `Alt+Enter` also send where the terminal delivers them — Windows Terminal
  gives you `Ctrl+Enter`, a bare Linux console gives you `Alt+Enter`, and `Ctrl+J`
  works everywhere because it is a literal control byte no terminal can intercept.
  The composer grows from 1 to 6 text rows as you type, and a multi-line paste is
  inserted as one block rather than sending on the first newline.
- In the composer, type `@` for mention suggestions, `@inline_bot query` for
  inline bot results, or `/` for current-chat bot commands. When the inline
  suggestion panel is open, `Up`/`Down` changes the highlighted row, `Tab` or
  `Enter` accepts it, and `Esc` closes the panel without clearing typed text.
  Rows can also be clicked with the mouse.
- `/`: search the focused view
- `n` / `N`: next / previous search result (wraps)
- `G`: search all chats (chat list / folders), or return to the latest messages (message pane)
- `v`: mark/unmark the selected message for forwarding
- `f` / `F`: forward marked messages (`F` drops the original author)
- `End` / `G`: return to the latest messages while viewing older history
- `L`: toggle outgoing message layout (message pane)
- `R`: open the reaction panel for the selected message
- `#`: expand the pinned banner into the full pinned message list
- `D`: download/cache the selected message media preview
- `O`: open the selected message media preview externally
- `?`: open settings (General, Account, Network)
- `P`: show proxy settings
- `Esc`: close modal and return to chat list
- `q` or `Ctrl+C`: quit

Inline bot results are shown as a grid of thumbnails rather than a list, because
GIF bots return no titles and a list of identical rows gives you nothing to
choose between. Each cell renders the result's JPEG thumbnail with the pure Go
half-block renderer — the full animation is never downloaded just to preview it.
Arrow keys move left/right within a row and up/down by a whole row; `Enter` or
`Tab` sends the highlighted result, and clicking a cell picks it.

Compose suggestion limits: mention and command panels show at most five visible
entries. Inline bots that require location are reported as unsupported in this
MVP.

## Not there yet

Known gaps, so you can tell a missing feature from a bug:

- Repeat-search navigation (`n`/`N`) is not implemented; search jumps to a match
  but does not step through the rest.
- Sending is text-only. There is no upload path for photos, files, or voice.
- No message editing, forwarding, or search across all chats.
- Read receipts for supergroups are not wired up (private chats, groups, and
  broadcast channels are).
- Animated stickers (`.tgs` Lottie) show a static preview or placeholder; there is
  no terminal Lottie renderer.
- Inline animation of MP4/WebM needs `ffmpeg` in `PATH`. Without it those messages
  still show a static thumbnail.
- No QR login yet; user login is phone + code + 2FA.
- Tsumugi does not read a config file. Settings live in SQLite, with environment
  variables and CLI flags as overrides.
- Bot mode is a bot console, not a personal client: Telegram only delivers what a
  bot is allowed to receive.

## Development

Use the Go module proxy recommended for constrained networks:

```pwsh
$env:GOPROXY='https://goproxy.cn,direct'; go test ./...
```

Build a single executable:

```pwsh
$env:GOPROXY='https://goproxy.cn,direct'; New-Item -ItemType Directory -Force -Path .\dist | Out-Null; go build -trimpath -o .\dist\tsumugi.exe .\cmd\tsumugi
```

Bash equivalents:

```bash
export GOPROXY='https://goproxy.cn,direct'
go test ./...
mkdir -p ./dist && go build -trimpath -o ./dist/tsumugi ./cmd/tsumugi
```

Release builds are `CGO_ENABLED=0` and stamp the version through `-ldflags -X`
into `internal/version`, producing
`v{major}.{minor}.{patch}-{branch}-{commit12}[-dirty]`. `tsumugi --version` prints
it. Without ldflags the binary falls back to `debug.ReadBuildInfo()`, so `go run`
still reports useful VCS metadata.

See [`AGENTS.md`](AGENTS.md) for repository conventions and
[`internal/i18n/README.md`](internal/i18n/README.md) for adding translations.
