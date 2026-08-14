package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/rivo/uniseg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/network"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func Truncate(value string, width int) string {
	if width <= 0 || StringWidth(value) <= width {
		return value
	}
	suffix := "..."
	if width < len(suffix) {
		return strings.Repeat(".", width)
	}

	var b strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		cluster := graphemes.Str()
		w := uniseg.StringWidth(cluster)
		if used+w > width-len(suffix) {
			break
		}
		b.WriteString(cluster)
		used += w
	}
	b.WriteString(suffix)
	return b.String()
}

func StringWidth(value string) int {
	return uniseg.StringWidth(value)
}

// DisplayWidth returns visible terminal width for strings that may include tview color tags.
func DisplayWidth(value string) int {
	return tview.TaggedStringWidth(value)
}

func ChatRow(chat telegram.Chat, width int) string {
	title := chat.Title
	if title == "" {
		title = i18n.T(i18n.KeyUIUntitled)
	}
	if chat.Pinned {
		title = "📌 " + title
	}
	if chat.Unread > 0 {
		title = fmt.Sprintf("%s (%d)", title, chat.Unread)
	}
	if chat.Subtitle != "" {
		title = title + " | " + chat.Subtitle
	}
	// The message preview belongs on the list's secondary line, not here.
	return Truncate(title, width)
}

func MessageRow(message telegram.Message, width int) string {
	return MessageRowWithOpts(message, width, DefaultMessageRowOpts())
}

func MessageRowWithoutMediaPreview(message telegram.Message, width int) string {
	opts := DefaultMessageRowOpts()
	opts.IncludeMediaPreview = false
	return MessageRowWithOpts(message, width, opts)
}

func MessageRowWithOpts(message telegram.Message, width int, opts MessageRowOpts) string {
	return strings.Join(MessageRowLines(message, width, opts), "\n")
}

// MessageRowLines renders a message as display lines. The first line is the
// sender/time header; body, reactions, and media previews follow on new lines.
func MessageRowLines(message telegram.Message, width int, opts MessageRowOpts) []string {
	if line := ServiceLine(message, width); line != "" {
		return []string{line}
	}
	header := strings.TrimRight(messageRowMeta(message, opts), " ")
	if message.State == "deleted" {
		lines := []string{Truncate(header, width)}
		lines = append(lines, Truncate("[red]"+i18n.T(i18n.KeyDeleted), width))
		return lines
	}
	text := messageBodyText(message)
	var lines []string
	if header != "" {
		lines = append(lines, Truncate(header, width))
	}
	if viaLine := messageViaBotLine(message.ViaBotUsername, width); viaLine != "" {
		lines = append(lines, viaLine)
	}
	lines = appendBodyLines(lines, "", text, width)
	if reactLine := reactionsLine(message.Reactions, width); reactLine != "" {
		lines = append(lines, Truncate(reactLine, width))
	}
	if opts.IncludeMediaPreview && message.Media.PreviewText != "" && telegram.PreviewIsRaster(message.Media.PreviewText) {
		preview := tview.TranslateANSI(message.Media.PreviewText)
		for _, raw := range strings.Split(preview, "\n") {
			if raw == "" {
				continue
			}
			// Raster previews are pre-sized terminal art; word-wrapping would split
			// tview color tags like [#rrggbb] across lines and break rendering.
			lines = append(lines, raw)
		}
	}
	return lines
}

func MessageRowHeader(message telegram.Message, opts MessageRowOpts) string {
	prefix := outgoingPrefix(message, opts)
	meta := messageRowMeta(message, opts)
	if meta != "" {
		return prefix + " " + meta + " "
	}
	return prefix + " "
}

// MessageRowHeaderWidth is the display width of the timestamp/meta prefix on row one.
func MessageRowHeaderWidth(message telegram.Message, opts MessageRowOpts) int {
	return DisplayWidth(strings.TrimRight(messageRowMeta(message, opts), " "))
}

// IndentDisplay prepends spaces so tagged/ANSI lines align under the message body.
func IndentDisplay(text string, columns int) string {
	if columns <= 0 || text == "" {
		return text
	}
	return strings.Repeat(" ", columns) + text
}

func appendBodyLines(out []string, header, body string, width int) []string {
	if header != "" {
		out = append(out, Truncate(header, width))
	}
	if body == "" {
		return out
	}
	bodyWidth := width
	if bodyWidth < 1 {
		bodyWidth = 1
	}
	for _, para := range strings.Split(body, "\n") {
		para = strings.TrimRight(para, "\r")
		wrapped := tview.WordWrap(para, bodyWidth)
		for _, wl := range wrapped {
			out = append(out, Truncate(wl, width))
		}
	}
	return out
}

func outgoingPrefix(message telegram.Message, opts MessageRowOpts) string {
	if message.Outgoing && opts.Layout == LayoutIM {
		return " "
	}
	if message.Outgoing {
		return ">"
	}
	return " "
}

