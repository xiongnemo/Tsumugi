package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const defaultMessageViewportLimit = 1000

type MessageViewport struct {
	*tview.Box

	rows         map[string]telegram.Message
	messages     []telegram.Message
	blocks       []messageBlock
	selected     int
	scroll       int
	followEnd    bool
	messageLimit int
	// pendingBelow counts messages appended while the viewport was not scrolled to the bottom.
	pendingBelow             int
	placeholder              string
	onAction                 func()
	onReachOlder             func()
	lastOlderFire            time.Time
	gifTick                  int
	inlineAnim               bool
	layoutMode               render.LayoutMode
	broadcastChan            bool
	groupReadMarks           bool
	layoutWidth              int
	layoutGIFTick            int
	layoutDirty              bool
	skipInlineAnimNextLayout bool
	layoutRelayoutBusy       bool
	pinnedBanner             string
}

type messageBlock struct {
	id         string
	lines      []string
	offset     int
	height     int
	alignRight bool
}

type MessageViewportStats struct {
	Messages      int
	Blocks        int
	RenderedLines int
	Limit         int
}

func NewMessageViewport() *MessageViewport {
	return &MessageViewport{
		Box:          tview.NewBox(),
		rows:         make(map[string]telegram.Message),
		selected:     -1,
		messageLimit: defaultMessageViewportLimit,
	}
}

func (v *MessageViewport) SetPinnedBanner(text string) {
	v.pinnedBanner = strings.TrimSpace(text)
}

func (v *MessageViewport) pinnedBannerLines() int {
	if v.pinnedBanner == "" {
		return 0
	}
	return 1
}

func (v *MessageViewport) messageAreaHeight(innerHeight int) int {
	h := innerHeight - v.pinnedBannerLines()
	if h < 1 {
		return 1
	}
	return h
}

func (v *MessageViewport) SetInlineAnim(enabled bool) {
	v.inlineAnim = enabled
	if !enabled {
		v.gifTick = 0
		media.ClearInlineAnimCache()
	}
	v.invalidateLayout()
}

func (v *MessageViewport) SetLayoutMode(mode render.LayoutMode) {
	if v.layoutMode == mode {
		return
	}
	v.layoutMode = mode
	v.skipInlineAnimNextLayout = true
	v.layoutRelayoutBusy = true
	v.invalidateLayout()
}

func (v *MessageViewport) SetLayoutRelayoutBusy(busy bool) {
	v.layoutRelayoutBusy = busy
}

func (v *MessageViewport) LayoutRelayoutBusy() bool {
	return v.layoutRelayoutBusy
}

func (v *MessageViewport) LayoutWidthForBuild() int {
	if v.layoutWidth > 0 {
		return v.layoutWidth
	}
	_, _, w, _ := v.GetInnerRect()
	if w <= 0 {
		return 80
	}
	return w
}

func (v *MessageViewport) ApplyPrebuiltLayout(blocks []messageBlock, width int, mode render.LayoutMode) {
	if len(blocks) != len(v.messages) {
		v.invalidateLayout()
		v.layoutRelayoutBusy = false
		v.skipInlineAnimNextLayout = false
		return
	}
	v.blocks = blocks
	v.layoutMode = mode
	v.layoutWidth = width
	v.layoutGIFTick = v.gifTick
	v.layoutDirty = false
	v.layoutRelayoutBusy = false
	v.skipInlineAnimNextLayout = false
}

