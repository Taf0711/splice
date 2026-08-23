package tui

import (
	"container/list"
	"sync"
)

// defaultTranscriptBodyHeightCacheMaxEntries bounds entry count, and
// defaultTranscriptBodyHeightCacheMaxKeyCharacters bounds retained key bytes.
// The entry cap alone was a real defect: keys embed the row's full render-cache
// fingerprint (including its text), and a session whose body-item count crosses
// the cap thrashes the cache — every streaming/spinner frame then re-renders
// the ENTIRE transcript during measurement (~23ms/frame at 600 rows, measured).
// The character budget keeps worst-case memory bounded while letting realistic
// long sessions (thousands of body items) stay fully cached.
const (
	defaultTranscriptBodyHeightCacheMaxEntries       = 4096
	defaultTranscriptBodyHeightCacheMaxKeyCharacters = 4 << 20 // 4 MiB of key bytes
)

type transcriptBodyHeightCache struct {
	mu               sync.Mutex
	maxEntries       int
	maxKeyChars      int
	retainedKeyChars int
	items            map[string]*list.Element
	lru              *list.List
}

type transcriptBodyHeightCacheEntry struct {
	key    string
	height int
}

func newTranscriptBodyHeightCache(maxEntries int) *transcriptBodyHeightCache {
	return newTranscriptBodyHeightCacheWithBudget(maxEntries, defaultTranscriptBodyHeightCacheMaxKeyCharacters)
}

func newTranscriptBodyHeightCacheWithBudget(maxEntries int, maxKeyChars int) *transcriptBodyHeightCache {
	return &transcriptBodyHeightCache{
		maxEntries:  maxEntries,
		maxKeyChars: maxKeyChars,
		items:       map[string]*list.Element{},
		lru:         list.New(),
	}
}

func (c *transcriptBodyHeightCache) get(key string) (int, bool) {
	if c == nil || key == "" {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return 0, false
	}
	c.lru.MoveToFront(element)
	return element.Value.(*transcriptBodyHeightCacheEntry).height, true
}

func (c *transcriptBodyHeightCache) set(key string, height int) {
	if c == nil || key == "" || c.maxEntries <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		element.Value.(*transcriptBodyHeightCacheEntry).height = height
		c.lru.MoveToFront(element)
		return
	}
	c.items[key] = c.lru.PushFront(&transcriptBodyHeightCacheEntry{key: key, height: height})
	c.retainedKeyChars += len(key)
	for len(c.items) > c.maxEntries || (c.maxKeyChars > 0 && c.retainedKeyChars > c.maxKeyChars) {
		element := c.lru.Back()
		if element == nil {
			break
		}
		entry := element.Value.(*transcriptBodyHeightCacheEntry)
		delete(c.items, entry.key)
		c.lru.Remove(element)
		c.retainedKeyChars -= len(entry.key)
	}
}
