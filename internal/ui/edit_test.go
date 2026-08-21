package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func editableMessage() telegram.Message {
	return telegram.Message{ID: "42", ChatID: "chat:1", Text: "helo world", Outgoing: true, State: "synced"}
}

func editTestApp(t *testing.T) (*App, <-chan telegram.Command) {
	t.Helper()
	app, cmds := newSendTestApp()
	app.root = tview.NewFlex().AddItem(app.messages, 0, 1, true)
	return app, cmds
}

// nextCommand returns the next command that is not a draft save.
//
// beginEdit flushes the draft before taking over the composer, which is deliberate - the draft
// belongs to the composer's normal contents and the composer is about to hold something else - so
// every edit test would otherwise read that save first.
func nextCommand(cmds <-chan telegram.Command) (telegram.Command, bool) {
	for {
		select {
		case cmd := <-cmds:
			if cmd.Kind == telegram.CommandSaveDraft {
				continue
			}
			return cmd, true
		default:
			return telegram.Command{}, false
		}
	}
}

func TestBeginEditLoadsTheMessageIntoTheComposer(t *testing.T) {
	app, _ := editTestApp(t)

	app.beginEdit(editableMessage())

	if app.editTarget == nil {
		t.Fatal("edit mode was not entered")
	}
	if got := app.composer.GetText(); got != "helo world" {
		t.Fatalf("composer = %q, want the message text", got)
	}
	if title := app.composer.GetTitle(); !strings.Contains(title, "helo world") {
		t.Fatalf("composer title = %q, want it to say what is being edited", title)
	}
	if app.app.GetFocus() != app.composer {
		t.Errorf("focus = %T, want the composer", app.app.GetFocus())
	}
}

// Only your own synced text messages. Offering it for anything else produces a server error the user
// can do nothing about.
func TestEditableMessageRule(t *testing.T) {
	base := editableMessage()
	if !telegram.EditableMessage(base) {
		t.Fatal("an own synced text message should be editable")
	}
	for name, mutate := range map[string]func(m *telegram.Message){
		"incoming":  func(m *telegram.Message) { m.Outgoing = false },
		"pending":   func(m *telegram.Message) { m.State = "pending" },
		"failed":    func(m *telegram.Message) { m.State = "failed" },
		"service":   func(m *telegram.Message) { m.ServiceKey = "service.pinned_message" },
		"textless":  func(m *telegram.Message) { m.Text = "" },
		"whitespac": func(m *telegram.Message) { m.Text = "   " },
	} {
		msg := base
		mutate(&msg)
		if telegram.EditableMessage(msg) {
			t.Errorf("%s: should not be editable", name)
		}
	}
}

func TestSubmitEditSendsTheCommand(t *testing.T) {
	app, cmds := editTestApp(t)
	app.beginEdit(editableMessage())
	app.composer.SetText("hello world", true)

	app.submitComposer()

	cmd, ok := nextCommand(cmds)
	if !ok {
		t.Fatal("no command was sent")
	}
	if cmd.Kind != telegram.CommandEditMessage {
		t.Fatalf("command = %q, want an edit", cmd.Kind)
	}
	if cmd.MessageID != 42 || cmd.PeerKey != "chat:1" || cmd.Text != "hello world" {
		t.Fatalf("command = %+v, want the edited text for message 42", cmd)
	}
	if app.editTarget != nil {
		t.Error("edit mode outlived the submission")
	}
	if got := app.composer.GetText(); got != "" {
		t.Errorf("composer = %q, want it emptied", got)
	}
}

// Unchanged text gets MESSAGE_NOT_MODIFIED from Telegram, which is a red error line for something
// that is really "nothing to do".
func TestSubmitEditIgnoresAnUnchangedMessage(t *testing.T) {
	app, cmds := editTestApp(t)
	app.beginEdit(editableMessage())

	app.submitComposer()

	if cmd, ok := nextCommand(cmds); ok {
		t.Fatalf("a command was sent for an unchanged edit: %+v", cmd)
	}
	if app.editTarget != nil {
		t.Error("edit mode should have ended")
	}
}

// An empty edit is a delete in Telegram's model. Refusing it in the UI keeps the composer contents
// so nothing typed is lost.
func TestSubmitEditRefusesEmptyText(t *testing.T) {
	app, cmds := editTestApp(t)
	app.beginEdit(editableMessage())
	app.composer.SetText("   ", true)

	app.submitComposer()

	if cmd, ok := nextCommand(cmds); ok {
		t.Fatalf("a command was sent for an empty edit: %+v", cmd)
	}
	if app.editTarget == nil {
		t.Error("edit mode was abandoned, losing the target")
	}
}

// Esc leaves edit mode before it does anything else, and empties the composer: the text is a copy of
// a message that still exists, so keeping it would make the next send a duplicate.
func TestEscCancelsEdit(t *testing.T) {
	app, _ := editTestApp(t)
	app.beginEdit(editableMessage())

	if got := app.capture(keyEvent(tcell.KeyEsc)); got != nil {
		t.Fatalf("Esc returned %v, want it consumed", got)
	}
	if app.editTarget != nil {
		t.Fatal("Esc did not leave edit mode")
	}
	if got := app.composer.GetText(); got != "" {
		t.Fatalf("composer = %q, want it emptied on cancel", got)
	}
}

// Editing text and sending a file are different operations, and an edit cannot carry media. The two
// modes are mutually exclusive rather than silently dropping one of them at send time.
func TestEditAndAttachmentAreMutuallyExclusive(t *testing.T) {
	app, _ := editTestApp(t)
	path := touch(t, t.TempDir(), "shot.png")

	app.stageAttachment(path)
	app.beginEdit(editableMessage())
	if app.attachment != nil {
		t.Error("entering edit mode kept a staged attachment")
	}

	app.stageAttachment(path)
	if app.editTarget != nil {
		t.Error("staging an attachment kept edit mode")
	}
}
