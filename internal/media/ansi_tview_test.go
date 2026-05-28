package media

import (
	"strings"
	"testing"
)

func TestANSISGRToTviewNoRawEscape(t *testing.T) {
	in := "\x1b[38;2;201;183;183m▀\x1b[0m"
	out := ANSISGRToTview(in)
	if strings.Contains(out, "\x1b") {
		t.Fatalf("expected escapes stripped, got %q", out)
	}
	if strings.Contains(out, "[38;2;") {
		t.Fatalf("literal SGR params leaked (tview tag clash): %q", out)
	}
}
