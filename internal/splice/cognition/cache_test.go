package cognition

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeStat is a controllable invalidation-hint seam.
type fakeStat struct {
	mu    sync.Mutex
	state map[string]fileStat
	// fail marks paths whose stat always fails (hint unavailable).
	fail map[string]bool
}

func newFakeStat() *fakeStat {
	return &fakeStat{state: make(map[string]fileStat), fail: make(map[string]bool)}
}

func (f *fakeStat) snapshot(path string) (fileStat, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[path] {
		return fileStat{}, false
	}
	return f.state[path], true
}

func (f *fakeStat) touch(path string) fileStat {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := fileStat{mtimeUnixNano: f.state[path].mtimeUnixNano + 1, size: f.state[path].size + 1}
	f.state[path] = next
	return next
}

func (f *fakeStat) setFail(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[path] = true
}

// fakeClassify is a controllable spawn seam returning a canned changed set
// and counting spawns.
type fakeClassify struct {
	mu      sync.Mutex
	sets    map[string]map[string]bool // commit -> changed set
	errOn   map[string]bool            // commit -> spawn error
	spawns  int
	failNow bool
}

func newFakeClassify() *fakeClassify {
	return &fakeClassify{sets: make(map[string]map[string]bool), errOn: make(map[string]bool)}
}

func (f *fakeClassify) classify(_ context.Context, _, commit string) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawns++
	if f.failNow || f.errOn[commit] {
		return nil, errors.New("spawn failed")
	}
	return f.sets[commit], nil
}

func (f *fakeClassify) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spawns
}

func newTestCache(classify *fakeClassify, stat *fakeStat) *FreshnessCache {
	cache := NewFreshnessCache()
	cache.classify = classify.classify
	cache.stat = stat.snapshot
	return cache
}

// TestCacheFirstInvocationSpawnsOnceSecondSpawnsZero pins the memoization
// contract: the first classify for a (commit, generation) runs the exact diff
// once; later invocations for the same pair classify in-process.
func TestCacheFirstInvocationSpawnsOnceSecondSpawnsZero(t *testing.T) {
	classify := newFakeClassify()
	classify.sets["c1"] = map[string]bool{"edited.go": true}
	stat := newFakeStat()
	stat.state["/repo/edited.go"] = fileStat{mtimeUnixNano: 100, size: 10}
	cache := newTestCache(classify, stat)

	if got := cache.Classify(context.Background(), "/repo", "c1", "edited.go"); got != FreshnessStale {
		t.Fatalf("first = %q, want stale", got)
	}
	if got := cache.SpawnCount(); got != 1 {
		t.Fatalf("spawn count after first = %d, want 1", got)
	}
	// Same anchor again: zero new spawns, same verdict.
	if got := cache.Classify(context.Background(), "/repo", "c1", "edited.go"); got != FreshnessStale {
		t.Fatalf("second = %q, want stale", got)
	}
	if got := cache.SpawnCount(); got != 1 {
		t.Fatalf("spawn count after re-entry = %d, want 1", got)
	}
	// A different anchor against the same memoized set: still zero spawns.
	stat.state["/repo/clean.go"] = fileStat{mtimeUnixNano: 100, size: 4}
	if got := cache.Classify(context.Background(), "/repo", "c1", "clean.go"); got != FreshnessFresh {
		t.Fatalf("new anchor = %q, want fresh", got)
	}
	if got := cache.SpawnCount(); got != 1 {
		t.Fatalf("spawn count after new anchor = %d, want 1", got)
	}
}

// TestCacheGenerationBumpReSpawns pins the generation semantics: a
// Splice-permitted mutation bumps the generation, and the next classify for
// the same commit re-spawns the exact diff (the memoized set from the old
// generation must never be reused).
func TestCacheGenerationBumpReSpawns(t *testing.T) {
	classify := newFakeClassify()
	classify.sets["c1"] = map[string]bool{"a.go": true}
	stat := newFakeStat()
	stat.state["/repo/a.go"] = fileStat{mtimeUnixNano: 1, size: 1}
	cache := newTestCache(classify, stat)

	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessStale {
		t.Fatalf("pre-bump = %q, want stale", got)
	}
	cache.BumpGeneration()
	if cache.Generation() != 1 {
		t.Fatalf("generation = %d, want 1", cache.Generation())
	}
	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessStale {
		t.Fatalf("post-bump = %q, want stale", got)
	}
	if got := cache.SpawnCount(); got != 2 {
		t.Fatalf("spawn count after bump = %d, want 2 (exact re-proof)", got)
	}
}

