package splice

// Work package E3 (warm-cost handoff Section 9): freshness and applicability
// validated separately, with typed admission results.
//
// Freshness is BYTE-level: every supporting SourceRef digest is re-hashed
// against the current authorized source, INCLUDING dirty and untracked
// relevant files (the worktree's actual bytes, not a git-object view).
// Missing files, unavailable digests, unknown revisions, changed dependency
// digests, and ambiguous symbol ownership are stale/unknown, and unknown
// fails closed.
//
// Applicability is CLAIM-level: a record answers a specific need kind and
// subject only within its narrow applicability statement. A valid source
// location can replace a location search; it can never certify the full
// behavior of a package, and a semantic hit can never remove a required
// dependency.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// AdmissionDecision is the typed result of admission. It names the verdict
// and the reason, so callers and telemetry never re-derive either.
type AdmissionDecision string

const (
	// AdmissionAccepted: the record is admissible for the SPECIFIC need.
	AdmissionAccepted AdmissionDecision = "accepted-for-specific-need"
	// AdmissionHintOnly: the record may be delivered as knowledge but
	// authorizes no substitution.
	AdmissionHintOnly AdmissionDecision = "hint-only"
	// AdmissionStale: the record's bytes or dependencies changed.
	AdmissionStale AdmissionDecision = "stale"
	// AdmissionUnsupported: the record's claim exceeds its evidence
	// (behavioral claim without behavioral verification, missing
	// applicability, unknown symbol ownership).
	AdmissionUnsupported AdmissionDecision = "unsupported"
	// AdmissionUnavailable: freshness could not be evaluated (missing
	// files, unreadable source, unknown revision).
	AdmissionUnavailable AdmissionDecision = "unavailable"
)

// admissionContext is what E3 needs to evaluate one candidate: the current
// authorized workspace (the run's ACTUAL worktree) and the need the record
// claims to answer.
type admissionContext struct {
	// Workspace is the run's actual worktree root. Digests re-hash bytes
	// HERE, including dirty and untracked files.
	Workspace string
}

// admitRecord validates one candidate record against the current authorized
// source for one specific need, returning the typed decision. The checks run
// freshness FIRST (byte identity), then applicability (claim scope): a
// byte-fresh record with an over-broad claim is unsupported, never accepted.
func admitRecord(ctx context.Context, rec *ReuseRecord, need ContextNeed, ac admissionContext) (AdmissionDecision, string) {
	if rec == nil {
		return AdmissionHintOnly, "no reuse record payload: legacy node"
	}
	// Hint gate: without narrow applicability a record is knowledge, never
	// substitution.
	if strings.TrimSpace(rec.Applicability) == "" {
		return AdmissionHintOnly, "no applicability conditions recorded"
	}
	// Freshness gate: every supporting digest must match the current
	// authorized bytes.
	decision, why := admitFreshness(rec, ac)
	if decision != AdmissionAccepted {
		return decision, why
	}
	// Applicability gate: the claim must fit the need.
	return admitApplicability(rec, need)
}

// admitFreshness re-hashes each supporting source and dependency digest
// against the current worktree bytes. Any mismatch is stale; any inability
// to evaluate (missing file, empty recorded digest, unreadable source) is
// unavailable, and unavailable fails closed.
func admitFreshness(rec *ReuseRecord, ac admissionContext) (AdmissionDecision, string) {
	if rec.WorktreeIdentity == "" {
		return AdmissionUnavailable, "record carries no worktree identity"
	}
	for _, s := range rec.Supporting {
		if s.Path == "" {
			return AdmissionUnavailable, "supporting reference without a path"
		}
		if s.Digest == "" {
			// The capture could not hash the bytes it observed. It cannot
			// certify anything now either.
			return AdmissionUnavailable, "supporting digest for " + s.Path + " unavailable at capture"
		}
		digest, err := currentFileDigest(ac.Workspace, s.Path)
		if err != nil {
			if os.IsNotExist(err) {
				return AdmissionStale, "supporting file " + s.Path + " no longer exists"
			}
			return AdmissionUnavailable, "supporting file " + s.Path + " unreadable: " + err.Error()
		}
		if digest == "" {
			return AdmissionUnavailable, "digest for " + s.Path + " unavailable"
		}
		if digest != s.Digest {
			return AdmissionStale, "bytes of " + s.Path + " changed since capture"
		}
	}
	for _, d := range rec.Dependencies {
		if d.Path == "" || d.Digest == "" {
			return AdmissionUnavailable, "dependency reference incomplete"
		}
		digest, err := currentFileDigest(ac.Workspace, d.Path)
		if err != nil {
			if os.IsNotExist(err) {
				return AdmissionStale, "dependency " + d.Path + " no longer exists"
			}
			return AdmissionUnavailable, "dependency " + d.Path + " unreadable: " + err.Error()
		}
		if digest != d.Digest {
			return AdmissionStale, "dependency " + d.Path + " changed since capture"
		}
	}
	return AdmissionAccepted, "all supporting and dependency digests match the current authorized source"
}

