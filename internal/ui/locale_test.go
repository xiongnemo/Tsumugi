package ui

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func TestLocalizedChatDisplayUsesPreviewKey(t *testing.T) {
	i18n.SetLocale("zh")
	t.Cleanup(func() { i18n.SetLocale("en") })

	got := localizedChatDisplay(telegram.Chat{
		Title:       "Nemo",
		Subtitle:    "group",
		LastPreview: "[Poll]",
		PreviewKey:  i18n.KeyMediaPoll,
	})
	if got.LastPreview != "[投票]" {
		t.Fatalf("preview = %q, want [投票]", got.LastPreview)
	}
}

func TestLocalizedChatDisplayRendersServicePreviewArgs(t *testing.T) {
	i18n.SetLocale("zh")
	t.Cleanup(func() { i18n.SetLocale("en") })

	got := localizedChatDisplay(telegram.Chat{
		LastPreview: "added Ada",
		PreviewKey:  i18n.KeyServiceUsersAdded,
		PreviewArg:  "Ada",
	})
	if got.LastPreview != "添加了 Ada" {
		t.Fatalf("preview = %q", got.LastPreview)
	}
}

// A user who literally types a media placeholder must not have it rewritten.
func TestLocalizedChatDisplayLeavesUserTextAlone(t *testing.T) {
	i18n.SetLocale("zh")
	t.Cleanup(func() { i18n.SetLocale("en") })

	got := localizedChatDisplay(telegram.Chat{LastPreview: "hello world"})
	if got.LastPreview != "hello world" {
		t.Fatalf("preview = %q", got.LastPreview)
	}
}

func TestLocalizedChatDisplayTranslatesChatKind(t *testing.T) {
	i18n.SetLocale("zh")
	t.Cleanup(func() { i18n.SetLocale("en") })

	got := localizedChatDisplay(telegram.Chat{Title: "Nemo", Subtitle: "group"})
	if got.Subtitle != "群组" {
		t.Fatalf("subtitle = %q, want 群组", got.Subtitle)
	}
}
