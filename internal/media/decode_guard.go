package media

import (
	"os/exec"
	"sync"
)

// decodeFailures remembers media that cannot be decoded into animation frames.
//
// The inline animation tick runs every 120ms and asks for the current frame of every animatable
// medium on screen. Nothing recorded a failure, so a video ffmpeg could not decode — or one that is
// simply a single frame — never populated the frame cache, and every tick started the work again:
// a temp directory, a fresh ffmpeg process, and a directory removal, eight times a second, forever.
// That alone can saturate a core with one such message visible.
//
// A failure is final for the run. These paths are content-addressed files in Tsumugi's own media
// cache, so the bytes behind a path do not change; if a download is later replaced, the path changes
// with it.
var decodeFailures sync.Map // path -> struct{}

func markDecodeFailed(path string) {
	if path != "" {
		decodeFailures.Store(path, struct{}{})
	}
}

func decodeFailed(path string) bool {
	if path == "" {
		return true
	}
	_, failed := decodeFailures.Load(path)
	return failed
}

// resetDecodeFailuresForTest clears the negative cache between tests.
func resetDecodeFailuresForTest() {
	decodeFailures.Range(func(key, _ any) bool {
		decodeFailures.Delete(key)
		return true
	})
}

// ffmpegAvailable reports whether ffmpeg is on PATH, resolved once.
//
// exec.LookPath walks PATH and stats candidates. It was called from VideoPathMayAnimate, which the
// animation tick reaches once per message per tick — so a screenful of video stickers turned into
// hundreds of filesystem probes a second for an answer that cannot change while the process runs.
var ffmpegAvailable = sync.OnceValue(func() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
})

// ffprobeAvailable is the same question for ffprobe, which ships beside ffmpeg but not always: a
// minimal build or a hand-placed binary can have one without the other, and reading dimensions out
// of a video needs specifically this one.
var ffprobeAvailable = sync.OnceValue(func() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
})

// FFmpegAvailable reports whether ffmpeg can be run.
//
// Exported over the same resolved-once value the animation path uses rather than a second
// exec.LookPath: that call walks PATH and stats every candidate, and doing it per frame per message
// was one of the things saturating a core.
func FFmpegAvailable() bool { return ffmpegAvailable() }

// FFprobeAvailable reports whether ffprobe can be run. Callers must degrade rather than fail when it
// cannot: ffmpeg is an optional dependency of this project, not a requirement.
func FFprobeAvailable() bool { return ffprobeAvailable() }