// admitApplicability checks the claim scope against the need. The narrow
// rules, in order:
//
//   - an open-discovery need is never answered by substitution;
//   - a location record (location-only: no behavioral conclusion) answers
//     locate/inspect/obtain needs for the SAME subject;
//   - a behavioral claim needs verification status passed, and even then it
//     answers only within its recorded subject, never package-wide;
//   - ambiguous symbol ownership (the subject names multiple files) cannot
//     be resolved by the record alone.
func admitApplicability(rec *ReuseRecord, need ContextNeed) (AdmissionDecision, string) {
	if need.Kind == NeedOpenDiscovery {
		return AdmissionHintOnly, "open-ended discovery is never answered by substitution"
	}
	switch need.Kind {
	case NeedInspectEditTarget:
		// Inspecting an edit target needs the CURRENT BODY bytes. A
		// location/identity record certifies where the symbol is; it does
		// not carry the current declaration body. Reuse must therefore
		// leave the read operation in the cold plan.
		return AdmissionHintOnly, "edit-target needs current body bytes; location evidence is not a body view"
	case NeedLocateNamedOperation, NeedObtainReferencedDeclaration:
		// Location-class needs: the record must speak about the same
		// subject (file or file#symbol).
		if !recordSpeaksOfSubject(rec, need.Subject) {
			return AdmissionHintOnly, "record does not address the needed subject " + need.Subject
		}
		// A behavioral conclusion delivered for a location need is fine as
		// long as verification backs it; unverified behavioral text is
		// reduced to a hint.
		if rec.Conclusion != "" && rec.VerificationStatus != VerificationStatusPassed && isBehavioralConclusion(rec) {
			return AdmissionHintOnly, "behavioral conclusion without passed verification is hint-only"
		}
		return AdmissionAccepted, "location evidence matches the needed subject at current bytes"
	case NeedUnderstandIntegrationRelationship:
		// Integration questions need verified BEHAVIORAL evidence: a
		// location-only record (its conclusion names locations or
		// declarations) says nothing about how packages interact, even
		// when the bytes are fresh and verification passed.
		if !isBehavioralConclusion(rec) {
			return AdmissionHintOnly, "location-only record cannot certify integration behavior"
		}
		if rec.VerificationStatus != VerificationStatusPassed {
			return AdmissionHintOnly, "integration claim without passed verification"
		}
		if !recordSpeaksOfSubject(rec, need.Subject) {
			return AdmissionHintOnly, "integration record does not address " + need.Subject
		}
		return AdmissionAccepted, "verified behavioral evidence covers the needed subject"
	case NeedResolveConcreteFailure:
		if rec.Kind != "failure" || rec.FailureFingerprint == "" {
			return AdmissionHintOnly, "failure resolution needs a fingerprinted failure record"
		}
		return AdmissionAccepted, "failure fingerprint matches the recorded failure evidence"
	}
	return AdmissionHintOnly, "need kind outside the substitution table"
}

// isBehavioralConclusion reports whether the record's conclusion claims
// behavior rather than location. Deterministic and conservative: anything
// beyond "located/declared/defines" phrasing counts as behavioral.
func isBehavioralConclusion(rec *ReuseRecord) bool {
	c := strings.ToLower(rec.Conclusion)
	for _, loc := range []string{"located", "declared", "defines", "modified by"} {
		if strings.Contains(c, loc) {
			return false
		}
	}
	return true
}

