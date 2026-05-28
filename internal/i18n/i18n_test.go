package i18n

import "testing"

func TestSetLocaleAndTranslate(t *testing.T) {
	SetLocale("zh")
	if got := T(KeyMessageEmpty); got != "(空消息)" {
		t.Fatalf("empty message = %q", got)
	}
	if got := MediaLabel(KeyMediaPoll, ""); got != "[投票]" {
		t.Fatalf("poll label = %q", got)
	}

	SetLocale("en")
	if got := T(KeyMessageEmpty); got != "(empty message)" {
		t.Fatalf("empty message = %q", got)
	}
}

func TestMediaLabelFallsBackToKind(t *testing.T) {
	SetLocale("en")
	if got := MediaLabel("", "voice"); got != "[Voice]" {
		t.Fatalf("voice label = %q", got)
	}
}

func TestLocalizeKnownMediaLabel(t *testing.T) {
	SetLocale("zh")
	if got := LocalizeKnown("[Poll]"); got != "[投票]" {
		t.Fatalf("poll = %q", got)
	}
	SetLocale("en")
	if got := LocalizeKnown("[投票]"); got != "[Poll]" {
		t.Fatalf("poll reverse = %q", got)
	}
}

func TestChatKindLocalized(t *testing.T) {
	SetLocale("zh")
	if got := ChatKind("private"); got != "私聊" {
		t.Fatalf("private = %q", got)
	}
}

func TestTfSyncProgress(t *testing.T) {
	SetLocale("zh")
	got := Tf(KeyStatusSyncingProgress, 4, 718, "Example")
	if got != "同步 4/718：Example" {
		t.Fatalf("status = %q", got)
	}
}
