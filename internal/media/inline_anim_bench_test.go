package media

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func writeBenchGIF(b *testing.B, frames int) string {
	b.Helper()
	g := &gif.GIF{}
	pal := color.Palette{color.Black, color.White, color.RGBA{R: 200, G: 30, B: 90, A: 255}}
	for f := 0; f < frames; f++ {
		img := image.NewPaletted(image.Rect(0, 0, 320, 240), pal)
		for y := 0; y < 240; y++ {
			for x := 0; x < 320; x++ {
				img.SetColorIndex(x, y, uint8((x+y+f)%3))
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 8)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(b.TempDir(), "bench.gif")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		b.Fatal(err)
	}
	return path
}

func writeBenchJPEG(b *testing.B) string {
	b.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(b.TempDir(), "bench.jpg")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		b.Fatal(err)
	}
	return path
}

// One animated message, one animation tick, at the size the message list uses.
func BenchmarkAnimatedInlineANSIGIF(b *testing.B) {
	path := writeBenchGIF(b, 12)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if AnimatedInlineANSI(path, i, PreviewMaxCols, PreviewMaxRows) == "" {
			b.Fatal("empty render")
		}
	}
}

// The fallback branch: a still image re-decoded and re-rendered per tick.
func BenchmarkRenderTerminalPreviewJPEG(b *testing.B) {
	path := writeBenchJPEG(b)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if RenderTerminalPreview(path, PreviewMaxCols, PreviewMaxRows) == "" {
			b.Fatal("empty render")
		}
	}
}

// The path the animation tick really takes for a still fallback: same arguments every tick.
func BenchmarkStillFallbackANSIRepeated(b *testing.B) {
	path := writeBenchJPEG(b)
	// Warm, as it is after the first tick.
	stillFallbackANSI(path, PreviewMaxCols, PreviewMaxRows)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if stillFallbackANSI(path, PreviewMaxCols, PreviewMaxRows) == "" {
			b.Fatal("empty render")
		}
	}
}

// A video sticker with no ffmpeg is the case that was re-rendering per tick.
func BenchmarkAnimatedInlineANSIStillFallbackRepeated(b *testing.B) {
	src := writeBenchJPEG(b)
	// A .webm path with JPEG content stands in for the thumbnail fallback: the video branch is
	// taken, ffmpeg yields nothing, and the still fallback answers.
	path := filepath.Join(b.TempDir(), "sticker.webm")
	data, err := os.ReadFile(src)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	AnimatedInlineANSI(path, 0, PreviewMaxCols, PreviewMaxRows)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		AnimatedInlineANSI(path, i, PreviewMaxCols, PreviewMaxRows)
	}
}
