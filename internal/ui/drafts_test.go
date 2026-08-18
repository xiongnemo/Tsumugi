package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func TestApplyDraftEventRestoresIntoEmptyComposer(t *testing.T) {
	app := newSuggestionTestApp()
	app.statusForeground = nil // exercise the nil-safe status path

	app.applyDraftEvent(telegram.Event{
		Kind:      telegram.EventDraft,
		PeerKey:   "chat:1",
		DraftText: "half typed",
	})

	if got := app.composer.GetText(); got != "half typed" {
		t.Fatalf("composer = %q, want the draft restored", got)
	}
}

// Replacing characters under a live cursor is the one failure this feature cannot afford.
func TestApplyDraftEventNeverClobbersTypedText(t *testing.T) {
	app := newSuggestionTestApp()
	app.composer.SetText("mine, still typing", true)

	app.applyDraftEvent(telegram.Event{
		Kind:        telegram.EventDraft,
		PeerKey:     "chat:1",
		DraftText:   "from another client",
		DraftRemote: true,
	})

	if got := app.composer.GetText(); got != "mine, still typing" {
		t.Fatalf("composer = %q, want the typed text untouched", got)
	}
}

func TestApplyDraftEventIgnoresOtherChats(t *testing.T) {
	app := newSuggestionTestApp()

	app.applyDraftEvent(telegram.Event{
		Kind:      telegram.EventDraft,
		PeerKey:   "chat:999",
		DraftText: "elsewhere",
	})

	if got := app.composer.GetText(); got != "" {
		t.Fatalf("composer = %q, want untouched for another chat", got)
	}
}

// Leaving a chat has to file the draft against the chat being left, and clear the box so the
// text does not follow you into the next conversation.
func TestLeaveChatForDraftFlushesAndClears(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("unsent", true)

	app.leaveChatForDraft()

	select {
	case cmd := <-cmds:
		if cmd.Kind != telegram.CommandSaveDraft {
			t.Fatalf("kind = %v, want save_draft", cmd.Kind)
		}
		if cmd.PeerKey != "chat:1" {
			t.Fatalf("peer = %q, want the chat being left", cmd.PeerKey)
		}
		if cmd.Text != "unsent" {
			t.Fatalf("text = %q", cmd.Text)
		}
	default:
		t.Fatal("no draft save was issued")
	}
	if got := app.composer.GetText(); got != "" {
		t.Fatalf("composer = %q, want cleared so text does not leak across chats", got)
	}
}

// Synthetic rows shown before a connection exists are not real peers.
func TestDraftSaveSkipsSyntheticPeers(t *testing.T) {
	app, cmds := newSendTestApp()
	for _, peer := range []string{"", "welcome", "empty"} {
		app.currentChat = peer
		app.composer.SetText("text", true)
		app.flushDraft(peer)
		select {
		case cmd := <-cmds:
			t.Fatalf("peer %q issued %v, want nothing", peer, cmd.Kind)
		default:
		}
	}
}

// Sending must cancel a pending debounced save, or it would re-file text that already went out.
func TestSubmitComposerCancelsPendingDraftSave(t *testing.T) {
	app, cmds := newSendTestApp()
	app.composer.SetText("outgoing", true)
	app.draftTimer = time.AfterFunc(time.Hour, func() {})
	app.draftPeer = "chat:1"

	if got := app.capture(ctrlKey(tcell.KeyCtrlJ, tcell.ModCtrl)); got != nil {
		t.Fatalf("Ctrl+J returned %v, want consumed", got)
	}
	if app.draftTimer != nil {
		t.Fatal("pending draft timer survived the send")
	}
	if app.draftPeer != "" {
		t.Fatalf("draftPeer = %q, want cleared", app.draftPeer)
	}

	var kinds []telegram.CommandKind
	for {
		select {
		case cmd := <-cmds:
			kinds = append(kinds, cmd.Kind)
			continue
		default:
		}
		break
	}
	if len(kinds) == 0 || kinds[0] != telegram.CommandSendText {
		t.Fatalf("commands = %v, want send_text first", kinds)
	}
}
