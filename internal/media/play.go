package media

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
)

// Playing a voice note mostly already worked: OpenPath hands the cached file to the desktop, which
// picks a player. What it cannot do is a bare Linux console, where xdg-open has no desktop to ask and
// silently does nothing - so the one place a terminal client is most likely to be used is the one
// place "open" did not play anything.

// consolePlayers are tried in order. mpv and ffplay handle every format Telegram sends; paplay and
// aplay are the minimal fallbacks that exist on a system with no media stack at all.
//
// ffplay's flags matter: without them it opens a window and waits, which on a console means a process
// that never exits.
var consolePlayers = []struct {
	Name string
	Args []string
}{
	{"mpv", []string{"--no-video", "--really-quiet"}},
	{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
	{"paplay", nil},
	{"aplay", nil},
}

// consolePlayer resolves the first available player once.
//
// sync.OnceValue for the same reason the ffmpeg probe uses it: exec.LookPath walks PATH and stats
// every candidate, and this is reachable from a key the user can hold down.
var consolePlayer = sync.OnceValue(func() (found struct {
	Name string
	Args []string
}) {
	for _, player := range consolePlayers {
		if _, err := exec.LookPath(player.Name); err == nil {
			found.Name = player.Name
			found.Args = player.Args
			return found
		}
	}
	return found
})

// PlayAudio plays a cached audio file.
//
// The desktop handler comes first, because on a desktop it is what respects the user's chosen player.
// A CLI player is the fallback, and the only option on a console.
func PlayAudio(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if desktopHandlerAvailable() {
		if err := OpenPath(path); err == nil {
			return nil
		}
	}
	player := consolePlayer()
	if player.Name == "" {
		return fmt.Errorf("no audio player found: install mpv or ffplay")
	}
	cmd := exec.Command(player.Name, append(player.Args, path)...)
	// Never inherit stdout or stderr: a player writing progress to the terminal would draw straight
	// over the interface, which is the rule in CLAUDE.md.
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// desktopHandlerAvailable reports whether handing the file to the OS is likely to play it.
//
// On Windows and macOS there is always a handler. On Linux there is one only under a session bus:
// xdg-open on a bare VT returns success and plays nothing, which is exactly the case this whole file
// exists for, so "success" from it cannot be trusted there.
func desktopHandlerAvailable() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	default:
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}

// PlayableAudioKind reports whether a media kind is something to play rather than to open.
func PlayableAudioKind(kind string) bool {
	switch kind {
	case "voice", "audio":
		return true
	default:
		return false
	}
}