func buildAllMessageBlocks(messages []telegram.Message, width int, mode render.LayoutMode, broadcast bool, groupRead bool, inlineAnim bool, gifTick int, skipInlineAnim bool) []messageBlock {
	if width <= 0 {
		width = 80
	}
	gutter := 2
	contentWidth := maxInt(1, width-gutter)
	rowOpts := render.MessageRowOpts{
		Layout:              mode,
		IncludeMediaPreview: true,
		BroadcastChannel:    broadcast,
		GroupReadMarks:      groupRead,
	}
	opts := blockBuildOpts{
		layoutMode:     mode,
		broadcast:      broadcast,
		inlineAnim:     inlineAnim,
		gifTick:        gifTick,
		skipInlineAnim: skipInlineAnim,
	}
	blocks := make([]messageBlock, 0, len(messages))
	offset := 0
	for _, message := range messages {
		block, _ := buildMessageBlock(message, contentWidth, rowOpts, opts)
		block.offset = offset
		offset += block.height
		blocks = append(blocks, block)
	}
	return blocks
}

type blockBuildOpts struct {
	layoutMode     render.LayoutMode
	broadcast      bool
	inlineAnim     bool
	gifTick        int
	skipInlineAnim bool
}

func (v *MessageViewport) SetBroadcastChannel(enabled bool) {
	v.broadcastChan = enabled
	v.invalidateLayout()
}

func (v *MessageViewport) SetGroupReadMarks(enabled bool) {
	v.groupReadMarks = enabled
	v.invalidateLayout()
}

func (v *MessageViewport) invalidateLayout() {
	v.blocks = nil
	v.layoutDirty = true
}

func (v *MessageViewport) messageCap() int {
	if v.messageLimit <= 0 {
		return defaultMessageViewportLimit
	}
	return v.messageLimit
}

func (v *MessageViewport) setMessageLimitForTest(limit int) {
	v.messageLimit = limit
	v.rebuild(v.selectedID())
	v.invalidateLayout()
}

func (v *MessageViewport) Stats() MessageViewportStats {
	lines := 0
	for _, block := range v.blocks {
		lines += len(block.lines)
	}
	return MessageViewportStats{
		Messages:      len(v.messages),
		Blocks:        len(v.blocks),
		RenderedLines: lines,
		Limit:         v.messageCap(),
	}
}

func (v *MessageViewport) PatchMessages(updates []telegram.Message) bool {
	if len(updates) == 0 {
		return false
	}
	changed := false
	for _, upd := range updates {
		if _, ok := v.rows[upd.ID]; !ok {
			continue
		}
		v.rows[upd.ID] = upd
		changed = true
	}
	if !changed {
		return false
	}
	anchor := v.selectedID()
	_, _, iw, ih := v.GetInnerRect()
	if iw <= 0 {
		iw = v.layoutWidth
	}
	if iw <= 0 {
		iw = 80
	}
	if ih <= 0 {
		ih = 24
	}
	v.layout(iw)
	oldTotal := v.totalHeight()
	oldMaxScroll := maxInt(0, oldTotal-ih)
	atBottom := oldMaxScroll <= 0 || v.scroll >= oldMaxScroll-1
	oldSelOffset := 0
	hadAnchor := false
	if !atBottom && anchor != "" && v.selected >= 0 && v.selected < len(v.blocks) && v.selectedID() == anchor {
		oldSelOffset = v.blocks[v.selected].offset
		hadAnchor = true
	}
	v.rebuild(anchor)
	v.invalidateLayout()
	v.layout(iw)
	if atBottom {
		v.followEnd = true
		v.pendingBelow = 0
	} else if hadAnchor && v.selected >= 0 && v.selected < len(v.blocks) && v.selectedID() == anchor {
		delta := v.blocks[v.selected].offset - oldSelOffset
		v.scroll += delta
		maxScr := maxInt(0, v.totalHeight()-ih)
		if v.scroll > maxScr {
			v.scroll = maxScr
		}
		if v.scroll < 0 {
			v.scroll = 0
		}
	}
	return true
}

func (v *MessageViewport) ResetInlineAnimTick() {
	v.gifTick = 0
}

func (v *MessageViewport) SetActionFunc(fn func()) {
	v.onAction = fn
}

func (v *MessageViewport) SetOnReachOlder(fn func()) {
	v.onReachOlder = fn
}

