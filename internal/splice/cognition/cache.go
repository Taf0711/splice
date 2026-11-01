package cognition

import (
	"container/list"
	"context"
	"os"
	"sync"
)

// cache.go implements the C1b layer 2 (run-local memoization): the batch
// changed-path set for (sourceCommit, worktree generation) is reused for the
// run's lifetime, so repair re-entry and later stages pay ZERO git spawns in
// the steady state. This is memoization of an EXACT git result, never a
// substitute for it: no mtime/size pair ever promotes unknown to fresh or
// fresh to anything else. mtime+size is an invalidation HINT ONLY (research
// report section 8): metadata changed means the cached verdict MAY be wrong,
// so the whole entry is dropped and the real diff re-spawned. Unchanged
// metadata means the entry survives; it never proves freshness.

// WorktreeGeneration is a run-local logical counter incremented whenever
// Splice itself performs or permits a repository mutation (writer stage,
// patch apply, format, repair re-entry, write-capable tools). It scopes the
// memo cache: a new generation invalidates every cached set, because the
// working tree the previous batch observed no longer exists.
type WorktreeGeneration uint64

// fileStat is the invalidation-hint snapshot for one anchor path.
type fileStat struct {
	mtimeUnixNano int64
	size          int64
	mode          os.FileMode
}

// cacheEntry is one memoized batch result: the changed-path set git reported
// for (commit, generation), plus the per-anchor stat snapshots taken when an
// anchor was classified against this set. A stat snapshot never proves
// freshness; it only detects EXTERNAL working-tree mutations (anything
// Splice did not bump the generation for) so the entry can be dropped and
// the exact diff re-run.
type cacheEntry struct {
	commit     string
	changed    map[string]bool
	generation WorktreeGeneration
	// stats records mtime+size per anchor path classified so far. Only files
	// are guarded; a package-directory anchor is not stat'd (a directory's
	// mtime is not a reliable mutation signal), so a directory-anchored
	// classification is re-proven by the next generation bump or guard drop.
	stats map[string]fileStat
}

// FreshnessCache memoizes batched freshness results for one run. It is safe
// for concurrent use (the pass loop and repair re-entry can interleave);
// the lock guards the map and LRU list only, never a git call.
type FreshnessCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front = most recent, back = evicted first
	gen     WorktreeGeneration
	// classify is the spawn seam, injectable for tests. It must implement the
	// EXACT git semantics (ChangedPaths); the cache only memoizes it.
	classify func(ctx context.Context, repoRoot, commit string) (map[string]bool, error)
	// stat is the invalidation-hint seam, injectable for tests. ok=false
	// means the hint is unavailable: the entry is dropped and re-spawned.
	stat func(path string) (fileStat, bool)
	// capacity bounds the number of memoized (commit, generation) sets.
	capacity int
	// spawns counts production classify invocations (test instrumentation).
	spawns int
}

// NewFreshnessCache returns a run-local cache. The capacity default is 8
// batch-set entries (the C1b spec bound).
func NewFreshnessCache() *FreshnessCache {
	return &FreshnessCache{
		entries:  make(map[string]*list.Element),
		order:    list.New(),
		capacity: 8,
		classify: defaultClassify,
		stat:     defaultStat,
	}
}

// defaultClassify is the production spawn seam: one porcelain changed-path
// diff. Errors propagate; the cache stores nothing on error, so the next
// invocation retries the real check (the C0 behavior).
func defaultClassify(ctx context.Context, repoRoot, commit string) (map[string]bool, error) {
	return ChangedPaths(ctx, repoRoot, commit, nil)
}

// defaultStat reads the invalidation hint for one anchor path. ok=false is an
// invalidation signal, never a truth signal: the caller drops the entry and
// re-spawns the real diff (fail open to the real check, never to "fresh").
func defaultStat(path string) (fileStat, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStat{}, false
	}
	return fileStat{
		mtimeUnixNano: info.ModTime().UnixNano(),
		size:          info.Size(),
		mode:          info.Mode(),
	}, true
}

// cacheKey is the memo key. The report (section 7) makes the generation part
// of the key: the same commit can legitimately yield a different changed set
// after Splice itself mutates the working tree.
func cacheKey(commit string, gen WorktreeGeneration) string {
	return commit + "\x00" + uint64ToKey(gen)
}

func uint64ToKey(gen WorktreeGeneration) string {
	buf := [8]byte{}
	for i := range buf {
		buf[i] = byte(gen >> (8 * (7 - i)))
	}
	return string(buf[:])
}

// BumpGeneration advances the worktree generation. Callers invoke it when
// Splice performs or permits a repository mutation (writer stage output
// applied, patch applied, format run, repair re-entry, write-capable tool
// execution). The next Classify for any commit re-spawns the exact batch.
func (c *FreshnessCache) BumpGeneration() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
}

// Generation reports the current generation (test support).
func (c *FreshnessCache) Generation() WorktreeGeneration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// Reset drops every cached set and rewinds the generation. It is the per-run
// lifecycle seam: a cache must never leak across runs.
func (c *FreshnessCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element)
	c.order.Init()
	c.gen = 0
}

