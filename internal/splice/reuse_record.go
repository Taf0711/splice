package splice

// Work package E2 (warm-cost handoff Section 9): typed reuse records.
//
// A reuse record is the evidence payload that lets a later run SUBSTITUTE a
// discovery operation instead of merely receiving text. It rides the
// existing graph node's Metadata field: no new node kind, no anchor-kind
// invention, no sidecar schema change. Legacy nodes without a record (or
// with one below the required schema version) remain HINTS.
//
// Capture contract pinned here:
//
//   - captureFromVerifiedRun requires evidence that applicable required
//     checks EXECUTED and PASSED. trace_status completed is not verified:
//     a completed run whose test stage ran zero tests contributes only
//     provisional captures.
//   - Native pre-evaluator captures are provisional (status recorded, later
//     eligibility attached); they are never labeled verified here.
//   - Failure records carry a failure fingerprint, the code version, and a
//     supporting diagnostic reference.
//   - Test-command records include the actual command, the package/config
//     dependencies, environment assumptions, and the observed result, so
//     procedure freshness admission can check the files the command needs
//     without weakening freshness globally.
//
// No mandatory post-run reflection call exists in this package and none is
// added: everything here is deterministic extraction from run artifacts.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ReuseRecordSchemaVersion is the schema version a metadata payload must
// carry to be admitted as a reuse record rather than a hint.
const ReuseRecordSchemaVersion = 1

// Capture origin values. CaptureOriginRuntime means the producing run
// extracted the record itself (the only origin the automatic-cognition gate
// accepts); CaptureOriginEvalImport marks a frozen capture set imported by
// a harness; CaptureOriginManual marks any human/model-authored record.
const (
	CaptureOriginRuntime    = "runtime"
	CaptureOriginEvalImport = "eval-import"
	CaptureOriginManual     = "manual"
)

// Verification status values for a record's verification observation.
const (
	VerificationStatusPassed      = "passed"
	VerificationStatusProvisional = "provisional"
	VerificationStatusUnverified  = "unverified"
	VerificationStatusFailed      = "failed"
)

// SourceRef is one supporting source reference with the raw content digest
// observed at capture time. Freshness admission (E3) re-hashes these paths
// against the current authorized source, including dirty and untracked
// files, and any mismatch is stale.
type SourceRef struct {
	Path    string `json:"path"`
	Symbol  string `json:"symbol,omitempty"`
	StartLn int    `json:"start_line,omitempty"`
	EndLn   int    `json:"end_line,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// DependencyRef is one dependency or configuration input the record's claim
// depends on: a go.mod digest, a config file digest, a toolchain version.
type DependencyRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// ReuseRecord is the typed metadata payload stored on a graph node. It is
// JSON-serialized into GraphUpsertInput.Metadata under the recordKey.
type ReuseRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	// Identity is the stable source-qualified identity (project-qualified,
	// content-versioned). Two records with the same identity but different
	// ContentVersion are different versions of the same claim.
	Identity string `json:"identity"`
	// AnsweredNeed is the E1 need kind and subject this record answers.
	AnsweredNeed string `json:"answered_need"`
	// Applicability states the narrow conditions under which the record
	// applies. Empty applicability is a hint, never a substitution.
	Applicability string `json:"applicability,omitempty"`
	// Conclusion is the concise claim, when a claim is needed. Location-only
	// records may leave it empty.
	Conclusion string `json:"conclusion,omitempty"`
	// Supporting sources and their digests at capture time.
	Supporting []SourceRef `json:"supporting"`
	// Dependencies and configuration digests.
	Dependencies []DependencyRef `json:"dependencies,omitempty"`
	// Producer provenance: the run that observed the evidence, the capture
	// origin, and the ACTUAL worktree identity (a snapshot revision naming
	// the exact bytes verified, not merely the project root).
	ProducerRun      string `json:"producer_run"`
	CaptureOrigin    string `json:"capture_origin"`
	WorktreeIdentity string `json:"worktree_identity"`
	// Verification observation and its evidence references.
	VerificationStatus string   `json:"verification_status"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	// Limitations is free-form bounded prose about what the record does NOT
	// establish.
	Limitations string `json:"limitations,omitempty"`
	// Status is the record lifecycle: active records are candidates;
	// anything else is not.
	Status string `json:"status"`
	// ContentVersion is the digest over the record's claim-relevant
	// content. Changed content under the same identity is a NEW version.
	ContentVersion string `json:"content_version"`
	// FailureFingerprint applies to failure-kind records only.
	FailureFingerprint string `json:"failure_fingerprint,omitempty"`
	// TestCommand applies to procedure-kind records: the actual command,
	// its dependencies, environment assumptions, and the observed result.
	TestCommand        string   `json:"test_command,omitempty"`
	TestPackages       []string `json:"test_packages,omitempty"`
	EnvironmentAssumed string   `json:"environment_assumed,omitempty"`
	ObservedResult     string   `json:"observed_result,omitempty"`
}

// recordKey is the metadata key the record payload is stored under.
const recordKey = "reuse_record"

// recordIdentity builds the stable source-qualified identity for a record:
// project path plus kind plus the subject the record is about. The project
// path is a STORAGE identity; the worktree identity (a revision naming the
// exact bytes) is carried separately and is what freshness reads.
func recordIdentity(projectPath, kind, subject string) string {
	return projectPath + "\x00" + kind + "\x00" + subject
}

