package telegram

import (
	"testing"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
)

func TestClassifyMessageMediaPoll(t *testing.T) {
	i18n.SetLocale("en")
	media := classifyMessageMedia(&tg.MessageMediaPoll{})
	if media.Kind != "poll" {
		t.Fatalf("kind = %q, want poll", media.Kind)
	}
	if media.Label != "[Poll]" {
		t.Fatalf("label = %q, want [Poll]", media.Label)
	}
	if media.LabelKey != i18n.KeyMediaPoll {
		t.Fatalf("label key = %q", media.LabelKey)
	}
}

func TestClassifyMessageMediaPollLocalized(t *testing.T) {
	i18n.SetLocale("zh")
	media := classifyMessageMedia(&tg.MessageMediaPoll{})
	if media.Label != "[投票]" {
		t.Fatalf("label = %q, want [投票]", media.Label)
	}
}

func TestClassifyDocumentVoiceAndAudio(t *testing.T) {
	i18n.SetLocale("en")
	voice := classifyDocument(&tg.Document{
		MimeType: "audio/ogg",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAudio{Voice: true, Duration: 12},
		},
	})
	if voice.Kind != "voice" || voice.Label != "[Voice]" {
		t.Fatalf("voice = %+v", voice)
	}

	audio := classifyDocument(&tg.Document{
		MimeType: "audio/mpeg",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAudio{Voice: false, Duration: 180},
		},
	})
	if audio.Kind != "audio" || audio.Label != "[Audio]" {
		t.Fatalf("audio = %+v", audio)
	}
}

func TestMessagePreviewUsesPollLabel(t *testing.T) {
	i18n.SetLocale("en")
	msg := &tg.Message{Media: &tg.MessageMediaPoll{}}
	got := messagePreview(msg)
	if got.Text != "[Poll]" {
		t.Fatalf("preview = %q, want [Poll]", got.Text)
	}
	// The key is persisted so a locale switch re-renders without reverse lookup.
	if got.Key != i18n.KeyMediaPoll {
		t.Fatalf("preview key = %q, want %q", got.Key, i18n.KeyMediaPoll)
	}
}

func TestMessagePreviewKeepsUserTextUnkeyed(t *testing.T) {
	i18n.SetLocale("en")
	got := messagePreview(&tg.Message{Message: "[Poll]"})
	if got.Text != "[Poll]" {
		t.Fatalf("preview = %q", got.Text)
	}
	// A user who literally types "[Poll]" must not have it translated on locale switch.
	if got.Key != "" {
		t.Fatalf("preview key = %q, want empty for user text", got.Key)
	}
}

func TestMessagePreviewCarriesStickerAlt(t *testing.T) {
	i18n.SetLocale("en")
	got := messagePreview(&tg.Message{Media: &tg.MessageMediaDocument{
		Document: &tg.Document{
			MimeType:   "image/webp",
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Alt: "😀"}},
		},
	}})
	if got.Key != i18n.KeyMediaSticker || got.Arg != "😀" {
		t.Fatalf("preview = %+v, want sticker key with alt arg", got)
	}
	if rendered := i18n.PreviewText(got.Key, got.Arg); rendered != got.Text {
		t.Fatalf("PreviewText = %q, want %q", rendered, got.Text)
	}
}

func TestLocalizeMediaAttachmentRefreshesLabel(t *testing.T) {
	media := MediaAttachment{Kind: "poll", LabelKey: i18n.KeyMediaPoll, Label: "[Poll]"}
	i18n.SetLocale("zh")
	got := LocalizeMediaAttachment(media)
	if got.Label != "[投票]" {
		t.Fatalf("label = %q, want [投票]", got.Label)
	}
}
