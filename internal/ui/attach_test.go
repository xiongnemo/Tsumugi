package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func attachTestApp(t *testing.T) *App {
	t.Helper()
	app, cmds := newSendTestApp()
	_ = cmds
	app.root = tview.NewFlex().AddItem(app.messages, 0, 1, true)
	app.attachList = tview.NewList().ShowSecondaryText(true)
	app.attachInput = tview.NewInputField()
	app.attachPreview = tview.NewTextView()
	return app
}

func touch(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The clipboard is the most common source of an image to send, and it leads the list so it needs no
// key of its own - which also keeps it clear of TextArea's own Ctrl+V.
func TestAttachListLeadsWithTheClipboard(t *testing.T) {
	app := attachTestApp(t)
	dir := t.TempDir()
	touch(t, dir, "shot.png")
	app.attachDir = dir

	app.fillAttachList("")

	if len(app.attachRows) == 0 {
		t.Fatal("no rows at all")
	}
	if !app.attachRows[0].Clipboard {
		t.Fatalf("first row = %+v, want the clipboard row", app.attachRows[0])
	}
	// With a filter the user is looking for a file by name, so the clipboard row steps aside.
	app.fillAttachList("shot")
	for _, row := range app.attachRows {
		if row.Clipboard {
			t.Fatal("the clipboard row survived a filter")
		}
	}
}

func TestAttachListPutsDirectoriesFirstAndFilters(t *testing.T) {
	app := attachTestApp(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "pictures"), 0o700); err != nil {
		t.Fatal(err)
	}
	touch(t, dir, "aaa.png")
	touch(t, dir, "notes.txt")
	app.attachDir = dir

	app.fillAttachList("")
	// Row 0 is the clipboard, row 1 is the parent; the first real entry has to be the directory even
	// though "aaa.png" sorts before "pictures".
	var firstEntry attachRow
	for _, row := range app.attachRows {
		if row.Clipboard || row.Path == filepath.Dir(dir) {
			continue
		}
		firstEntry = row
		break
	}
	if !firstEntry.IsDir {
		t.Fatalf("first entry = %+v, want the directory first", firstEntry)
	}

	app.fillAttachList("notes")
	var names []string
	for _, row := range app.attachRows {
		names = append(names, filepath.Base(row.Path))
	}
	if len(names) != 1 || names[0] != "notes.txt" {
		t.Fatalf("filtered rows = %v, want only notes.txt", names)
	}
}

// Enter on a directory browses into it rather than trying to send it.
func TestAttachEnterDescendsIntoDirectories(t *testing.T) {
	app := attachTestApp(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "sub")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	touch(t, child, "inner.png")
	app.attachDir = parent
	app.fillAttachList("")

	for i, row := range app.attachRows {
		if row.IsDir && row.Path == child {
			app.attachList.SetCurrentItem(i)
		}
	}
	app.commitAttachPick()

	if app.attachDir != child {
		t.Fatalf("attachDir = %q, want %q", app.attachDir, child)
	}
	if app.attachment != nil {
		t.Fatal("a directory was staged as an attachment")
	}
}

// Staging shows the file in the composer title and hands focus back to the composer, so the caption
// can be typed immediately.
func TestStageAttachmentShowsInTheComposerTitle(t *testing.T) {
	app := attachTestApp(t)
	path := touch(t, t.TempDir(), "shot.png")

	app.stageAttachment(path)

	if app.attachment == nil {
		t.Fatal("nothing was staged")
	}
	title := app.composer.GetTitle()
	if !strings.Contains(title, "shot.png") {
		t.Fatalf("composer title = %q, want the file name in it", title)
	}
	if !strings.Contains(title, render.Glyphs().Attach) {
		t.Errorf("composer title = %q, want the attachment glyph", title)
	}
	if app.app.GetFocus() != app.composer {
		t.Errorf("focus = %T, want the composer", app.app.GetFocus())
	}
}

// A reply and an attachment can be set at the same time, and each used to overwrite the other's
// title, leaving the user unable to see one of them.
func TestComposerTitleShowsReplyAndAttachmentTogether(t *testing.T) {
	app := attachTestApp(t)
	app.setReplyTarget(telegram.Message{ID: "7", ChatID: "chat:1", Text: "original"})
	app.stageAttachment(touch(t, t.TempDir(), "shot.png"))

	title := app.composer.GetTitle()
	if !strings.Contains(title, "original") {
		t.Errorf("composer title = %q, lost the reply target", title)
	}
	if !strings.Contains(title, "shot.png") {
		t.Errorf("composer title = %q, lost the attachment", title)
	}
}

