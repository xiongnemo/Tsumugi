package render

import (
	"testing"
	"unicode/utf8"
)

func withEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
	ResetGlyphsForTest()
	t.Cleanup(ResetGlyphsForTest)
}

// Every semantic glyph needs an ASCII counterpart, or a bare Linux console shows tofu where the
// meaning is.
func TestAsciiGlyphsAreAllASCII(t *testing.T) {
	set := asciiGlyphs
	for name, value := range map[string]string{
		"Marked":   set.Marked,
		"Pinned":   set.Pinned,
		"Views":    set.Views,
		"Typing":   set.Typing,
		"ReadOne":  set.ReadOne,
		"ReadBoth": set.ReadBoth,
	} {
		if value == "" {
			t.Errorf("%s has no ASCII fallback", name)
			continue
		}
		for _, r := range value {
			if r > 0x7F {
				t.Errorf("%s = %q contains a non-ASCII rune %q", name, value, r)
			}
		}
	}
}

// Only the mark is width-critical: it shares the message pane's fixed 2-cell gutter, so a wider
// fallback would shift every message's content on the terminal that can least afford it. The read
// and view glyphs are appended at the end of a row, where width does not shift anything.
func TestAsciiMarkGlyphIsSingleWidth(t *testing.T) {
	if n := utf8.RuneCountInString(asciiGlyphs.Marked); n != 1 {
		t.Fatalf("ASCII mark %q is %d runes, want 1 to fit the gutter", asciiGlyphs.Marked, n)
	}
	if n := utf8.RuneCountInString(unicodeGlyphs.Marked); n != 1 {
		t.Fatalf("Unicode mark %q is %d runes, want 1 to fit the gutter", unicodeGlyphs.Marked, n)
	}
}

func TestTsumugiASCIIForcesTheFallback(t *testing.T) {
	withEnv(t, "TSUMUGI_ASCII", "1")

	if got := Glyphs().Marked; got != asciiGlyphs.Marked {
		t.Fatalf("Marked = %q, want the ASCII fallback %q", got, asciiGlyphs.Marked)
	}
}

func TestTermLinuxSelectsTheFallback(t *testing.T) {
	t.Setenv("TSUMUGI_ASCII", "")
	withEnv(t, "TERM", "linux")

	if got := Glyphs().Pinned; got != asciiGlyphs.Pinned {
		t.Fatalf("Pinned = %q, want the ASCII fallback on a Linux console", got)
	}
}

// Windows consoles set no TERM and do render Unicode, so an absent TERM must not be read as
// evidence of an ASCII-only terminal.
func TestEmptyTermKeepsUnicode(t *testing.T) {
	t.Setenv("TSUMUGI_ASCII", "")
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	withEnv(t, "TERM", "")

	if got := Glyphs().Pinned; got != unicodeGlyphs.Pinned {
		t.Fatalf("Pinned = %q, want the Unicode glyph when TERM is unset", got)
	}
}

func TestNonUTF8LocaleSelectsTheFallback(t *testing.T) {
	t.Setenv("TSUMUGI_ASCII", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	withEnv(t, "LANG", "en_US.ISO-8859-1")

	if got := Glyphs().Views; got != asciiGlyphs.Views {
		t.Fatalf("Views = %q, want the ASCII fallback in a single-byte locale", got)
	}
}

func TestUTF8LocaleKeepsUnicode(t *testing.T) {
	t.Setenv("TSUMUGI_ASCII", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	withEnv(t, "LANG", "en_US.UTF-8")

	if got := Glyphs().Views; got != unicodeGlyphs.Views {
		t.Fatalf("Views = %q, want the Unicode glyph in a UTF-8 locale", got)
	}
}

func TestGutterMarkerIsAlwaysTwoCells(t *testing.T) {
	for _, forced := range []string{"0", "1"} {
		withEnv(t, "TSUMUGI_ASCII", forced)
		for _, tc := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
			got := GutterMarker(tc[0], tc[1])
			if n := utf8.RuneCountInString(got); n != 2 {
				t.Errorf("TSUMUGI_ASCII=%s GutterMarker(%v,%v) = %q is %d cells, want 2",
					forced, tc[0], tc[1], got, n)
			}
		}
	}
}
