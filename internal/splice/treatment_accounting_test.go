package splice

// Suppression-accounting arithmetic pins (D2 tests 4 and 5): omitted and
// retained operations must sum to the default request, and a replacement
// (a narrower operation covering the same need) counts as work done, not
// as zero.

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func readQuery(path string) schemas.ContextQuery {
	p := path
	return schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &p, MaxResults: 10, MaxChars: 5000}
}

func fullDefaultRequest() schemas.ContextRequest {
	// Eight default reads, no listing: the arithmetic pin isolates read
	// accounting from listing suppression.
	q := []schemas.ContextQuery{}
	for _, p := range []string{
		"internal/a.go", "internal/b.go", "internal/c.go", "internal/d.go",
		"internal/e.go", "internal/f.go", "internal/g.go", "internal/h.go",
	} {
		q = append(q, readQuery(p))
	}
	return schemas.ContextRequest{Reason: "test", Queries: q}
}

// TestEightDefaultReadsWithTwoRetainedYieldSixOmitted pins D2 test 4: with
// two of the eight default reads covered by the scope's known files (and
// therefore still issued), six default reads are structurally omitted.
func TestEightDefaultReadsWithTwoRetainedYieldSixOmitted(t *testing.T) {
	defaultReq := fullDefaultRequest()
	scope := StageScopePlan{
		CognitionResolved: true,
		KnownFiles:        []string{"internal/a.go", "internal/b.go"},
		AllowGlobalList:   false,
	}
	scoped, sup := ScopedContextRequest(defaultReq, scope, "test")
	// The scoped request still reads a.go and b.go (covered files), so
	// exactly six default reads are omitted.
	if sup.ContextQueriesDefault != 8 {
		t.Fatalf("default = %d, want 8", sup.ContextQueriesDefault)
	}
	if sup.FileReadsSuppressed != 6 {
		t.Fatalf("FileReadsSuppressed = %d, want 6", sup.FileReadsSuppressed)
	}
	if sup.ContextQueriesSuppressed != 6 {
		t.Fatalf("ContextQueriesSuppressed = %d, want 6", sup.ContextQueriesSuppressed)
	}
	// The executed request must contain the two retained reads.
	issued := 0
	for _, q := range scoped.Queries {
		if q.QueryType == schemas.ContextReadFile && q.Path != nil && (*q.Path == "internal/a.go" || *q.Path == "internal/b.go") {
			issued++
		}
	}
	if issued != 2 {
		t.Fatalf("retained reads issued = %d, want 2", issued)
	}
	if len(scoped.Queries) != 2 {
		t.Fatalf("scoped queries = %d, want exactly the two retained reads", len(scoped.Queries))
	}
	_ = sup.ContextQueriesExecuted
}

// TestRetainedReadIsNotSuppressedWork pins the arithmetic identity for the
// retained files: a default read whose file the scoped request still issues
// is retained work. Retained + omitted = default, with no double counting.
func TestRetainedReadIsNotSuppressedWork(t *testing.T) {
	defaultReq := fullDefaultRequest()
	scope := StageScopePlan{
		CognitionResolved: true,
		KnownFiles:        []string{"internal/a.go", "internal/b.go"},
		AllowGlobalList:   false,
	}
	_, sup := ScopedContextRequest(defaultReq, scope, "test")
	// Every default read is either still issued (retained, in the executed
	// set) or omitted (suppressed). The two covered files are retained.
	if sup.FileReadsSuppressed != sup.ContextQueriesDefault-2 {
		t.Fatalf("omitted = %d, want default-2 = %d", sup.FileReadsSuppressed, sup.ContextQueriesDefault-2)
	}
}

// TestOutlineReplacementIsRecordedAsWork pins D2 test 5: when the scope's
// coverage comes from a symbol outline rather than the default full-file
// read, the executed request still contains the outline operation, and the
// accounting records executed work, not zero. A smaller read that preserves
// the needed coverage is a replacement, never a silent drop.
func TestOutlineReplacementIsRecordedAsWork(t *testing.T) {
	defaultReq := fullDefaultRequest()
	sym := "EnforceRetention"
	scope := StageScopePlan{
		CognitionResolved: true,
		KnownSymbols:      []string{sym},
		AllowGlobalList:   false,
	}
	scoped, sup := ScopedContextRequest(defaultReq, scope, "test")
	// The scoped request issues the symbol outline: replacement work.
	outlineIssued := false
	for _, q := range scoped.Queries {
		if q.QueryType == schemas.ContextGetSymbol && q.Symbol != nil && *q.Symbol == sym {
			outlineIssued = true
		}
	}
	if !outlineIssued {
		t.Fatal("scoped request must issue the symbol outline (replacement work)")
	}
	// All eight default reads are structurally omitted (the scoped request
	// replaces them with the outline), and the accounting says so honestly.
	if sup.FileReadsSuppressed != 8 {
		t.Fatalf("FileReadsSuppressed = %d, want 8 (all replaced by the outline)", sup.FileReadsSuppressed)
	}
	if len(scoped.Queries) == 0 {
		t.Fatal("replacement is work: the executed request must not be empty")
	}
}
