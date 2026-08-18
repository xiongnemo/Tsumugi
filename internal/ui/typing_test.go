package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func typingEvent(peerKey, name, actionKey string) telegram.Event {
	return telegram.Event{
		Kind:            telegram.EventTyping,
		PeerKey:         peerKey,
		TypingName:      name,
		TypingActionKey: actionKey,
		TypingActive:    true,
	}
}

func TestTypingTitleHintNamesOneTypist(t *testing.T) {
	app := newSuggestionTestApp()

	app.applyTypingEvent(typingEvent("chat:1", "Alice", i18n.KeyTypingIsTyping))

	hint := app.typingTitleHint()
	if !strings.Contains(hint, "Alice") {
		t.Fatalf("hint = %q, want it to name Alice", hint)
	}
	if strings.Contains(hint, "%") {
		t.Fatalf("hint = %q, want the template rendered", hint)
	}
}

func TestTypingTitleHintCollapsesManyTypists(t *testing.T) {
	app := newSuggestionTestApp()

	app.applyTypingEvent(typingEvent("chat:1", "Alice", i18n.KeyTypingIsTyping))
	app.applyTypingEvent(typingEvent("chat:1", "Bob", i18n.KeyTypingIsUploading))

	hint := app.typingTitleHint()
	if !strings.Contains(hint, "2") {
		t.Fatalf("hint = %q, want the count of typists", hint)
	}
	if strings.Contains(hint, "Alice") {
		t.Fatalf("hint = %q, want names collapsed rather than listed", hint)
	}
}

// A peer who types once and stops sends no further update, so expiry has to be time-based.
func TestTypingTitleHintExpires(t *testing.T) {
	app := newSuggestionTestApp()

	app.applyTypingEvent(typingEvent("chat:1", "Alice", i18n.KeyTypingIsTyping))
	app.typingByPeer["chat:1"]["Alice"] = typingState{
		actionKey: i18n.KeyTypingIsTyping,
		at:        time.Now().Add(-typingTTL - time.Second),
	}

	if hint := app.typingTitleHint(); hint != "" {
		t.Fatalf("hint = %q, want empty after the TTL", hint)
	}
	if _, ok := app.typingByPeer["chat:1"]; ok {
		t.Fatal("the expired peer entry should have been pruned")
	}
}

func TestApplyTypingEventInactiveClearsTypist(t *testing.T) {
	app := newSuggestionTestApp()

	app.applyTypingEvent(typingEvent("chat:1", "Alice", i18n.KeyTypingIsTyping))
	app.applyTypingEvent(telegram.Event{
		Kind:         telegram.EventTyping,
		PeerKey:      "chat:1",
		TypingName:   "Alice",
		TypingActive: false,
	})

	if hint := app.typingTitleHint(); hint != "" {
		t.Fatalf("hint = %q, want empty after a cancel", hint)
	}
}

// Typing arrives for background chats too. It must be remembered per peer but shown only for
// the open one, or the title would credit the wrong conversation.
func TestTypingTitleHintIgnoresOtherChats(t *testing.T) {
	app := newSuggestionTestApp()

	app.applyTypingEvent(typingEvent("chat:999", "Carol", i18n.KeyTypingIsTyping))

	if hint := app.typingTitleHint(); hint != "" {
		t.Fatalf("hint = %q, want empty for another chat", hint)
	}
	if _, ok := app.typingByPeer["chat:999"]; !ok {
		t.Fatal("the other chat's state should still be recorded")
	}
}

func TestMessagesPaneTitleShowsTypingAfterTheChatName(t *testing.T) {
	app := newSuggestionTestApp()
	app.currentTitle = "Alice | private"

	app.applyTypingEvent(typingEvent("chat:1", "Alice", i18n.KeyTypingIsTyping))

	title := app.messagesPaneTitleText()
	nameAt := strings.Index(title, "Alice | private")
	hintAt := strings.Index(title, i18n.Tf(i18n.KeyTypingIsTyping, "Alice"))
	if nameAt != 0 {
		t.Fatalf("title = %q, want the chat name first", title)
	}
	if hintAt <= nameAt {
		t.Fatalf("title = %q, want the typing hint right after the chat name", title)
	}
}

func TestNotifyTypingThrottlesKeystrokes(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("h", true)

	app.notifyTyping()
	app.notifyTyping()
	app.notifyTyping()

	if got := drainTypingCommands(cmds); len(got) != 1 {
		t.Fatalf("sent %d typing commands, want 1 for a keystroke burst", len(got))
	}
}

func TestNotifyTypingResumesAfterTheInterval(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("h", true)

	app.notifyTyping()
	app.typingNotifiedAt = time.Now().Add(-typingNotifyInterval - time.Millisecond)
	app.notifyTyping()

	if got := drainTypingCommands(cmds); len(got) != 2 {
		t.Fatalf("sent %d typing commands, want 2 once the interval elapsed", len(got))
	}
}

// Emptying the box is not composing: the peer's indicator has to come down.
func TestNotifyTypingCancelsWhenComposerEmptied(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("h", true)
	app.notifyTyping()
	app.composer.SetText("", true)

	app.notifyTyping()

	got := drainTypingCommands(cmds)
	if len(got) != 2 {
		t.Fatalf("sent %d typing commands, want a start then a cancel", len(got))
	}
	if !got[0].Typing || got[1].Typing {
		t.Fatalf("commands = %+v, want Typing true then false", got)
	}
}

func TestLeaveChatCancelsOurTypingForTheChatBeingLeft(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("half written", true)
	app.notifyTyping()
	drainTypingCommands(cmds)

	app.leaveChatForDraft()

	got := drainTypingCommands(cmds)
	if len(got) != 1 {
		t.Fatalf("sent %d typing commands on leaving, want 1 cancel", len(got))
	}
	if got[0].Typing {
		t.Fatalf("command = %+v, want a cancel", got[0])
	}
	if got[0].PeerKey != "chat:1" {
		t.Fatalf("cancel went to %q, want the chat being left", got[0].PeerKey)
	}
}

func TestLeaveChatForgetsTheirTyping(t *testing.T) {
	app := newSuggestionTestApp()
	app.applyTypingEvent(typingEvent("chat:1", "Alice", i18n.KeyTypingIsTyping))

	app.leaveChatForDraft()

	if _, ok := app.typingByPeer["chat:1"]; ok {
		t.Fatal("returning to the chat should not show a stale indicator")
	}
}

func drainTypingCommands(cmds <-chan telegram.Command) []telegram.Command {
	var out []telegram.Command
	for {
		select {
		case cmd := <-cmds:
			if cmd.Kind == telegram.CommandSetTyping {
				out = append(out, cmd)
			}
		default:
			return out
		}
	}
}