// TestCacheStatHintChangeDropsEntry pins the invalidation-hint rule: a
// changed mtime+size hint on a classified anchor means the working tree moved
// outside Splice's knowledge, so the whole entry is dropped and the exact
// diff re-spawned. The hint itself never produces a verdict.
func TestCacheStatHintChangeDropsEntry(t *testing.T) {
	classify := newFakeClassify()
	classify.sets["c1"] = map[string]bool{"a.go": true}
	stat := newFakeStat()
	stat.state["/repo/a.go"] = fileStat{mtimeUnixNano: 1, size: 1}
	cache := newTestCache(classify, stat)

	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessStale {
		t.Fatalf("pre-hint = %q, want stale", got)
	}
	// The file's metadata changed externally (same content or not is
	// unknowable): drop and re-prove with a real spawn.
	stat.touch("/repo/a.go")
	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessStale {
		t.Fatalf("post-hint = %q, want stale (re-proven)", got)
	}
	if got := cache.SpawnCount(); got != 2 {
		t.Fatalf("spawn count after hint change = %d, want 2", got)
	}
}

// TestCacheStatFailureDropsEntry pins fail-open-to-the-real-check: a stat
// failure never returns a cached verdict; the entry is dropped and the
// exact diff re-spawned.
func TestCacheStatFailureDropsEntry(t *testing.T) {
	classify := newFakeClassify()
	classify.sets["c1"] = map[string]bool{"a.go": true}
	stat := newFakeStat()
	stat.state["/repo/a.go"] = fileStat{mtimeUnixNano: 1, size: 1}
	cache := newTestCache(classify, stat)

	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessStale {
		t.Fatalf("pre-fail = %q, want stale", got)
	}
	stat.setFail("/repo/a.go")
	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessStale {
		t.Fatalf("post-fail = %q, want stale (re-proven)", got)
	}
	if got := cache.SpawnCount(); got != 2 {
		t.Fatalf("spawn count after stat failure = %d, want 2", got)
	}
}

// TestCacheSpawnErrorFailsClosedAndRetries pins the spawn-error path: an
// error from the exact diff is unknown (never a verdict), nothing is cached,
// and the next invocation retries the real check.
func TestCacheSpawnErrorFailsClosedAndRetries(t *testing.T) {
	classify := newFakeClassify()
	classify.sets["c1"] = map[string]bool{}
	classify.errOn["c1"] = true
	stat := newFakeStat()
	stat.state["/repo/a.go"] = fileStat{mtimeUnixNano: 1, size: 1}
	cache := newTestCache(classify, stat)

	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessUnknown {
		t.Fatalf("error path = %q, want unknown", got)
	}
	// The spawn happened (the diff ran and failed), but nothing was memoized:
	// the cache reports zero stored sets.
	if got := classify.count(); got != 1 {
		t.Fatalf("git spawn count = %d, want 1", got)
	}
	if got := cache.SpawnCount(); got != 0 {
		t.Fatalf("stored sets after error = %d, want 0", got)
	}
	// Nothing cached: clear the error and retry, which spawns again and
	// succeeds.
	classify.mu.Lock()
	classify.errOn["c1"] = false
	classify.mu.Unlock()
	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessFresh {
		t.Fatalf("retry = %q, want fresh", got)
	}
	if got := classify.count(); got != 2 {
		t.Fatalf("retry git spawn count = %d, want 2", got)
	}
}

// TestCacheEmptyInputsFailClosed pins the empty-input rule at the cache door.
func TestCacheEmptyInputsFailClosed(t *testing.T) {
	cache := NewFreshnessCache()
	classify := newFakeClassify()
	cache.classify = classify.classify
	cache.stat = newFakeStat().snapshot
	for _, tc := range []struct{ root, commit, anchor string }{
		{"", "c1", "a.go"},
		{"/repo", "", "a.go"},
		{"/repo", "c1", ""},
	} {
		if got := cache.Classify(context.Background(), tc.root, tc.commit, tc.anchor); got != FreshnessUnknown {
			t.Fatalf("empty input %+v = %q, want unknown", tc, got)
		}
	}
	if cache.SpawnCount() != 0 {
		t.Fatalf("empty inputs must not spawn, spawns = %d", cache.SpawnCount())
	}
}

// TestCacheZeroValueWorks pins that the zero-value cache is usable: callers
// constructing FreshnessCache{} directly still get default seams.
func TestCacheZeroValueWorks(t *testing.T) {
	root, commit := newBatchRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	cache := &FreshnessCache{}
	if got := cache.Classify(context.Background(), root, commit, "internal/auth/session.go"); got != FreshnessFresh {
		t.Fatalf("zero-value cache = %q, want fresh", got)
	}
}

