package ui

import (
	"strconv"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func readTestMessages(chatID string, ids ...int) []telegram.Message {
	var out []telegram.Message
	for _, id := range ids {
		out = append(out, telegram.Message{
			ID:        strconv.Itoa(id),
			ChatID:    chatID,
			Text:      "line",
			CreatedAt: time.Unix(int64(id), 0),
		})
	}
	return out
}

func drainMarkRead(cmds <-chan telegram.Command) []telegram.Command {
	var out []telegram.Command
	for {
		select {
		case cmd := <-cmds:
			if cmd.Kind == telegram.CommandMarkRead {
				out = append(out, cmd)
			}
		default:
			return out
		}
	}
}

func newMarkReadTestApp() (*App, <-chan telegram.Command) {
	app, cmds := newSendTestApp()
	app.messages.SetRect(0, 0, 40, 10)
	app.messages.SetOnSelectionChanged(app.onMessageCursorMoved)
	return app, cmds
}

// Opening a chat lands the selection on the newest message, which is what makes the read mark
// fire without a chat-open hook of its own.
func TestSetMessagesMarksChatReadToNewest(t *testing.T) {
	app, cmds := newMarkReadTestApp()

	app.messages.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	app.flushMarkRead()

	got := drainMarkRead(cmds)
	if len(got) != 1 {
		t.Fatalf("sent %d mark-read commands, want 1", len(got))
	}
	if got[0].MessageID != 12 || got[0].PeerKey != "chat:1" {
		t.Fatalf("command = %+v, want chat:1 up to 12", got[0])
	}
}

// The hole this closes: read-marking and broadcast view-marking used to depend on App calling a
// notifier by hand at four key bindings, so PgUp, PgDn, Home, End, the wheel and clicks moved
// the cursor silently. Asserted on the callback rather than on an emitted command, because a
// chat whose newest message is already read legitimately emits nothing.
func TestPageKeysFireTheSelectionCallback(t *testing.T) {
	var ids []int
	for i := 0; i < 40; i++ {
		ids = append(ids, 100+i)
	}
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 10)
	v.SetMessages(readTestMessages("chat:1", ids...))

	fired := 0
	var seen []string
	v.SetOnSelectionChanged(func() {
		fired++
		selected, _ := v.SelectedMessage()
		seen = append(seen, selected.ID)
	})
	// Home and End are the deterministic pair: they set the selection outright rather than
	// deriving it from a scroll offset that only settles during Draw. Neither ever had a manual
	// notify in App, so they exercise exactly the hole this closes.
	handler := v.InputHandler()
	for _, key := range []tcell.Key{tcell.KeyHome, tcell.KeyEnd, tcell.KeyHome} {
		handler(tcell.NewEventKey(key, 0, tcell.ModNone), func(tview.Primitive) {})
	}

	if fired != 3 {
		t.Fatalf("callback fired %d times for 3 selection-moving keys, want 3", fired)
	}
	if want := []string{"100", "139", "100"}; !equalStrings(seen, want) {
		t.Fatalf("selections = %v, want %v", seen, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// readHistory is monotonic, so intermediate values can be dropped — but only if the last one
// lands, which is what the pending slot is for.
func TestMarkReadCoalescesToTheHighestID(t *testing.T) {
	app, cmds := newMarkReadTestApp()
	app.markReadSentAt = time.Now() // force the pending path rather than an immediate flush

	app.scheduleMarkReadUpTo("chat:1", "11")
	app.scheduleMarkReadUpTo("chat:1", "13")
	app.scheduleMarkReadUpTo("chat:1", "12")
	app.flushMarkRead()

	got := drainMarkRead(cmds)
	if len(got) != 1 {
		t.Fatalf("sent %d commands, want the burst coalesced into 1", len(got))
	}
	if got[0].MessageID != 13 {
		t.Fatalf("MessageID = %d, want the highest (13) of the burst", got[0].MessageID)
	}
}

func TestMarkReadNeverGoesBackwards(t *testing.T) {
	app, cmds := newMarkReadTestApp()
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	app.flushMarkRead()
	if got := drainMarkRead(cmds); len(got) != 1 || got[0].MessageID != 12 {
		t.Fatalf("setup marks = %+v, want a single read up to 12", got)
	}

	// Scrolling back up to an older message is not "unreading" it.
	app.messages.SelectByID("10")
	app.flushMarkRead()

	if got := drainMarkRead(cmds); len(got) != 0 {
		t.Fatalf("sent %+v, want nothing for a backwards cursor move", got)
	}
}

// A read mark must complete even if the user switches chats while it waits out the interval,
// and it must be filed against the chat it was queued for.
func TestMarkReadSurvivesAChatSwitch(t *testing.T) {
	app, cmds := newMarkReadTestApp()
	app.markReadSentAt = time.Now() // force the pending path rather than an immediate flush

	app.scheduleMarkReadUpTo("chat:1", "55")
	app.currentChat = "chat:2"
	app.scheduleMarkReadUpTo("chat:2", "77")
	app.flushMarkRead()

	got := drainMarkRead(cmds)
	if len(got) != 2 {
		t.Fatalf("sent %d commands, want one per peer", len(got))
	}
	byPeer := map[string]int{}
	for _, cmd := range got {
		byPeer[cmd.PeerKey] = cmd.MessageID
	}
	if byPeer["chat:1"] != 55 || byPeer["chat:2"] != 77 {
		t.Fatalf("marks = %v, want chat:1→55 and chat:2→77", byPeer)
	}
}

func TestMarkReadSkippedForBots(t *testing.T) {
	app, cmds := newMarkReadTestApp()
	app.cfg.AuthMode = config.AuthBot

	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.flushMarkRead()

	if got := drainMarkRead(cmds); len(got) != 0 {
		t.Fatalf("sent %+v, want nothing — bots cannot read history", got)
	}
}

func TestMarkReadSkippedForSavedMessagesAndPlaceholders(t *testing.T) {
	app, cmds := newMarkReadTestApp()

	for _, peer := range []string{"self:5", "welcome", "empty", ""} {
		app.scheduleMarkReadUpTo(peer, "99")
	}
	app.flushMarkRead()

	if got := drainMarkRead(cmds); len(got) != 0 {
		t.Fatalf("sent %+v, want nothing for Saved Messages or synthetic rows", got)
	}
}

// A locally pending send has no server id yet, so there is nothing to read up to.
func TestMarkReadIgnoresLocalMessageIDs(t *testing.T) {
	app, cmds := newMarkReadTestApp()

	app.scheduleMarkReadUpTo("chat:1", "local-3")
	app.flushMarkRead()

	if got := drainMarkRead(cmds); len(got) != 0 {
		t.Fatalf("sent %+v, want nothing for a local id", got)
	}
}

// An arrival while the view follows the tail is on screen and read, even though the highlight
// deliberately stays where it was.
func TestIncomingMessageAtTailMarksRead(t *testing.T) {
	app, cmds := newMarkReadTestApp()
	app.currentChat = "chat:1"
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.flushMarkRead()
	drainMarkRead(cmds)

	app.appendMessage(telegram.Message{ID: "12", ChatID: "chat:1", Text: "hi", CreatedAt: time.Unix(12, 0)})
	app.flushMarkRead()

	got := drainMarkRead(cmds)
	if len(got) != 1 || got[0].MessageID != 12 {
		t.Fatalf("marks = %+v, want a read up to 12", got)
	}
	// The highlight must not have jumped; that is an explicit viewport guarantee.
	if selected, _ := app.messages.SelectedMessage(); selected.ID != "11" {
		t.Fatalf("selection = %q, want the highlight to stay on 11", selected.ID)
	}
}

// Sending a message is not reading one; the outgoing echo must not mark anything.
func TestOutgoingMessageDoesNotMarkRead(t *testing.T) {
	app, cmds := newMarkReadTestApp()
	app.currentChat = "chat:1"
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.flushMarkRead()
	drainMarkRead(cmds)

	app.appendMessage(telegram.Message{ID: "12", ChatID: "chat:1", Outgoing: true, Text: "mine", CreatedAt: time.Unix(12, 0)})
	app.flushMarkRead()

	if got := drainMarkRead(cmds); len(got) != 0 {
		t.Fatalf("sent %+v, want nothing for our own message", got)
	}
}

// Message ids are per-peer, so two chats can share a newest id. An unqualified dedup key would
// swallow the notification on switching between them.
func TestSelectionNotifyKeyIsChatQualified(t *testing.T) {
	fired := 0
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 10)
	v.SetOnSelectionChanged(func() { fired++ })

	v.SetMessages(readTestMessages("chat:1", 10, 500))
	v.SetMessages(readTestMessages("chat:2", 20, 500))

	if fired != 2 {
		t.Fatalf("callback fired %d times, want 2 — the same id in a different chat is a change", fired)
	}
}