func (v *MessageViewport) fireReachOlder() {
	if v.onReachOlder == nil || len(v.messages) == 0 {
		return
	}
	if time.Since(v.lastOlderFire) < 600*time.Millisecond {
		return
	}
	v.lastOlderFire = time.Now()
	v.onReachOlder()
}

func (v *MessageViewport) SetText(text string) {
	v.placeholder = text
	v.rows = make(map[string]telegram.Message)
	v.messages = nil
	v.blocks = nil
	v.selected = -1
	v.scroll = 0
	v.pendingBelow = 0
	v.invalidateLayout()
}

func (v *MessageViewport) Clear() {
	v.SetText("")
}

func (v *MessageViewport) SetMessages(messages []telegram.Message) {
	v.placeholder = ""
	v.rows = make(map[string]telegram.Message, len(messages))
	for _, message := range messages {
		v.rows[message.ID] = message
	}
	v.rebuild("")
	v.selected = -1
	if len(v.messages) > 0 {
		v.selected = len(v.messages) - 1
	}
	v.followEnd = true
	v.pendingBelow = 0
	v.invalidateLayout()
}

// SetMessagesReplace replaces the in-memory message list. When preserveViewport is true, the
// currently selected message (if any) stays selected and scroll is shifted by the prepended
// content height so the viewport does not jump when older messages are merged in.
func (v *MessageViewport) SetMessagesReplace(messages []telegram.Message, preserveViewport bool) {
	if !preserveViewport {
		v.SetMessages(messages)
		return
	}

	anchorID := v.selectedID()
	if anchorID == "" {
		v.SetMessages(messages)
		return
	}

	_, _, iw, ih := v.GetInnerRect()
	if iw <= 0 {
		iw = 80
	}
	if ih <= 0 {
		ih = 24
	}

	var oldSelOffset int
	hadAnchorBlock := false
	if v.selected >= 0 && v.selected < len(v.blocks) && !v.layoutDirty && v.layoutWidth == iw && v.selectedID() == anchorID {
		oldSelOffset = v.blocks[v.selected].offset
		hadAnchorBlock = true
	} else {
		v.layout(iw)
		if v.selected >= 0 && v.selected < len(v.blocks) && v.selectedID() == anchorID {
			oldSelOffset = v.blocks[v.selected].offset
			hadAnchorBlock = true
		}
	}

	v.placeholder = ""
	v.rows = make(map[string]telegram.Message, len(messages))
	for _, message := range messages {
		v.rows[message.ID] = message
	}
	v.rebuild(anchorID)
	v.followEnd = false
	v.pendingBelow = 0
	v.invalidateLayout()

	v.layout(iw)

	if hadAnchorBlock && v.selected >= 0 && v.selected < len(v.blocks) && v.selectedID() == anchorID {
		delta := v.blocks[v.selected].offset - oldSelOffset
		v.scroll += delta
		maxScr := maxInt(0, v.totalHeight()-ih)
		if v.scroll > maxScr {
			v.scroll = maxScr
		}
		if v.scroll < 0 {
			v.scroll = 0
		}
	}
	v.ensureSelectedVisibleWithoutRelayout(ih)
}

// ReplaceMessagesPreserveState swaps the visible message data without triggering
// history-loading side effects owned by App.setMessages.
func (v *MessageViewport) ReplaceMessagesPreserveState(messages []telegram.Message) {
	anchorID := v.selectedID()
	scroll := v.scroll
	followEnd := v.followEnd
	pendingBelow := v.pendingBelow

	v.placeholder = ""
	v.rows = make(map[string]telegram.Message, len(messages))
	for _, message := range messages {
		v.rows[message.ID] = message
	}
	v.rebuild(anchorID)
	v.scroll = scroll
	v.followEnd = followEnd
	v.pendingBelow = pendingBelow
	v.invalidateLayout()
}

