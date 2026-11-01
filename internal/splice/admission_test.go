package splice

import (
	"os"
	"path/filepath"
	"testing"
)

// e3Workspace returns a workspace with two files and digests for them.
func e3Workspace(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"internal/audit/log.go":       "package audit\n\nfunc Enforce() {}\n",
		"internal/audit/retention.go": "package audit\n\nfunc Deficit() int { return 0 }\n",
	}
	digests := map[string]string{}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := currentFileDigest(dir, rel)
		if err != nil {
			t.Fatal(err)
		}
		digests[rel] = d
	}
	return dir, digests
}

func e3Record(digests map[string]string) *ReuseRecord {
	return &ReuseRecord{
		SchemaVersion: ReuseRecordSchemaVersion,
		Kind:          "fact",
		Identity:      recordIdentity("/p", "fact", "internal/audit/log.go"),
		AnsweredNeed:  NeedLocateNamedOperation,
		Applicability: "location and declared symbols of internal/audit/log.go at the recorded revision",
		Conclusion:    "internal/audit/log.go defines Enforce; verified at revision rev1",
		Supporting: []SourceRef{
			{Path: "internal/audit/log.go", Digest: digests["internal/audit/log.go"]},
		},
		ProducerRun:        "run-1",
		CaptureOrigin:      CaptureOriginRuntime,
		WorktreeIdentity:   "rev1",
		VerificationStatus: VerificationStatusPassed,
		Status:             "active",
		ContentVersion:     "x",
	}
}

func e3Need() ContextNeed {
	return ContextNeed{ID: "locate:internal/audit/log.go#Enforce", Kind: NeedLocateNamedOperation,
		Subject: "internal/audit/log.go#Enforce", Origin: NeedOriginTaskText, Required: true}
}

func TestAdmissionAcceptsFreshLocationRecord(t *testing.T) {
	ws, digests := e3Workspace(t)
	rec := e3Record(digests)
	decision, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws})
	if decision != AdmissionAccepted {
		t.Fatalf("decision = %s (%s), want accepted", decision, why)
	}
}

