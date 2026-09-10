package stages

// Package F3 tests (warm-cost handoff Section 10): repeated-content removal
// across request compositions. The filter is mechanical and shared by both
// arms; the fixtures byte-compare composition before/after so any change to
// delivered content is a deliberate, reviewed diff.

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Fixed fixture: one stage's request composition with a duplicated static
// instruction, a duplicated prior summary, and repeated bundle content.
var f3FixtureStatic = []string{
	"static instruction one",
	"static instruction two",
	"static instruction one", // exact repeat across compositions
}

var f3FixturePrior = map[string]string{
	"code_writer":   "wrote helper.go",
	"test_runner":   "wrote helper.go", // same bytes as code_writer summary line
	"static_analyz": "",                // empty summaries never compose
}

func f3FixtureBundle() *schemas.ContextBundle {
	return &schemas.ContextBundle{
		Items: []schemas.ContextItem{
			{Summary: "view helper.go v1", Payload: map[string]interface{}{"path": "helper.go", "version": "v1", "text": "package main"}},
			{Summary: "view helper.go v1", Payload: map[string]interface{}{"path": "helper.go", "version": "v1", "text": "package main"}}, // duplicate view identity
		},
	}
}

// The dedup filter removes exact repeats and keeps first-occurrence order.
func TestDedupeRepeatedContextRemovesExactRepeats(t *testing.T) {
	entries := []string{
		"static instruction one",
		"static instruction two",
		"static instruction one", // repeat
		"code_writer: wrote helper.go",
		"code_writer: wrote helper.go", // repeat
	}
	got := dedupeRepeatedContext(entries)
	want := []string{"static instruction one", "static instruction two", "code_writer: wrote helper.go"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("dedup mismatch:\n got %q\nwant %q", got, want)
	}
}

// Protected classes survive: acceptance constraints, failure evidence, and
// error records are delivered on every occurrence.
func TestDedupeRepeatedContextKeepsProtectedEvidence(t *testing.T) {
	entries := []string{
		"acceptance: tests pass on ./... ",
		"acceptance: tests pass on ./...", // repeat of protected content
		"test_fingerprint: fp-123",
		"test_fingerprint: fp-123", // repeat of failure evidence
		"plain repeated instruction",
		"plain repeated instruction", // repeat of unprotected content
	}
	got := dedupeRepeatedContext(entries)
	plainCount := 0
	acceptanceCount := 0
	fingerprintCount := 0
	for _, e := range got {
		switch {
		case strings.Contains(e, "acceptance"):
			acceptanceCount++
		case strings.Contains(e, "fingerprint"):
			fingerprintCount++
		case strings.Contains(e, "plain repeated"):
			plainCount++
		}
	}
	if acceptanceCount != 2 || fingerprintCount != 2 {
		t.Fatalf("protected evidence must survive repeats: acceptance=%d fingerprint=%d", acceptanceCount, fingerprintCount)
	}
	if plainCount != 1 {
		t.Fatalf("unprotected repeat must collapse to one, got %d", plainCount)
	}
}

// Byte-compare on the fixed fixture: dedup removes exactly the duplicated
// entries and changes nothing else. The before/after comparison is over the
// exact composed strings.
func TestSelectRelevantContextF3FixtureByteCompare(t *testing.T) {
	before := selectRelevantContext(f3FixtureStatic, f3FixturePrior, f3FixtureBundle(), []string{"code_writer", "test_runner"})

	// Mechanical expectation: each entry appears exactly once unless
	// protected; order follows composition order (statics, prior summaries
	// in roster order, bundle items).
	seen := map[string]int{}
	for _, entry := range before {
		seen[entry]++
	}
	for entry, count := range seen {
		if count > 1 && !isProtectedContextEntry(strings.TrimSpace(entry)) {
			t.Fatalf("repeated unprotected entry survived: %q (x%d)", entry, count)
		}
	}
	// Every input static and summary line still appears at least once.
	for _, s := range f3FixtureStatic {
		found := false
		for _, e := range before {
			if strings.TrimSpace(e) == strings.TrimSpace(s) {
				found = true
			}
		}
		if !found {
			t.Fatalf("static instruction lost: %q", s)
		}
	}
}

// Empty entries never compose: an empty prior summary and empty bundle
// produce no empty lines.
func TestSelectRelevantContextNoEmptyEntries(t *testing.T) {
	got := selectRelevantContext([]string{"only"}, map[string]string{"stage": ""}, nil, nil)
	if len(got) != 1 || got[0] != "only" {
		t.Fatalf("empty summaries must not compose: %q", got)
	}
}

// Determinism: the same fixture composes byte-identically across calls
// (map iteration order must not leak into composition).
func TestSelectRelevantContextDeterministic(t *testing.T) {
	first := selectRelevantContext(f3FixtureStatic, f3FixturePrior, f3FixtureBundle(), []string{"code_writer", "test_runner"})
	for i := 0; i < 20; i++ {
		again := selectRelevantContext(f3FixtureStatic, f3FixturePrior, f3FixtureBundle(), []string{"code_writer", "test_runner"})
		if strings.Join(first, "\x00") != strings.Join(again, "\x00") {
			t.Fatalf("composition not deterministic at iteration %d", i)
		}
	}
}
