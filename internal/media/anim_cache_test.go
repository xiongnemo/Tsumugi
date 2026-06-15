package media

import (
	"image"
	"testing"
)

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