func (v *MessageViewport) AppendMessage(message telegram.Message) {
	v.placeholder = ""
	selectedID := v.selectedID()

	_, _, w, h := v.GetInnerRect()
	v.layout(w)
	oldLen := len(v.messages)
	lastIdx := oldLen - 1
	selectedWasLast := oldLen > 0 && v.selected == lastIdx

	maxScr := maxInt(0, v.totalHeight()-h)
	var atBottom bool
	switch {
	case h <= 0 || oldLen == 0:
		atBottom = true
	case maxScr <= 0:
		atBottom = selectedWasLast
	default:
		atBottom = v.scroll >= maxScr-1
	}

	v.rows[message.ID] = message
	v.rebuild(selectedID)
	v.invalidateLayout()

	if atBottom {
		v.followEnd = true
		v.pendingBelow = 0
	} else {
		v.pendingBelow++
	}
}

// ApplyReadOutboxMaxID marks outgoing synced messages as read up to maxID (private chats only).
func (v *MessageViewport) ApplyReadOutboxMaxID(maxID int, privateChat bool) {
	if !privateChat || maxID <= 0 || len(v.messages) == 0 {
		return
	}
	changed := false
	for i := range v.messages {
		msg := v.messages[i]
		if !msg.Outgoing || msg.State != "synced" || msg.ReadByPeer {
			continue
		}
		id, err := strconv.Atoi(msg.ID)
		if err != nil || id <= 0 || id > maxID {
			continue
		}
		msg.ReadByPeer = true
		v.messages[i] = msg
		v.rows[msg.ID] = msg
		changed = true
	}
	if !changed {
		return
	}
	anchor := v.selectedID()
	_, _, w, _ := v.GetInnerRect()
	v.rebuild(anchor)
	v.invalidateLayout()
	if w > 0 {
		v.layout(w)
	}
}

// PendingBelow returns how many messages arrived while the user was scrolled above the bottom.
func (v *MessageViewport) PendingBelow() int {
	return v.pendingBelow
}

func (v *MessageViewport) RemoveIDs(ids []string) {
	if len(ids) == 0 {
		return
	}
	selectedID := v.selectedID()
	for _, id := range ids {
		delete(v.rows, id)
	}
	v.rebuild(selectedID)
	if len(v.messages) == 0 {
		v.selected = -1
	}
	v.invalidateLayout()
}

func (v *MessageViewport) Messages() []telegram.Message {
	return append([]telegram.Message(nil), v.messages...)
}

func (v *MessageViewport) OldestMessageID() string {
	if len(v.messages) == 0 {
		return ""
	}
	return v.messages[0].ID
}

func (v *MessageViewport) SelectedMessage() (telegram.Message, bool) {
	if v.selected < 0 || v.selected >= len(v.messages) {
		return telegram.Message{}, false
	}
	return v.messages[v.selected], true
}

func (v *MessageViewport) SelectDelta(delta int) {
	if len(v.messages) == 0 {
		return
	}
	if delta < 0 && v.selected == 0 {
		v.fireReachOlder()
		return
	}
	v.selected += delta
	if v.selected < 0 {
		v.selected = 0
	}
	if v.selected >= len(v.messages) {
		v.selected = len(v.messages) - 1
	}
	v.followEnd = false
	v.ensureSelectedVisible()
}

func (v *MessageViewport) SelectByID(id string) bool {
	for i, message := range v.messages {
		if message.ID == id {
			v.selected = i
			v.followEnd = false
			v.ensureSelectedVisible()
			return true
		}
	}
	return false
}

func (v *MessageViewport) ScrollToEnd() {
	v.followEnd = true
	v.pendingBelow = 0
	if len(v.messages) > 0 && v.selected < 0 {
		v.selected = len(v.messages) - 1
	}
}

