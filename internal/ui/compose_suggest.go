package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const (
	composeSuggestMaxVisible = 5
	composeSuggestPanelRows  = composeSuggestMaxVisible + 2
	composeSuggestDebounce   = 300 * time.Millisecond
)

type composeSuggestMode string

const (
	composeSuggestNone     composeSuggestMode = ""
	composeSuggestMentions composeSuggestMode = "mentions"
	composeSuggestCommands composeSuggestMode = "commands"
	composeSuggestInline   composeSuggestMode = "inline"
)

type composeToken struct {
	Mode        composeSuggestMode
	Start       int
	End         int
	Query       string
	BotUsername string
}

type composeSuggestState struct {
	panel *tview.List
	// grid replaces the list for inline bot results, which are usually titleless media.
	grid  *inlineResultGrid
	ghost *tview.TextView

	mode        composeSuggestMode
	token       composeToken
	requestID   int64
	highlighted int
	items       []composeSuggestItem
	more        bool
	loading     bool
	timer       *time.Timer
	internalSet bool

	mentionEntities []telegram.MessageEntityMentionName
}

type composeSuggestItem struct {
	Kind      composeSuggestMode
	Main      string
	Secondary string
	Insert    string
	Suffix    string

	Mention telegram.MentionSuggestion
	Command telegram.BotCommandSuggestion
	Inline  telegram.InlineResultSuggestion
}

func newComposeSuggestState(theme Theme) *composeSuggestState {
	panel := tview.NewList().
		ShowSecondaryText(true).
		SetSelectedBackgroundColor(theme.Selection).
		SetSelectedTextColor(tcell.ColorWhite).
		SetHighlightFullLine(true)
	panel.SetBorder(true).
		SetBorderColor(theme.Border).
		SetTitleColor(theme.Accent)
	ghost := tview.NewTextView().
		SetDynamicColors(true).
		SetTextColor(theme.Dim).
		SetWrap(false)
	return &composeSuggestState{panel: panel, grid: newInlineResultGrid(theme), ghost: ghost}
}

func parseComposeToken(text string) composeToken {
	end := len(text)
	if end == 0 {
		return composeToken{}
	}
	start := lastTokenStart(text)
	token := text[start:end]
	if strings.HasPrefix(text, "/") && start == 0 && !strings.ContainsAny(text, " \t\r\n") {
		return composeToken{Mode: composeSuggestCommands, Start: 0, End: end, Query: strings.TrimPrefix(text, "/")}
	}
	if strings.HasPrefix(text, "@") {
		space := strings.IndexFunc(text, unicode.IsSpace)
		if space > 1 {
			bot := strings.TrimPrefix(text[:space], "@")
			if isTelegramUsername(bot) {
				query := strings.TrimLeftFunc(text[space:], unicode.IsSpace)
				return composeToken{Mode: composeSuggestInline, Start: space + leadingSpaceBytes(text[space:]), End: end, Query: query, BotUsername: bot}
			}
		}
	}
	if strings.HasPrefix(token, "@") && !strings.ContainsAny(token, " \t\r\n") {
		return composeToken{Mode: composeSuggestMentions, Start: start, End: end, Query: strings.TrimPrefix(token, "@")}
	}
	return composeToken{}
}

func lastTokenStart(text string) int {
	for i := len(text); i > 0; {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		if unicode.IsSpace(r) {
			return i
		}
		i -= size
	}
	return 0
}

func leadingSpaceBytes(text string) int {
	n := 0
	for n < len(text) {
		r, size := utf8.DecodeRuneInString(text[n:])
		if !unicode.IsSpace(r) {
			break
		}
		n += size
	}
	return n
}

