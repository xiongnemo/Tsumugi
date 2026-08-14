# Internationalization (i18n)

Tsumugi stores user-visible strings in embedded JSON locale files. Go code references
translation keys; text is rendered at display time so locale switches apply immediately.

## Layout

```
internal/i18n/
  locales/
    en.json      # source of truth for keys
    zh.json      # must mirror en.json keys
  catalog.go     # go:embed loader, T/Tf
  keys.go        # typed key constants
  msg.go         # Msg type for deferred rendering
  i18n.go        # SetLocale, helpers (ChatKind, FolderTitle, …)
  catalog_test.go
```

## Key naming

Use dot-separated prefixes:

| Prefix | Use |
|--------|-----|
| `ui.*` | Pane titles, placeholders, composer labels |
| `status.*` | Status bar messages |
| `action.*` | Message action menu labels |
| `search.*` | Search modal |
| `auth.*` | Login prompts |
| `proxy.*` | Proxy settings modal |
| `media.*` | Media placeholders and cache hints |
| `message.*` | Message row states (`(sending)`, `(empty)`, …) |
| `service.*` | Telegram service/system messages (joins, pins, title changes, …) |
| `settings.*` | Settings hub |

### `service.*` keys take at most one argument

A `service.*` template either contains exactly one `%s` or none, and
`telegram.classifyMessageAction` guarantees an argument is supplied precisely when the
template has one. If you translate a `service.*` string, keep the placeholder exactly as it
is in `en.json`: adding one where English has none renders a literal `%s`, and dropping one
renders a `%!(EXTRA …)` suffix. `TestServiceActionArgMatchesTemplate` checks both locales.

## Adding a string

1. Add the key and English text to `locales/en.json`.
2. Add the same key and translation to `locales/zh.json`.
3. Optionally add a constant in `keys.go`.
4. Use `i18n.T(key)` for static text or `i18n.Tf(key, args…)` for formatted text.
5. For status/events that cross layers, use `i18n.M(key, args…)` and render with `Msg.String()`.

Run tests:

```pwsh
$env:GOPROXY='https://goproxy.cn,direct'; go test ./internal/i18n/...
```

`TestLocaleKeyParity` fails if `en.json` and `zh.json` key sets differ.

## Placeholders

Formatted strings use Go `fmt` verbs (`%s`, `%d`, …). Translators must keep the same
placeholders in the same order in every locale file. `TestTfPlaceholders` checks this.

## Status messages

Backend code should emit `i18n.Msg` (via `Event.StatusMsg` or `App.setStatusMsg`), not
English literals. The UI stores the last status as a `Msg` and re-renders it when the
locale changes.

Raw error text from Telegram or I/O is wrapped with `status.error` (`%s`) rather than
reverse-translated.

## Adding a language

1. Copy `locales/en.json` to `locales/ja.json` (example).
2. Translate values; keep keys identical.
3. Add the file to the `//go:embed` directive in `catalog.go`.
4. Extend `tableFor()` and `SetLocale()` in `i18n.go`.
5. Expose the locale code in settings if users should select it.

## What we do not translate

- Telegram user/chat names and message bodies
- Folder titles supplied by Telegram (user-defined)
- Service/system messages (deferred)
