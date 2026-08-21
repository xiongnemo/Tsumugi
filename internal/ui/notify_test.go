package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func arrival(peerKey string, msg telegram.Message) telegram.Event {
	return telegram.Event{
		Kind:     telegram.EventMessages,
		PeerKey:  peerKey,
		Messages: []telegram.Message{msg},
		Append:   true,
	}
}

func incoming(text string) telegram.Message {
	return telegram.Message{ID: "9", ChatID: "chat:2", Text: text, State: "synced"}
}

// The suppression rules are the feature. A bell that fires for your own messages, for the chat you
// are reading, or for a chat you muted is one people switch off entirely.
func TestNotifiableMessageIgnoresWhatShouldNotRing(t *testing.T) {
	if _, ok := notifiableMessage(arrival("chat:2", incoming("hello"))); !ok {
		t.Fatal("a plain incoming message should notify")
	}
	cases := map[string]telegram.Event{
		"own message":  arrival("chat:2", telegram.Message{ID: "9", Outgoing: true, State: "synced", Text: "mine"}),
		"service row":  arrival("chat:2", telegram.Message{ID: "9", State: "synced", ServiceKey: "service.pinned_message"}),
		"pending echo": arrival("chat:2", telegram.Message{ID: "-9", State: "pending", Text: "sending"}),
		"no peer":      arrival("", incoming("hello")),
		"not appended": {Kind: telegram.EventMessages, PeerKey: "chat:2", Messages: []telegram.Message{incoming("x")}},
		"other kind":   {Kind: telegram.EventStatus, PeerKey: "chat:2"},
	}
	for name, event := range cases {
		if _, ok := notifiableMessage(event); ok {
			t.Errorf("%s should not notify", name)
		}
	}
}

func TestNotifyArrivalRespectsTheSettingTheChatAndTheMute(t *testing.T) {
	newApp := func(notify string) *App {
		app := newCaptureTestApp()
		app.settings = settings.Settings{Notify: notify}
		app.allChats = []telegram.Chat{
			{ID: "chat:1", Title: "Open"},
			{ID: "chat:2", Title: "Other"},
			{ID: "chat:3", Title: "Muted", Muted: true},
		}
		app.currentChat = "chat:1"
		return app
	}

	// A ring is only observable through lastNotifyAt in a headless test; the bell itself needs a
	// terminal.
	app := newApp(settings.NotifyBell)
	app.notifyArrival(arrival("chat:2", incoming("hello")))
	if app.lastNotifyAt.IsZero() {
		t.Fatal("an incoming message in another chat did not notify")
	}

	for name, prepare := range map[string]func(*App) telegram.Event{
		"notifications off": func(a *App) telegram.Event {
			a.settings.Notify = settings.NotifyOff
			return arrival("chat:2", incoming("hello"))
		},
		"chat on screen": func(a *App) telegram.Event {
			return arrival("chat:1", telegram.Message{ID: "9", ChatID: "chat:1", Text: "hi", State: "synced"})
		},
		"muted chat": func(a *App) telegram.Event {
			return arrival("chat:3", telegram.Message{ID: "9", ChatID: "chat:3", Text: "hi", State: "synced"})
		},
	} {
		quiet := newApp(settings.NotifyBell)
		event := prepare(quiet)
		quiet.notifyArrival(event)
		if !quiet.lastNotifyAt.IsZero() {
			t.Errorf("%s: should not have notified", name)
		}
	}
}

// One ring per burst. Ten messages in a second is ten bells otherwise, which is noise.
func TestNotifyArrivalIsThrottled(t *testing.T) {
	app := newCaptureTestApp()
	app.settings = settings.Settings{Notify: settings.NotifyBell}
	app.allChats = []telegram.Chat{{ID: "chat:2", Title: "Other"}}
	app.currentChat = "chat:1"

	// A fixed clock, so "did it ring" is an exact comparison rather than a race with the platform's
	// timer resolution.
	base := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	now := base
	app.notifyNow = func() time.Time { return now }

	app.notifyArrival(arrival("chat:2", incoming("one")))
	if !app.lastNotifyAt.Equal(base) {
		t.Fatalf("first message did not ring: %v", app.lastNotifyAt)
	}

	now = base.Add(notifyThrottle - time.Millisecond)
	app.notifyArrival(arrival("chat:2", incoming("two")))
	if !app.lastNotifyAt.Equal(base) {
		t.Fatal("a second message inside the throttle window rang again")
	}

	now = base.Add(notifyThrottle)
	app.notifyArrival(arrival("chat:2", incoming("three")))
	if !app.lastNotifyAt.Equal(now) {
		t.Fatal("a message after the window did not ring")
	}
}

// A terminal that does not implement the sequence prints it as visible garbage, and a bare console
// certainly does not.
func TestDesktopNotificationsRefuseHopelessTerminals(t *testing.T) {
	for _, term := range []string{"linux", "dumb", "", "  "} {
		if desktopNotificationsPossible(term) {
			t.Errorf("TERM=%q should not get an OSC notification", term)
		}
	}
	for _, term := range []string{"xterm-256color", "screen", "wezterm"} {
		if !desktopNotificationsPossible(term) {
			t.Errorf("TERM=%q should be allowed to try", term)
		}
	}
}

// A semicolon or an escape byte in a message ends the sequence early, and the remainder would spill
// onto the screen as text.
func TestOSC777SanitizesItsFields(t *testing.T) {
	payload := osc777("Chat; with ; semicolons", "body\x1b]0;evil\x07 and\nnewlines")
	// Three separators belong to the sequence itself - 777;notify;title;body - and no more, because
	// a fourth would mean a semicolon survived from the message and ended the sequence early.
	if strings.Count(payload, ";") != 3 {
		t.Fatalf("payload has %d semicolons, want exactly the three separators: %q", strings.Count(payload, ";"), payload)
	}
	if strings.Contains(payload[5:], "\x1b]") {
		t.Fatalf("payload carries a nested escape sequence: %q", payload)
	}
	for _, bad := range []string{"\n", "\r", "\x07"} {
		if strings.Contains(payload, bad) {
			t.Errorf("payload contains %q: %q", bad, payload)
		}
	}
	if !strings.HasPrefix(payload, "\x1b]777;notify;") || !strings.HasSuffix(payload, "\x1b\\") {
		t.Fatalf("payload is not a well-formed OSC 777: %q", payload)
	}
	if osc777("", "") != "" {
		t.Error("an empty notification should produce nothing at all")
	}
}

// An unrecognised value must fall back to the bell, not to silence: a typo in a shell profile should
// not quietly disable notifications.
func TestNotifyOptionIndexFallsBackToTheBell(t *testing.T) {
	if got := notifyOptionIndex("nonsense"); notifyOptions[got].Value != settings.NotifyBell {
		t.Fatalf("unknown value mapped to %q, want the bell", notifyOptions[got].Value)
	}
	if got := notifyValueAt(-1); got != settings.DefaultNotify {
		t.Fatalf("out-of-range index gave %q", got)
	}
	for i, option := range notifyOptions {
		if notifyValueAt(i) != option.Value {
			t.Fatalf("index %d maps to %q, want %q", i, notifyValueAt(i), option.Value)
		}
	}
}
