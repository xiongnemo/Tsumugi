package media

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

const maxVideoInlineFrames = 48

type videoDecoded struct {
	frames []image.Image
}

var (
	videoPathCache sync.Map
	videoDecodeGrp singleflight.Group
)

func videoContainerPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".webm")
}

// VideoPathMayAnimate reports whether path looks like an inline-animatable video without decoding.
func VideoPathMayAnimate(path string) bool {
	if !videoContainerPath(path) {
		return false
	}
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func videoFramesCached(path string) (*videoDecoded, bool) {
	v, ok := videoPathCache.Load(path)
	if !ok {
		return nil, false
	}
	d, ok := v.(*videoDecoded)
	return d, ok && d != nil && len(d.frames) > 0
}

func VideoAnimatable(path string) bool {
	if !VideoPathMayAnimate(path) {
		return false
	}
	d, ok := videoFramesCached(path)
	if ok {
		return len(d.frames) > 1
	}
	return true
}

// MP4Animatable reports whether path is an ffmpeg-decodable MP4 animation.
func MP4Animatable(path string) bool {
	if !strings.HasSuffix(strings.ToLower(path), ".mp4") {
		return false
	}
	return VideoAnimatable(path)
}

func ensureVideoDecodedAsync(path string) {
	if path == "" || !VideoPathMayAnimate(path) {
		return
	}
	if _, ok := videoFramesCached(path); ok {
		return
	}
	go func() {
		_, _, _ = videoDecodeGrp.Do(path, func() (any, error) {
			return decodeVideoFrames(path)
		})
	}()
}

func decodeVideoFrames(path string) (*videoDecoded, error) {
	if d, ok := videoFramesCached(path); ok {
		return d, nil
	}
	tmpDir, err := os.MkdirTemp("", "tsumugi-video-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	pattern := filepath.Join(tmpDir, "frame_%04d.png")
	cmd := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-i", path,
		"-vf", "fps=8",
		"-frames:v", fmt.Sprintf("%d", maxVideoInlineFrames),
		pattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg video frames: %w: %s", err, strings.TrimSpace(string(out)))
	}

	matches, err := filepath.Glob(filepath.Join(tmpDir, "frame_*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	frames := make([]image.Image, 0, len(matches))
	for _, framePath := range matches {
		f, err := os.Open(framePath)
		if err != nil {
			continue
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil || img == nil {
			continue
		}
		frames = append(frames, img)
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("video: no frames in %s", path)
	}
	d := &videoDecoded{frames: frames}
	videoPathCache.Store(path, d)
	return d, nil
}

// StillPreviewANSIToTview renders the first frame of a local video as a tview-safe raster preview.
func StillPreviewANSIToTview(path string, maxCols, maxRows int) string {
	if maxCols <= 0 || maxRows <= 0 || path == "" || !VideoPathMayAnimate(path) {
		return ""
	}
	d, err := decodeVideoFrames(path)
	if err != nil || d == nil || len(d.frames) == 0 {
		return ""
	}
	raw := RenderTerminalImage(d.frames[0], maxCols, maxRows)
	return ANSISGRToTview(raw)
}

func animatedVideoANSI(path string, tick int, maxCols, maxRows int) string {
	if maxCols <= 0 || maxRows <= 0 || path == "" {
		return ""
	}
	if d, ok := videoFramesCached(path); ok && len(d.frames) > 0 {
		frame := tick % len(d.frames)
		return RenderTerminalImage(d.frames[frame], maxCols, maxRows)
	}
	ensureVideoDecodedAsync(path)
	return RenderTerminalPreview(path, maxCols, maxRows)
}

func animatedMP4ANSI(path string, tick int, maxCols, maxRows int) string {
	return animatedVideoANSI(path, tick, maxCols, maxRows)
}
