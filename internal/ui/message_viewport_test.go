package ui

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func TestNormalizeInlinePreviewLinesFixedHeight(t *testing.T) {
	got := normalizeInlinePreviewLines("", 0)
	if len(got) != media.PreviewMaxRows {
		t.Fatalf("lines = %d, want %d", len(got), media.PreviewMaxRows)
	}
}

func TestPatchMessagesPreservesBottomScroll(t *testing.T) {
	v := NewMessageViewport()
	v.SetMessages([]telegram.Message{
		{ID: "1", Text: "hello", Media: telegram.MediaAttachment{Kind: "photo", PreviewText: "[Photo]"}},
		{ID: "2", Text: "tail", Media: telegram.MediaAttachment{Kind: "photo", PreviewText: "[Photo]"}},
	})
	v.Box.SetRect(0, 0, 80, 10)
	v.layout(78)
	maxScr := maxInt(0, v.totalHeight()-10)
	v.scroll = maxScr
	v.followEnd = false

	changed := v.PatchMessages([]telegram.Message{{
		ID:   "1",
		Text: "hello",
		Media: telegram.MediaAttachment{
			Kind:        "photo",
			PreviewText: "[#0000ff]" + stringsRepeat("█", 40*media.PreviewMaxRows),
		},
	}})
	if !changed {
		t.Fatal("expected patch to apply")
	}
	if !v.followEnd {
		t.Fatal("expected followEnd when patched at bottom")
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