// PinnedRow renders one entry of the pinned message list: timestamp, then the message text
// or its media label. Service rows fall back to their system line.
func PinnedRow(message telegram.Message, width int) string {
	prefix := ""
	if !message.CreatedAt.IsZero() {
		prefix = messageRowTime(message.CreatedAt) + " "
	}
	body := message.Text
	if body == "" {
		if line := ServiceLine(message, 0); line != "" {
			body = line
		} else if message.Media.Label != "" {
			body = message.Media.Label
		} else {
			body = i18n.T(i18n.KeyMessageEmpty)
		}
	}
	return Truncate(prefix+firstLine(body), width)
}

func firstLine(text string) string {
	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		return text[:idx]
	}
	return text
}

// InlineResultDetail summarises an inline bot result's media so titleless results (the
// normal case for GIF bots) are still distinguishable from each other in the panel.
func InlineResultDetail(result telegram.InlineResultSuggestion) string {
	var parts []string
	if result.Width > 0 && result.Height > 0 {
		parts = append(parts, fmt.Sprintf("%dx%d", result.Width, result.Height))
	}
	if result.Duration > 0 {
		parts = append(parts, fmt.Sprintf("%ds", result.Duration))
	}
	if result.Size > 0 {
		parts = append(parts, HumanSize(result.Size))
	}
	return strings.Join(parts, " · ")
}

// HumanSize formats a byte count for compact single-line display.
func HumanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KB", "MB", "GB"} {
		value /= unit
		if value < unit {
			if value < 10 {
				return fmt.Sprintf("%.1f%s", value, suffix)
			}
			return fmt.Sprintf("%.0f%s", value, suffix)
		}
	}
	return fmt.Sprintf("%.0fGB", value)
}

// ServiceLine renders a Telegram service (system) message as a single dim line, e.g.
// "19:14  Nemo pinned a message". Returns "" for ordinary messages.
func ServiceLine(message telegram.Message, width int) string {
	if message.ServiceKey == "" {
		return ""
	}
	text := i18n.T(message.ServiceKey)
	if message.ServiceArg != "" {
		text = i18n.Tf(message.ServiceKey, message.ServiceArg)
	}
	parts := make([]string, 0, 3)
	if !message.CreatedAt.IsZero() {
		parts = append(parts, messageRowTime(message.CreatedAt))
	}
	if message.Author != "" {
		parts = append(parts, message.Author)
	}
	parts = append(parts, text)
	return Truncate("[gray]"+strings.Join(parts, " ")+"[-]", width)
}

func messageViaBotLine(username string, width int) string {
	if username == "" {
		return ""
	}
	return Truncate(fmt.Sprintf("[gray]%s[-]", i18n.Tf(i18n.KeyMessageViaBot, username)), width)
}

func messageRowMeta(message telegram.Message, opts MessageRowOpts) string {
	meta := ""
	if !message.CreatedAt.IsZero() {
		meta = messageRowTime(message.CreatedAt)
	}
	if message.Author != "" && !message.Outgoing {
		meta = colorizeSender(message.Author, message.AuthorColor) + " " + meta
	}
	if message.State == "pending" {
		meta += " [yellow]" + i18n.T(i18n.KeySending)
	}
	if message.State == "failed" {
		meta += " [red]" + i18n.T(i18n.KeyFailed)
	}
	meta += readMeta(message, opts)
	return meta
}

func readMeta(message telegram.Message, opts MessageRowOpts) string {
	if opts.BroadcastChannel && message.Views > 0 {
		return channelViewsMeta(message.Views)
	}
	if opts.GroupReadMarks {
		return groupReadMeta(message)
	}
	return outboxReadMeta(message)
}

func channelViewsMeta(views int) string {
	count := telegram.FormatViewCount(views)
	if count == "" {
		return ""
	}
	return fmt.Sprintf(" [gray]👁 %s[-]", count)
}

func groupReadMeta(message telegram.Message) string {
	if !message.Outgoing || message.State != "synced" || message.GroupReadCount <= 0 {
		return ""
	}
	return fmt.Sprintf(" [gray]%s[-]", i18n.Tf(i18n.KeyMessageGroupReadCount, message.GroupReadCount))
}

func messageBodyText(message telegram.Message) string {
	text := message.Text
	if message.ForwardSource != "" {
		text = "[fwd: " + message.ForwardSource + "] " + text
	}
	if message.ReplyToID != "" {
		tag := "[reply " + message.ReplyToID + "]"
		if strings.TrimSpace(message.ReplyToAuthor) != "" {
			tag = "[reply " + message.ReplyToAuthor + " #" + message.ReplyToID + "]"
		}
		text = tag + " " + text
	}
	if message.Media.Kind != "" {
		if text != "" {
			text += " "
		}
		text += message.Media.Label
		if message.Media.Duration > 0 && !strings.Contains(message.Media.Label, "s") {
			text += fmt.Sprintf(" %ds", message.Media.Duration)
		}
		if message.Media.FileName != "" {
			text += " " + message.Media.FileName
		}
		if message.Media.LocalPath != "" {
			text += " " + i18n.T(i18n.KeyMediaCached)
		}
	}
	if text == "" {
		text = i18n.T(i18n.KeyMessageEmpty)
	}
	return text
}

