package ui

import (
	"strings"
	"testing"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// The divider must be part of the first-unread message's own block. blocks and messages have to
// stay 1:1 — Draw compares block index against v.selected, and ApplyPrebuiltLayout bails on a
// length mismatch — so a divider block of its own would break selection entirely.
func TestUnreadDividerKeepsBlocksAlignedWithMessages(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	msgs := readTestMessages("chat:1", 10, 11, 12)
	v.SetMessages(msgs)

	v.SetUnreadDividerID("11")
	v.layout(40)

	if len(v.blocks) != len(msgs) {
		t.Fatalf("blocks = %d, messages = %d; they must stay 1:1", len(v.blocks), len(msgs))
	}
	if v.blocks[1].id != "11" {
		t.Fatalf("block[1].id = %q, want the divider folded into message 11", v.blocks[1].id)
	}
	if !strings.Contains(v.blocks[1].lines[0], i18n.T(i18n.KeyUIUnreadDivider)) {
		t.Fatalf("block[1] first line = %q, want the divider prepended", v.blocks[1].lines[0])
	}
	// The neighbours must be untouched.
	for _, i := range []int{0, 2} {
		if strings.Contains(strings.Join(v.blocks[i].lines, "\n"), i18n.T(i18n.KeyUIUnreadDivider)) {
			t.Fatalf("block[%d] also carries a divider", i)
		}
	}
}

func TestUnreadDividerClearedWhenEmpty(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	v.SetMessages(readTestMessages("chat:1", 10, 11))
	v.SetUnreadDividerID("11")
	v.layout(40)
	withDivider := v.blocks[1].height

	v.SetUnreadDividerID("")
	v.layout(40)

	if v.blocks[1].height != withDivider-1 {
		t.Fatalf("height = %d, want %d after removing the divider", v.blocks[1].height, withDivider-1)
	}
}

// Selection must still map to the right message once a divider has changed a block's height.
func TestSelectionSurvivesTheDivider(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 12)
	v.SetMessages(readTestMessages("chat:1", 10, 11, 12))
	v.SetUnreadDividerID("11")

	if !v.SelectByID("12") {
		t.Fatal("select 12")
	}
	if selected, _ := v.SelectedMessage(); selected.ID != "12" {
		t.Fatalf("selected = %q, want 12", selected.ID)
	}
}

// ensureSelectedVisible scrolls the minimum, which for a jump lands the target on the bottom
// edge with none of the unread messages below it on screen.
func TestScrollSelectedToTopPutsTargetAtTop(t *testing.T) {
	v := NewMessageViewport()
	v.SetRect(0, 0, 40, 8)
	var ids []int
	for i := 0; i < 40; i++ {
		ids = append(ids, 100+i)
	}
	v.SetMessages(readTestMessages("chat:1", ids...))
	v.SelectByID("120")

	v.ScrollSelectedToTop()

	v.layout(40)
	if v.scroll != v.blocks[v.selected].offset {
		t.Fatalf("scroll = %d, want the selected block's offset %d", v.scroll, v.blocks[v.selected].offset)
	}
}

func TestApplyHistoryWindowSetsDividerAndWindowedFlag(t *testing.T) {
	app := newSuggestionTestApp()

	app.applyHistoryWindow(telegram.Event{
		Kind:            telegram.EventMessages,
		PeerKey:         "chat:1",
		WindowedHistory: true,
		FirstUnreadID:   "42",
	})

	if !app.historyWindowed {
		t.Fatal("historyWindowed = false, want true")
	}
	if app.unreadDividerID != "42" {
		t.Fatalf("unreadDividerID = %q, want 42", app.unreadDividerID)
	}
}

// A later non-windowed replace for the same chat means we are back at the tail.
func TestApplyHistoryWindowClearsOnTailReplace(t *testing.T) {
	app := newSuggestionTestApp()
	app.applyHistoryWindow(telegram.Event{WindowedHistory: true, FirstUnreadID: "42"})

	app.applyHistoryWindow(telegram.Event{Kind: telegram.EventMessages, PeerKey: "chat:1"})

	if app.historyWindowed || app.unreadDividerID != "" {
		t.Fatalf("windowed=%v divider=%q, want cleared", app.historyWindowed, app.unreadDividerID)
	}
}

// A windowed pane must not be yanked back to the bottom by setMessages.
func TestSetMessagesDoesNotScrollToEndWhileWindowed(t *testing.T) {
	app := newSuggestionTestApp()
	app.messages.SetRect(0, 0, 40, 8)
	var ids []int
	for i := 0; i < 40; i++ {
		ids = append(ids, 100+i)
	}

	app.applyHistoryWindow(telegram.Event{WindowedHistory: true, FirstUnreadID: "120"})
	app.setMessages(readTestMessages("chat:1", ids...), false)

	if app.messages.followEnd {
		t.Fatal("followEnd = true; a windowed replace must not jump to the tail")
	}
}

func TestReturnToTailOnlyWhenWindowed(t *testing.T) {
	app, cmds := newSendTestApp()

	if app.returnToTail() {
		t.Fatal("returnToTail should do nothing when already at the tail")
	}
	if got := drainOpenChat(cmds); len(got) != 0 {
		t.Fatalf("sent %+v, want nothing", got)
	}

	app.applyHistoryWindow(telegram.Event{WindowedHistory: true, FirstUnreadID: "42"})
	if !app.returnToTail() {
		t.Fatal("returnToTail should act while windowed")
	}

	got := drainOpenChat(cmds)
	if len(got) != 1 {
		t.Fatalf("sent %d open-chat commands, want 1", len(got))
	}
	if got[0].JumpToUnread {
		t.Fatal("returning to the tail must not ask for the unread jump again")
	}
	if app.historyWindowed || app.unreadDividerID != "" {
		t.Fatalf("windowed=%v divider=%q, want cleared immediately", app.historyWindowed, app.unreadDividerID)
	}
}

func TestPaneTitleAdvertisesTheWayBack(t *testing.T) {
	app := newSuggestionTestApp()
	app.currentTitle = "Alice | private"
	app.messages.SetMessages(readTestMessages("chat:1", 10, 11))
	app.applyHistoryWindow(telegram.Event{WindowedHistory: true, FirstUnreadID: "11"})

	title := app.messagesPaneTitleText()

	if !strings.Contains(title, i18n.T(i18n.KeyUIWindowedHistory)) {
		t.Fatalf("title = %q, want it to advertise the way back to the latest", title)
	}
}

func drainOpenChat(cmds <-chan telegram.Command) []telegram.Command {
	var out []telegram.Command
	for {
		select {
		case cmd := <-cmds:
			if cmd.Kind == telegram.CommandOpenChat {
				out = append(out, cmd)
			}
		default:
			return out
		}
	}
}
