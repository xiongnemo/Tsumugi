package ui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/nemo/Tsumugi/internal/media"
)

const memoryDiagnosticsInterval = 30 * time.Second

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
	fmt.Fprintf(os.Stderr,
		"tsumugi mem: alloc=%dMB heap=%dMB sys=%dMB goroutines=%d viewport_messages=%d viewport_blocks=%d rendered_lines=%d viewport_limit=%d anim_cache_entries=%d anim_cache=%dMB anim_cache_budget=%dMB gap_fill_queue=%d\n",
		bytesToMB(ms.Alloc),
		bytesToMB(ms.HeapAlloc),
		bytesToMB(ms.Sys),
		runtime.NumGoroutine(),
		viewport.Messages,
		viewport.Blocks,
		viewport.RenderedLines,
		viewport.Limit,
		cache.Entries,
		bytesToMB(uint64(cache.Bytes)),
		bytesToMB(uint64(cache.Budget)),
		gapFillQueued,
	)
}

func bytesToMB(n uint64) uint64 {
	return n / (1024 * 1024)
}
