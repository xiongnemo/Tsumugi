package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func drainKind(cmds <-chan telegram.Command, kind telegram.CommandKind) []telegram.Command {
	var out []telegram.Command
	for {
		select {
		case cmd := <-cmds:
			if cmd.Kind == kind {
				out = append(out, cmd)
			}
		default:
			return out
		}
	}
}

// The gap this closes: scrolling up loaded older history and scrolling down loaded nothing, which
// only became visible once opening a chat could land in the middle of it.
func TestReachNewerRequestsThePageAfterTheNewestHeld(t *testing.T) {
	app, cmds := newSendTestApp()
	app.messages.SetRect(0, 0, 40, 10)
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	app.applyHistoryWindow(telegram.Event{WindowedHistory: true, FirstUnreadID: "11"})

	app.onReachNewerMessages()

	got := drainKind(cmds, telegram.CommandLoadNewer)
	if len(got) != 1 {
		t.Fatalf("sent %d load-newer commands, want 1", len(got))
	}
	if got[0].MessageID != 12 {
		t.Fatalf("anchor = %d, want the newest held message 12", got[0].MessageID)
	}
}

// At the tail there is nothing newer, so asking would be a round trip per scroll.
func TestReachNewerSilentAtTheTail(t *testing.T) {
	app, cmds := newSendTestApp()
	app.messages.SetRect(0, 0, 40, 10)
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.historyWindowed = false

	app.onReachNewerMessages()

	if got := drainKind(cmds, telegram.CommandLoadNewer); len(got) != 0 {
		t.Fatalf("sent %+v, want nothing when already at the tail", got)
	}
}

// A completed forward page retires the way-back-to-latest hint.
func TestReachedNewestClearsTheWindowedFlag(t *testing.T) {
	app, _ := newSendTestApp()
	app.messages.SetRect(0, 0, 40, 10)
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.applyHistoryWindow(telegram.Event{WindowedHistory: true, FirstUnreadID: "11"})

	app.applyEvent(telegram.Event{
		Kind:          telegram.EventMessages,
		PeerKey:       "chat:1",
		Merge:         true,
		Messages:      readTestMessages("chat:1", 12, 13),
		ReachedNewest: true,
	})

	if app.historyWindowed {
		t.Fatal("reaching the newest message must retire the windowed state")
	}
}

// An empty forward page carries no messages at all, so it has to be handled before the
// removal-only early return swallows it.
func TestEmptyReachedNewestPageStillClearsTheFlag(t *testing.T) {
	app, _ := newSendTestApp()
	app.messages.SetRect(0, 0, 40, 10)
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.applyHistoryWindow(telegram.Event{WindowedHistory: true, FirstUnreadID: "11"})

	app.applyEvent(telegram.Event{
		Kind:          telegram.EventMessages,
		PeerKey:       "chat:1",
		ReachedNewest: true,
	})

	if app.historyWindowed {
		t.Fatal("an empty forward page means the tail is already held")
	}
}

// G has to mean "the end" whether or not the pane is showing a window: a user who wants the latest
// message should not have to know which state they are in.
func TestJumpToLatestWorksFromEitherState(t *testing.T) {
	for _, windowed := range []bool{true, false} {
		app, cmds := newSendTestApp()
		app.messages.SetRect(0, 0, 40, 10)
		app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
		app.historyWindowed = windowed

		app.jumpToLatest()

		got := drainKind(cmds, telegram.CommandOpenChat)
		if len(got) != 1 {
			t.Fatalf("windowed=%v: sent %d open-chat commands, want 1", windowed, len(got))
		}
		if got[0].JumpToUnread {
			t.Fatalf("windowed=%v: must not ask for the unread jump when going to the latest", windowed)
		}
		if app.historyWindowed {
			t.Fatalf("windowed=%v: the flag should be cleared immediately", windowed)
		}
	}
}

// Reading forward off the end of what is held is the ordinary way the newer page is wanted.
func TestSelectDeltaAtTheEndFiresReachNewer(t *testing.T) {
	fired := 0
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 10)
	v.SetMessages(readTestMessages("chat:1", 10, 11))
	v.SetOnReachNewer(func() { fired++ })
	v.SelectByID("11")

	v.SelectDelta(1)

	if fired == 0 {
		t.Fatal("moving past the last message should ask for newer history")
	}
}

// Holding the key at the boundary must not become one request per repeat.
func TestReachNewerIsThrottled(t *testing.T) {
	fired := 0
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 10)
	v.SetMessages(readTestMessages("chat:1", 10, 11))
	v.SetOnReachNewer(func() { fired++ })
	v.SelectByID("11")

	for i := 0; i < 20; i++ {
		v.SelectDelta(1)
	}

	if fired > 1 {
		t.Fatalf("fired %d times for a held key, want it throttled", fired)
	}
}

// End is the other way a reader asks for what comes next.
func TestEndFiresReachNewer(t *testing.T) {
	fired := 0
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 10)
	v.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	v.SetOnReachNewer(func() { fired++ })

	v.InputHandler()(tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), func(tview.Primitive) {})

	if fired == 0 {
		t.Fatal("End should ask for newer history")
	}
}
