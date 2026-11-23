// Package tempgc removes temporary files that Splice leaves behind. It removes
// old per-workspace Go build caches and run directories of killed processes.
// It uses only the standard library, so other Splice packages can import it.
package tempgc

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	cacheMaxAge  = 7 * 24 * time.Hour
	cacheMinIdle = time.Hour
	staleDirAge  = 24 * time.Hour
	gcInterval   = time.Hour

	defaultMaxCacheBytes int64 = 10 << 30
	markerName                 = ".splice-last-used"
	stampName                  = ".gc-stamp"
	lockName                   = ".gc.lock"
	spliceDirName              = "splice"
	goCacheDirName             = "gocache"
	envGC                      = "SPLICE_GC"
	envGoCacheMaxGB            = "SPLICE_GOCACHE_MAX_GB"
)

// staleDirPrefixes names the run directories that Collect may remove.
var staleDirPrefixes = []string{
	"splice-test-report-",
	"splice-git-",
	"splice-cli-test-",
	"splice-test-config-",
}

// Options controls one collection run.
type Options struct {
	Root          string // temp root; "" means os.TempDir()
	Now           time.Time
	DryRun        bool
	MaxCacheBytes int64 // 0 means the default (10 GiB), or SPLICE_GOCACHE_MAX_GB
}

// Report counts what one collection run removed or kept.
type Report struct {
	CachesRemoved, CachesKept       int
	CacheBytesFreed, CacheBytesKept int64
	StaleDirsRemoved                int
	StaleBytesFreed                 int64
	Errors                          []string // one line per path that could not be removed
}

// collecting is true while one background collection runs in this process.
var collecting atomic.Bool

// skipUnderTests makes MaybeCollectAsync a no-op in test binaries. Only the
// package tests clear it.
var skipUnderTests = true

// collectRoot overrides the temp root for background collection. Only the
// package tests set it. "" means os.TempDir().
var collectRoot = ""

func tempRoot() string {
	if collectRoot != "" {
		return collectRoot
	}
	return os.TempDir()
}

// Collect removes stale Go build caches and stale run directories under the
// temp root. It returns an error only when the temp root cannot be read.
func Collect(opts Options) (Report, error) {
	root := opts.Root
	if root == "" {
		root = os.TempDir()
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	maxBytes := opts.MaxCacheBytes
	if maxBytes <= 0 {
		maxBytes = defaultCacheCap()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Report{}, fmt.Errorf("read temp root %q: %w", root, err)
	}
	var rep Report
	collectCaches(filepath.Join(root, spliceDirName, goCacheDirName), now, maxBytes, opts.DryRun, &rep)
	collectStaleDirs(root, entries, now, opts.DryRun, &rep)
	return rep, nil
}

// TouchCache marks a Go cache directory as used now. It ignores errors.
func TouchCache(dir string) {
	touch(filepath.Join(dir, markerName))
}

// MaybeCollectAsync starts one background collection when the throttle allows
// it. It returns at once and never blocks the caller.
func MaybeCollectAsync() {
	if os.Getenv(envGC) == "off" || (skipUnderTests && testing.Testing()) {
		return
	}
	dir := filepath.Join(tempRoot(), spliceDirName)
	if info, err := os.Stat(filepath.Join(dir, stampName)); err == nil && time.Since(info.ModTime()) < gcInterval {
		return
	}
	if !collecting.CompareAndSwap(false, true) {
		return
	}
	go collectInBackground(dir)
}

func collectInBackground(dir string) {
	defer collecting.Store(false)
	defer func() { _ = recover() }()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	lock := filepath.Join(dir, lockName)
	if !acquireLock(lock) {
		return
	}
	defer func() { _ = os.Remove(lock) }()
	touch(filepath.Join(dir, stampName))
	_, _ = Collect(Options{Root: collectRoot})
}

// acquireLock creates the lock file. It removes a lock older than gcInterval
// and retries once. It returns false when another run holds the lock.
func acquireLock(path string) bool {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return true
		}
		if !errors.Is(err, fs.ErrExist) {
			return false
		}
		info, err := os.Lstat(path)
		if err != nil || time.Since(info.ModTime()) <= gcInterval {
			return false
		}
		if err := os.Remove(path); err != nil {
			return false
		}
	}
	return false
}

