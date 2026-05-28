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