func (v *MessageViewport) Draw(screen tcell.Screen) {
	v.Box.DrawForSubclass(screen, v)
	x, y, width, height := v.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	bannerLines := v.pinnedBannerLines()
	msgY := y + bannerLines
	msgHeight := v.messageAreaHeight(height)
	if bannerLines > 0 {
		line := render.Truncate("📌 "+v.pinnedBanner, width)
		tview.Print(screen, line, x, y, width, tview.AlignLeft, tcell.ColorYellow)
	}
	if len(v.messages) == 0 {
		v.drawPlaceholder(screen, x, msgY, width, msgHeight)
		return
	}
	v.layout(width)
	totalHeight := v.totalHeight()
	maxScroll := maxInt(0, totalHeight-msgHeight)
	if v.followEnd {
		v.scroll = maxScroll
		v.followEnd = false
	}
	if maxScroll <= 0 || v.scroll >= maxScroll-1 {
		v.pendingBelow = 0
	}
	v.ensureSelectedVisibleWithoutRelayout(msgHeight)
	if v.scroll > maxScroll {
		v.scroll = maxScroll
	}
	if v.scroll < 0 {
		v.scroll = 0
	}
	selectedStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkCyan).Bold(true)
	gutter := 2
	contentWidth := maxInt(1, width-gutter)
	for blockIndex, block := range v.blocks {
		for lineIndex, line := range block.lines {
			absoluteY := block.offset + lineIndex
			screenY := msgY + absoluteY - v.scroll
			if screenY < msgY || screenY >= msgY+msgHeight {
				continue
			}
			selected := blockIndex == v.selected
			cursor := "  "
			if selected {
				cursor = "> "
			}
			lineText := line
			pad := 0
			if block.alignRight {
				pad = maxInt(0, contentWidth-render.DisplayWidth(lineText))
			}
			drawX := x + gutter + pad
			lineWidth := gutter + pad + render.DisplayWidth(lineText)
			if selected {
				highlightStart := drawX
				highlightEnd := drawX + render.DisplayWidth(lineText)
				if block.alignRight {
					for fillX := highlightStart; fillX < highlightEnd && fillX < x+width; fillX++ {
						screen.SetContent(fillX, screenY, ' ', nil, selectedStyle)
					}
				} else {
					for fillX := x; fillX < x+width; fillX++ {
						screen.SetContent(fillX, screenY, ' ', nil, selectedStyle)
					}
				}
			}
			tview.Print(screen, cursor, x, screenY, gutter, tview.AlignLeft, tcell.ColorWhite)
			tview.Print(screen, lineText, drawX, screenY, width-(drawX-x), tview.AlignLeft, tcell.ColorWhite)
			_ = lineWidth
		}
	}
}

func (v *MessageViewport) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return v.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyUp:
			v.SelectDelta(-1)
		case tcell.KeyDown:
			v.SelectDelta(1)
		case tcell.KeyPgUp:
			_, _, iw, ih := v.GetInnerRect()
			if ih > 0 && len(v.messages) > 0 {
				v.layout(iw)
				if v.scroll <= 0 {
					v.fireReachOlder()
				} else {
					v.scroll -= 10
					if v.scroll < 0 {
						v.scroll = 0
					}
				}
				v.followEnd = false
				v.syncSelectionAfterScrollUp(iw)
			}
		case tcell.KeyPgDn:
			_, _, iw, ih := v.GetInnerRect()
			if ih > 0 && len(v.messages) > 0 {
				v.layout(iw)
				maxScr := maxInt(0, v.totalHeight()-ih)
				v.scroll += 10
				if v.scroll > maxScr {
					v.scroll = maxScr
				}
				v.followEnd = false
				v.syncSelectionAfterScrollDown(iw, ih)
			}
		case tcell.KeyHome:
			if len(v.messages) > 0 {
				v.selected = 0
				v.ensureSelectedVisible()
			}
		case tcell.KeyEnd:
			if len(v.messages) > 0 {
				v.selected = len(v.messages) - 1
				v.ScrollToEnd()
			}
		case tcell.KeyEnter:
			if v.onAction != nil {
				v.onAction()
			}
		default:
			switch event.Rune() {
			case 'j':
				v.SelectDelta(1)
			case 'k':
				v.SelectDelta(-1)
			}
		}
	})
}

