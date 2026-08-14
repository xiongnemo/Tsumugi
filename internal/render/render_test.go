package render

import (
	"strings"
	"testing"
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/network"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func init() {
	i18n.SetLocale("en")
}

func TestTruncateUsesDisplayWidth(t *testing.T) {
	got := Truncate("你好telegram", 8)
	if StringWidth(got) > 8 {
		t.Fatalf("width = %d, want <= 8 for %q", StringWidth(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
}

func TestFooterShowsProxy(t *testing.T) {
	footer := Footer("user", "v0.0.1", network.ProxyConfig{
		Kind:    network.ProxySOCKS5,
		Source:  network.SourceManual,
		Address: "127.0.0.1:1080",
	})
	if !strings.Contains(footer, "Shift+Tab previous focus") {
		t.Fatalf("footer did not include reverse focus hint: %q", footer)
	}
	if !strings.Contains(footer, "proxy: 127.0.0.1:1080") {
		t.Fatalf("footer did not include proxy: %q", footer)
	}
}

func TestMessageRowShowsSenderTimeAndMedia(t *testing.T) {
	now := time.Now()
	created := time.Date(now.Year(), now.Month(), now.Day(), 22, 15, 0, 0, time.Local)
	row := MessageRow(telegram.Message{
		Author:      "Nemo",
		AuthorColor: 3,
		Text:        "look",
		CreatedAt:   created,
		Media:       telegram.MediaAttachment{Kind: "gif", Label: "[GIF]", FileName: "clip.mp4"},
	}, 120)
	for _, want := range []string{"Nemo", "22:15", "look", "[GIF]", "clip.mp4"} {
		if !strings.Contains(row, want) {
			t.Fatalf("row %q missing %q", row, want)
		}
	}
}

func TestMessageRowNonTodayShowsDate(t *testing.T) {
	day := time.Now().AddDate(0, 0, -3)
	created := time.Date(day.Year(), day.Month(), day.Day(), 15, 19, 0, 0, time.Local)
	wantDate := created.Format("2006-01-02")
	row := MessageRow(telegram.Message{
		Author:    "Nemo",
		Text:      "ping",
		CreatedAt: created,
	}, 80)
	if !strings.Contains(row, wantDate) || !strings.Contains(row, "15:19") {
		t.Fatalf("row should include calendar date for non-today: %q", row)
	}
}

func TestOutgoingMessageRowDoesNotShowAuthor(t *testing.T) {
	now := time.Now()
	created := time.Date(now.Year(), now.Month(), now.Day(), 22, 16, 0, 0, time.Local)
	row := MessageRow(telegram.Message{
		Author:    "me",
		Text:      "hello",
		Outgoing:  true,
		CreatedAt: created,
		State:     "pending",
	}, 120)
	if strings.Contains(row, "me") {
		t.Fatalf("outgoing row should not show author: %q", row)
	}
	if !strings.Contains(row, "(sending)") {
		t.Fatalf("pending row should show sending state: %q", row)
	}
}

func TestIMLayoutOutgoingOmitsPrefix(t *testing.T) {
	row := MessageRowWithOpts(telegram.Message{
		Text:     "hello",
		Outgoing: true,
		State:    "synced",
	}, 80, MessageRowOpts{Layout: LayoutIM})
	if strings.Contains(row, ">") {
		t.Fatalf("IM layout should not use > prefix: %q", row)
	}
}

func TestBroadcastChannelViewsMeta(t *testing.T) {
	row := MessageRowWithOpts(telegram.Message{
		Text:     "post",
		Outgoing: true,
		State:    "synced",
		Views:    1200,
	}, 80, MessageRowOpts{BroadcastChannel: true})
	if !strings.Contains(row, "👁") || !strings.Contains(row, "1.2k") {
		t.Fatalf("broadcast views meta missing: %q", row)
	}
}

func TestGroupReadMeta(t *testing.T) {
	i18n.SetLocale("en")
	row := MessageRowWithOpts(telegram.Message{
		Text:           "hello group",
		Outgoing:       true,
		State:          "synced",
		GroupReadCount: 3,
	}, 80, MessageRowOpts{GroupReadMarks: true})
	if !strings.Contains(row, "read 3") {
		t.Fatalf("group read meta missing: %q", row)
	}
	if strings.Contains(row, "✓") {
		t.Fatalf("group read meta should not reuse private checkmarks: %q", row)
	}

	withoutCount := MessageRowWithOpts(telegram.Message{
		Text:     "hello group",
		Outgoing: true,
		State:    "synced",
	}, 80, MessageRowOpts{GroupReadMarks: true})
	if strings.Contains(withoutCount, "✓") || strings.Contains(withoutCount, "read") {
		t.Fatalf("group message without count should not show private read meta: %q", withoutCount)
	}
}

func TestReactionsMetaStrip(t *testing.T) {
	row := MessageRowWithOpts(telegram.Message{
		Text: "hello",
		Reactions: []telegram.ReactionSummary{
			{Emoji: "👍", Count: 3},
			{Emoji: "❤️", Count: 1, Chosen: true},
		},
	}, 80, DefaultMessageRowOpts())
	if !strings.Contains(row, "👍3") {
		t.Fatalf("reactions strip missing thumbs: %q", row)
	}
}

func TestOutgoingMessageRowShowsReadReceipts(t *testing.T) {
	now := time.Now()
	row := MessageRow(telegram.Message{
		Text:       "hello",
		Outgoing:   true,
		CreatedAt:  now,
		State:      "synced",
		ReadByPeer: true,
	}, 120)
	if !strings.Contains(row, "✓✓") {
		t.Fatalf("read row should show double check: %q", row)
	}
	sent := MessageRow(telegram.Message{
		Text:      "hello",
		Outgoing:  true,
		CreatedAt: now,
		State:     "synced",
	}, 120)
	if !strings.Contains(sent, "✓") || strings.Contains(sent, "✓✓") {
		t.Fatalf("sent row should show single check: %q", sent)
	}
}

func TestMessageRowShowsPreviewText(t *testing.T) {
	row := MessageRow(telegram.Message{
		Text: "gif",
		Media: telegram.MediaAttachment{
			Kind:        "gif",
			Label:       "[GIF]",
			LocalPath:   "media.gif",
			PreviewText: "[#0000ff]██[-]",
		},
	}, 120)
	if !strings.Contains(row, "██") || !strings.Contains(row, "[cached]") {
		t.Fatalf("row did not include media preview/cached state: %q", row)
	}
}

func TestMessageDetailWithoutPreviewOmitsRasterText(t *testing.T) {
	msg := telegram.Message{
		ID:        "1",
		Text:      "photo",
		CreatedAt: time.Now(),
		Media: telegram.MediaAttachment{
			Kind:        "photo",
			Label:       "[Photo]",
			PreviewText: "RASTER",
		},
	}
	if got := MessageDetailWithoutPreview(msg); strings.Contains(got, "RASTER") {
		t.Fatalf("detail contained preview raster: %q", got)
	}
}

func TestDisplayWidthIgnoresTviewTags(t *testing.T) {
	plain := StringWidth("hello")
	tagged := DisplayWidth("[green]hello[-]")
	if plain != tagged {
		t.Fatalf("DisplayWidth tagged = %d, plain = %d", tagged, plain)
	}
	raster := DisplayWidth("[#ff0000]██[-][#00ff00]██[-]")
	if raster != 4 {
		t.Fatalf("DisplayWidth raster = %d, want 4", raster)
	}
}

func TestMessageRowLinesIndentContinuation(t *testing.T) {
	created := time.Date(2026, 5, 6, 3, 54, 0, 0, time.Local)
	lines := MessageRowLines(telegram.Message{
		Author:    "Nemo",
		Text:      "hello",
		CreatedAt: created,
		Media: telegram.MediaAttachment{
			Kind:        "photo",
			Label:       "[Photo]",
			PreviewText: "[#ff0000]██[-][#00ff00]██[-]",
		},
	}, 80, DefaultMessageRowOpts())
	if len(lines) < 3 {
		t.Fatalf("expected header, body, and preview lines, got %v", lines)
	}
	if !strings.Contains(lines[0], "Nemo") || !strings.Contains(lines[1], "hello") {
		t.Fatalf("header and body should be on separate lines: %v", lines)
	}
	if strings.HasPrefix(lines[2], " ") {
		t.Fatalf("preview line should start at column 0, got %q", lines[2])
	}
}

func TestMessageRowLinesWrapIndent(t *testing.T) {
	created := time.Date(2026, 5, 6, 3, 54, 0, 0, time.Local)
	longBody := strings.Repeat("字", 30)
	lines := MessageRowLines(telegram.Message{
		Text:      longBody,
		CreatedAt: created,
	}, 20, DefaultMessageRowOpts())
	if len(lines) < 3 {
		t.Fatalf("expected header plus wrapped body lines, got %v", lines)
	}
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], " ") {
			t.Fatalf("wrapped body line %d should start at column 0, got %q", i, lines[i])
		}
	}
}

