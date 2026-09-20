package splice

// Package F4 tests (warm-cost handoff Section 10): lookup memoization by
// content version. The same stage need within a run must not re-hash
// unchanged sources; new evidence (a recorded mutation) invalidates the
// cached digest so admission re-proves freshness.

import (
	"os"
	"path/filepath"
	"testing"
)

func f4WriteTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

// The memo returns the same digest as the uncached path for unchanged bytes.
func TestDigestMemoMatchesUncached(t *testing.T) {
	dir := f4WriteTemp(t, "a.go", "package main")
	uncached, err := currentFileDigestUncached(dir, "a.go")
	if err != nil {
		t.Fatalf("uncached hash: %v", err)
	}
	memo := newDigestMemo()
	if digest, err := memoHash(memo, dir, "a.go"); err != nil || digest != uncached {
		t.Fatalf("memoized digest mismatch: %q vs %q (err %v)", digest, uncached, err)
	}
}

// Same content version: the second lookup does not re-read the file. The
// file is deleted underneath the memo; a cached hit survives, proving no
// re-hash happened.
func TestDigestMemoSkipsRehashWithoutNewEvidence(t *testing.T) {
	dir := f4WriteTemp(t, "a.go", "package main")
	memo := newDigestMemo()
	if _, err := memoHash(memo, dir, "a.go"); err != nil {
		t.Fatalf("first hash: %v", err)
	}
	// Simulate time passing without mutation evidence: remove the file. A
	// re-hash would fail; the memo hit must return the cached digest.
	path := filepath.Join(dir, "a.go")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	digest, err := memoHash(memo, dir, "a.go")
	if err != nil {
		t.Fatalf("memo hit must not re-hash: %v", err)
	}
	want, _ := currentFileDigestUncached(f4WriteTemp(t, "a.go", "package main"), "a.go")
	if digest != want {
		t.Fatalf("cached digest changed: %q vs %q", digest, want)
	}
}

// New evidence invalidates: a recorded mutation of the file forces the next
// lookup to re-hash the current bytes.
func TestDigestMemoInvalidatesOnMutation(t *testing.T) {
	dir := f4WriteTemp(t, "a.go", "package one")
	memo := newDigestMemo()
	first, err := memoHash(memo, dir, "a.go")
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	// Mutate the file (new evidence: the writer recorded a change).
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package two"), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	// Without invalidation the memo would serve stale bytes; the contract is
	// that the pipeline records the mutation, so invalidate.
	memo.Invalidate(dir, "a.go")
	second, err := memoHash(memo, dir, "a.go")
	if err != nil {
		t.Fatalf("rehash after invalidation: %v", err)
	}
	if first == second {
		t.Fatalf("mutation must change the digest: %q", first)
	}
	// The memo now serves the new version for subsequent lookups.
	third, err := memoHash(memo, dir, "a.go")
	if err != nil || third != second {
		t.Fatalf("post-invalidation memo hit mismatch: %q vs %q (err %v)", third, second, err)
	}
}

// InvalidateAll (broad write set) drops every entry.
func TestDigestMemoInvalidateAll(t *testing.T) {
	dirA := f4WriteTemp(t, "a.go", "package a")
	dirB := f4WriteTemp(t, "b.go", "package b")
	memo := newDigestMemo()
	da, err := memoHash(memo, dirA, "a.go")
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	db, err := memoHash(memo, dirB, "b.go")
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	memo.InvalidateAll()
	if err := os.WriteFile(filepath.Join(dirA, "a.go"), []byte("package a2"), 0o644); err != nil {
		t.Fatalf("mutate a: %v", err)
	}
	na, err := memoHash(memo, dirA, "a.go")
	if err != nil || na == da {
		t.Fatalf("InvalidateAll must force a re-hash: %q vs %q (err %v)", na, da, err)
	}
	nb, err := memoHash(memo, dirB, "b.go")
	if err != nil || nb != db {
		t.Fatalf("unchanged file re-hashes to the same digest: %q vs %q (err %v)", nb, db, err)
	}
}

// Missing files stay errors through the memo (unavailable fails closed).
func TestDigestMemoPreservesErrors(t *testing.T) {
	dir := t.TempDir()
	memo := newDigestMemo()
	if _, err := memoHash(memo, dir, "missing.go"); err == nil {
		t.Fatalf("missing file must stay an error")
	}
	// The error is memoized at the same generation (no re-read), but an
	// invalidation re-hashes and the error persists correctly.
	memo.Invalidate(dir, "missing.go")
	if _, err := memoHash(memo, dir, "missing.go"); err == nil {
		t.Fatalf("missing file error must persist after invalidation")
	}
}

// The run-scoped seam: with no memo installed, behavior is identical to the
// uncached path; with one installed, results match and stay consistent.
func TestRunDigestMemoSeam(t *testing.T) {
	dir := f4WriteTemp(t, "a.go", "package seam")
	SetRunDigestMemo(nil)
	plain, err := currentFileDigest(dir, "a.go")
	if err != nil {
		t.Fatalf("plain digest: %v", err)
	}
	memo := newDigestMemo()
	SetRunDigestMemo(memo)
	defer SetRunDigestMemo(nil)
	cached, err := currentFileDigest(dir, "a.go")
	if err != nil || cached != plain {
		t.Fatalf("memoized digest differs from plain: %q vs %q (err %v)", cached, plain, err)
	}
}

// memoHash hashes through the memo, storing on miss.
func memoHash(m *digestMemo, workspace, relPath string) (string, error) {
	if digest, ok, err := m.lookup(workspace, relPath); ok {
		return digest, err
	}
	digest, err := currentFileDigestUncached(workspace, relPath)
	m.store(workspace, relPath, digest, err)
	return digest, err
}
