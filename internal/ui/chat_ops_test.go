package ui

import (
	"testing"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func chatOpsTestApp(t *testing.T) (*App, <-chan telegram.Command) {
	t.Helper()
	app, cmds := newSendTestApp()
	app.root = tview.NewFlex().AddItem(app.messages, 0, 1, true)
	app.allChats = []telegram.Chat{
		{ID: "chat:1", Title: "Group"},
		{ID: "channel:2", Title: "Channel", Muted: true, Pinned: true, FolderID: 1},
		{ID: "user:3", Title: "Alice"},
	}
	app.currentChat = "chat:1"
	app.currentTitle = "Group"
	return app, cmds
}

// The overlay offers the opposite of the current state, read from the chat list the user is looking
// at, so the label always describes what pressing Enter will do.
func TestChatOpsOffersTheOppositeOfTheCurrentState(t *testing.T) {
	app, _ := chatOpsTestApp(t)

	app.currentChat = "chat:1"
	app.openChatOps()
	first := ids(app.chatOpsRows)
	if !contains(first, "mute") || !contains(first, "pin") || !contains(first, "archive") {
		t.Fatalf("ops for a plain group = %v", first)
	}
	app.closeChatOps()

	app.currentChat = "channel:2"
	app.currentTitle = "Channel"
	app.openChatOps()
	second := ids(app.chatOpsRows)
	if !contains(second, "unmute") || !contains(second, "unpin") || !contains(second, "unarchive") {
		t.Fatalf("ops for a muted, pinned, archived channel = %v", second)
	}
}

// Leaving a private chat is not a thing; offering it would produce a server error.
func TestChatOpsOffersLeaveOnlyWhereItMeansSomething(t *testing.T) {
	app, _ := chatOpsTestApp(t)

	app.currentChat = "user:3"
	app.currentTitle = "Alice"
	app.openChatOps()
	if contains(ids(app.chatOpsRows), "leave") {
		t.Error("leave was offered for a private chat")
	}
	app.closeChatOps()

	app.currentChat = "chat:1"
	app.openChatOps()
	if !contains(ids(app.chatOpsRows), "leave") {
		t.Error("leave was not offered for a group")
	}
}

func TestChatOpsSendsTheRightCommands(t *testing.T) {
	cases := []struct {
		op      string
		kind    telegram.CommandKind
		mute    bool
		undo    bool
		current string
	}{
		{"mute", telegram.CommandMutePeer, true, false, "chat:1"},
		{"pin", telegram.CommandPinDialog, false, false, "chat:1"},
		{"archive", telegram.CommandArchiveDialog, false, false, "chat:1"},
		{"unmute", telegram.CommandMutePeer, false, false, "channel:2"},
		{"unpin", telegram.CommandPinDialog, false, true, "channel:2"},
		{"unarchive", telegram.CommandArchiveDialog, false, true, "channel:2"},
	}
	for _, tc := range cases {
		app, cmds := chatOpsTestApp(t)
		app.currentChat = tc.current
		app.openChatOps()
		index := -1
		for i, op := range app.chatOpsRows {
			if op.ID == tc.op {
				index = i
			}
		}
		if index < 0 {
			t.Fatalf("%s was not offered for %s", tc.op, tc.current)
		}
		app.commitChatOp(index)

		cmd, ok := nextCommand(cmds)
		if !ok {
			t.Fatalf("%s sent no command", tc.op)
		}
		if cmd.Kind != tc.kind || cmd.PeerKey != tc.current || cmd.Mute != tc.mute || cmd.Unpin != tc.undo {
			t.Fatalf("%s sent %+v", tc.op, cmd)
		}
	}
}

// Leaving is the one action here that cannot be undone from this overlay, so it must not fire on a
// single Enter.
func TestChatOpsLeaveAsksFirst(t *testing.T) {
	app, cmds := chatOpsTestApp(t)
	app.openChatOps()
	index := -1
	for i, op := range app.chatOpsRows {
		if op.ID == "leave" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("leave was not offered")
	}

	app.commitChatOp(index)

	if cmd, ok := nextCommand(cmds); ok {
		t.Fatalf("leave sent %+v without confirmation", cmd)
	}
}

func ids(ops []chatOp) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.ID)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
