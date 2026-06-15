package config

import (
	"strings"
	"testing"
)

func TestParseAuthMode(t *testing.T) {
	for _, mode := range []string{"", "user", "bot"} {
		if _, err := ParseAuthMode(mode); err != nil {
			t.Fatalf("ParseAuthMode(%q): %v", mode, err)
		}
	}
	if _, err := ParseAuthMode("admin"); err == nil {
		t.Fatal("expected error for unknown auth mode")
	}
}

func TestLoadRejectsInvalidEnvAPIID(t *testing.T) {
	t.Setenv("TSUMUGI_API_ID", "")
	t.Setenv("API_ID", "not-a-number")
	_, err := Load(CLIFlags{ConfigPath: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "API_ID") {
		t.Fatalf("Load error = %v, want API_ID parse error", err)
	}
}

func TestReadyForTelegramAllowsInteractiveUserPhone(t *testing.T) {
	cfg := Config{AuthMode: AuthUser, APIID: 123, APIHash: "hash"}
	if !cfg.ReadyForTelegram() {
		t.Fatal("user mode with app credentials should be ready for Telegram auth flow")
	}
	if !cfg.NeedsOnboarding() {
		t.Fatal("missing user phone should still trigger onboarding")
	}
}

func TestNeedsOnboardingByAuthMode(t *testing.T) {
	user := Config{AuthMode: AuthUser, APIID: 123, APIHash: "hash", Phone: "+15551234567"}
	if user.NeedsOnboarding() {
		t.Fatal("complete user credentials should not need onboarding")
	}
	bot := Config{AuthMode: AuthBot, APIID: 123, APIHash: "hash"}
	if !bot.NeedsOnboarding() || bot.ReadyForTelegram() {
		t.Fatal("bot mode without token should require onboarding and not be ready")
	}
	bot.BotToken = "123:token"
	if bot.NeedsOnboarding() || !bot.ReadyForTelegram() {
		t.Fatal("complete bot credentials should be ready")
	}
}