// TestCacheEvictionAtEight pins the bounded cache: the ninth distinct
// (commit, generation) entry evicts the oldest.
func TestCacheEvictionAtEight(t *testing.T) {
	classify := newFakeClassify()
	stat := newFakeStat()
	for i := 0; i < 10; i++ {
		commit := string(rune('a' + i))
		classify.sets[commit] = map[string]bool{}
		stat.state["/repo/a.go"] = fileStat{mtimeUnixNano: int64(1000 + i), size: int64(i)}
	}
	cache := newTestCache(classify, stat)
	// Ten distinct commits for the same anchor and generation.
	for i := 0; i < 10; i++ {
		commit := string(rune('a' + i))
		// Each commit needs a distinct stat snapshot so re-entry does not
		// drop entries: use distinct anchors to keep the hint bookkeeping
		// per-entry.
		anchor := "a.go"
		if got := cache.Classify(context.Background(), "/repo", commit, anchor); got != FreshnessFresh {
			t.Fatalf("commit %s = %q, want fresh", commit, got)
		}
	}
	if got := cache.SpawnCount(); got != 10 {
		t.Fatalf("spawn count = %d, want 10", got)
	}
	// The oldest entry (commit 'a') was evicted: classifying it again
	// re-spawns. The newest ('j') is still memoized: zero new spawn.
	if got := cache.Classify(context.Background(), "/repo", "a", "a.go"); got != FreshnessFresh {
		t.Fatalf("evicted re-entry = %q, want fresh", got)
	}
	if got := cache.SpawnCount(); got != 11 {
		t.Fatalf("oldest entry not evicted: spawns = %d, want 11", got)
	}
	if got := cache.Classify(context.Background(), "/repo", "j", "a.go"); got != FreshnessFresh {
		t.Fatalf("recent re-entry = %q, want fresh", got)
	}
	if got := cache.SpawnCount(); got != 11 {
		t.Fatalf("recent entry was evicted: spawns = %d, want 11", got)
	}
}

// TestCacheResetDropsEverything pins the per-run lifecycle seam.
func TestCacheResetDropsEverything(t *testing.T) {
	classify := newFakeClassify()
	classify.sets["c1"] = map[string]bool{}
	stat := newFakeStat()
	stat.state["/repo/a.go"] = fileStat{mtimeUnixNano: 1, size: 1}
	cache := newTestCache(classify, stat)

	cache.Classify(context.Background(), "/repo", "c1", "a.go")
	cache.BumpGeneration()
	cache.Reset()
	if cache.Generation() != 0 {
		t.Fatalf("generation after reset = %d, want 0", cache.Generation())
	}
	if got := cache.Classify(context.Background(), "/repo", "c1", "a.go"); got != FreshnessFresh {
		t.Fatalf("post-reset = %q, want fresh", got)
	}
	if got := cache.SpawnCount(); got != 2 {
		t.Fatalf("reset must drop memoized sets: spawns = %d, want 2", got)
	}
}

// TestCacheDirectoryAnchorSkipsStatGuard pins the directory-anchor rule: a
// package anchor is classified from the memoized set without a per-entry
// stat hint (a directory mtime is not a reliable mutation signal; Splice
// mutations bump the generation, re-proving exactly).
func TestCacheDirectoryAnchorSkipsStatGuard(t *testing.T) {
	classify := newFakeClassify()
	classify.sets["c1"] = map[string]bool{"pkg/inner.go": true}
	stat := newFakeStat()
	cache := newTestCache(classify, stat)

	if got := cache.Classify(context.Background(), "/repo", "c1", "pkg"); got != FreshnessStale {
		t.Fatalf("dir anchor = %q, want stale", got)
	}
	if got := cache.Classify(context.Background(), "/repo", "c1", "pkg"); got != FreshnessStale {
		t.Fatalf("dir anchor re-entry = %q, want stale", got)
	}
	if got := cache.SpawnCount(); got != 1 {
		t.Fatalf("dir anchor spawns = %d, want 1", got)
	}
}

// TestCacheRealGitEndToEnd drives the cache over a REAL git repo with the
// production classify seam: fresh verdicts, a mid-run external edit caught
// by the hint, and a generation bump re-proving after a Splice mutation.
func TestCacheRealGitEndToEnd(t *testing.T) {
	root, commit := newBatchRepo(t, map[string]string{
		"internal/auth/session.go": "package auth\n",
	})
	cache := NewFreshnessCache()
	anchor := "internal/auth/session.go"

	// Fresh: file matches the source commit.
	if got := cache.Classify(context.Background(), root, commit, anchor); got != FreshnessFresh {
		t.Fatalf("fresh = %q, want fresh", got)
	}
	// Re-entry: memoized, still fresh, zero spawns.
	if got := cache.Classify(context.Background(), root, commit, anchor); got != FreshnessFresh {
		t.Fatalf("re-entry = %q, want fresh", got)
	}
	if got := cache.SpawnCount(); got != 1 {
		t.Fatalf("spawns = %d, want 1", got)
	}

	// External edit (Splice did not do this): the hint catches it and the
	// exact diff re-proves stale.
	if err := os.WriteFile(filepath.Join(root, anchor), []byte("package auth\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cache.Classify(context.Background(), root, commit, anchor); got != FreshnessStale {
		t.Fatalf("after external edit = %q, want stale", got)
	}

	// Splice-permitted mutation: bump the generation, then revert the file.
	// The new generation re-proves with a fresh spawn.
	cache.BumpGeneration()
	if err := os.WriteFile(filepath.Join(root, anchor), []byte("package auth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cache.Classify(context.Background(), root, commit, anchor); got != FreshnessFresh {
		t.Fatalf("after bump+revert = %q, want fresh", got)
	}
	if got := cache.SpawnCount(); got != 3 {
		t.Fatalf("spawns = %d, want 3", got)
	}
}
