package ui

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	termmedia "github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/storage"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const (
	// attachRowLimit caps one directory listing. A folder with ten thousand files would otherwise
	// build a list nobody scrolls; the filter is how you reach the rest.
	attachRowLimit = 500
	// attachPreviewMaxBytes skips previewing very large images. The render happens on the UI thread
	// as the cursor moves, so an unbounded decode would stall the interface mid-browse.
	attachPreviewMaxBytes = 24 << 20
	// attachDirSettingKey remembers where the picker was last used.
	//
	// Stored straight in the settings table rather than in settings.Settings on purpose: the forms
	// there rebuild that struct as a literal, so every field added to it is one more field a future
	// form can silently reset. A path the picker remembers has no business in that blast radius.
	attachDirSettingKey = "attach_dir"
)

// attachRow is one picker row. Exactly one of the three shapes applies.
type attachRow struct {
	Path      string
	IsDir     bool
	Clipboard bool
}

// attachment is a file staged for sending, shown in the composer title until it goes out.
type attachment struct {
	Path   string
	Name   string
	Size   int64
	AsFile bool
}

// openAttachPicker browses the filesystem for something to send.
//
// asFile decides up front whether the file goes as a document or as a photo, mirroring the f/F pair
// the forward picker uses: the filter field owns every letter key once the overlay is open, so an
// in-overlay letter toggle is not available. Ctrl+F flips it afterwards for anyone who changes their
// mind, and the title always says which mode is armed.
func (a *App) openAttachPicker(asFile bool) {
	if !a.draftablePeer(a.currentChat) {
		a.setStatusMsg(i18n.KeyStatusNoChatSelected)
		return
	}

	// One row per entry: with secondary text every file costs two rows, which halves how much of a
	// directory is on screen for a size that fits on the same line.
	list := tview.NewList().ShowSecondaryText(false)
	input := tview.NewInputField().SetLabel(i18n.T(i18n.KeyAttachFilterLabel))
	preview := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	preview.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyUIPreview) + " ")
	hint := tview.NewTextView().SetDynamicColors(true).SetText("[gray]" + i18n.T(i18n.KeyAttachHint))

	a.attachList = list
	a.attachInput = input
	a.attachPreview = preview
	a.attachAsFile = asFile
	a.attachDir = a.attachStartDir()

	input.SetChangedFunc(func(text string) { a.fillAttachList(text) })
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.commitAttachPick()
		}
	})
	// Enter on the list and a double-click both land here. Without it the highlight moves and
	// nothing can ever be chosen - the exact hole the forward picker shipped with.
	list.SetSelectedFunc(func(int, string, string, rune) { a.commitAttachPick() })
	list.SetChangedFunc(func(int, string, string, rune) { a.refreshAttachPreview() })
	// Focus stays on the filter so typing always filters, so the list's own movement keys never
	// reach it. Forwarding them is the deliberate custom focus arrangement AGENTS.md allows.
	input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
			if handler := list.InputHandler(); handler != nil {
				handler(event, func(tview.Primitive) {})
			}
			return nil
		case tcell.KeyCtrlF:
			a.attachAsFile = !a.attachAsFile
			a.applyAttachTitle()
			return nil
		}
		return event
	})

	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(input, 1, 0, true).
		AddItem(list, 0, 1, false).
		AddItem(hint, 1, 0, false)
	layout := tview.NewFlex().
		AddItem(left, 0, 3, true).
		AddItem(preview, 0, 2, false)
	a.attachLayout = layout
	a.applyAttachTitle()

	a.fillAttachList("")
	a.app.SetRoot(layout, true)
	a.app.SetFocus(input)
}

// applyAttachTitle names the mode and the directory being browsed.
//
// The directory belongs here rather than on a row: as the parent row's secondary text it was a full
// path taking a whole line and pushing the files off screen.
func (a *App) applyAttachTitle() {
	if a.attachLayout == nil {
		return
	}
	key := i18n.KeyAttachTitle
	if a.attachAsFile {
		key = i18n.KeyAttachTitleAsFile
	}
	title := i18n.T(key)
	if a.attachDir != "" {
		title += " · " + render.Truncate(a.attachDir, 40)
	}
	a.attachLayout.SetBorder(true).SetTitle(" " + title + " ")
}

func (a *App) closeAttachPicker() {
	a.attachList = nil
	a.attachInput = nil
	a.attachPreview = nil
	a.attachLayout = nil
	a.restoreMessageFocus()
}

