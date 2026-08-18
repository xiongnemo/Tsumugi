package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func ctrlKey(key tcell.Key, mods tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(key, 0, mods)
}

// App.commands is send-only, so a test that wants to read it back needs its own handle.
func newSendTestApp() (*App, <-chan telegram.Command) {
	app := newSuggestionTestApp()
	cmds := make(chan telegram.Command, 4)
	app.commands = cmds
	return app, cmds
}

// The reason app.go's InputField type assertion had to be replaced: a TextArea fails it, and
// the main-shell guard does not bail either because the composer is in its allowed set. Every
// typed rune would reach the global switch, so typing "quick" would quit the app.
func TestCaptureLeavesTypedRunesToTheComposer(t *testing.T) {
	app := newSuggestionTestApp()

	for _, r := range []rune{'q', 'i', 'j', 'k', 'D', 'O', 'L', 'R', 'n', '/', '#', '?'} {
		event := tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
		if got := app.capture(event); got != event {
			t.Fatalf("capture(%q) = %v, want the event passed through to the composer", r, got)
		}
	}
}

func TestComposerSendKey(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		mods tcell.ModMask
		want bool
	}{
		{name: "ctrl+enter sends", key: tcell.KeyEnter, mods: tcell.ModCtrl, want: true},
		{name: "alt+enter sends", key: tcell.KeyEnter, mods: tcell.ModAlt, want: true},
		{name: "ctrl+j sends", key: tcell.KeyCtrlJ, mods: tcell.ModCtrl, want: true},
		{name: "bare ctrl+j sends", key: tcell.KeyCtrlJ, mods: tcell.ModNone, want: true},
		{name: "plain enter is a newline", key: tcell.KeyEnter, mods: tcell.ModNone, want: false},
		{name: "shift+enter is a newline", key: tcell.KeyEnter, mods: tcell.ModShift, want: false},
		{name: "tab is not a send", key: tcell.KeyTAB, mods: tcell.ModNone, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composerSendKey(ctrlKey(tt.key, tt.mods)); got != tt.want {
				t.Fatalf("composerSendKey = %v, want %v", got, tt.want)
			}
		})
	}
	if composerSendKey(nil) {
		t.Fatal("composerSendKey(nil) = true")
	}
}

func TestCaptureCtrlJSendsMessage(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("  hello\nworld  ", true)

	if got := app.capture(ctrlKey(tcell.KeyCtrlJ, tcell.ModCtrl)); got != nil {
		t.Fatalf("Ctrl+J returned %v, want consumed", got)
	}
	select {
	case cmd := <-cmds:
		if cmd.Kind != telegram.CommandSendText {
			t.Fatalf("kind = %v, want send_text", cmd.Kind)
		}
		// Interior newlines survive; only the surrounding whitespace is trimmed.
		if cmd.Text != "hello\nworld" {
			t.Fatalf("text = %q, want %q", cmd.Text, "hello\nworld")
		}
	default:
		t.Fatal("no command was sent")
	}
	if app.composer.GetText() != "" {
		t.Fatalf("composer = %q, want cleared", app.composer.GetText())
	}
}

func TestCapturePlainEnterReachesComposerAsNewline(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("line", true)

	event := ctrlKey(tcell.KeyEnter, tcell.ModNone)
	if got := app.capture(event); got != event {
		t.Fatalf("plain Enter = %v, want passed through", got)
	}
	select {
	case cmd := <-cmds:
		t.Fatalf("plain Enter sent %v, want nothing", cmd.Kind)
	default:
	}
}

// Ctrl+Enter must be tested before the suggestion panel, which matches bare Enter with no
// modifier check and would otherwise accept a suggestion instead of sending.
func TestCtrlEnterSendsWhileSuggestionPanelIsOpen(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("@al", true)
	app.suggest.mode = composeSuggestMentions
	app.suggest.token = composeToken{Mode: composeSuggestMentions, Start: 0, End: 3, Query: "al"}
	app.suggest.items = []composeSuggestItem{
		{Kind: composeSuggestMentions, Main: "Alice", Insert: "@alice", Mention: telegram.MentionSuggestion{Username: "alice", Name: "Alice"}},
	}
	app.renderComposeSuggestions()

	if got := app.capture(ctrlKey(tcell.KeyEnter, tcell.ModCtrl)); got != nil {
		t.Fatalf("Ctrl+Enter returned %v, want consumed", got)
	}
	select {
	case cmd := <-cmds:
		if cmd.Text != "@al" {
			t.Fatalf("text = %q, want the typed token sent as-is, not the completion", cmd.Text)
		}
	default:
		t.Fatal("no command was sent; the suggestion panel swallowed Ctrl+Enter")
	}
}

func TestComposerTextRows(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		innerWidth int
		want       int
	}{
		{name: "empty", text: "", innerWidth: 40, want: 1},
		{name: "single short line", text: "hello", innerWidth: 40, want: 1},
		{name: "three hard breaks", text: "a\nb\nc", innerWidth: 40, want: 3},
		{name: "wraps at width", text: strings.Repeat("x", 25), innerWidth: 10, want: 3},
		// CJK is double-width, so ten glyphs need twenty columns and wrap twice at width 10.
		{name: "cjk counts display width", text: strings.Repeat("中", 10), innerWidth: 10, want: 2},
		{name: "clamped to max", text: strings.Repeat("a\n", 20), innerWidth: 40, want: composerMaxTextRows},
		{name: "no geometry yet counts hard breaks", text: strings.Repeat("x", 500) + "\ny", innerWidth: 0, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composerTextRows(tt.text, tt.innerWidth); got != tt.want {
				t.Fatalf("composerTextRows(%q, %d) = %d, want %d", tt.text, tt.innerWidth, got, tt.want)
			}
		})
	}
}

// The stack height must be derived, not hardcoded, or the panel and the composer fight over
// the same rows.
func TestComposeStackRowsIsPanelPlusComposerPlusGhost(t *testing.T) {
	app := newSuggestionTestApp()
	app.suggest.panelRows = 7

	want := 7 + app.composerBoxRows() + composeGhostRows
	if got := app.composeStackRows(); got != want {
		t.Fatalf("composeStackRows = %d, want %d", got, want)
	}
}

func TestParseComposeTokenIsCursorAware(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		cursor int
		want   composeToken
	}{
		{
			name: "mention on the second line", text: "first line\n@al", cursor: 14,
			want: composeToken{Mode: composeSuggestMentions, Start: 11, End: 14, Query: "al"},
		},
		{
			name: "command on the second line", text: "hello\n/pi", cursor: 9,
			want: composeToken{Mode: composeSuggestCommands, Start: 6, End: 9, Query: "pi"},
		},
		{
			name: "inline bot on the second line", text: "hi\n@gifbot cats", cursor: 15,
			want: composeToken{Mode: composeSuggestInline, Start: 11, End: 15, Query: "cats", BotUsername: "gifbot"},
		},
		{
			// Cursor mid-buffer: the token ends at the cursor, not at the end of the text.
			name: "cursor before trailing text", text: "@al and more", cursor: 3,
			want: composeToken{Mode: composeSuggestMentions, Start: 0, End: 3, Query: "al"},
		},
		{
			name: "cursor at zero yields nothing", text: "@al", cursor: 0,
			want: composeToken{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseComposeToken(tt.text, tt.cursor); got != tt.want {
				t.Fatalf("parseComposeToken(%q, %d) = %+v, want %+v", tt.text, tt.cursor, got, tt.want)
			}
		})
	}
}
