package media

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Sending a screenshot is the most common thing anyone wants to send, and on every platform it
// starts life in the clipboard rather than as a file. There is no pure-Go way to read an image out
// of a clipboard, so this shells out the same way copyText does for the other direction: no new
// dependency, and nothing to ship beside the executable.
//
// Every platform path can be absent — a bare Linux console has no clipboard at all — so the failure
// has to read as "not available here", not as a broken feature.

// clipboardTimeout bounds the helper. A clipboard read is instant when it works; without a bound a
// missing X display could hang the UI thread until the user gives up.
const clipboardTimeout = 10 * time.Second

// ClipboardImage writes the clipboard's image to a PNG in dir and returns its path.
//
// The caller owns the file. It is written into Tsumugi's own media directory rather than the system
// temp so that a failed send leaves something the user can still find and retry from.
func ClipboardImage(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	out := filepath.Join(dir, fmt.Sprintf("clipboard-%d.png", time.Now().UnixNano()))
	if err := writeClipboardImage(out); err != nil {
		_ = os.Remove(out)
		return "", err
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(out)
		return "", fmt.Errorf("clipboard has no image")
	}
	return out, nil
}

func writeClipboardImage(out string) error {
	switch runtime.GOOS {
	case "windows":
		// Get-Clipboard -Format Image hands back a System.Drawing.Bitmap, or nothing when the
		// clipboard holds text or is empty. Exiting non-zero in that case is what turns "no image"
		// into an error rather than a zero-byte PNG.
		script := `Add-Type -AssemblyName System.Drawing;` +
			`$img = Get-Clipboard -Format Image;` +
			`if ($null -eq $img) { exit 1 };` +
			`$img.Save($env:TSUMUGI_CLIPBOARD_OUT, [System.Drawing.Imaging.ImageFormat]::Png)`
		return runClipboardCommand(out, "powershell", []string{"-NoProfile", "-STA", "-Command", script}, false)
	case "darwin":
		// pngpaste is not part of macOS, so this is a best effort; the caller reports it plainly.
		return runClipboardCommand(out, "pngpaste", []string{out}, false)
	default:
		// Wayland first, then X11. Both write the image to stdout.
		if err := runClipboardCommand(out, "wl-paste", []string{"--type", "image/png"}, true); err == nil {
			return nil
		}
		return runClipboardCommand(out, "xclip", []string{"-selection", "clipboard", "-t", "image/png", "-o"}, true)
	}
}

// runClipboardCommand runs a helper, either letting it write the file itself or capturing its stdout.
func runClipboardCommand(out, name string, args []string, captureStdout bool) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("clipboard images need %s", name)
	}
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "TSUMUGI_CLIPBOARD_OUT="+out)
	// Deliberately not inheriting stderr: this runs while the TUI owns the terminal, and a helper
	// complaining about a missing display would draw over the interface.
	cmd.Stderr = nil
	if captureStdout {
		file, err := os.Create(out)
		if err != nil {
			return err
		}
		cmd.Stdout = file
		err = runWithTimeout(cmd)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	return runWithTimeout(cmd)
}

func runWithTimeout(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(clipboardTimeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("clipboard read timed out")
	}
}