func isTelegramUsername(value string) bool {
	if len(value) < 3 {
		return false
	}
	for _, r := range value {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func (a *App) initComposeSuggestions() {
	a.suggest = newComposeSuggestState(a.theme)
	a.suggest.panel.SetSelectedFunc(func(index int, _, _ string, _ rune) {
		a.acceptComposeSuggestion(index)
		if a.app != nil && a.composer != nil {
			a.app.SetFocus(a.composer)
		}
	})
}

func (a *App) onComposerChanged(text string) {
	if a.suggest == nil {
		return
	}
	if !a.suggest.internalSet {
		a.suggest.mentionEntities = nil
	}
	tok := parseComposeToken(text)
	if tok.Mode == composeSuggestNone || a.currentChat == "" || a.currentChat == "welcome" || a.currentChat == "empty" {
		a.closeComposeSuggestions()
		return
	}
	a.suggest.token = tok
	a.suggest.mode = tok.Mode
	a.suggest.requestID++
	a.suggest.highlighted = 0
	a.suggest.items = nil
	a.suggest.more = false
	a.suggest.loading = true
	a.renderComposeSuggestions()

	requestID := a.suggest.requestID
	peerKey := a.currentChat
	if a.suggest.timer != nil {
		a.suggest.timer.Stop()
	}
	a.suggest.timer = time.AfterFunc(composeSuggestDebounce, func() {
		cmd := telegram.Command{
			RequestID:   requestID,
			PeerKey:     peerKey,
			Query:       tok.Query,
			BotUsername: tok.BotUsername,
		}
		switch tok.Mode {
		case composeSuggestMentions:
			cmd.Kind = telegram.CommandSearchMentions
		case composeSuggestCommands:
			cmd.Kind = telegram.CommandLoadBotCommands
		case composeSuggestInline:
			cmd.Kind = telegram.CommandQueryInlineBot
		default:
			return
		}
		select {
		case a.commands <- cmd:
		default:
		}
	})
}

func (a *App) applyComposeSuggestions(event telegram.Event) {
	if a.suggest == nil || event.PeerKey != a.currentChat {
		return
	}
	if event.RequestID != a.suggest.requestID {
		return
	}
	items := make([]composeSuggestItem, 0, composeSuggestMaxVisible)
	switch event.Kind {
	case telegram.EventMentionSuggestions:
		if a.suggest.mode != composeSuggestMentions || event.Query != a.suggest.token.Query {
			return
		}
		for _, item := range event.Mentions {
			insert := "@" + item.Username
			if item.Username == "" {
				insert = item.Name
			}
			items = append(items, composeSuggestItem{
				Kind:      composeSuggestMentions,
				Main:      item.Name,
				Secondary: mentionSecondary(item),
				Insert:    insert,
				Suffix:    completionSuffix("@"+a.suggest.token.Query, insert),
				Mention:   item,
			})
		}
		a.suggest.more = event.HasMore
	case telegram.EventBotCommandSuggestions:
		if a.suggest.mode != composeSuggestCommands || event.Query != a.suggest.token.Query {
			return
		}
		for _, item := range event.BotCommands {
			insert := "/" + item.Command
			if item.BotUsername != "" && item.NeedsBotSuffix {
				insert += "@" + item.BotUsername
			}
			items = append(items, composeSuggestItem{
				Kind:      composeSuggestCommands,
				Main:      insert,
				Secondary: commandSecondary(item),
				Insert:    insert,
				Suffix:    completionSuffix("/"+a.suggest.token.Query, insert),
				Command:   item,
			})
		}
		a.suggest.more = false
	case telegram.EventInlineResultSuggestions:
		if a.suggest.mode != composeSuggestInline || event.Query != a.suggest.token.Query || event.BotUsername != a.suggest.token.BotUsername {
			return
		}
		for i, item := range event.InlineResults {
			main := item.Title
			if main == "" {
				// GIF/video bots return no title, so number the rows and fall back to the
				// result type; otherwise every row reads the same and cannot be told apart.
				main = fmt.Sprintf("%d. %s", i+1, item.Type)
			}
			if strings.TrimSpace(main) == "" {
				main = item.ID
			}
			secondary := item.Description
			if secondary == "" {
				secondary = render.InlineResultDetail(item)
			}
			items = append(items, composeSuggestItem{
				Kind:      composeSuggestInline,
				Main:      main,
				Secondary: secondary,
				Inline:    item,
			})
		}
		a.suggest.more = event.HasMore || event.NextOffset != ""
		if event.Placeholder != "" && strings.TrimSpace(a.suggest.token.Query) == "" {
			a.suggest.ghost.SetText("[gray]" + tview.Escape(event.Placeholder))
		}
	default:
		return
	}
	a.suggest.items = items
	a.suggest.loading = false
	a.suggest.highlighted = 0
	a.renderComposeSuggestions()
}

func mentionSecondary(item telegram.MentionSuggestion) string {
	parts := make([]string, 0, 3)
	if item.Username != "" {
		parts = append(parts, "@"+item.Username)
	}
	if item.IsBot {
		parts = append(parts, "bot")
	}
	if item.Source != "" {
		parts = append(parts, item.Source)
	}
	return strings.Join(parts, " ")
}

func commandSecondary(item telegram.BotCommandSuggestion) string {
	secondary := item.Description
	if item.BotUsername != "" {
		if secondary != "" {
			secondary += " "
		}
		secondary += "@" + item.BotUsername
	}
	return secondary
}

func completionSuffix(prefix, completion string) string {
	if strings.HasPrefix(strings.ToLower(completion), strings.ToLower(prefix)) {
		return completion[len(prefix):]
	}
	return ""
}

func (a *App) renderComposeSuggestions() {
	if a.suggest == nil {
		return
	}
	open := a.suggest.mode != composeSuggestNone && (a.suggest.loading || len(a.suggest.items) > 0)
	// Inline bot results are drawn as a thumbnail grid; mentions and commands stay a list.
	useGrid := open && a.suggest.mode == composeSuggestInline && len(a.suggest.items) > 0
	if len(a.suggest.items) > 0 {
		if a.suggest.highlighted >= len(a.suggest.items) {
			a.suggest.highlighted = len(a.suggest.items) - 1
		}
		if a.suggest.highlighted < 0 {
			a.suggest.highlighted = 0
		}
	}
	panelRows := composeSuggestPanelRows
	if useGrid {
		results := make([]telegram.InlineResultSuggestion, 0, len(a.suggest.items))
		for _, item := range a.suggest.items {
			results = append(results, item.Inline)
		}
		a.suggest.grid.SetResults(results)
		a.suggest.grid.SetSelected(a.suggest.highlighted)
		a.suggest.grid.SetTitle(" " + a.composeSuggestHeader() + " ")
		a.suggest.grid.onAccept = func(index int) { a.acceptComposeSuggestion(index) }
		panelRows = inlineGridPanelHeight(len(results), a.inlineGridInnerWidth())
		a.requestInlineThumbs(results)
	}
	if a.composeStack != nil {
		composeRows := 4
		if open {
			if useGrid {
				a.composeStack.ResizeItem(a.suggest.panel, 0, 0)
				a.composeStack.ResizeItem(a.suggest.grid, panelRows, 0)
			} else {
				a.composeStack.ResizeItem(a.suggest.grid, 0, 0)
				a.composeStack.ResizeItem(a.suggest.panel, panelRows, 0)
			}
			if a.rightPane != nil {
				composeRows = panelRows + 4
				a.rightPane.ResizeItem(a.composeStack, composeRows, 0)
			}
		} else {
			a.composeStack.ResizeItem(a.suggest.panel, 0, 0)
			a.composeStack.ResizeItem(a.suggest.grid, 0, 0)
			if a.rightPane != nil {
				a.rightPane.ResizeItem(a.composeStack, composeRows, 0)
			}
		}
	}
	if useGrid {
		a.suggest.panel.Clear()
		a.renderComposeGhost()
		return
	}
	a.suggest.panel.Clear()
	a.suggest.panel.SetTitle(" " + a.composeSuggestHeader() + " ")
	for i, item := range a.suggest.items {
		idx := i
		a.suggest.panel.AddItem(tview.Escape(item.Main), tview.Escape(item.Secondary), 0, func() {
			a.acceptComposeSuggestion(idx)
		})
	}
	if len(a.suggest.items) > 0 {
		a.suggest.panel.SetCurrentItem(a.suggest.highlighted)
	}
	a.renderComposeGhost()
}

func (a *App) composeSuggestHeader() string {
	if a.suggest == nil {
		return ""
	}
	count := len(a.suggest.items)
	if a.suggest.loading {
		switch a.suggest.mode {
		case composeSuggestMentions:
			return "Mentions loading"
		case composeSuggestCommands:
			return "Commands loading"
		case composeSuggestInline:
			return "Inline results loading"
		}
	}
	more := ""
	if a.suggest.more {
		more = "+"
	}
	switch a.suggest.mode {
	case composeSuggestMentions:
		return fmt.Sprintf("Mentions %d%s loaded", count, more)
	case composeSuggestCommands:
		return fmt.Sprintf("Commands %d", count)
	case composeSuggestInline:
		return fmt.Sprintf("Inline results %d%s", count, more)
	default:
		return ""
	}
}

func (a *App) renderComposeGhost() {
	if a.suggest == nil || a.suggest.ghost == nil {
		return
	}
	hint := ""
	if len(a.suggest.items) > 0 && a.suggest.highlighted >= 0 && a.suggest.highlighted < len(a.suggest.items) {
		item := a.suggest.items[a.suggest.highlighted]
		if item.Suffix != "" {
			hint = item.Suffix
		} else if item.Kind == composeSuggestInline && item.Secondary != "" {
			hint = item.Secondary
		}
	}
	if hint == "" && a.suggest.mode == composeSuggestInline && strings.TrimSpace(a.suggest.token.Query) == "" {
		hint = "type a query for @" + a.suggest.token.BotUsername
	}
	if hint == "" {
		a.suggest.ghost.SetText("")
		return
	}
	a.suggest.ghost.SetText("[gray]" + tview.Escape(hint))
}

func (a *App) closeComposeSuggestions() {
	if a.suggest == nil {
		return
	}
	if a.suggest.timer != nil {
		a.suggest.timer.Stop()
	}
	a.suggest.mode = composeSuggestNone
	a.suggest.items = nil
	a.suggest.loading = false
	a.suggest.more = false
	a.suggest.panel.Clear()
	a.suggest.panel.SetTitle("")
	a.suggest.grid.SetResults(nil)
	a.suggest.grid.SetTitle("")
	a.suggest.ghost.SetText("")
	if a.composeStack != nil {
		a.composeStack.ResizeItem(a.suggest.panel, 0, 0)
		a.composeStack.ResizeItem(a.suggest.grid, 0, 0)
	}
	if a.rightPane != nil {
		a.rightPane.ResizeItem(a.composeStack, 4, 0)
	}
}

func (a *App) captureComposeSuggestions(event *tcell.EventKey) bool {
	if a.suggest == nil || a.app == nil || a.app.GetFocus() != a.composer || a.suggest.mode == composeSuggestNone {
		return false
	}
	if len(a.suggest.items) == 0 && !a.suggest.loading {
		return false
	}
	if a.suggest.mode == composeSuggestInline && len(a.suggest.items) > 0 {
		// Grid navigation: left/right within a row, up/down by a whole row.
		switch event.Key() {
		case tcell.KeyLeft:
			a.moveComposeSuggestionGrid(-1, 0)
			return true
		case tcell.KeyRight:
			a.moveComposeSuggestionGrid(1, 0)
			return true
		case tcell.KeyUp:
			a.moveComposeSuggestionGrid(0, -1)
			return true
		case tcell.KeyDown:
			a.moveComposeSuggestionGrid(0, 1)
			return true
		}
	}
	switch event.Key() {
	case tcell.KeyUp:
		a.moveComposeSuggestion(-1)
		return true
	case tcell.KeyDown:
		a.moveComposeSuggestion(1)
		return true
	case tcell.KeyEnter, tcell.KeyTAB:
		a.acceptComposeSuggestion(a.suggest.highlighted)
		return true
	case tcell.KeyEsc:
		a.closeComposeSuggestions()
		return true
	default:
		return false
	}
}

func (a *App) moveComposeSuggestion(delta int) {
	if a.suggest == nil || len(a.suggest.items) == 0 {
		return
	}
	a.suggest.highlighted = (a.suggest.highlighted + delta + len(a.suggest.items)) % len(a.suggest.items)
	a.suggest.panel.SetCurrentItem(a.suggest.highlighted)
	a.renderComposeGhost()
}

func (a *App) acceptComposeSuggestion(index int) {
	if a.suggest == nil || index < 0 || index >= len(a.suggest.items) {
		return
	}
	item := a.suggest.items[index]
	if item.Kind == composeSuggestInline {
		replyToID := 0
		if a.replyTarget != nil {
			fmt.Sscanf(a.replyTarget.ID, "%d", &replyToID)
		}
		a.commands <- telegram.Command{
			Kind:        telegram.CommandSendInlineResult,
			PeerKey:     a.currentChat,
			QueryID:     item.Inline.QueryID,
			ResultID:    item.Inline.ID,
			ReplyToID:   replyToID,
			BotUsername: a.suggest.token.BotUsername,
		}
		a.setComposerText("")
		a.clearReplyTarget()
		a.closeComposeSuggestions()
		return
	}
	text := a.composer.GetText()
	start, end := a.suggest.token.Start, a.suggest.token.End
	if start < 0 || end < start || end > len(text) {
		return
	}
	insert := item.Insert
	if insert == "" {
		return
	}
	newText := text[:start] + insert + text[end:]
	a.setComposerText(newText)
	if item.Kind == composeSuggestMentions && item.Mention.Username == "" {
		a.suggest.mentionEntities = append(a.suggest.mentionEntities, telegram.MessageEntityMentionName{
			Offset:     utf16Len(text[:start]),
			Length:     utf16Len(insert),
			UserID:     item.Mention.UserID,
			AccessHash: item.Mention.AccessHash,
		})
	}
	a.closeComposeSuggestions()
}

func (a *App) setComposerText(text string) {
	if a.suggest != nil {
		a.suggest.internalSet = true
		defer func() { a.suggest.internalSet = false }()
	}
	a.composer.SetText(text)
}

func (a *App) takeMentionEntitiesForSend(rawText, sendText string) []telegram.MessageEntityMentionName {
	if a.suggest == nil || len(a.suggest.mentionEntities) == 0 {
		return nil
	}
	out := append([]telegram.MessageEntityMentionName(nil), a.suggest.mentionEntities...)
	a.suggest.mentionEntities = nil
	leftTrimBytes := len(rawText) - len(strings.TrimLeftFunc(rawText, unicode.IsSpace))
	if leftTrimBytes > 0 && sendText != rawText {
		shift := utf16Len(rawText[:leftTrimBytes])
		kept := out[:0]
		for _, entity := range out {
			entity.Offset -= shift
			if entity.Offset >= 0 {
				kept = append(kept, entity)
			}
		}
		out = kept
	}
	return out
}

func utf16Len(text string) int {
	n := 0
	for _, r := range text {
		if r <= 0xffff {
			n++
		} else {
			n += len(utf16.Encode([]rune{r}))
		}
	}
	return n
}

// inlineGridInnerWidth is the width available for grid cells, used both for layout and for
// deciding how many rows the panel needs.
func (a *App) inlineGridInnerWidth() int {
	if a.suggest == nil || a.suggest.grid == nil {
		return inlineCellWidth
	}
	_, _, width, _ := a.suggest.grid.GetInnerRect()
	if width <= 0 {
		// Before the first draw the primitive has no rect yet; assume one row so the panel
		// opens at a sane height and re-lays out once tview assigns geometry.
		return inlineCellWidth * composeSuggestMaxVisible
	}
	return width
}

// moveComposeSuggestionGrid steps the highlight through the thumbnail grid and keeps the
// shared highlighted index in sync so Enter accepts the same item the grid draws as selected.
func (a *App) moveComposeSuggestionGrid(dx, dy int) {
	if a.suggest == nil || a.suggest.grid == nil || len(a.suggest.items) == 0 {
		return
	}
	a.suggest.grid.MoveSelection(dx, dy)
	a.suggest.highlighted = a.suggest.grid.Selected()
	a.renderComposeGhost()
}

// requestInlineThumbs asks for thumbnails the grid does not have yet. Only results currently
// in the panel are fetched, so this stays bounded no matter how much the user types.
func (a *App) requestInlineThumbs(results []telegram.InlineResultSuggestion) {
	if a.suggest == nil || a.suggest.grid == nil {
		return
	}
	requestID := a.suggest.requestID
	for _, result := range results {
		if result.Thumb.DocumentID == 0 || result.Thumb.ThumbSize == "" {
			continue
		}
		if a.suggest.grid.HasPreview(result.ID) {
			continue
		}
		select {
		case a.commands <- telegram.Command{
			Kind:      telegram.CommandFetchInlineThumb,
			RequestID: requestID,
			ResultID:  result.ID,
			Media:     result.Thumb,
		}:
		default:
			return
		}
	}
}

// applyInlineThumb stores one rendered thumbnail. Replies for a superseded query are dropped
// so a fast typist does not see thumbnails from an earlier search.
func (a *App) applyInlineThumb(event telegram.Event) {
	if a.suggest == nil || a.suggest.grid == nil {
		return
	}
	if event.RequestID != a.suggest.requestID || a.suggest.mode != composeSuggestInline {
		return
	}
	a.suggest.grid.SetPreview(event.ResultID, event.ThumbPreview)
}
