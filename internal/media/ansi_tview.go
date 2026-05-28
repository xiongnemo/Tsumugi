package media

import (
	"bytes"
	"io"
	"strings"

	"github.com/rivo/tview"
)

const maxANSIRasterBytes = 8 << 20

// ANSISGRToTview converts raw terminal SGR/CSI color sequences (e.g. 24-bit)
// into tview color tags so TextView can render them. Use for output from
// RenderTerminalPreview or similar ANSI emitters. OSC/APC sequences that terminals
// sometimes emit are stripped; on parse failure the result is empty to avoid "garbage" in the TUI.
func ANSISGRToTview(s string) (out string) {
	if s == "" {
		return ""
	}
	if len(s) > maxANSIRasterBytes {
		s = s[:maxANSIRasterBytes]
	}
	s = stripOSCSequences(s)
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()
	var buf bytes.Buffer
	w := tview.ANSIWriter(&buf)
	if _, err := io.Copy(w, strings.NewReader(s)); err != nil {
		return ""
	}
	return buf.String()
}

// stripOSCSequences removes OSC (\x1b] … BEL or ST) chunks that can confuse
// tview.ANSIWriter or leak as visible text when ESC is mishandled.
func stripOSCSequences(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	src := []byte(s)
	for i := 0; i < len(src); {
		if i+1 < len(src) && src[i] == 0x1b && src[i+1] == ']' {
			i += 2
			for i < len(src) {
				if src[i] == 0x07 {
					i++
					break
				}
				if i+1 < len(src) && src[i] == 0x1b && src[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}