// attachStartDir is where browsing begins: where it was left last time, then the home directory.
func (a *App) attachStartDir() string {
	if a.db != nil {
		if stored, ok, err := a.db.GetSetting(context.Background(), attachDirSettingKey); err == nil && ok {
			if info, err := os.Stat(stored); err == nil && info.IsDir() {
				return stored
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

func (a *App) rememberAttachDir(dir string) {
	if a.db == nil || dir == "" {
		return
	}
	_ = a.db.SetSetting(context.Background(), attachDirSettingKey, dir)
}

// fillAttachList rebuilds the rows for the current directory and filter.
func (a *App) fillAttachList(filter string) {
	if a.attachList == nil {
		return
	}
	a.attachList.Clear()
	a.attachRows = a.attachRows[:0]
	needle := strings.ToLower(strings.TrimSpace(filter))

	// The clipboard leads, and only with no filter: someone who typed a name is looking for a file.
	// It is offered unconditionally rather than probed, because probing means running the helper on
	// every keystroke - the row itself reports plainly when there is no image.
	if needle == "" {
		a.attachList.AddItem(i18n.T(i18n.KeyAttachClipboard), "", 0, nil)
		a.attachRows = append(a.attachRows, attachRow{Clipboard: true})
		if parent := filepath.Dir(a.attachDir); parent != a.attachDir {
			a.attachList.AddItem("../", "", 0, nil)
			a.attachRows = append(a.attachRows, attachRow{Path: parent, IsDir: true})
		}
	}

	entries, err := os.ReadDir(a.attachDir)
	if err != nil {
		a.attachList.AddItem(i18n.Tf(i18n.KeyStatusError, err.Error()), "", 0, nil)
		return
	}
	// Directories first, then files, each alphabetically: the order every file browser uses, and
	// the one that makes descending a tree predictable.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	for _, entry := range entries {
		if len(a.attachRows) >= attachRowLimit {
			return
		}
		name := entry.Name()
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		if strings.HasPrefix(name, ".") {
			// Hidden entries are noise in a send-a-file list; the filter still reaches them by name.
			if needle == "" {
				continue
			}
		}
		path := filepath.Join(a.attachDir, name)
		if entry.IsDir() {
			a.attachList.AddItem(name+"/", "", 0, nil)
			a.attachRows = append(a.attachRows, attachRow{Path: path, IsDir: true})
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		a.attachList.AddItem(attachRowLabel(name, storage.FormatBytes(info.Size())), "", 0, nil)
		a.attachRows = append(a.attachRows, attachRow{Path: path})
	}
	a.refreshAttachPreview()
}

// refreshAttachPreview renders the highlighted file, or says what it is when it cannot be drawn.
func (a *App) refreshAttachPreview() {
	if a.attachPreview == nil || a.attachList == nil {
		return
	}
	row, ok := a.selectedAttachRow()
	if !ok || row.IsDir || row.Clipboard {
		a.attachPreview.SetText("")
		return
	}
	file, err := termmedia.InspectOutgoing(row.Path, a.attachAsFile)
	if err != nil {
		a.attachPreview.SetText("[red]" + err.Error())
		return
	}
	summary := i18n.T(outgoingKindLabelKey(file.Kind)) + " · " + storage.FormatBytes(file.Size)
	if w, h, ok := termmedia.ImageDimensions(file.Path); ok {
		summary += " · " + itoa(w) + "x" + itoa(h)
	}
	if file.Size > attachPreviewMaxBytes {
		a.attachPreview.SetText(summary)
		return
	}
	raster := termmedia.RasterPreviewANSIToTview(file.Path, termmedia.RasterPreviewOptions{
		MaxCols: termmedia.PreviewMaxCols,
		MaxRows: termmedia.PreviewMaxRows,
	})
	if raster == "" {
		a.attachPreview.SetText(summary)
		return
	}
	a.attachPreview.SetText(raster + "\n" + summary)
}

func (a *App) selectedAttachRow() (attachRow, bool) {
	if a.attachList == nil {
		return attachRow{}, false
	}
	index := a.attachList.GetCurrentItem()
	if index < 0 || index >= len(a.attachRows) {
		return attachRow{}, false
	}
	return a.attachRows[index], true
}

// commitAttachPick descends into a directory or stages a file.
func (a *App) commitAttachPick() {
	row, ok := a.selectedAttachRow()
	if !ok {
		a.setStatusMsg(i18n.KeyStatusAttachNothingSelected)
		return
	}
	switch {
	case row.IsDir:
		a.attachDir = row.Path
		a.rememberAttachDir(row.Path)
		if a.attachInput != nil {
			// Clearing the filter re-runs fillAttachList through the changed callback; setting the
			// text is what redraws the new directory.
			a.attachInput.SetText("")
		}
		a.fillAttachList("")
	case row.Clipboard:
		path, err := termmedia.ClipboardImage(a.cfg.Paths.MediaDir)
		if err != nil {
			a.setStatusError(err)
			return
		}
		a.stageAttachment(path)
	default:
		a.stageAttachment(row.Path)
	}
}

// stageAttachment holds a file until the composer is sent.
func (a *App) stageAttachment(path string) {
	file, err := termmedia.InspectOutgoing(path, a.attachAsFile)
	if err != nil {
		a.setStatusError(err)
		return
	}
	// Attaching while editing leaves edit mode: an edit cannot carry media, so the two modes are
	// mutually exclusive and the newer intent wins.
	a.cancelEdit()
	a.attachment = &attachment{Path: file.Path, Name: file.FileName, Size: file.Size, AsFile: a.attachAsFile}
	a.rememberAttachDir(filepath.Dir(file.Path))
	a.closeAttachPicker()
	a.applyComposerTitle()
	a.app.SetFocus(a.composer)
	a.updateFocusStyle()
	a.setStatusMsg(i18n.KeyStatusAttachmentStaged, file.FileName)
}

// clearAttachment drops the staged file. Called on send, on chat switch and on Esc, for the same
// reason the reply target is: an attachment that follows the user into another chat is a mis-send
// waiting to happen.
func (a *App) clearAttachment() bool {
	if a.attachment == nil {
		return false
	}
	a.attachment = nil
	a.applyComposerTitle()
	return true
}

// applyComposerTitle shows the reply target and the staged attachment in the composer border.
//
// One function for both, because they can be set at the same time and each used to overwrite the
// other's title.
func (a *App) applyComposerTitle() {
	if a.composer == nil {
		return
	}
	// Edit mode owns the whole title: the composer holds a copy of an existing message, and showing
	// a reply target beside it would describe a message that is not being sent.
	if title := a.editTitle(); title != "" {
		a.composer.SetTitle(" " + title + " ")
		return
	}
	parts := make([]string, 0, 2)
	if a.replyTarget != nil {
		parts = append(parts, i18n.Tf(i18n.KeyUIReplyTo, replyTargetLabel(*a.replyTarget)))
	}
	if a.attachment != nil {
		parts = append(parts, render.Glyphs().Attach+" "+a.attachment.Name+" "+storage.FormatBytes(a.attachment.Size))
	}
	if len(parts) == 0 {
		a.composer.SetTitle(" " + i18n.T(i18n.KeyUICompose) + " ")
		return
	}
	a.composer.SetTitle(" " + strings.Join(parts, " · ") + " ")
}

// replyTargetLabel is the short description of a reply target.
//
// Truncated by display width: the previous label[:40] cut a byte slice, which splits a CJK rune in
// half and leaves a broken glyph in the border.
func replyTargetLabel(msg telegram.Message) string {
	label := strings.TrimSpace(msg.Text)
	if label == "" {
		label = msg.Media.Label
	}
	if label == "" {
		label = "message " + msg.ID
	}
	return render.Truncate(label, 40)
}

// attachNameColumn is where the size column starts. Names longer than this push it right rather than
// being cut: the name is what the user is looking for, the size is a detail.
const attachNameColumn = 26

// attachRowLabel puts the size on the same line as the name, padded into a column.
//
// Display width, not len: a CJK filename is twice as wide per rune, and padding by bytes would leave
// the sizes visibly ragged in exactly the directories most likely to have long names.
func attachRowLabel(name, size string) string {
	label := name
	if pad := attachNameColumn - render.StringWidth(name); pad > 0 {
		label += strings.Repeat(" ", pad)
	}
	return label + "  " + size
}

func outgoingKindLabelKey(kind string) string {
	switch kind {
	case string(termmedia.KindPhoto):
		return i18n.KeyMediaPhoto
	case string(termmedia.KindGIFAnimation):
		return i18n.KeyMediaGIF
	case string(termmedia.KindVideo):
		return i18n.KeyMediaVideo
	case "audio":
		return i18n.KeyMediaAudio
	default:
		return i18n.KeyMediaDocument
	}
}

// itoa keeps the dimension string allocation-free of fmt for a line redrawn on every cursor move.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	return string(digits[i:])
}
