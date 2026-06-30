package media

import (
	"container/list"
	"image"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultInlineAnimCacheBytes int64 = 32 * 1024 * 1024
	inlineAnimCacheEnv                = "TSUMUGI_INLINE_ANIM_CACHE_MB"
	bytesPerMegabyte            int64 = 1024 * 1024
)

type AnimCacheStats struct {
	Entries int
	Bytes   int64
	Budget  int64
}

type animCacheEntry struct {
	key   string
	value any
	bytes int64
}

type animFrameCache struct {
	mu     sync.Mutex
	items  map[string]*list.Element
	order  *list.List
	bytes  int64
	budget int64
}

var inlineAnimFrames = newAnimFrameCache(defaultInlineAnimCacheBudget())

func defaultInlineAnimCacheBudget() int64 {
	return inlineAnimCacheBudgetFromEnv(os.Getenv(inlineAnimCacheEnv))
}

func inlineAnimCacheBudgetFromEnv(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultInlineAnimCacheBytes
	}
	mb, err := strconv.ParseInt(value, 10, 64)
	if err != nil || mb <= 0 {
		return defaultInlineAnimCacheBytes
	}
	if mb > (1<<63-1)/bytesPerMegabyte {
		return defaultInlineAnimCacheBytes
	}
	return mb * bytesPerMegabyte
}

func newAnimFrameCache(budget int64) *animFrameCache {
	if budget <= 0 {
		budget = defaultInlineAnimCacheBytes
	}
	return &animFrameCache{
		items:  make(map[string]*list.Element),
		order:  list.New(),
		budget: budget,
	}
}

func animCacheKey(kind, path string) string {
	return kind + ":" + path
}

func (c *animFrameCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*animCacheEntry).value, true
}

func (c *animFrameCache) add(key string, value any, bytes int64) {
	if value == nil {
		return
	}
	if bytes <= 0 {
		bytes = 1
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.items[key]; ok {
		entry := existing.Value.(*animCacheEntry)
		c.bytes -= entry.bytes
		entry.value = value
		entry.bytes = bytes
		c.bytes += bytes
		c.order.MoveToFront(existing)
	} else {
		entry := &animCacheEntry{key: key, value: value, bytes: bytes}
		c.items[key] = c.order.PushFront(entry)
		c.bytes += bytes
	}

	if c.budget > 0 && bytes > c.budget {
		c.removeLocked(key)
		return
	}
	c.evictLocked()
}

func (c *animFrameCache) stats() AnimCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return AnimCacheStats{
		Entries: len(c.items),
		Bytes:   c.bytes,
		Budget:  c.budget,
	}
}

func (c *animFrameCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.order.Init()
	c.bytes = 0
}

func (c *animFrameCache) removeLocked(key string) {
	elem, ok := c.items[key]
	if !ok {
		return
	}
	entry := elem.Value.(*animCacheEntry)
	c.bytes -= entry.bytes
	delete(c.items, key)
	c.order.Remove(elem)
}

func (c *animFrameCache) evictLocked() {
	for c.budget > 0 && c.bytes > c.budget {
		elem := c.order.Back()
		if elem == nil {
			return
		}
		c.removeLocked(elem.Value.(*animCacheEntry).key)
	}
}

func estimateFrameBytes(frames []image.Image) int64 {
	var total int64
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		bounds := frame.Bounds()
		w := bounds.Dx()
		h := bounds.Dy()
		if w <= 0 || h <= 0 {
			continue
		}
		total += int64(w) * int64(h) * 4
	}
	if total <= 0 && len(frames) > 0 {
		return int64(len(frames))
	}
	return total
}

func InlineAnimCacheStats() AnimCacheStats {
	return inlineAnimFrames.stats()
}

// ClearInlineAnimCache drops decoded animation frames without changing the cache budget.
func ClearInlineAnimCache() {
	inlineAnimFrames.clear()
}

func resetInlineAnimCacheForTest(budget int64) {
	inlineAnimFrames = newAnimFrameCache(budget)
}