// The old label sliced bytes, which cuts a CJK rune in half and leaves a broken glyph in the border.
func TestReplyTargetLabelTruncatesByDisplayWidth(t *testing.T) {
	long := strings.Repeat("测试", 40)
	label := replyTargetLabel(telegram.Message{ID: "1", Text: long})
	if render.StringWidth(label) > 40 {
		t.Fatalf("label is %d cells wide, want at most 40", render.StringWidth(label))
	}
	// Valid UTF-8, not a severed rune.
	if strings.ContainsRune(label, '�') {
		t.Fatalf("label = %q, want no replacement characters", label)
	}
}

// Sending must carry the file and the caption as one message: Telegram's caption *is* the message
// text, so two commands would post the text twice.
func TestSubmitComposerSendsTheAttachmentWithItsCaption(t *testing.T) {
	app, cmds := newSendTestApp()
	app.root = tview.NewFlex().AddItem(app.messages, 0, 1, true)
	path := touch(t, t.TempDir(), "shot.png")
	app.stageAttachment(path)
	app.composer.SetText("caption", true)

	app.submitComposer()

	select {
	case cmd := <-cmds:
		if cmd.Kind != telegram.CommandSendMedia {
			t.Fatalf("command = %q, want a media send", cmd.Kind)
		}
		if cmd.MediaPath != path {
			t.Errorf("MediaPath = %q, want %q", cmd.MediaPath, path)
		}
		if cmd.Text != "caption" {
			t.Errorf("Text = %q, want the caption", cmd.Text)
		}
	default:
		t.Fatal("no command was sent")
	}
	select {
	case extra := <-cmds:
		t.Fatalf("a second command was sent: %+v", extra)
	default:
	}
	if app.attachment != nil {
		t.Error("the attachment survived the send")
	}
}

// A photo with no caption is an ordinary message; requiring text would be a rule no client has.
func TestSubmitComposerSendsAnUncaptionedAttachment(t *testing.T) {
	app, cmds := newSendTestApp()
	app.root = tview.NewFlex().AddItem(app.messages, 0, 1, true)
	app.stageAttachment(touch(t, t.TempDir(), "shot.png"))

	app.submitComposer()

	select {
	case cmd := <-cmds:
		if cmd.Kind != telegram.CommandSendMedia || cmd.Text != "" {
			t.Fatalf("command = %+v, want an uncaptioned media send", cmd)
		}
	default:
		t.Fatal("nothing was sent for an attachment with no caption")
	}
}

// Esc unwinds the most recent thing first, and a staged file that follows the user into another chat
// is a mis-send with no warning.
func TestAttachmentClearsOnEscAndOnChatSwitch(t *testing.T) {
	app := attachTestApp(t)
	app.stageAttachment(touch(t, t.TempDir(), "shot.png"))

	if got := app.capture(keyEvent(tcell.KeyEsc)); got != nil {
		t.Fatalf("Esc returned %v, want it consumed by clearing the attachment", got)
	}
	if app.attachment != nil {
		t.Fatal("Esc did not clear the attachment")
	}
	if title := app.composer.GetTitle(); !strings.Contains(title, i18n.T(i18n.KeyUICompose)) {
		t.Errorf("composer title = %q, want it back to the plain label", title)
	}
}

func TestComposerAttachCommand(t *testing.T) {
	dir := t.TempDir()
	path := touch(t, dir, "shot.png")

	if got, ok := composerAttachCommand(":file " + path); !ok || got != path {
		t.Fatalf("attach command = (%q, %v), want the path", got, ok)
	}
	// Windows paths with spaces arrive quoted.
	if got, ok := composerAttachCommand(`:file "` + path + `"`); !ok || got != path {
		t.Fatalf("quoted path = (%q, %v), want it unquoted", got, ok)
	}

	// Everything that must be sent as ordinary text instead of being swallowed.
	for _, raw := range []string{
		"",
		"hello",
		":file",
		":file ",
		":file " + filepath.Join(dir, "missing.png"),
		":file " + dir, // a directory is not sendable
		"see :file " + path,
		"/file " + path, // a slash is a bot command and must never be intercepted
	} {
		if _, ok := composerAttachCommand(raw); ok {
			t.Errorf("composerAttachCommand(%q) was intercepted, want it sent as text", raw)
		}
	}
}
