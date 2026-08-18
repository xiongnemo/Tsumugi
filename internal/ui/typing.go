package ui

import (
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const (
	// Telegram documents a typing update as valid for six seconds. Matching that keeps the
	// indicator from lingering after the sender's own client has already given up on it.
	typingTTL = 6 * time.Second
	// How often a keystroke burst is allowed to enqueue a command. This is only spam control
	// for the command channel; the semantic throttle is typingRefreshInterval in the client.
	// Deliberately much shorter than that one, because the two compose multiplicatively: a UI
	// interval close to the client's would stretch the effective cadence past the six-second
	// expiry and make the receiver's indicator flicker.
	typingNotifyInterval = time.Second
	// A little past the TTL so the forced redraw sees an already-expired entry rather than
	// racing the comparison.
	typingExpiryRedrawDelay = typingTTL + 250*time.Millisecond
)

// typingState is one peer's most recent action. Keyed by display name rather than user ID
// because the update carries a name for rendering and nothing else is needed; two typists
// sharing a name merely collapse into one row, which is invisible in the "many" case anyway.
type typingState struct {
	actionKey string
	at        time.Time
}

// noteTyping records an incoming typing update.
func (a *App) applyTypingEvent(event telegram.Event) {
	if event.PeerKey == "" {
		return
	}
	if a.typingByPeer == nil {
		a.typingByPeer = make(map[string]map[string]typingState)
	}
	name := event.TypingName
	if name == "" {
		name = i18n.T(i18n.KeyUIUntitled)
	}
	if !event.TypingActive {
		delete(a.typingByPeer[event.PeerKey], name)
		if len(a.typingByPeer[event.PeerKey]) == 0 {
			delete(a.typingByPeer, event.PeerKey)
		}
	} else {
		if a.typingByPeer[event.PeerKey] == nil {
			a.typingByPeer[event.PeerKey] = make(map[string]typingState)
		}
		a.typingByPeer[event.PeerKey][name] = typingState{actionKey: event.TypingActionKey, at: time.Now()}
	}
	if event.PeerKey == a.currentChat {
		a.applyMessagesPaneTitle()
		a.scheduleTypingExpiry()
	}
}

// scheduleTypingExpiry forces the one redraw that lazy expiry cannot produce on its own.
//
// typingTitleHint prunes expired entries whenever it runs, which covers every redraw caused by
// something else. But a peer who types once and stops generates no further events, so without
// this timer the indicator would sit there until the user happened to press a key.
func (a *App) scheduleTypingExpiry() {
	if a.app == nil {
		return
	}
	if a.typingExpiry != nil {
		a.typingExpiry.Stop()
	}
	a.typingExpiry = time.AfterFunc(typingExpiryRedrawDelay, func() {
		a.app.QueueUpdateDraw(func() {
			a.applyMessagesPaneTitle()
		})
	})
}

// typingTitleHint renders the current chat's typing indicator, pruning expired entries as it
// goes. Returns "" when nobody is typing.
func (a *App) typingTitleHint() string {
	entries := a.typingByPeer[a.currentChat]
	if len(entries) == 0 {
		return ""
	}
	for name, state := range entries {
		if time.Since(state.at) > typingTTL {
			delete(entries, name)
		}
	}
	if len(entries) == 0 {
		delete(a.typingByPeer, a.currentChat)
		return ""
	}
	if len(entries) > 1 {
		return i18n.Tf(i18n.KeyTypingMany, len(entries))
	}
	for name, state := range entries {
		key := state.actionKey
		if key == "" {
			key = i18n.KeyTypingIsTyping
		}
		return i18n.Tf(key, name)
	}
	return ""
}

// clearTypingForPeer drops a chat's indicator on switching away, so returning later does not
// show a stale one from before.
func (a *App) clearTypingForPeer(peerKey string) {
	delete(a.typingByPeer, peerKey)
}

// notifyTyping tells the peer we are composing, rate-limited per peer.
func (a *App) notifyTyping() {
	if !a.draftablePeer(a.currentChat) {
		return
	}
	if a.suggest != nil && a.suggest.internalSet {
		// Programmatic edit (suggestion accepted, draft restored), not the user typing.
		return
	}
	if a.composer != nil && a.composer.GetText() == "" {
		// Clearing the box is not composing. Sending the cancel here is what makes the other
		// side's indicator disappear when the user deletes a half-written message.
		a.cancelTyping()
		return
	}
	if a.typingNotifiedPeer == a.currentChat && time.Since(a.typingNotifiedAt) < typingNotifyInterval {
		return
	}
	a.typingNotifiedPeer = a.currentChat
	a.typingNotifiedAt = time.Now()
	a.sendTypingCommand(a.currentChat, true)
}

// cancelTyping stops our own indicator: on send, on clearing the box, and on leaving the chat.
// It also resets the throttle so resuming typing notifies immediately instead of waiting out
// the interval.
func (a *App) cancelTyping() {
	peerKey := a.typingNotifiedPeer
	if peerKey == "" {
		return
	}
	a.typingNotifiedPeer = ""
	a.typingNotifiedAt = time.Time{}
	a.sendTypingCommand(peerKey, false)
}

func (a *App) sendTypingCommand(peerKey string, typing bool) {
	if a.commands == nil || peerKey == "" {
		return
	}
	select {
	case a.commands <- telegram.Command{Kind: telegram.CommandSetTyping, PeerKey: peerKey, Typing: typing}:
	default:
		// A dropped typing notice is not worth blocking the UI thread for.
	}
}