func TestMessageRowLinesSkipsNonRasterPreviewText(t *testing.T) {
	created := time.Date(2026, 5, 6, 9, 46, 0, 0, time.Local)
	lines := MessageRowLines(telegram.Message{
		Text:      "[Sticker] sticker.webp",
		CreatedAt: created,
		Media: telegram.MediaAttachment{
			Kind:        "sticker",
			Label:       "[Sticker]",
			FileName:    "sticker.webp",
			PreviewText: "[Sticker] 😩 sticker.webp",
		},
	}, 80, DefaultMessageRowOpts())
	if len(lines) != 2 {
		t.Fatalf("non-raster preview should not add lines, got %v", lines)
	}
}

func TestMessageRowBodyStartsOnNewLine(t *testing.T) {
	created := time.Date(2026, 5, 6, 12, 5, 0, 0, time.Local)
	short := MessageRowLines(telegram.Message{
		Author:    "816R",
		Text:      "饿饿饭饭",
		CreatedAt: created,
	}, 80, DefaultMessageRowOpts())
	long := MessageRowLines(telegram.Message{
		Author:    "Voiclin",
		Text:      "羡慕饥饿 play",
		CreatedAt: created,
	}, 80, DefaultMessageRowOpts())
	if len(short) < 2 || len(long) < 2 {
		t.Fatalf("expected header + body lines")
	}
	if short[1] != "饿饿饭饭" || !strings.HasPrefix(long[1], "羡慕") {
		t.Fatalf("body lines should align at column 0: %q vs %q", short[1], long[1])
	}
	if strings.Contains(short[0], "饿饿") || strings.Contains(long[0], "羡慕") {
		t.Fatalf("header line should not include body text: %q / %q", short[0], long[0])
	}
}