func (v *MessageViewport) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return v.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		x, y := event.Position()
		switch action {
		case tview.MouseScrollUp:
			if v.InInnerRect(x, y) {
				if len(v.messages) > 0 && v.scroll <= 0 {
					v.fireReachOlder()
				} else {
					v.scroll--
					if v.scroll < 0 {
						v.scroll = 0
					}
				}
				_, _, iw, ih := v.GetInnerRect()
				if ih > 0 && len(v.messages) > 0 {
					v.followEnd = false
					v.syncSelectionAfterScrollUp(iw)
				}
				return true, nil
			}
		case tview.MouseScrollDown:
			if v.InInnerRect(x, y) {
				v.scroll++
				v.followEnd = false
				_, _, iw, ih := v.GetInnerRect()
				if ih > 0 && len(v.messages) > 0 {
					v.layout(iw)
					maxScr := maxInt(0, v.totalHeight()-ih)
					if v.scroll > maxScr {
						v.scroll = maxScr
					}
					v.syncSelectionAfterScrollDown(iw, ih)
				}
				return true, nil
			}
		case tview.MouseLeftDown, tview.MouseLeftClick, tview.MouseLeftDoubleClick:
			if !v.InInnerRect(x, y) {
				return false, nil
			}
			setFocus(v)
			v.selectAtScreenY(y)
			if action == tview.MouseLeftDoubleClick && v.onAction != nil {
				v.onAction()
			}
			return true, nil
		}
		return false, nil
	})
}

func (v *MessageViewport) drawPlaceholder(screen tcell.Screen, x, y, width, height int) {
	lines := tview.WordWrap(v.placeholder, width)
	for i, line := range lines {
		if i >= height {
			return
		}
		tview.Print(screen, line, x, y+i, width, tview.AlignLeft, tcell.ColorWhite)
	}
}

