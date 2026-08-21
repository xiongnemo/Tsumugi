package render

import (
	"os"
	"strings"
)

// Glyphs used as semantics rather than decoration.
//
// A bare Linux text-mode console draws from a font of roughly 256 to 512 glyphs: no emoji, no
// CJK, and no guarantee beyond the base set. Message *text* cannot be fixed in-app on such a
// terminal, but our own chrome can, so every glyph that carries meaning goes through here and has
// an ASCII fallback. Half-block glyphs are excluded on purpose — they are in the standard console
// font, which is what keeps image previews and the login QR working there.
type GlyphSet struct {
	Marked   string
	Pinned   string
	Views    string
	Typing   string
	ReadOne  string
	ReadBoth string
	Attach   string
}

var unicodeGlyphs = GlyphSet{
	Marked:   "✓",
	Pinned:   "📌",
	Views:    "👁",
	Typing:   "✎",
	ReadOne:  "✓",
	ReadBoth: "✓✓",
	Attach:   "📎",
}

var asciiGlyphs = GlyphSet{
	Marked:   "*",
	Pinned:   "!",
	Views:    "v",
	Typing:   "~",
	ReadOne:  ".",
	ReadBoth: ":",
	Attach:   "@",
}

// glyphsCache is resolved once: the terminal cannot change identity mid-run, and Draw calls this
// per visible line.
var glyphsCache *GlyphSet

// Glyphs reports the glyph set the current terminal can actually render.
func Glyphs() GlyphSet {
	if glyphsCache == nil {
		set := unicodeGlyphs
		if asciiOnlyTerminal() {
			set = asciiGlyphs
		}
		glyphsCache = &set
	}
	return *glyphsCache
}

// asciiOnlyTerminal reports whether we are on a terminal with no dependable glyph coverage
// beyond ASCII.
//
// TERM=linux is the Linux virtual console. The UTF-8 locale check catches the other common
// case — a terminal in a legacy single-byte locale, where multi-byte output is mojibake
// regardless of the font. TSUMUGI_ASCII forces it either way, because no probe is reliable
// enough to be the only answer.
func asciiOnlyTerminal() bool {
	// Compared after trimming and only when non-empty: LookupEnv reports ok for an empty value,
	// so an unset-but-present variable would otherwise short-circuit the probes below and force
	// Unicode on a terminal that cannot render it.
	if forced := strings.TrimSpace(os.Getenv("TSUMUGI_ASCII")); forced != "" {
		return forced == "1" || strings.EqualFold(forced, "true")
	}
	term := os.Getenv("TERM")
	if term == "linux" || term == "vt100" || term == "vt220" || term == "dumb" {
		return true
	}
	// Windows consoles set no TERM and do render Unicode, so absence is not evidence of ASCII.
	if term == "" {
		return false
	}
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(key); v != "" {
			return !strings.Contains(strings.ToUpper(v), "UTF")
		}
	}
	return false
}

// GutterMarker encodes cursor and mark state into the message pane's 2-cell gutter.
//
// Both fit because the mark only ever needs one cell: "> " cursor, "✓ " marked, ">✓" both.
func GutterMarker(selected, marked bool) string {
	switch {
	case selected && marked:
		return ">" + Glyphs().Marked
	case selected:
		return "> "
	case marked:
		return Glyphs().Marked + " "
	default:
		return "  "
	}
}

// ResetGlyphsForTest clears the resolved glyph cache so a test can change the environment.
func ResetGlyphsForTest() {
	glyphsCache = nil
}
