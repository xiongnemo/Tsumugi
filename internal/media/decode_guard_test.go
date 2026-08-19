package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeTestStillPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 5), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "still.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The bug this pins: nothing recorded a failed decode, so the 120ms animation tick spawned a fresh
// ffmpeg process for an undecodable video eight times a second, forever. One such message on screen
// was enough to saturate a core.
func TestDecodeFailureStopsRetrying(t *testing.T) {
	resetDecodeFailuresForTest()
	t.Cleanup(resetDecodeFailuresForTest)

	path := filepath.Join(t.TempDir(), "broken.webm")
	if err := os.WriteFile(path, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}

	if decodeFailed(path) {
		t.Fatal("a path should not start out marked as failed")
	}
	markDecodeFailed(path)
	if !decodeFailed(path) {
		t.Fatal("the failure was not recorded")
	}
}

// A known-bad path must not report as animatable, or AdvanceGIF keeps returning true and the whole
// message list redraws at 8Hz for an animation that will never arrive.
func TestKnownBadVideoIsNotAnimatable(t *testing.T) {
	resetDecodeFailuresForTest()
	t.Cleanup(resetDecodeFailuresForTest)
	if !ffmpegAvailable() {
		t.Skip("ffmpeg is not installed, so VideoPathMayAnimate is false regardless")
	}

	path := filepath.Join(t.TempDir(), "broken.webm")
	if err := os.WriteFile(path, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !VideoAnimatable(path) {
		t.Fatal("an undecoded path is optimistically animatable until proven otherwise")
	}
	markDecodeFailed(path)
	if VideoAnimatable(path) {
		t.Fatal("a known-bad path must stop being reported as animatable")
	}
}

// A single-frame GIF has nothing to animate, and the optimistic answer used to keep the list
// redrawing forever.
func TestKnownBadGIFIsNotAnimatable(t *testing.T) {
	resetDecodeFailuresForTest()
	t.Cleanup(resetDecodeFailuresForTest)

	path := filepath.Join(t.TempDir(), "still.gif")
	if err := os.WriteFile(path, []byte("GIF89a not really"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !InlineAnimatable(path) {
		t.Fatal("an undecoded GIF is optimistically animatable")
	}
	markDecodeFailed(path)
	if InlineAnimatable(path) {
		t.Fatal("a known-bad GIF must stop being reported as animatable")
	}
}

// ffmpeg cannot appear or vanish in a way that matters mid-run, and this was being resolved with a
// PATH walk once per message per animation tick.
func TestFFmpegLookupIsResolvedOnce(t *testing.T) {
	first := ffmpegAvailable()
	for i := 0; i < 1000; i++ {
		if ffmpegAvailable() != first {
			t.Fatal("the cached answer changed")
		}
	}
}

// The still fallback is what every non-animatable inline medium renders, from a tick that fires
// eight times a second. It has to be cached.
func TestStillFallbackIsCached(t *testing.T) {
	img := writeTestStillPNG(t)

	first := stillFallbackANSI(img, PreviewMaxCols, PreviewMaxRows)
	if first == "" {
		t.Fatal("empty render")
	}
	// Removing the file proves the second call never touched it.
	if err := os.Remove(img); err != nil {
		t.Fatal(err)
	}
	if second := stillFallbackANSI(img, PreviewMaxCols, PreviewMaxRows); second != first {
		t.Fatal("the second call re-rendered instead of using the cache")
	}
}

// Keyed by size as well as path: the same file is rendered small in the message list and large in
// the detail preview.
func TestStillFallbackCacheIsSizeAware(t *testing.T) {
	img := writeTestStillPNG(t)

	small := stillFallbackANSI(img, 20, 6)
	large := stillFallbackANSI(img, 60, 20)

	if small == large {
		t.Fatal("different sizes returned the same render; the cache key ignores size")
	}
}
