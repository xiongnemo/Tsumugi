package debuglog

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

const defaultLogName = "debug-tsumugi.log"

var logMu sync.Mutex

// Enabled reports whether agent debug logging is active (TSUMUGI_DEBUG=1).
func Enabled() bool {
	return os.Getenv("TSUMUGI_DEBUG") == "1"
}

func logPath() string {
	if path := os.Getenv("TSUMUGI_DEBUG_LOG"); path != "" {
		return path
	}
	return defaultLogName
}

// Log appends one NDJSON debug line when TSUMUGI_DEBUG=1.
func Log(hypothesisID, location, message string, data map[string]any) {
	if !Enabled() {
		return
	}
	payload := map[string]any{
		"hypothesisId": hypothesisID,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Close()
}