// contentVersionDigest computes the content-version digest over the fields
// whose change means a new version of the claim: conclusion, supporting
// digests, dependency digests, and the verification status. Two records with
// the same identity and different digests are different versions.
func contentVersionDigest(r ReuseRecord) string {
	h := sha256.New()
	fmt.Fprintf(h, "conclusion\x00%s\x00", r.Conclusion)
	fmt.Fprintf(h, "verification\x00%s\x00", r.VerificationStatus)
	supporting := make([]string, 0, len(r.Supporting))
	for _, s := range r.Supporting {
		supporting = append(supporting, s.Path+"\x00"+s.Symbol+"\x00"+s.Digest)
	}
	sort.Strings(supporting)
	for _, s := range supporting {
		fmt.Fprintf(h, "supporting\x00%s\x00", s)
	}
	deps := make([]string, 0, len(r.Dependencies))
	for _, d := range r.Dependencies {
		deps = append(deps, d.Path+"\x00"+d.Digest)
	}
	sort.Strings(deps)
	for _, d := range deps {
		fmt.Fprintf(h, "dependency\x00%s\x00", d)
	}
	fmt.Fprintf(h, "test\x00%s\x00", r.TestCommand)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:])
}

// buildReuseRecord assembles and validates the typed payload for one
// GraphCapture. It returns nil when the capture cannot meet the record
// floor, which keeps the node a hint rather than forging evidence.
func buildReuseRecord(c GraphCapture) *ReuseRecord {
	kind := c.Kind
	if kind != "fact" && kind != "decision" && kind != "failure" && kind != "procedure" && kind != "conclusion" && kind != "evidence" {
		return nil
	}
	if c.Project == "" || c.RunID == "" || c.Revision == "" {
		return nil
	}
	subject := captureSubject(c)
	if subject == "" {
		return nil
	}
	rec := &ReuseRecord{
		SchemaVersion:      ReuseRecordSchemaVersion,
		Kind:               kind,
		Identity:           recordIdentity(c.Project, kind, subject),
		ProducerRun:        c.RunID,
		CaptureOrigin:      c.CaptureOrigin,
		WorktreeIdentity:   c.Revision,
		VerificationStatus: c.VerificationStatus,
		Status:             "active",
		Applicability:      c.Applicability,
		Conclusion:         firstLine(c.Claim),
	}
	if rec.CaptureOrigin == "" {
		rec.CaptureOrigin = CaptureOriginRuntime
	}
	for _, e := range c.Evidence {
		rec.EvidenceRefs = append(rec.EvidenceRefs, e.Kind+":"+e.Ref)
	}
	for _, a := range c.Anchors {
		switch a.Kind {
		case "file":
			rec.Supporting = append(rec.Supporting, SourceRef{Path: a.Value, Digest: c.FileDigests[a.Value]})
		case "symbol":
			path, sym := splitSymbolAnchor(a.Value)
			rec.Supporting = append(rec.Supporting, SourceRef{Path: path, Symbol: sym, Digest: c.FileDigests[path]})
		}
	}
	if kind == "failure" {
		rec.FailureFingerprint = c.FailureFingerprint
		if rec.FailureFingerprint == "" {
			// A failure record without a fingerprint cannot admit: it
			// could match any later failure.
			return nil
		}
	}
	if kind == "procedure" {
		rec.TestCommand = c.TestCommand
		rec.TestPackages = c.TestPackages
		rec.EnvironmentAssumed = c.EnvironmentAssumed
		rec.ObservedResult = c.ObservedResult
		if rec.TestCommand == "" || rec.ObservedResult == "" {
			// A procedure without the actual command and observed result
			// is exactly the admission failure this package fixes; keep
			// it a hint.
			return nil
		}
	}
	rec.ContentVersion = contentVersionDigest(*rec)
	return rec
}

// splitSymbolAnchor splits a path#Symbol anchor value.
func splitSymbolAnchor(v string) (path, sym string) {
	if i := strings.Index(v, "#"); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

// captureSubject picks the stable subject for the identity: the first file
// anchor, else the first symbol anchor's file, else the test anchor.
func captureSubject(c GraphCapture) string {
	for _, a := range c.Anchors {
		if a.Kind == "file" && a.Value != "" {
			return a.Value
		}
	}
	for _, a := range c.Anchors {
		if a.Kind == "symbol" && a.Value != "" {
			path, _ := splitSymbolAnchor(a.Value)
			return path
		}
	}
	for _, a := range c.Anchors {
		if a.Kind == "test" && a.Value != "" {
			return a.Value
		}
	}
	return ""
}

// parseReuseRecord decodes a node's metadata into a record when the payload
// carries the current schema version. Anything else (missing payload, older
// schema, undecodable JSON) is a hint.
func parseReuseRecord(metadata map[string]any) *ReuseRecord {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[recordKey]
	if !ok {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var rec ReuseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil
	}
	if rec.SchemaVersion != ReuseRecordSchemaVersion {
		return nil
	}
	return &rec
}

// verificationEvidence reports whether the run's records prove that
// applicable required checks EXECUTED and PASSED. A completed status is not
// proof: a test stage that ran zero tests (or was skipped) contributes no
// verification evidence, and skipped facts do not count.
func verificationEvidence(testStageRan bool, testsExecuted int, testsFailed int, acceptanceTotal, acceptancePassed int) bool {
	if testStageRan && testsExecuted > 0 && testsFailed == 0 {
		return true
	}
	// Acceptance facts with automated commands are the other evidence
	// class. Facts without commands (skipped) do not count either way.
	return acceptanceTotal > 0 && acceptancePassed == acceptanceTotal
}

// recordToMetadata serializes a record for GraphUpsertInput.Metadata.
func recordToMetadata(r *ReuseRecord) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{recordKey: r}
}