func TestMessageRowLinesHeaderBodySameStartColumn(t *testing.T) {
	created := time.Date(2026, 5, 6, 12, 5, 0, 0, time.Local)
	lines := MessageRowLines(telegram.Message{
		Author:      "Nemo",
		AuthorColor: 2,
		Text:        "hello",
		CreatedAt:   created,
	}, 80, DefaultMessageRowOpts())
	if len(lines) < 2 {
		t.Fatalf("expected header and body lines, got %v", lines)
	}
	if strings.HasPrefix(lines[0], " ") || strings.HasPrefix(lines[0], ">") {
		t.Fatalf("header should not include Draw gutter/outgoing prefix: %q", lines[0])
	}
	if strings.HasPrefix(lines[1], " ") || strings.HasPrefix(lines[1], ">") {
		t.Fatalf("body should start at column 0: %q", lines[1])
	}
	if !strings.Contains(lines[0], "Nemo") {
		t.Fatalf("header should include author: %q", lines[0])
	}
	if lines[1] != "hello" {
		t.Fatalf("unexpected body line: %q", lines[1])
	}
}

func TestMessageRowLinesViaBot(t *testing.T) {
	created := time.Date(2026, 5, 6, 12, 50, 0, 0, time.Local)
	lines := MessageRowLines(telegram.Message{
		Text:           "3 = 1+2",
		Outgoing:       true,
		CreatedAt:      created,
		ViaBotUsername: "CalcuBot",
	}, 80, MessageRowOpts{Layout: LayoutIM})
	if len(lines) < 3 {
		t.Fatalf("expected header, via, and body lines, got %v", lines)
	}
	if !strings.Contains(lines[1], "CalcuBot") {
		t.Fatalf("via line = %q, want bot username", lines[1])
	}
	if !strings.Contains(lines[1], "via @") {
		t.Fatalf("via line = %q, want localized via prefix", lines[1])
	}
	if lines[2] != "3 = 1+2" {
		t.Fatalf("body line = %q", lines[2])
	}
}

func TestMessageRowLinesRasterPreviewNotWordWrapped(t *testing.T) {
	created := time.Date(2026, 5, 6, 3, 54, 0, 0, time.Local)
	raster := strings.Repeat("[#ff0000]█[-][#00ff00]█[-]", 10)
	lines := MessageRowLines(telegram.Message{
		Author:    "Nemo",
		Text:      "sticker",
		CreatedAt: created,
		Media: telegram.MediaAttachment{
			Kind:        "sticker",
			Label:       "[Sticker]",
			PreviewText: raster,
		},
	}, 10, DefaultMessageRowOpts())
	if len(lines) < 3 {
		t.Fatalf("expected header, body, and preview lines, got %v", lines)
	}
	previewLines := lines[2:]
	if len(previewLines) != 1 {
		t.Fatalf("raster preview should stay one line per raster row, got %d: %v", len(previewLines), previewLines)
	}
	if !strings.Contains(previewLines[0], "[#ff0000]") || !strings.Contains(previewLines[0], "[#00ff00]") {
		t.Fatalf("word wrap broke color tags: %q", previewLines[0])
	}
}

