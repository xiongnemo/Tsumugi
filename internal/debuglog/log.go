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
	"strings"
	"sync"
	"time"
)

const defaultLogName = "debug-tsumugi.log"

var (
	mu      sync.Mutex
	once    sync.Once
	enabled bool
)

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

func logPath() string {
	if path := strings.TrimSpace(os.Getenv("TSUMUGI_DEBUG_LOG")); path != "" {
		return path
	}
	return defaultLogName
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
	f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
