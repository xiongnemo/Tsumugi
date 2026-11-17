// Package debuglog writes NDJSON diagnostics to a file.
//
// File-only by design: Tsumugi draws a full-screen TUI, so anything written to stdout or stderr
// lands in the same screen buffer tview is painting and corrupts it. There is no "just print it"
// option while the UI is up.
//
// Disabled unless TSUMUGI_DEBUG=1, so the hot paths that call it cost one environment-variable
// read that is resolved once.
package debuglog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// defaultLogName is the fallback when nothing called SetPath: the working directory, which is
// where every release before the XDG layout wrote it.
const defaultLogName = "debug-tsumugi.log"

var (
	mu         sync.Mutex
	once       sync.Once
	enabled    bool
	configured string
)

// SetPath sets where the log goes when TSUMUGI_DEBUG_LOG does not say: the state directory, or the
// --config-dir root's. Its directory is created on the first write.
func SetPath(path string) {
	mu.Lock()
	defer mu.Unlock()
	configured = path
}

// Enabled reports whether debug logging is active.
//
// Resolved once: the environment cannot change mid-run, and Log is called from paths that run per
// event.
func Enabled() bool {
	once.Do(func() {
		v := strings.TrimSpace(os.Getenv("TSUMUGI_DEBUG"))
		enabled = v == "1" || strings.EqualFold(v, "true")
	})
	return enabled
}

// logPath reports where to write, and whether the directory is Tsumugi's to create. A
// TSUMUGI_DEBUG_LOG path is taken as given.
func logPath() (string, bool) {
	if path := strings.TrimSpace(os.Getenv("TSUMUGI_DEBUG_LOG")); path != "" {
		return path, false
	}
	if configured != "" {
		return configured, true
	}
	return defaultLogName, false
}

// Log appends one NDJSON record.
func Log(event string, fields map[string]any) {
	if !Enabled() {
		return
	}
	payload := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		payload[k] = v
	}
	payload["event"] = event
	payload["ts"] = time.Now().Format(time.RFC3339Nano)

	raw, err := json.Marshal(payload)
	if err != nil {
		// Fall back to a record that at least records the loss, rather than dropping silently.
		raw, err = json.Marshal(map[string]any{
			"event":         event,
			"ts":            time.Now().Format(time.RFC3339Nano),
			"marshal_error": err.Error(),
		})
		if err != nil {
			return
		}
	}

	mu.Lock()
	defer mu.Unlock()
	path, ours := logPath()
	if ours {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Close()
}

// Error records an error with its full text.
//
// The point of this one: the status bar has a single truncated column, so a long RPC error is
// unreadable there by construction. This is where the untruncated version goes.
func Error(where string, err error, fields map[string]any) {
	if !Enabled() || err == nil {
		return
	}
	payload := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		payload[k] = v
	}
	payload["where"] = where
	payload["error"] = err.Error()
	Log("error", payload)
}
