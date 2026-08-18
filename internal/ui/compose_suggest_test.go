package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func TestParseComposeToken(t *testing.T) {
	tests := []struct {
		name string
		text string
		want composeToken
	}{
		{
			name: "mention at end",
			text: "hello @ali",
			want: composeToken{Mode: composeSuggestMentions, Start: 6, End: 10, Query: "ali"},
		},
		{
			name: "slash command",
			text: "/sta",
			want: composeToken{Mode: composeSuggestCommands, Start: 0, End: 4, Query: "sta"},
		},
		{
			name: "inline bot",
			text: "@gif cats",
			want: composeToken{Mode: composeSuggestInline, Start: 5, End: 9, Query: "cats", BotUsername: "gif"},
		},
		{
			name: "normal slash search text",
			text: "hello /sta",
			want: composeToken{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseComposeToken(tt.text, len(tt.text)); got != tt.want {
				t.Fatalf("parseComposeToken(%q) = %+v, want %+v", tt.text, got, tt.want)
			}
		})
	}
}

func newSuggestionTestApp() *App {
	app := newCaptureTestApp()
	app.commands = make(chan telegram.Command, 4)
	app.statusForeground = tview.NewTextView()
	app.currentChat = "chat:1"
	app.initComposeSuggestions()
	app.app.SetFocus(app.composer)
	return app
}

func TestComposeSuggestionNavigationAndAccept(t *testing.T) {
	app := newSuggestionTestApp()
	app.composer.SetText("@al", true)
	app.suggest.mode = composeSuggestMentions
	app.suggest.token = composeToken{Mode: composeSuggestMentions, Start: 0, End: 3, Query: "al"}
	app.suggest.items = []composeSuggestItem{
		{Kind: composeSuggestMentions, Main: "Alice", Insert: "@alice", Suffix: "ice", Mention: telegram.MentionSuggestion{Username: "alice", Name: "Alice"}},
		{Kind: composeSuggestMentions, Main: "Alex", Insert: "@alex", Suffix: "ex", Mention: telegram.MentionSuggestion{Username: "alex", Name: "Alex"}},
	}
	app.renderComposeSuggestions()

	if got := app.capture(keyEvent(tcell.KeyDown)); got != nil {
		t.Fatalf("Down returned %v, want consumed", got)
	}
	if app.suggest.highlighted != 1 {
		t.Fatalf("highlight = %d, want 1", app.suggest.highlighted)
	}
	if got := app.capture(keyEvent(tcell.KeyTAB)); got != nil {
		t.Fatalf("Tab returned %v, want consumed", got)
	}
	if got := app.composer.GetText(); got != "@alex" {
		t.Fatalf("composer text = %q, want @alex", got)
	}
	if app.suggest.mode != composeSuggestNone {
		t.Fatalf("suggestions still open: %q", app.suggest.mode)
	}
	if app.app.GetFocus() != app.composer {
		t.Fatalf("composer focus was not preserved")
	}
}

func TestComposeSuggestionEscKeepsText(t *testing.T) {
	app := newSuggestionTestApp()
	app.composer.SetText("@al", true)
	app.suggest.mode = composeSuggestMentions
	app.suggest.token = composeToken{Mode: composeSuggestMentions, Start: 0, End: 3, Query: "al"}
	app.suggest.items = []composeSuggestItem{{Kind: composeSuggestMentions, Main: "Alice", Insert: "@alice"}}
	app.renderComposeSuggestions()

	if got := app.capture(keyEvent(tcell.KeyEsc)); got != nil {
		t.Fatalf("Esc returned %v, want consumed", got)
	}
	if got := app.composer.GetText(); got != "@al" {
		t.Fatalf("composer text = %q, want unchanged", got)
	}
	if app.suggest.mode != composeSuggestNone {
		t.Fatalf("suggestions still open after Esc")
	}
}

func TestComposeSuggestionClosedTabKeepsFocusBehavior(t *testing.T) {
	app := newSuggestionTestApp()
	app.suggest.mode = composeSuggestNone
	app.app.SetFocus(app.composer)

	if got := app.capture(keyEvent(tcell.KeyTAB)); got != nil {
		t.Fatalf("closed Tab returned %v, want consumed by normal focus cycle", got)
	}
	if got := app.app.GetFocus(); got != app.folders {
		t.Fatalf("focus after closed Tab = %T, want folders", got)
	}
}

func TestComposeGhostShowsCompletionSuffix(t *testing.T) {
	app := newSuggestionTestApp()
	app.suggest.mode = composeSuggestMentions
	app.suggest.token = composeToken{Mode: composeSuggestMentions, Start: 0, End: 3, Query: "ali"}
	app.suggest.items = []composeSuggestItem{{Kind: composeSuggestMentions, Main: "Alice", Insert: "@alice", Suffix: "ce"}}
	app.renderComposeSuggestions()

	if got := app.suggest.ghost.GetText(true); got != "ce" {
		t.Fatalf("ghost text = %q, want ce", got)
	}
}

