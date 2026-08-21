package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// notifyThrottle is the shortest gap between two alerts.
//
// One ring per burst, not one per message: a group posting ten messages in a second would otherwise
// produce ten bells, which is noise rather than notification.
const notifyThrottle = 3 * time.Second

// notifyArrival alerts about an incoming message, if anything should.
//
// Called for every arriving message, including ones for chats that are not open - which is the whole
// point, since the open chat is already on screen. The suppression rules are the feature: a
// notification that fires for your own messages, for the conversation you are reading, or for a chat
// you muted is one people turn off entirely.
func (a *App) notifyArrival(event telegram.Event) {
	if a.settings.Notify == settings.NotifyOff {
		return
	}
	msg, ok := notifiableMessage(event)
	if !ok {
		return
	}
	if event.PeerKey == a.currentChat {
		// On screen already. Ringing for what the user is looking at is how a bell becomes something
		// to switch off.
		return
	}
	if a.chatIsMuted(event.PeerKey) {
		return
	}
	now := a.notifyClock()
	if now.Sub(a.lastNotifyAt) < notifyThrottle {
		return
	}
	a.lastNotifyAt = now
	a.ringBell()
	if a.settings.Notify == settings.NotifyDesktop {
		a.postDesktopNotification(a.chatTitleFor(event.PeerKey), msg)
	}
}

// notifyClock is the clock the throttle reads.
//
// Injectable because Windows' time.Now has coarse granularity - two calls microseconds apart can
// return the identical value - which makes "did the timestamp move" untestable against the real
// clock.
func (a *App) notifyClock() time.Time {
	if a.notifyNow != nil {
		return a.notifyNow()
	}
	return time.Now()
}

// notifiableMessage picks the message worth alerting about out of an event.
//
// Outgoing messages are excluded: they arrive through the same path when sent from another device,
// and a client that beeps at you for your own phone's messages is broken. Service rows are excluded
// too - "X joined the group" is not worth a sound.
func notifiableMessage(event telegram.Event) (telegram.Message, bool) {
	if event.Kind != telegram.EventMessages || !event.Append || event.PeerKey == "" {
		return telegram.Message{}, false
	}
	for i := len(event.Messages) - 1; i >= 0; i-- {
		msg := event.Messages[i]
		if msg.Outgoing || msg.ServiceKey != "" || msg.State != "synced" {
			continue
		}
		return msg, true
	}
	return telegram.Message{}, false
}

func (a *App) chatIsMuted(peerKey string) bool {
	if idx := chatIndexByID(a.allChats, peerKey); idx >= 0 {
		return a.allChats[idx].Muted
	}
	return false
}

func (a *App) chatTitleFor(peerKey string) string {
	if idx := chatIndexByID(a.allChats, peerKey); idx >= 0 {
		return a.allChats[idx].Title
	}
	return peerKey
}

// ringBell asks the terminal to alert.
//
// Through tcell's own Screen rather than by writing \a to stdout: tcell owns the terminal while the
// TUI runs, and writing to the same descriptor from anywhere else is what corrupts a frame. This is
// also the only notification mechanism a bare Linux console has.
func (a *App) ringBell() {
	if a.screen == nil {
		return
	}
	_ = a.screen.Beep()
}

// postDesktopNotification emits an OSC escape sequence for terminals that implement one.
//
// Two constraints, both learned the hard way in this codebase:
//
// It writes to os.Stdout, which tcell also owns. tcell buffers its output and flushes at the end of
// Show(), so a write from another goroutine can land inside a half-written frame. This is only ever
// called from the event-loop goroutine (applyEvent runs inside QueueUpdateDraw), where the buffer is
// between frames. OSC sequences move no cursor, so nothing needs redrawing afterwards.
//
// And it is opt-in, never guessed. A terminal that does not understand the sequence prints it as
// garbage into the middle of the interface, and TERM_PROGRAM sniffing is not reliable enough to bet
// the display on. TERM=linux is refused outright: a bare console is certain not to support it.
func (a *App) postDesktopNotification(chatTitle string, msg telegram.Message) {
	if !desktopNotificationsPossible(os.Getenv("TERM")) {
		return
	}
	body := strings.TrimSpace(msg.Text)
	if body == "" {
		body = strings.TrimSpace(msg.Media.Label)
	}
	if payload := osc777(chatTitle, body); payload != "" {
		fmt.Fprint(os.Stdout, payload)
	}
}

// desktopNotificationsPossible refuses the terminals where the sequence is certainly wrong.
func desktopNotificationsPossible(term string) bool {
	switch strings.TrimSpace(strings.ToLower(term)) {
	case "linux", "dumb", "":
		return false
	default:
		return true
	}
}

// osc777 builds the notification sequence.
//
// OSC 777 rather than OSC 9: it carries a title and a body as separate fields, which is what makes a
// notification readable, and the terminals that support notifications at all mostly support it.
// Semicolons and the string terminator are stripped from both fields because they end the sequence -
// a message containing one would otherwise spill its remainder onto the screen as text.
func osc777(title, body string) string {
	title = sanitizeOSC(title)
	body = sanitizeOSC(body)
	if title == "" && body == "" {
		return ""
	}
	return "\x1b]777;notify;" + title + ";" + body + "\x1b\\"
}

func sanitizeOSC(text string) string {
	text = strings.Map(func(r rune) rune {
		switch r {
		case ';', '\x1b', '\x07', '\n', '\r':
			return -1
		default:
			if r < 0x20 {
				return -1
			}
			return r
		}
	}, text)
	// Long enough to be useful, short enough that a pasted essay does not become a notification.
	const maxOSCField = 120
	if len(text) > maxOSCField {
		text = text[:maxOSCField]
	}
	return strings.TrimSpace(text)
}

// notifyOptions maps the setting's values to their labels, in display order.
//
// A slice rather than a map so the dropdown's index is stable: tview reports the chosen option by
// position, and a map's order would silently reassign what each position means between builds.
var notifyOptions = []struct {
	Value    string
	LabelKey string
}{
	{settings.NotifyOff, i18n.KeySettingsNotifyOff},
	{settings.NotifyBell, i18n.KeySettingsNotifyBell},
	{settings.NotifyDesktop, i18n.KeySettingsNotifyDeskop},
}

func notifyOptionLabels() []string {
	out := make([]string, 0, len(notifyOptions))
	for _, option := range notifyOptions {
		out = append(out, i18n.T(option.LabelKey))
	}
	return out
}

func notifyOptionIndex(value string) int {
	for i, option := range notifyOptions {
		if option.Value == value {
			return i
		}
	}
	// The bell, matching settings.DefaultNotify: an unrecognised value must not read as "off".
	return 1
}

func notifyValueAt(index int) string {
	if index < 0 || index >= len(notifyOptions) {
		return settings.DefaultNotify
	}
	return notifyOptions[index].Value
}
