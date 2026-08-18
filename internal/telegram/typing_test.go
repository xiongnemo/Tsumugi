package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

func TestTypingActionKey(t *testing.T) {
	cases := []struct {
		name       string
		action     tg.SendMessageActionClass
		wantKey    string
		wantActive bool
	}{
		{"nil", nil, "", false},
		{"cancel", &tg.SendMessageCancelAction{}, "", false},
		{"typing", &tg.SendMessageTypingAction{}, i18n.KeyTypingIsTyping, true},
		{"voice", &tg.SendMessageRecordAudioAction{}, i18n.KeyTypingIsRecordingVoice, true},
		{"round video", &tg.SendMessageRecordRoundAction{}, i18n.KeyTypingIsRecordingVoice, true},
		{"photo", &tg.SendMessageUploadPhotoAction{}, i18n.KeyTypingIsUploading, true},
		{"document", &tg.SendMessageUploadDocumentAction{}, i18n.KeyTypingIsUploading, true},
		// An action we do not enumerate must still show something rather than vanish.
		{"unknown", &tg.SendMessageGamePlayAction{}, i18n.KeyTypingIsActive, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, active := typingActionKey(tc.action)
			if key != tc.wantKey || active != tc.wantActive {
				t.Fatalf("typingActionKey = (%q, %v), want (%q, %v)", key, active, tc.wantKey, tc.wantActive)
			}
		})
	}
}

// Every action key is rendered with exactly one name argument, so a template with a different
// placeholder count would print "%!s(MISSING)" or drop the name.
func TestTypingActionTemplatesTakeOneStringArg(t *testing.T) {
	keys := []string{
		i18n.KeyTypingIsTyping,
		i18n.KeyTypingIsRecordingVoice,
		i18n.KeyTypingIsUploading,
		i18n.KeyTypingIsActive,
	}
	for _, locale := range []string{"en", "zh"} {
		i18n.SetLocale(locale)
		for _, key := range keys {
			tmpl := i18n.T(key)
			if got := strings.Count(tmpl, "%s"); got != 1 {
				t.Errorf("%s[%s] = %q, want exactly one %%s, got %d", key, locale, tmpl, got)
			}
			if strings.Contains(tmpl, "%d") {
				t.Errorf("%s[%s] = %q, want no %%d (rendered with a name)", key, locale, tmpl)
			}
		}
		if tmpl := i18n.T(i18n.KeyTypingMany); strings.Count(tmpl, "%d") != 1 {
			t.Errorf("typing.many[%s] = %q, want exactly one %%d (rendered with a count)", locale, tmpl)
		}
	}
	i18n.SetLocale("en")
}

func TestClaimTypingSlotThrottlesRepeats(t *testing.T) {
	c := &GotdClient{typingSentAt: make(map[string]time.Time)}

	if !c.claimTypingSlot("user:1", true) {
		t.Fatal("first notification should go out")
	}
	if c.claimTypingSlot("user:1", true) {
		t.Fatal("a repeat inside the refresh interval should be suppressed")
	}
	if !c.claimTypingSlot("user:2", true) {
		t.Fatal("throttle must be per peer")
	}

	// Once the interval has elapsed the indicator needs refreshing before Telegram expires it.
	c.typingSentAt["user:1"] = time.Now().Add(-typingRefreshInterval - time.Second)
	if !c.claimTypingSlot("user:1", true) {
		t.Fatal("a notification after the refresh interval should go out")
	}
}

// A cancel must never be throttled away, and it resets the peer so resuming typing notifies at
// once rather than waiting out the interval.
func TestClaimTypingSlotAlwaysAllowsCancel(t *testing.T) {
	c := &GotdClient{typingSentAt: make(map[string]time.Time)}

	c.claimTypingSlot("user:1", true)
	if !c.claimTypingSlot("user:1", false) {
		t.Fatal("cancel should always go out")
	}
	if !c.claimTypingSlot("user:1", true) {
		t.Fatal("typing after a cancel should not be throttled")
	}
}

// typingRefreshInterval has to leave headroom inside Telegram's six-second validity window, or
// the receiver's indicator blinks off between refreshes.
func TestTypingRefreshIntervalFitsTelegramValidity(t *testing.T) {
	if typingRefreshInterval >= 6*time.Second {
		t.Fatalf("typingRefreshInterval = %s, want well under the 6s validity window", typingRefreshInterval)
	}
}

func TestPeerSupportsTypingExcludesSavedMessages(t *testing.T) {
	if peerSupportsTyping(storage.Peer{Kind: "self"}) {
		t.Fatal("Saved Messages has nobody to notify")
	}
	for _, kind := range []string{"user", "chat", "channel"} {
		if !peerSupportsTyping(storage.Peer{Kind: kind}) {
			t.Errorf("kind %q should support typing", kind)
		}
	}
}