func TestComposeCommandSuggestionShowsAndAcceptsBotSuffix(t *testing.T) {
	tests := []struct {
		name   string
		accept func(t *testing.T, app *App)
	}{
		{
			name: "enter",
			accept: func(t *testing.T, app *App) {
				t.Helper()
				if got := app.capture(keyEvent(tcell.KeyEnter)); got != nil {
					t.Fatalf("Enter returned %v, want consumed", got)
				}
			},
		},
		{
			name: "tab",
			accept: func(t *testing.T, app *App) {
				t.Helper()
				if got := app.capture(keyEvent(tcell.KeyTAB)); got != nil {
					t.Fatalf("Tab returned %v, want consumed", got)
				}
			},
		},
		{
			name: "mouse click",
			accept: func(t *testing.T, app *App) {
				t.Helper()
				app.suggest.panel.SetRect(0, 0, 40, 6)
				consumed, _ := app.suggest.panel.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(1, 1, tcell.ButtonPrimary, tcell.ModNone), func(p tview.Primitive) {
					app.app.SetFocus(p)
				})
				if !consumed {
					t.Fatalf("mouse click was not consumed")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newSuggestionTestApp()
			app.composer.SetText("/pi", true)
			app.suggest.mode = composeSuggestCommands
			app.suggest.token = composeToken{Mode: composeSuggestCommands, Start: 0, End: 3, Query: "pi"}
			app.suggest.requestID = 7
			app.applyComposeSuggestions(telegram.Event{
				Kind:      telegram.EventBotCommandSuggestions,
				PeerKey:   app.currentChat,
				RequestID: 7,
				Query:     "pi",
				BotCommands: []telegram.BotCommandSuggestion{{
					Command:        "ping",
					Description:    "Pong",
					BotUsername:    "anzupop_bot",
					NeedsBotSuffix: true,
				}},
			})

			main, secondary := app.suggest.panel.GetItemText(0)
			if main != "/ping@anzupop_bot" {
				t.Fatalf("main text = %q, want /ping@anzupop_bot", main)
			}
			if secondary != "Pong @anzupop_bot" {
				t.Fatalf("secondary text = %q, want Pong @anzupop_bot", secondary)
			}
			if got := app.suggest.ghost.GetText(true); got != "ng@anzupop_bot" {
				t.Fatalf("ghost text = %q, want ng@anzupop_bot", got)
			}

			tt.accept(t, app)
			if got := app.composer.GetText(); got != "/ping@anzupop_bot" {
				t.Fatalf("composer text = %q, want /ping@anzupop_bot", got)
			}
			if app.suggest.mode != composeSuggestNone {
				t.Fatalf("suggestions still open: %q", app.suggest.mode)
			}
			if app.app.GetFocus() != app.composer {
				t.Fatalf("composer focus was not preserved")
			}
		})
	}
}

func TestComposeCommandSuggestionFallsBackWithoutBotSuffix(t *testing.T) {
	app := newSuggestionTestApp()
	app.composer.SetText("/pi", true)
	app.suggest.mode = composeSuggestCommands
	app.suggest.token = composeToken{Mode: composeSuggestCommands, Start: 0, End: 3, Query: "pi"}
	app.suggest.requestID = 8
	app.applyComposeSuggestions(telegram.Event{
		Kind:      telegram.EventBotCommandSuggestions,
		PeerKey:   app.currentChat,
		RequestID: 8,
		Query:     "pi",
		BotCommands: []telegram.BotCommandSuggestion{{
			Command:     "ping",
			Description: "Pong",
			BotUsername: "anzupop_bot",
		}},
	})

	main, _ := app.suggest.panel.GetItemText(0)
	if main != "/ping" {
		t.Fatalf("main text = %q, want /ping", main)
	}
	if got := app.suggest.ghost.GetText(true); got != "ng" {
		t.Fatalf("ghost text = %q, want ng", got)
	}
	if got := app.capture(keyEvent(tcell.KeyEnter)); got != nil {
		t.Fatalf("Enter returned %v, want consumed", got)
	}
	if got := app.composer.GetText(); got != "/ping" {
		t.Fatalf("composer text = %q, want /ping", got)
	}
}

func TestMentionEntityUTF16OffsetAndTrimAdjustment(t *testing.T) {
	app := newSuggestionTestApp()
	app.suggest.mentionEntities = []telegram.MessageEntityMentionName{{
		Offset: utf16Len("  hi 😊 "),
		Length: utf16Len("Alice"),
		UserID: 42,
	}}
	got := app.takeMentionEntitiesForSend("  hi 😊 Alice", "hi 😊 Alice")
	if len(got) != 1 {
		t.Fatalf("entities = %+v, want one", got)
	}
	if want := utf16Len("hi 😊 "); got[0].Offset != want {
		t.Fatalf("offset = %d, want %d", got[0].Offset, want)
	}
	if got[0].Length != 5 {
		t.Fatalf("length = %d, want 5", got[0].Length)
	}
}
