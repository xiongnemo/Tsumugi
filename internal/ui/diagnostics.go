package ui

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"time"

	"github.com/nemo/Tsumugi/internal/media"
)

const (
	memoryDiagnosticsInterval = 30 * time.Second
	memoryDiagnosticsFile     = "tsumugi-debug-mem.jsonl"
)

func (a *App) runMemoryDiagnostics(ctx context.Context) {
	if os.Getenv("TSUMUGI_DEBUG_MEM") != "1" {
		return
	}

	ticker := time.NewTicker(memoryDiagnosticsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.logMemoryDiagnostics(ctx)
		}
	}
}

func (a *App) logMemoryDiagnostics(ctx context.Context) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	viewport := MessageViewportStats{}
	gapFillQueued := 0
	done := make(chan struct{})
	a.app.QueueUpdate(func() {
		if a.messages != nil {
			viewport = a.messages.Stats()
		}
		gapFillQueued = len(a.gapFillQueued)
		close(done)
	})

	select {
	case <-done:
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
		return
	}

	cache := media.InlineAnimCacheStats()
	appendMemoryDebugLog(ms, viewport, cache, gapFillQueued)
}

func appendMemoryDebugLog(ms runtime.MemStats, viewport MessageViewportStats, cache media.AnimCacheStats, gapFillQueued int) {
	payload := map[string]any{
		"message":   "memory diagnostics snapshot",
		"timestamp": time.Now().UnixMilli(),
		"data": map[string]any{
			"allocBytes":        ms.Alloc,
			"heapAllocBytes":    ms.HeapAlloc,
			"heapIdleBytes":     ms.HeapIdle,
			"heapReleasedBytes": ms.HeapReleased,
			"heapInuseBytes":    ms.HeapInuse,
			"sysBytes":          ms.Sys,
			"numGC":             ms.NumGC,
			"goroutines":        runtime.NumGoroutine(),
			"viewportMessages":  viewport.Messages,
			"viewportBlocks":    viewport.Blocks,
			"renderedLines":     viewport.RenderedLines,
			"viewportLimit":     viewport.Limit,
			"animCacheEntries":  cache.Entries,
			"animCacheBytes":    cache.Bytes,
			"animCacheBudget":   cache.Budget,
			"gapFillQueued":     gapFillQueued,
		},
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return
	}
	f, err := os.OpenFile(memoryDiagnosticsFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