func TestChatRowMarksPinned(t *testing.T) {
	got := ChatRow(telegram.Chat{Title: "Pinned chat", Pinned: true}, 80)
	if !strings.HasPrefix(got, "📌 ") {
		t.Fatalf("ChatRow() = %q, want pinned prefix", got)
	}
}

func TestChatRowUsesDisplayWidthForCJKEmoji(t *testing.T) {
	got := ChatRow(telegram.Chat{
		Title:       "聊天群組😊聊天群組😊",
		Subtitle:    "群組副標題",
		LastPreview: "最新訊息😊最新訊息",
	}, 16)
	if width := StringWidth(got); width > 16 {
		t.Fatalf("ChatRow width = %d, want <= 16: %q", width, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("ChatRow should be truncated with ellipsis: %q", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("ChatRow contains replacement rune: %q", got)
	}
}

func TestServiceLineRendersSystemMessage(t *testing.T) {
	msg := telegram.Message{
		ID:         "512",
		Author:     "Nemo",
		CreatedAt:  time.Date(2026, 8, 12, 19, 14, 0, 0, time.UTC),
		ServiceKey: "service.pinned_message",
	}
	line := ServiceLine(msg, 80)
	if !strings.Contains(line, "Nemo") || !strings.Contains(line, "pinned a message") {
		t.Fatalf("service line = %q", line)
	}
	if !strings.HasPrefix(line, "[gray]") {
		t.Fatalf("service line should be dim, got %q", line)
	}

	// A service message replaces the whole row: no sender/body/meta layout.
	lines := MessageRowLines(msg, 80, DefaultMessageRowOpts())
	if len(lines) != 1 || lines[0] != line {
		t.Fatalf("MessageRowLines = %#v, want single service line", lines)
	}
}

func TestServiceLineWithArgument(t *testing.T) {
	line := ServiceLine(telegram.Message{
		ServiceKey: "service.users_added",
		ServiceArg: "Ada Lovelace",
	}, 80)
	if !strings.Contains(line, "added Ada Lovelace") {
		t.Fatalf("service line = %q", line)
	}
}

func TestServiceLineEmptyForOrdinaryMessage(t *testing.T) {
	if got := ServiceLine(telegram.Message{Text: "hello"}, 80); got != "" {
		t.Fatalf("ServiceLine = %q, want empty", got)
	}
}

func TestChatRowIsTitleAndKindOnly(t *testing.T) {
	got := ChatRow(telegram.Chat{
		Title:       "Nemo",
		Subtitle:    "group",
		LastPreview: "[Photo]",
		Unread:      3,
	}, 100)
	if got != "Nemo (3) | group" {
		t.Fatalf("ChatRow = %q, want %q", got, "Nemo (3) | group")
	}
	// The preview belongs on the list's secondary line; repeating it here produced
	// "name | kind preview | preview" once the locale changed.
	if strings.Contains(got, "[Photo]") {
		t.Fatalf("ChatRow should not include the preview: %q", got)
	}
}

func TestInlineResultDetailDistinguishesTitlelessResults(t *testing.T) {
	// GIF bots return no title or description, so the detail line is the only thing that
	// tells one row from another.
	got := InlineResultDetail(telegram.InlineResultSuggestion{
		Type:     "gif",
		Width:    480,
		Height:   270,
		Duration: 3,
		Size:     1_200_000,
	})
	if got != "480x270 · 3s · 1.1MB" {
		t.Fatalf("detail = %q", got)
	}
	if empty := InlineResultDetail(telegram.InlineResultSuggestion{Type: "article"}); empty != "" {
		t.Fatalf("detail = %q, want empty when no media metadata", empty)
	}
}

func TestPinnedRowFallsBackToMediaAndService(t *testing.T) {
	when := time.Date(2026, 8, 14, 19, 14, 0, 0, time.UTC)

	text := PinnedRow(telegram.Message{CreatedAt: when, Text: "first line\nsecond line"}, 100)
	if strings.Contains(text, "second line") {
		t.Fatalf("pinned row should keep one line: %q", text)
	}

	media := PinnedRow(telegram.Message{CreatedAt: when, Media: telegram.MediaAttachment{Kind: "photo", Label: "[Photo]"}}, 100)
	if !strings.Contains(media, "[Photo]") {
		t.Fatalf("pinned row = %q", media)
	}

	service := PinnedRow(telegram.Message{CreatedAt: when, ServiceKey: "service.pinned_message"}, 100)
	if !strings.Contains(service, "pinned a message") {
		t.Fatalf("pinned row = %q", service)
	}
}