func TestAdmissionRevokesOnDirtyDependencyNewDeclAndDeletion(t *testing.T) {
	ws, digests := e3Workspace(t)
	rec := e3Record(digests)

	// Dirty dependency: edit the supporting file itself.
	if err := os.WriteFile(filepath.Join(ws, "internal", "audit", "log.go"), []byte("package audit\n// dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionStale {
		t.Fatalf("dirty file: %s (%s)", d, why)
	}

	// Deletion of a supporting file.
	ws2, digests2 := e3Workspace(t)
	rec2 := e3Record(digests2)
	if err := os.Remove(filepath.Join(ws2, "internal", "audit", "log.go")); err != nil {
		t.Fatal(err)
	}
	if d, why := admitRecord(t.Context(), rec2, e3Need(), admissionContext{Workspace: ws2}); d != AdmissionStale {
		t.Fatalf("deleted file: %s (%s)", d, why)
	}

	// New declaration elsewhere in the same package does not by itself
	// change the recorded file's bytes: a rename of the SYMBOL however must
	// revoke. Rename test: the symbol subject no longer exists because the
	// file's bytes changed under the same path (rename captured as byte
	// change).
	ws3, digests3 := e3Workspace(t)
	rec3 := e3Record(digests3)
	if err := os.WriteFile(filepath.Join(ws3, "internal", "audit", "log.go"), []byte("package audit\n\nfunc Renamed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, why := admitRecord(t.Context(), rec3, e3Need(), admissionContext{Workspace: ws3}); d != AdmissionStale {
		t.Fatalf("renamed symbol: %s (%s)", d, why)
	}
}

func TestAdmissionUnknownHashIsUnavailable(t *testing.T) {
	ws, digests := e3Workspace(t)
	rec := e3Record(digests)
	rec.Supporting[0].Digest = ""
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionUnavailable {
		t.Fatalf("unknown hash: %s (%s)", d, why)
	}
	// Missing worktree identity: unavailable.
	rec2 := e3Record(digests)
	rec2.WorktreeIdentity = ""
	if d, _ := admitRecord(t.Context(), rec2, e3Need(), admissionContext{Workspace: ws}); d != AdmissionUnavailable {
		t.Fatalf("missing identity: %s", d)
	}
}

func TestAdmissionChangedDependencyDigestIsStale(t *testing.T) {
	ws, digests := e3Workspace(t)
	rec := e3Record(digests)
	rec.Dependencies = []DependencyRef{{Path: "go.mod", Digest: "deadbeef"}}
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionStale {
		t.Fatalf("changed dependency: %s (%s)", d, why)
	}
	// Matching dependency digest passes.
	ws2, digests2 := e3Workspace(t)
	if err := os.WriteFile(filepath.Join(ws2, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dgo, err := currentFileDigest(ws2, "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	rec2 := e3Record(digests2)
	rec2.Dependencies = []DependencyRef{{Path: "go.mod", Digest: dgo}}
	if d, why := admitRecord(t.Context(), rec2, e3Need(), admissionContext{Workspace: ws2}); d != AdmissionAccepted {
		t.Fatalf("matching dependency: %s (%s)", d, why)
	}
}

func TestSemanticDistractorCannotRemoveRequiredDependency(t *testing.T) {
	ws, digests := e3Workspace(t)
	need := e3Need()
	// The distractor speaks about a DIFFERENT file but ranks first
	// semantically; the required record speaks about the needed file.
	distractor := e3Record(digests)
	distractor.Supporting = []SourceRef{{Path: "internal/audit/retention.go", Digest: digests["internal/audit/retention.go"]}}
	distractor.Identity = recordIdentity("/p", "fact", "internal/audit/retention.go")
	required := e3Record(digests)

	admitted, rejected := admitCandidates([]nodeWithRecord{
		{NodeID: 1, NeedID: need.ID, Record: distractor},
		{NodeID: 2, NeedID: need.ID, Record: required},
	}, []ContextNeed{need}, admissionContext{Workspace: ws})

	if len(admitted) != 1 || admitted[0].Record != required {
		t.Fatalf("admitted = %+v, want the required record", admitted)
	}
	if len(rejected) != 1 || rejected[0].Decision != AdmissionHintOnly {
		t.Fatalf("rejected = %+v, want the distractor hint-only", rejected)
	}
	// The distractor alone must NOT answer the need: required dependency
	// stays open.
	admitted2, _ := admitCandidates([]nodeWithRecord{{NodeID: 1, NeedID: need.ID, Record: distractor}},
		[]ContextNeed{need}, admissionContext{Workspace: ws})
	if len(admitted2) != 0 {
		t.Fatal("distractor answered a need it does not address")
	}
}

func TestLegacyStructuralNodeRemainsHint(t *testing.T) {
	ws, _ := e3Workspace(t)
	// No record payload at all: legacy node.
	if d, why := admitRecord(t.Context(), nil, e3Need(), admissionContext{Workspace: ws}); d != AdmissionHintOnly {
		t.Fatalf("legacy node: %s (%s)", d, why)
	}
	// Record without applicability: hint.
	rec := e3Record(nil)
	rec.Supporting[0].Digest = "whatever"
	rec.Applicability = ""
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionHintOnly {
		t.Fatalf("no-applicability record: %s (%s)", d, why)
	}
}

func TestOneResolvedNeedLeavesOthersOpen(t *testing.T) {
	ws, digests := e3Workspace(t)
	n1 := ContextNeed{ID: "locate:internal/audit/log.go#Enforce", Kind: NeedLocateNamedOperation,
		Subject: "internal/audit/log.go#Enforce", Origin: NeedOriginTaskText, Required: true}
	n2 := ContextNeed{ID: "inspect:internal/audit/retention.go", Kind: NeedInspectEditTarget,
		Subject: "internal/audit/retention.go", Origin: NeedOriginTaskText, Required: true}
	rec := e3Record(digests)
	admitted, rejected := admitCandidates([]nodeWithRecord{{NodeID: 1, NeedID: n1.ID, Record: rec}},
		[]ContextNeed{n1, n2}, admissionContext{Workspace: ws})
	if len(admitted) != 1 || admitted[0].Need.ID != n1.ID {
		t.Fatalf("admitted = %+v", admitted)
	}
	// n2 has no candidate: it stays open, not silently resolved.
	for _, a := range admitted {
		if a.Need.ID == n2.ID {
			t.Fatal("unaddressed need was resolved")
		}
	}
	if len(rejected) != 0 {
		t.Fatalf("unexpected rejections: %+v", rejected)
	}
}

func TestBehavioralClaimWithoutVerificationIsHint(t *testing.T) {
	ws, digests := e3Workspace(t)
	rec := e3Record(digests)
	rec.Conclusion = "EnforceRetention drops events older than the cutoff"
	rec.VerificationStatus = VerificationStatusProvisional
	if d, why := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionHintOnly {
		t.Fatalf("unverified behavioral claim: %s (%s)", d, why)
	}
	// The same claim with passed verification is accepted.
	rec.VerificationStatus = VerificationStatusPassed
	if d, _ := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionAccepted {
		t.Fatalf("verified behavioral claim rejected: %s", d)
	}
}

func TestLocationRecordReplacesLocationSearchOnly(t *testing.T) {
	ws, digests := e3Workspace(t)
	rec := e3Record(digests)
	// Accepted for a location need...
	if d, _ := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: ws}); d != AdmissionAccepted {
		t.Fatal("location record rejected for location need")
	}
	// ...but an integration need needs verified behavioral evidence; this
	// location-only record cannot certify package behavior.
	intNeed := ContextNeed{ID: "integrate:x", Kind: NeedUnderstandIntegrationRelationship,
		Subject: "internal/audit/log.go", Origin: NeedOriginPriorEvidence, Required: true}
	if d, why := admitRecord(t.Context(), rec, intNeed, admissionContext{Workspace: ws}); d != AdmissionHintOnly {
		t.Fatalf("location record answered integration need: %s (%s)", d, why)
	}
}

func TestUnchangedRootDifferentBytesNeverCertifies(t *testing.T) {
	// Same project root string, different worktree bytes: the digest check
	// must reject. This is the test the Section 9 list names explicitly.
	wsA, _ := e3Workspace(t)
	_, digestsA := e3Workspace(t)
	rec := e3Record(digestsA)
	// wsA and the record's capture workspace are different directories
	// with the SAME relative layout and same root LABEL in telemetry, but
	// the record was captured from wsB's bytes. wsA's log.go has different
	// bytes than wsB's? e3Workspace writes identical bodies, so mutate
	// wsA to simulate another worktree with the same root path role.
	if err := os.WriteFile(filepath.Join(wsA, "internal", "audit", "log.go"), []byte("package audit\n// different worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, _ := admitRecord(t.Context(), rec, e3Need(), admissionContext{Workspace: wsA}); d == AdmissionAccepted {
		t.Fatal("different worktree bytes certified under the same root")
	}
}