// recordSpeaksOfSubject reports whether any supporting reference or the
// identity covers the need's subject. Subject forms: "path", "path#Symbol",
// or "Name (declared in a.go, b.go)" for ambiguous ones (never resolvable).
func recordSpeaksOfSubject(rec *ReuseRecord, subject string) bool {
	if subject == "" {
		return false
	}
	if strings.Contains(subject, "(declared in") {
		return false // ambiguous ownership: record alone cannot resolve
	}
	wantFile, wantSym := splitSubject(subject)
	for _, s := range rec.Supporting {
		if wantSym != "" {
			if s.Path == wantFile && (s.Symbol == wantSym || s.Symbol == "") {
				return true
			}
			continue
		}
		if s.Path == wantFile {
			return true
		}
	}
	return false
}

// splitSubject splits "path#Symbol" into (path, symbol); a bare path returns
// (path, "").
func splitSubject(subject string) (string, string) {
	if i := strings.Index(subject, "#"); i >= 0 {
		return subject[:i], subject[i+1:]
	}
	return subject, ""
}

// currentFileDigest hashes path under workspace. The path is repo-relative
// and slash-normalized; the read hits the actual worktree file, so dirty and
// untracked relevant files are included by construction. Missing files
// return an error the caller classifies; unreadable files return
// ("", err) which callers treat as unavailable.
//
// Package F4 (warm-cost handoff Section 10): results are memoized by
// content version through the run's digest memo. The memo is invalidation-
// exact, never optimistic: a recorded mutation of the file (or any file in
// the worktree, via InvalidateAll) drops the entry so the next admission
// re-hashes the current bytes. The mutation record is the same
// PriorChangedFiles evidence the pipeline already tracks (writer output,
// repair re-entry writes), so a pipeline-permitted edit always invalidates.
func currentFileDigest(workspace, relPath string) (string, error) {
	if workspace == "" || relPath == "" {
		return "", errDigestUnavailable(relPath)
	}
	if memo := runDigestMemo(); memo != nil {
		if digest, ok, err := memo.lookup(workspace, relPath); ok {
			return digest, err
		}
		digest, err := currentFileDigestUncached(workspace, relPath)
		memo.store(workspace, relPath, digest, err)
		return digest, err
	}
	return currentFileDigestUncached(workspace, relPath)
}

// currentFileDigestUncached is the uncached hash: one file read plus SHA-256.
func currentFileDigestUncached(workspace, relPath string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(relPath)))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// digestMemo memoizes (workspace, relPath) -> content digest for one run.
// The zero-lookup seam keeps tests and offline tools (which run without a
// memo) byte-identical to the pre-F4 behavior. Concurrent use is safe: the
// pass loop and repair re-entry can interleave.
type digestMemo struct {
	mu         sync.Mutex
	entries    map[digestMemoKey]digestMemoEntry
	capacity   int
	generation uint64
}

type digestMemoKey struct {
	workspace string
	path      string
}

type digestMemoEntry struct {
	digest     string
	err        error
	generation uint64
}

// newDigestMemo returns a bounded memo. Capacity 512 entries is far above the
// per-run file set; the LRU bound keeps pathological runs honest.
func newDigestMemo() *digestMemo {
	return &digestMemo{entries: map[digestMemoKey]digestMemoEntry{}, capacity: 512}
}

// lookup returns the memoized digest. ok=false means re-hash.
func (m *digestMemo) lookup(workspace, relPath string) (digest string, ok bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := digestMemoKey{workspace: workspace, path: relPath}
	entry, exists := m.entries[key]
	if !exists || entry.generation != m.generation {
		return "", false, nil
	}
	return entry.digest, true, entry.err
}

// store records one hash result at the current generation.
func (m *digestMemo) store(workspace, relPath, digest string, hashErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) >= m.capacity {
		// Bound exceeded: drop everything (simpler than LRU; correctness is
		// unaffected because a drop only causes a re-hash).
		m.entries = map[digestMemoKey]digestMemoEntry{}
	}
	m.entries[digestMemoKey{workspace: workspace, path: relPath}] = digestMemoEntry{
		digest:     digest,
		err:        hashErr,
		generation: m.generation,
	}
}

