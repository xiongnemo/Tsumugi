package media

import (
	"fmt"
	"image"
	"image/gif"
	"os"
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
	go func() {
		_, _, _ = gifDecodeGrp.Do(path, func() (any, error) {
			return decodeGIFAllFrames(path)
		})
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
		return RenderTerminalPreview(path, maxCols, maxRows)
	default:
		return RenderTerminalPreview(path, maxCols, maxRows)
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
		return true
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
	return RenderTerminalPreview(path, maxCols, maxRows)
}
