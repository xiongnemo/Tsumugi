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
	if got := messagePreview(msg); got != "[Poll]" {
		t.Fatalf("preview = %q, want [Poll]", got)
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