func touch(path string) {
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// defaultCacheCap returns the cache size cap in bytes. It reads
// SPLICE_GOCACHE_MAX_GB when that value is a positive integer.
func defaultCacheCap() int64 {
	if value := os.Getenv(envGoCacheMaxGB); value != "" {
		gb, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil && gb > 0 && gb <= math.MaxInt64/(1<<30) {
			return gb << 30
		}
	}
	return defaultMaxCacheBytes
}

type cacheEntry struct {
	path    string
	used    time.Time
	size    int64
	removed bool
	failed  bool
}

func (c *cacheEntry) remove(dryRun bool, rep *Report) bool {
	if err := removePath(c.path, dryRun); err != nil {
		c.failed = true
		rep.Errors = append(rep.Errors, err.Error())
		return false
	}
	c.removed = true
	return true
}

func collectCaches(dir string, now time.Time, maxBytes int64, dryRun bool, rep *Report) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			rep.Errors = append(rep.Errors, fmt.Sprintf("read Go cache directory %q: %v", dir, err))
		}
		return
	}
	var caches []*cacheEntry
	for _, e := range entries {
		if !isCacheName(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				rep.Errors = append(rep.Errors, fmt.Sprintf("inspect Go cache %q: %v", path, err))
			}
			continue
		}
		if !info.IsDir() {
			continue
		}
		used := info.ModTime()
		if marker, err := os.Lstat(filepath.Join(path, markerName)); err == nil {
			used = marker.ModTime()
		}
		caches = append(caches, &cacheEntry{path: path, used: used, size: dirSize(path)})
	}
	sort.SliceStable(caches, func(i, j int) bool { return caches[i].used.Before(caches[j].used) })

	// Remove every cache that is older than the maximum age.
	for _, c := range caches {
		if now.Sub(c.used) > cacheMaxAge {
			c.remove(dryRun, rep)
		}
	}
	var total int64
	for _, c := range caches {
		if !c.removed {
			total += c.size
		}
	}
	// Remove the least recently used caches until the total fits the cap.
	// Never remove a cache that was used within cacheMinIdle.
	for _, c := range caches {
		if total <= maxBytes {
			break
		}
		if c.removed || c.failed || now.Sub(c.used) <= cacheMinIdle {
			continue
		}
		if c.remove(dryRun, rep) {
			total -= c.size
		}
	}
	for _, c := range caches {
		if c.removed {
			rep.CachesRemoved++
			rep.CacheBytesFreed += c.size
		} else {
			rep.CachesKept++
			rep.CacheBytesKept += c.size
		}
	}
}

func collectStaleDirs(root string, entries []fs.DirEntry, now time.Time, dryRun bool, rep *Report) {
	for _, e := range entries {
		name := e.Name()
		if !hasStalePrefix(name) {
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				rep.Errors = append(rep.Errors, fmt.Sprintf("inspect run directory %q: %v", path, err))
			}
			continue
		}
		if !info.IsDir() || now.Sub(info.ModTime()) <= staleDirAge || !ownedByCurrentUser(info) {
			continue
		}
		size := dirSize(path)
		if err := removePath(path, dryRun); err != nil {
			rep.Errors = append(rep.Errors, err.Error())
			continue
		}
		rep.StaleDirsRemoved++
		rep.StaleBytesFreed += size
	}
}

func hasStalePrefix(name string) bool {
	for _, prefix := range staleDirPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// isCacheName reports whether name is a 64 character lowercase hex string.
func isCacheName(name string) bool {
	if len(name) != 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// dirSize sums the sizes of regular files under root. It never follows
// symbolic links.
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func removePath(path string, dryRun bool) error {
	if dryRun {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %q: %w", path, err)
	}
	return nil
}