func reactionsLine(reactions []telegram.ReactionSummary, width int) string {
	if len(reactions) == 0 {
		return ""
	}
	parts := make([]string, 0, len(reactions))
	for _, reaction := range reactions {
		emoji := reaction.Emoji
		if emoji == "" {
			emoji = reaction.CustomAlt
		}
		if emoji == "" {
			continue
		}
		label := fmt.Sprintf("%s%d", emoji, reaction.Count)
		if reaction.Chosen {
			label = "[green]" + label + "[-]"
		}
		parts = append(parts, label)
	}
	if len(parts) == 0 {
		return ""
	}
	return Truncate(strings.Join(parts, " "), width)
}

func reactionsMeta(reactions []telegram.ReactionSummary, width int) string {
	if line := reactionsLine(reactions, width-2); line != "" {
		return Truncate("  "+line, width)
	}
	return ""
}

// messageRowTime formats CreatedAt for list rows: clock-only for today, date + time otherwise.
func messageRowTime(t time.Time) string {
	t = t.Local()
	now := time.Now().In(t.Location())
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

func outboxReadMeta(message telegram.Message) string {
	if !message.Outgoing || message.State != "synced" {
		return ""
	}
	if message.ReadByPeer {
		return " [green]✓✓"
	}
	return " ✓"
}

// MessageDetail is a non-truncated, word-wrap-friendly body for modals (no fixed width).
func MessageDetail(message telegram.Message) string {
	return messageDetail(message, true)
}

func MessageDetailWithoutPreview(message telegram.Message) string {
	return messageDetail(message, false)
}

func messageDetail(message telegram.Message, includePreview bool) string {
	opts := DefaultMessageRowOpts()
	prefix := outgoingPrefix(message, opts)
	meta := messageRowMeta(message, opts)
	if message.State == "deleted" {
		return fmt.Sprintf("%s %s [red]%s", prefix, meta, i18n.T(i18n.KeyDeleted))
	}
	if line := ServiceLine(message, 0); line != "" {
		return line
	}
	text := messageBodyText(message)
	if includePreview && message.Media.PreviewText != "" {
		text = text + "\n\n" + message.Media.PreviewText
	}
	if reactLine := reactionsMeta(message.Reactions, 0); reactLine != "" {
		text = text + "\n" + reactLine
	}
	if meta != "" {
		return fmt.Sprintf("%s %s\n\n%s", prefix, meta, text)
	}
	return fmt.Sprintf("%s\n\n%s", prefix, text)
}

func colorizeSender(name string, seed int) string {
	colors := []string{"red", "green", "yellow", "blue", "purple", "aqua", "orange", "teal", "lime", "fuchsia", "maroon", "navy"}
	if seed < 0 {
		seed = -seed
	}
	return "[" + colors[seed%len(colors)] + "]" + name + "[-]"
}

func Footer(mode, version string, proxy network.ProxyConfig) string {
	parts := []string{
		i18n.T(i18n.KeyUIFooterTabFocus),
		i18n.T(i18n.KeyUIFooterBacktabFocus),
		i18n.T(i18n.KeyUIFooterEnter),
		i18n.T(i18n.KeyUIFooterLayout),
		i18n.T(i18n.KeyUIFooterReact),
		i18n.T(i18n.KeyUIFooterPinned),
		i18n.T(i18n.KeyUIFooterCompose),
		i18n.T(i18n.KeyUIFooterSearch),
		i18n.T(i18n.KeyUIFooterDownload),
		i18n.T(i18n.KeyUIFooterOpen),
		i18n.T(i18n.KeyUIFooterSettings),
		i18n.T(i18n.KeyUIFooterProxy),
		i18n.T(i18n.KeyUIFooterQuit),
	}
	if mode != "" {
		parts = append([]string{fmt.Sprintf(i18n.T(i18n.KeyUIFooterMode), mode)}, parts...)
	}
	if proxy.Active() {
		parts = append(parts, fmt.Sprintf(i18n.T(i18n.KeyUIFooterProxyActive), proxy.MaskedAddress()))
	}
	if version != "" {
		parts = append(parts, version)
	}
	return strings.Join(parts, " | ")
}

func ProxyEntry(entry network.UIEntry) string {
	mark := " "
	if entry.Active {
		mark = "*"
	}
	return fmt.Sprintf("%s %s: %s", mark, entry.Name, entry.Description)
}