// Classify returns the freshness of anchorPath at sourceCommit for the
// current generation, memoizing the batch spawn. On a cache miss it runs the
// exact porcelain diff once for the (commit, generation) pair; on a hit it
// classifies in-process. The per-anchor stat guard is an invalidation hint:
// a changed hint (or a stat failure) drops the WHOLE entry and re-spawns.
func (c *FreshnessCache) Classify(ctx context.Context, repoRoot, commit, anchorPath string) FreshnessState {
	if repoRoot == "" || commit == "" || anchorPath == "" {
		// Fail closed, identical to the direct path's empty-input rule.
		return FreshnessUnknown
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Lazy seam initialization: a zero-value cache is usable (the spec's
	// D4 "zero-value cache works"); nil seams fall back to the production
	// implementations here rather than only in the constructor.
	if c.classify == nil {
		c.classify = defaultClassify
	}
	if c.stat == nil {
		c.stat = defaultStat
	}
	if c.capacity <= 0 {
		c.capacity = 8
	}
	if c.order == nil {
		c.order = list.New()
	}
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}

	key := cacheKey(commit, c.gen)
	elem, ok := c.entries[key]
	if !ok {
		changed, err := c.classify(ctx, repoRoot, commit)
		if err != nil {
			return FreshnessUnknown
		}
		c.spawns++
		elem = c.order.PushFront(&cacheEntry{
			commit:     commit,
			changed:    changed,
			generation: c.gen,
			stats:      make(map[string]fileStat),
		})
		c.entries[key] = elem
		c.evictLocked()
	}
	entry := elem.Value.(*cacheEntry)

	// Per-anchor invalidation hint. Only anchors this entry has already
	// classified carry a snapshot; a NEW anchor is stat'd once and
	// remembered. Directory anchors are not stat'd: a directory mtime is
	// not a reliable mutation signal, and the next Splice mutation bumps
	// the generation anyway, re-proving everything exactly.
	if isDirAnchor(anchorPath) {
		c.order.MoveToFront(elem)
		return ClassifyBatch(anchorPath, entry.changed)
	}
	abs := absAnchorPath(repoRoot, anchorPath)
	if previous, seen := entry.stats[abs]; seen {
		now, ok := c.stat(abs)
		if !ok || now != previous {
			return c.dropAndRespawnLocked(ctx, repoRoot, commit, key, elem, anchorPath)
		}
	} else {
		if _, ok := c.stat(abs); !ok {
			return c.dropAndRespawnLocked(ctx, repoRoot, commit, key, elem, anchorPath)
		}
		entry.stats[abs] = mustStatOrZero(c.stat, abs)
	}
	c.order.MoveToFront(elem)
	return ClassifyBatch(anchorPath, entry.changed)
}

// dropAndRespawnLocked drops the entry whose invalidation hint changed and
// re-runs the exact diff for the same (commit, generation), then classifies
// the anchor against the fresh set.
func (c *FreshnessCache) dropAndRespawnLocked(ctx context.Context, repoRoot, commit, key string, elem *list.Element, anchorPath string) FreshnessState {
	delete(c.entries, key)
	c.order.Remove(elem)
	changed, err := c.classify(ctx, repoRoot, commit)
	if err != nil {
		return FreshnessUnknown
	}
	c.spawns++
	newElem := c.order.PushFront(&cacheEntry{
		commit:     commit,
		changed:    changed,
		generation: c.gen,
		stats:      make(map[string]fileStat),
	})
	c.entries[key] = newElem
	c.evictLocked()
	return ClassifyBatch(anchorPath, changed)
}

// isDirAnchor reports whether the anchor is a package-directory anchor. The
// anchor form mirrors AnchorPathForKey: everything without a source-file
// extension in its last segment is treated as a directory prefix anchor.
func isDirAnchor(anchorPath string) bool {
	// Mirror AnchorPathForKey's output forms: file paths end in an extension,
	// package dirs do not. A dot in the last segment alone is not enough
	// (dotfiles); keep it conservative: treat anchors whose last segment has
	// a known-source-style extension as files, everything else as dirs. The
	// exactness test covers both anchor classes end-to-end.
	base := anchorPath
	if i := lastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return !hasSourceExtension(base)
}

// lastIndexByte is a local helper to avoid strings import churn.
func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// hasSourceExtension mirrors the source-extension set in keys.go.
func hasSourceExtension(base string) bool {
	dot := lastIndexByte(base, '.')
	if dot < 0 {
		return false
	}
	ext := base[dot+1:]
	switch ext {
	case "go", "py", "ts", "js", "rs", "java", "c", "cc", "cpp", "h", "hpp",
		"sh", "md", "json", "yaml", "yml", "toml":
		return true
	}
	return false
}

// absAnchorPath joins the repo root and the anchor the way the run stores
// paths (repo-relative anchors, absolute stat targets).
func absAnchorPath(repoRoot, anchorPath string) string {
	return repoRoot + string(os.PathSeparator) + anchorPath
}

// mustStatOrZero takes the hint snapshot for a newly seen anchor. The ok
// path was already checked by the caller; on the not-ok path the entry was
// already dropped, so this only records the snapshot.
func mustStatOrZero(stat func(string) (fileStat, bool), abs string) fileStat {
	s, ok := stat(abs)
	if !ok {
		return fileStat{}
	}
	return s
}

// evictLocked enforces the bounded cache: oldest batch-set entry goes first.
func (c *FreshnessCache) evictLocked() {
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			return
		}
		entry := oldest.Value.(*cacheEntry)
		delete(c.entries, cacheKey(entry.commit, entry.generation))
		c.order.Remove(oldest)
	}
}

// SpawnCount reports how many exact diff spawns the cache ran (test support).
func (c *FreshnessCache) SpawnCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spawns
}