// Invalidate drops the memoized digest for one file. Call it when the
// pipeline records a mutation of that file.
func (m *digestMemo) Invalidate(workspace, relPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, digestMemoKey{workspace: workspace, path: relPath})
}

// InvalidateAll drops every entry. Call it when the worktree as a whole may
// have moved (repair re-entry with a broad write set).
func (m *digestMemo) InvalidateAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	m.entries = map[digestMemoKey]digestMemoEntry{}
}

// run-scoped memo seam. Tests and offline slices run with no memo (nil), so
// their behavior is byte-identical to the uncached path.
var (
	digestMemoMu sync.Mutex
	digestMemoV  *digestMemo
)

// SetRunDigestMemo installs the run-scoped memo. Pass nil to clear (tests).
// The memo lives for one pipeline run: a new run installs a fresh memo.
func SetRunDigestMemo(m *digestMemo) {
	digestMemoMu.Lock()
	defer digestMemoMu.Unlock()
	digestMemoV = m
}

// runDigestMemo returns the active memo, or nil when none is installed.
func runDigestMemo() *digestMemo {
	digestMemoMu.Lock()
	defer digestMemoMu.Unlock()
	return digestMemoV
}

type digestUnavailableError string

func (e digestUnavailableError) Error() string {
	return "digest unavailable for " + string(e)
}

func errDigestUnavailable(p string) error { return digestUnavailableError(p) }

// AdmittedResolution pairs an admitted record with the resolution the plan
// records for it (E1's ReuseResolution), including the concrete cold-plan
// operations the record replaces.
type AdmittedResolution struct {
	Need     ContextNeed
	Record   *ReuseRecord
	Decision AdmissionDecision
	Reason   string
	Replaced []string
	ViewReqs []string
}

// admitCandidates runs semantic-or-exact candidates through admission for a
// need set. It returns one admitted resolution per need at most (candidates
// are ranked by the caller) and the rejected decisions for telemetry. A
// semantic distractor cannot remove a required dependency: a need stays
// unresolved unless SOME record is admitted for IT specifically.
func admitCandidates(candidates []nodeWithRecord, needs []ContextNeed, ac admissionContext) (admitted []AdmittedResolution, rejected []AdmittedResolution) {
	byNeed := map[string]ContextNeed{}
	for _, n := range needs {
		byNeed[n.ID] = n
	}
	for _, cand := range candidates {
		need, ok := byNeed[cand.NeedID]
		if !ok {
			continue
		}
		decision, why := admitRecord(context.Background(), cand.Record, need, ac)
		res := AdmittedResolution{
			Need:     need,
			Record:   cand.Record,
			Decision: decision,
			Reason:   why,
		}
		if decision == AdmissionAccepted {
			res.Replaced = replacedOperationsFor(need)
			admitted = append(admitted, res)
			delete(byNeed, cand.NeedID)
		} else {
			rejected = append(rejected, res)
		}
	}
	sort.Slice(admitted, func(i, j int) bool { return admitted[i].Need.ID < admitted[j].Need.ID })
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].Need.ID < rejected[j].Need.ID })
	return admitted, rejected
}

// nodeWithRecord pairs a graph node with its parsed reuse record plus the
// need id the retrieval stage matched it to.
type nodeWithRecord struct {
	NodeID int64
	NeedID string
	Record *ReuseRecord
}

// replacedOperationsFor names the concrete cold-plan operations a record
// admitted for need replaces. The mapping is by need kind, one operation
// each: the discovery the cold plan would otherwise perform.
func replacedOperationsFor(need ContextNeed) []string {
	switch need.Kind {
	case NeedLocateNamedOperation:
		return []string{"symbol lookup for " + need.Subject}
	case NeedInspectEditTarget:
		return []string{"read of " + need.Subject}
	case NeedObtainReferencedDeclaration:
		return []string{"declaration lookup for " + need.Subject}
	case NeedUnderstandIntegrationRelationship:
		return []string{"integration read of " + need.Subject}
	case NeedResolveConcreteFailure:
		return []string{"failure diagnosis for " + need.Subject}
	}
	return nil
}
