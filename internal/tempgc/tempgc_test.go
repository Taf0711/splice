package tempgc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// hexName returns a valid 64 character cache name made of one hex digit.
func hexName(c byte) string {
	return strings.Repeat(string(c), 64)
}

func writeSized(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

// makeCache creates a Go cache directory with one file of size bytes and a
// marker last used at used. The directory mtime is also set to used.
func makeCache(t *testing.T, root, name string, used time.Time, size int) string {
	t.Helper()
	dir := filepath.Join(root, "splice", "gocache", name)
	writeSized(t, filepath.Join(dir, "obj"), size)
	marker := filepath.Join(dir, markerName)
	writeSized(t, marker, 0)
	if err := os.Chtimes(marker, used, used); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, used, used); err != nil {
		t.Fatal(err)
	}
	return dir
}

// makeRunDir creates a run directory with its mtime set to age before testNow.
func makeRunDir(t *testing.T, root, name string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, name)
	writeSized(t, filepath.Join(dir, "file"), 10)
	when := testNow.Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatal(err)
	return false
}

func TestCollectRemovesCacheOlderThanSevenDays(t *testing.T) {
	root := t.TempDir()
	old := makeCache(t, root, hexName('a'), testNow.Add(-8*day), 10)
	recent := makeCache(t, root, hexName('b'), testNow.Add(-2*day), 10)

	rep, err := Collect(Options{Root: root, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if exists(t, old) {
		t.Fatal("cache older than 7 days was kept")
	}
	if !exists(t, recent) {
		t.Fatal("cache used 2 days ago was removed")
	}
	if rep.CachesRemoved != 1 || rep.CachesKept != 1 || rep.CacheBytesFreed != 10 || rep.CacheBytesKept != 10 {
		t.Fatalf("unexpected report: %+v", rep)
	}
}

func TestCollectMarkerMTimeWins(t *testing.T) {
	root := t.TempDir()
	// Directory is old, but the marker is recent: keep.
	kept := makeCache(t, root, hexName('a'), testNow.Add(-10*day), 1)
	marker := filepath.Join(kept, markerName)
	if err := os.Chtimes(marker, testNow.Add(-1*day), testNow.Add(-1*day)); err != nil {
		t.Fatal(err)
	}
	// Directory is recent, but the marker is old: remove.
	removed := makeCache(t, root, hexName('b'), testNow.Add(-1*day), 1)
	if err := os.Chtimes(filepath.Join(removed, markerName), testNow.Add(-10*day), testNow.Add(-10*day)); err != nil {
		t.Fatal(err)
	}

	if _, err := Collect(Options{Root: root, Now: testNow}); err != nil {
		t.Fatal(err)
	}
	if !exists(t, kept) {
		t.Fatal("cache with a recent marker was removed")
	}
	if exists(t, removed) {
		t.Fatal("cache with an old marker was kept")
	}
}

func TestCollectCapRemovesLeastRecentlyUsedFirst(t *testing.T) {
	root := t.TempDir()
	newest := makeCache(t, root, hexName('a'), testNow.Add(-2*time.Hour), 100)
	middle := makeCache(t, root, hexName('b'), testNow.Add(-3*time.Hour), 100)
	oldest := makeCache(t, root, hexName('c'), testNow.Add(-4*time.Hour), 100)

	rep, err := Collect(Options{Root: root, Now: testNow, MaxCacheBytes: 150})
	if err != nil {
		t.Fatal(err)
	}
	if exists(t, oldest) || exists(t, middle) {
		t.Fatal("least recently used caches were kept over the cap")
	}
	if !exists(t, newest) {
		t.Fatal("most recent cache was removed although the total fits the cap")
	}
	if rep.CachesRemoved != 2 || rep.CachesKept != 1 || rep.CacheBytesKept != 100 {
		t.Fatalf("unexpected report: %+v", rep)
	}
}

func TestCollectCapKeepsCacheUsedWithinMinIdle(t *testing.T) {
	root := t.TempDir()
	recent := makeCache(t, root, hexName('a'), testNow.Add(-10*time.Minute), 100)
	idle := makeCache(t, root, hexName('b'), testNow.Add(-2*time.Hour), 100)

	rep, err := Collect(Options{Root: root, Now: testNow, MaxCacheBytes: 50})
	if err != nil {
		t.Fatal(err)
	}
	if exists(t, idle) {
		t.Fatal("idle cache was kept over the cap")
	}
	if !exists(t, recent) {
		t.Fatal("cache used 10 minutes ago was removed while the cap was exceeded")
	}
	if rep.CacheBytesKept != 100 {
		t.Fatalf("expected 100 bytes kept, got %+v", rep)
	}
}

func TestCollectIgnoresForeignEntriesInGoCache(t *testing.T) {
	root := t.TempDir()
	gocache := filepath.Join(root, "splice", "gocache")

	notHex := filepath.Join(gocache, "not-a-hex-name")
	writeSized(t, filepath.Join(notHex, "obj"), 10)
	if err := os.Chtimes(notHex, testNow.Add(-30*day), testNow.Add(-30*day)); err != nil {
		t.Fatal(err)
	}

	upper := filepath.Join(gocache, strings.Repeat("A", 64))
	writeSized(t, filepath.Join(upper, "obj"), 10)
	if err := os.Chtimes(upper, testNow.Add(-30*day), testNow.Add(-30*day)); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "link-target")
	writeSized(t, filepath.Join(target, "obj"), 10)
	link := filepath.Join(gocache, hexName('c'))
	if err := os.MkdirAll(gocache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Collect(Options{Root: root, Now: testNow}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{notHex, upper, link} {
		if !exists(t, path) {
			t.Fatalf("%s was removed", path)
		}
	}
	if !exists(t, filepath.Join(target, "obj")) {
		t.Fatal("symlink target was removed")
	}
}

func TestCollectStaleRunDirectories(t *testing.T) {
	root := t.TempDir()
	staleReport := makeRunDir(t, root, "splice-test-report-x", 2*day)
	freshReport := makeRunDir(t, root, "splice-test-report-y", time.Hour)
	staleGit := makeRunDir(t, root, "splice-git-1", 2*day)
	toolOutput := makeRunDir(t, root, "splice-tool-output-501", 3*day)
	baseline := makeRunDir(t, root, "splice-baseline", 3*day)
	other := makeRunDir(t, root, "other-dir", 3*day)

	rep, err := Collect(Options{Root: root, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if exists(t, staleReport) || exists(t, staleGit) {
		t.Fatal("stale run directory was kept")
	}
	for _, path := range []string{freshReport, toolOutput, baseline, other} {
		if !exists(t, path) {
			t.Fatalf("%s was removed", path)
		}
	}
	if rep.StaleDirsRemoved != 2 || rep.StaleBytesFreed != 20 {
		t.Fatalf("unexpected report: %+v", rep)
	}
}

func TestCollectDryRunReportsSameCountsAndDeletesNothing(t *testing.T) {
	root := t.TempDir()
	makeCache(t, root, hexName('a'), testNow.Add(-8*day), 100)
	makeCache(t, root, hexName('b'), testNow.Add(-2*time.Hour), 100)
	makeCache(t, root, hexName('c'), testNow.Add(-3*time.Hour), 100)
	stale := makeRunDir(t, root, "splice-cli-test-sessions-1", 2*day)

	dry, err := Collect(Options{Root: root, Now: testNow, DryRun: true, MaxCacheBytes: 150})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(t, stale) || !exists(t, filepath.Join(root, "splice", "gocache", hexName('a'))) {
		t.Fatal("dry run deleted files")
	}

	real, err := Collect(Options{Root: root, Now: testNow, MaxCacheBytes: 150})
	if err != nil {
		t.Fatal(err)
	}
	if dry.CachesRemoved != real.CachesRemoved || dry.CachesKept != real.CachesKept ||
		dry.CacheBytesFreed != real.CacheBytesFreed || dry.CacheBytesKept != real.CacheBytesKept ||
		dry.StaleDirsRemoved != real.StaleDirsRemoved || dry.StaleBytesFreed != real.StaleBytesFreed {
		t.Fatalf("dry run report %+v differs from real run %+v", dry, real)
	}
}

func TestCollectMissingRootReturnsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if _, err := Collect(Options{Root: missing, Now: testNow}); err == nil {
		t.Fatal("expected an error for a missing temp root")
	}
}

func TestCollectEmptyRootReportsNothing(t *testing.T) {
	rep, err := Collect(Options{Root: t.TempDir(), Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CachesRemoved != 0 || rep.CachesKept != 0 || rep.StaleDirsRemoved != 0 || len(rep.Errors) != 0 {
		t.Fatalf("unexpected report for empty root: %+v", rep)
	}
}

func TestTouchCacheMarksDirectoryUsed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "splice", "gocache", hexName('a'))
	writeSized(t, filepath.Join(dir, "obj"), 1)
	if err := os.Chtimes(dir, testNow.Add(-10*day), testNow.Add(-10*day)); err != nil {
		t.Fatal(err)
	}

	TouchCache(dir)

	info, err := os.Stat(filepath.Join(dir, markerName))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("marker mtime %v was not set to now", info.ModTime())
	}
}

func TestMaybeCollectAsyncReturnsUnderTests(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	start := time.Now()
	MaybeCollectAsync()
	if time.Since(start) > 5*time.Second {
		t.Fatal("MaybeCollectAsync blocked under tests")
	}
	if _, err := os.Stat(filepath.Join(tmp, "splice")); !os.IsNotExist(err) {
		t.Fatalf("MaybeCollectAsync created the splice directory under tests: %v", err)
	}
}
