package media

import (
	"fmt"
	"image"
	"image/gif"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sync/singleflight"
)

type gifDecoded struct {
	frames []image.Image
}

var (
	gifDecodeGrp singleflight.Group
)

func gifFramesCached(path string) (*gifDecoded, bool) {
	v, ok := inlineAnimFrames.get(animCacheKey("gif", path))
	if !ok {
		return nil, false
	}
	d, ok := v.(*gifDecoded)
	return d, ok && d != nil && len(d.frames) > 0
}

func ensureGIFDecodedAsync(path string) {
	if path == "" {
		return
	}
	lower := strings.ToLower(path)
	if !strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".mp4") {
		return
	}
	if _, ok := gifFramesCached(path); ok {
		return
	}
	if decodeFailed(path) {
		return
	}
	go func() {
		_, err, _ := gifDecodeGrp.Do(path, func() (any, error) {
			return decodeGIFAllFrames(path)
		})
		if err != nil {
			markDecodeFailed(path)
			return
		}
		if d, ok := gifFramesCached(path); !ok || len(d.frames) < 2 {
			// A single-frame GIF has nothing to animate; without this the optimistic "true"
			// below keeps the list redrawing at 8Hz forever.
			markDecodeFailed(path)
		}
	}()
}

func decodeGIFAllFrames(path string) (*gifDecoded, error) {
	if d, ok := gifFramesCached(path); ok {
		return d, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		return nil, err
	}
	frames := make([]image.Image, 0, len(g.Image))
	for _, im := range g.Image {
		if im != nil {
			frames = append(frames, im)
		}
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("gif: no frames in %s", path)
	}
	d := &gifDecoded{frames: frames}
	inlineAnimFrames.add(animCacheKey("gif", path), d, estimateFrameBytes(frames))
	return d, nil
}

func AnimatedGIFANSI(path string, tick int, maxCols, maxRows int) string {
	return AnimatedInlineANSI(path, tick, maxCols, maxRows)
}

// AnimatedInlineANSI renders one frame of an inline GIF or MP4 animation.
func AnimatedInlineANSI(path string, tick int, maxCols, maxRows int) string {
	if maxCols <= 0 || maxRows <= 0 || path == "" {
		return ""
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".gif") && !strings.HasSuffix(lower, ".mp4"):
		return animatedGIFANSI(path, tick, maxCols, maxRows)
	case videoContainerPath(lower):
		if ansi := animatedVideoANSI(path, tick, maxCols, maxRows); ansi != "" {
			return ansi
		}
		// No ffmpeg, so this is a still thumbnail and the tick cannot change it.
		return stillFallbackANSI(path, maxCols, maxRows)
	default:
		return stillFallbackANSI(path, maxCols, maxRows)
	}
}

// InlineAnimatable reports whether path can be animated in the message list.
func InlineAnimatable(path string) bool {
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".gif") && !strings.HasSuffix(lower, ".mp4"):
		if d, ok := gifFramesCached(path); ok {
			return len(d.frames) > 1
		}
		return !decodeFailed(path)
	case videoContainerPath(lower):
		return VideoAnimatable(path)
	default:
		return false
	}
}

// GIFAnimatable reports whether path is a multi-frame GIF.
func GIFAnimatable(path string) bool {
	lower := strings.ToLower(path)
	if !strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".mp4") {
		return false
	}
	if d, ok := gifFramesCached(path); ok {
		return len(d.frames) > 1
	}
	return true
}

func animatedGIFANSI(path string, tick int, maxCols, maxRows int) string {
	if d, ok := gifFramesCached(path); ok && len(d.frames) > 0 {
		frame := tick % len(d.frames)
		return RenderTerminalImage(d.frames[frame], maxCols, maxRows)
	}
	ensureGIFDecodedAsync(path)
	// Frames are still decoding; the placeholder still is the same on every tick until they land.
	return stillFallbackANSI(path, maxCols, maxRows)
}

// stillFallbackANSI renders a still preview at the given size, cached.
//
// Every inline medium that cannot actually animate lands here — a video sticker with no ffmpeg
// installed, or a GIF whose frames are still being decoded — and it is reached from the 120ms
// animation tick, so it used to re-decode and re-resample the image eight times a second for every
// such message on screen. The result does not depend on the tick, so it never needed recomputing:
// measured at 2.4ms and 549KB per call, a screenful produced tens of megabytes per second of
// garbage, and the collector turned that into a busy core.
//
// Keyed by size as well as path, because the same file is rendered at one size in the message list
// and another in the detail preview.
func stillFallbackANSI(path string, maxCols, maxRows int) string {
	key := animCacheKey("still:"+strconv.Itoa(maxCols)+"x"+strconv.Itoa(maxRows), path)
	if cached, ok := inlineAnimFrames.get(key); ok {
		if ansi, ok := cached.(string); ok {
			return ansi
		}
	}
	ansi := RenderTerminalPreview(path, maxCols, maxRows)
	if ansi != "" {
		inlineAnimFrames.add(key, ansi, int64(len(ansi)))
	}
	return ansi
}
