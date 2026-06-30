package media

import (
	"image"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInlineAnimCacheBudgetEnvOverride(t *testing.T) {
	if got := defaultInlineAnimCacheBytes; got != 32*1024*1024 {
		t.Fatalf("default cache budget = %d, want 32 MiB", got)
	}
	if got := inlineAnimCacheBudgetFromEnv("64"); got != 64*1024*1024 {
		t.Fatalf("env cache budget = %d, want 64 MiB", got)
	}
	for _, value := range []string{"", "0", "-1", "abc"} {
		if got := inlineAnimCacheBudgetFromEnv(value); got != defaultInlineAnimCacheBytes {
			t.Fatalf("env cache budget for %q = %d, want default %d", value, got, defaultInlineAnimCacheBytes)
		}
	}
}

func TestInlineAnimCacheEvictsByByteBudget(t *testing.T) {
	resetInlineAnimCacheForTest(800)
	t.Cleanup(func() { resetInlineAnimCacheForTest(defaultInlineAnimCacheBytes) })

	first := &gifDecoded{frames: []image.Image{image.NewRGBA(image.Rect(0, 0, 10, 10))}}
	second := &videoDecoded{frames: []image.Image{image.NewRGBA(image.Rect(0, 0, 10, 10))}}
	third := &gifDecoded{frames: []image.Image{image.NewRGBA(image.Rect(0, 0, 10, 10))}}

	inlineAnimFrames.add(animCacheKey("gif", "first"), first, estimateFrameBytes(first.frames))
	inlineAnimFrames.add(animCacheKey("video", "second"), second, estimateFrameBytes(second.frames))
	if _, ok := inlineAnimFrames.get(animCacheKey("gif", "first")); !ok {
		t.Fatal("first entry should still be cached before budget overflow")
	}

	inlineAnimFrames.add(animCacheKey("gif", "third"), third, estimateFrameBytes(third.frames))
	if _, ok := inlineAnimFrames.get(animCacheKey("video", "second")); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if _, ok := inlineAnimFrames.get(animCacheKey("gif", "first")); !ok {
		t.Fatal("recently used entry should remain cached")
	}
	if _, ok := inlineAnimFrames.get(animCacheKey("gif", "third")); !ok {
		t.Fatal("new entry should remain cached")
	}
	if stats := InlineAnimCacheStats(); stats.Entries != 2 || stats.Bytes > stats.Budget {
		t.Fatalf("unexpected cache stats after eviction: %+v", stats)
	}
}

func TestInlineAnimCacheSkipsOversizedEntry(t *testing.T) {
	resetInlineAnimCacheForTest(100)
	t.Cleanup(func() { resetInlineAnimCacheForTest(defaultInlineAnimCacheBytes) })

	large := &gifDecoded{frames: []image.Image{image.NewRGBA(image.Rect(0, 0, 10, 10))}}
	inlineAnimFrames.add(animCacheKey("gif", "large"), large, estimateFrameBytes(large.frames))
	if _, ok := inlineAnimFrames.get(animCacheKey("gif", "large")); ok {
		t.Fatal("oversized entry should not be cached")
	}
}

func TestClearInlineAnimCacheDropsDecodedFrames(t *testing.T) {
	resetInlineAnimCacheForTest(defaultInlineAnimCacheBytes)
	t.Cleanup(func() { resetInlineAnimCacheForTest(defaultInlineAnimCacheBytes) })

	frame := &gifDecoded{frames: []image.Image{image.NewRGBA(image.Rect(0, 0, 10, 10))}}
	inlineAnimFrames.add(animCacheKey("gif", "cached"), frame, estimateFrameBytes(frame.frames))
	if stats := InlineAnimCacheStats(); stats.Entries == 0 || stats.Bytes == 0 {
		t.Fatalf("cache was not populated before clear: %+v", stats)
	}

	ClearInlineAnimCache()
	if stats := InlineAnimCacheStats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("cache was not cleared: %+v", stats)
	}
}

func TestStillPreviewDoesNotPopulateInlineAnimCache(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	resetInlineAnimCacheForTest(defaultInlineAnimCacheBytes)
	t.Cleanup(func() { resetInlineAnimCacheForTest(defaultInlineAnimCacheBytes) })

	videoPath := filepath.Join(t.TempDir(), "still.mp4")
	cmd := exec.Command(ffmpegPath,
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "color=c=red:s=8x8:d=0.1",
		"-frames:v", "1",
		"-pix_fmt", "yuv420p",
		videoPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not create test video: %v: %s", err, out)
	}

	if preview := StillPreviewANSIToTview(videoPath, 4, 2); preview == "" {
		t.Fatal("expected a still preview")
	}
	if stats := InlineAnimCacheStats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("still preview populated animation cache: %+v", stats)
	}
}
