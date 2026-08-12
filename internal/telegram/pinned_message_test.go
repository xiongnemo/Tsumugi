package telegram

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/storage"
)

func TestPinnedMessagePreviewUsesText(t *testing.T) {
	got := pinnedMessagePreview(storage.Message{Text: "本群都是什么傻逼"})
	if got != "本群都是什么傻逼" {
		t.Fatalf("preview = %q", got)
	}
}

func TestPinnedMessagePreviewUsesMediaLabel(t *testing.T) {
	got := pinnedMessagePreview(storage.Message{
		MediaJSON: `{"kind":"photo","label_key":"media.photo","label":"[Photo]"}`,
	})
	if got == "" {
		t.Fatal("expected media label preview")
	}
}