func (v *MessageViewport) AdvanceGIF() bool {
	if !v.inlineAnim {
		return false
	}
	found := false
	for _, m := range v.messages {
		if !inlineAnimKind(m.Media.Kind) || m.Media.LocalPath == "" {
			continue
		}
		if media.InlineAnimatable(m.Media.LocalPath) {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	v.gifTick++
	return true
}

func inlineAnimKind(kind string) bool {
	return kind == "gif" || kind == "video_sticker"
}

func (v *MessageViewport) layout(width int) {
	if width <= 0 {
		width = 1
	}
	if !v.layoutDirty && v.layoutWidth == width && len(v.blocks) == len(v.messages) && len(v.messages) > 0 {
		if v.inlineAnim && v.layoutGIFTick != v.gifTick {
			v.refreshInlineAnimBlocks(width)
		}
		return
	}
	v.doFullLayout(width)
}

func (v *MessageViewport) doFullLayout(width int) {
	skipInlineAnim := v.skipInlineAnimNextLayout
	v.skipInlineAnimNextLayout = false
	v.blocks = make([]messageBlock, 0, len(v.messages))
	offset := 0
	gutter := 2
	contentWidth := maxInt(1, width-gutter)
	rowOpts := render.MessageRowOpts{
		Layout:              v.layoutMode,
		IncludeMediaPreview: true,
		BroadcastChannel:    v.broadcastChan,
		GroupReadMarks:      v.groupReadMarks,
	}
	for _, message := range v.messages {
		block, _ := v.buildMessageBlock(message, contentWidth, rowOpts, skipInlineAnim)
		block.offset = offset
		offset += block.height
		v.blocks = append(v.blocks, block)
	}
	v.layoutWidth = width
	v.layoutGIFTick = v.gifTick
	v.layoutDirty = false
	v.layoutRelayoutBusy = false
}

func (v *MessageViewport) buildMessageBlock(message telegram.Message, contentWidth int, rowOpts render.MessageRowOpts, skipInlineAnim bool) (messageBlock, bool) {
	return buildMessageBlock(message, contentWidth, rowOpts, blockBuildOpts{
		layoutMode:     v.layoutMode,
		broadcast:      v.broadcastChan,
		inlineAnim:     v.inlineAnim,
		gifTick:        v.gifTick,
		skipInlineAnim: skipInlineAnim,
	})
}

func buildMessageBlock(message telegram.Message, contentWidth int, rowOpts render.MessageRowOpts, opts blockBuildOpts) (messageBlock, bool) {
	inlineMedia := !opts.skipInlineAnim && opts.inlineAnim && inlineAnimKind(message.Media.Kind) && message.Media.LocalPath != ""
	rowOpts.IncludeMediaPreview = !inlineMedia
	lines := render.MessageRowLines(message, contentWidth, rowOpts)
	if inlineMedia {
		previewLines := normalizeInlinePreviewLines(message.Media.LocalPath, opts.gifTick)
		lines = append(previewLines, lines...)
	}
	return messageBlock{
		id:         message.ID,
		lines:      lines,
		height:     len(lines) + 1,
		alignRight: opts.layoutMode == render.LayoutIM && message.Outgoing,
	}, inlineMedia
}

func normalizeInlinePreviewLines(path string, tick int) []string {
	rows := media.PreviewMaxRows
	if rows <= 0 {
		return nil
	}
	out := make([]string, rows)
	if path != "" {
		if ansi := media.AnimatedInlineANSI(path, tick, media.PreviewMaxCols, rows); ansi != "" {
			got := strings.Split(media.ANSISGRToTview(ansi), "\n")
			for i := 0; i < rows && i < len(got); i++ {
				out[i] = got[i]
			}
		}
	}
	return out
}

func (v *MessageViewport) refreshInlineAnimBlocks(width int) int {
	gutter := 2
	contentWidth := maxInt(1, width-gutter)
	rowOpts := render.MessageRowOpts{
		Layout:              v.layoutMode,
		IncludeMediaPreview: false,
		BroadcastChannel:    v.broadcastChan,
		GroupReadMarks:      v.groupReadMarks,
	}
	offset := 0
	animCount := 0
	for i, message := range v.messages {
		if i >= len(v.blocks) {
			break
		}
		inlineMedia := v.inlineAnim && inlineAnimKind(message.Media.Kind) && message.Media.LocalPath != ""
		if inlineMedia {
			animCount++
			block, _ := v.buildMessageBlock(message, contentWidth, rowOpts, false)
			v.blocks[i].lines = block.lines
			v.blocks[i].height = block.height
			v.blocks[i].alignRight = block.alignRight
		}
		v.blocks[i].offset = offset
		offset += v.blocks[i].height
	}
	v.layoutGIFTick = v.gifTick
	return animCount
}

func (v *MessageViewport) ensureSelectedVisibleWithoutRelayout(height int) {
	if v.selected < 0 || v.selected >= len(v.messages) || v.selected >= len(v.blocks) {
		return
	}
	if height <= 0 {
		return
	}
	block := v.blocks[v.selected]
	if block.offset < v.scroll {
		v.scroll = block.offset
		return
	}
	if block.offset+block.height > v.scroll+height {
		v.scroll = block.offset + block.height - height
	}
}

func (v *MessageViewport) rebuild(selectedID string) {
	v.messages = v.messages[:0]
	for _, message := range v.rows {
		v.messages = append(v.messages, message)
	}
	sort.SliceStable(v.messages, func(i, j int) bool {
		if v.messages[i].CreatedAt.Equal(v.messages[j].CreatedAt) {
			return messageIDLess(v.messages[i].ID, v.messages[j].ID)
		}
		return v.messages[i].CreatedAt.Before(v.messages[j].CreatedAt)
	})
	v.trimMessagesToCap(selectedID)
	v.selected = -1
	if selectedID != "" {
		for i, message := range v.messages {
			if message.ID == selectedID {
				v.selected = i
				break
			}
		}
	}
	if v.selected < 0 && len(v.messages) > 0 {
		v.selected = len(v.messages) - 1
	}
}

func (v *MessageViewport) trimMessagesToCap(anchorID string) {
	limit := v.messageCap()
	if limit <= 0 || len(v.messages) <= limit {
		return
	}
	start := len(v.messages) - limit
	if anchorID != "" {
		if anchorIdx := messageIndexByID(v.messages, anchorID); anchorIdx >= 0 {
			start = anchorIdx - limit/2
			if start < 0 {
				start = 0
			}
			if maxStart := len(v.messages) - limit; start > maxStart {
				start = maxStart
			}
		}
	}
	kept := append([]telegram.Message(nil), v.messages[start:start+limit]...)
	v.messages = kept
	v.rows = make(map[string]telegram.Message, len(kept))
	for _, message := range kept {
		v.rows[message.ID] = message
	}
}

func messageIndexByID(messages []telegram.Message, id string) int {
	for i, message := range messages {
		if message.ID == id {
			return i
		}
	}
	return -1
}

func (v *MessageViewport) selectedID() string {
	if v.selected < 0 || v.selected >= len(v.messages) {
		return ""
	}
	return v.messages[v.selected].ID
}

func (v *MessageViewport) selectAtScreenY(screenY int) {
	_, innerY, width, _ := v.GetInnerRect()
	v.layout(width)
	target := v.scroll + screenY - innerY
	for i, block := range v.blocks {
		if target >= block.offset && target < block.offset+block.height {
			v.selected = i
			v.followEnd = false
			return
		}
	}
}

func (v *MessageViewport) ensureSelectedVisible() {
	if v.selected < 0 || v.selected >= len(v.messages) {
		return
	}
	_, _, width, height := v.GetInnerRect()
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		return
	}
	msgHeight := v.messageAreaHeight(height)
	v.layout(width)
	v.ensureSelectedVisibleWithoutRelayout(msgHeight)
}

func (v *MessageViewport) totalHeight() int {
	if len(v.blocks) == 0 {
		return 0
	}
	last := v.blocks[len(v.blocks)-1]
	return last.offset + last.height
}

func messageIDLess(left, right string) bool {
	leftID, leftErr := strconv.Atoi(left)
	rightID, rightErr := strconv.Atoi(right)
	if leftErr == nil && rightErr == nil {
		return leftID < rightID
	}
	return left < right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (v *MessageViewport) syncSelectionAfterScrollUp(width int) {
	v.layout(width)
	total := v.totalHeight()
	if total == 0 {
		return
	}
	line := v.scroll
	if line >= total {
		line = total - 1
	}
	v.selected = v.blockIndexAtContentLine(width, line)
}

func (v *MessageViewport) syncSelectionAfterScrollDown(width, innerH int) {
	v.layout(width)
	total := v.totalHeight()
	if total == 0 {
		return
	}
	maxScr := maxInt(0, total-innerH)
	if v.scroll > maxScr {
		v.scroll = maxScr
	}
	lastLine := v.scroll + innerH - 1
	if lastLine >= total {
		lastLine = total - 1
	}
	v.selected = v.blockIndexAtContentLine(width, lastLine)
}

func (v *MessageViewport) blockIndexAtContentLine(width, line int) int {
	v.layout(width)
	if len(v.blocks) == 0 {
		return -1
	}
	t := v.totalHeight()
	if line < 0 {
		line = 0
	}
	if line >= t {
		line = t - 1
	}
	for i, b := range v.blocks {
		if line >= b.offset && line < b.offset+b.height {
			return i
		}
	}
	return len(v.blocks) - 1
}
