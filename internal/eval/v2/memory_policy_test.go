package v2

import (
	"bytes"
	"strings"
	"testing"
)

func TestMemorySnapshotRoundTripCanonicalAndImport(t *testing.T) {
	snapshot := testMemorySnapshot(t)
	data, err := snapshot.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeMemorySnapshot(data, nil)
	if err != nil {
		t.Fatalf("DecodeMemorySnapshot: %v", err)
	}
	second, err := decoded.Encode(nil)
	if err != nil {
		t.Fatalf("second Encode: %v", err)
	}
	if !bytes.Equal(data, second) {
		t.Fatalf("canonical JSON changed on round trip:\n%s\n%s", data, second)
	}
	manifest := validManifest()
	manifest.CorpusProvenanceSHA256 = snapshot.CorpusProvenanceSHA256
	manifest.SnapshotSHA256 = snapshot.SnapshotSHA256
	imported, err := ImportSnapshot(data, manifest)
	if err != nil {
		t.Fatalf("ImportSnapshot: %v", err)
	}
	items := imported.Items()
	items[0].DeliveredID = "observation:mutated-copy"
	if got, ok := imported.Item("observation:one"); !ok || got.DeliveredID != "observation:one" {
		t.Fatalf("imported snapshot was mutable through Items copy: %+v, %v", got, ok)
	}
	if imported.WorkspacePath() != "" {
		t.Fatalf("unexpected materialization path %q", imported.WorkspacePath())
	}
}

func TestMemorySnapshotAdversarialValidation(t *testing.T) {
	base := testMemorySnapshot(t)
	cases := []struct {
		name string
		edit func(*MemorySnapshot)
		want string
	}{
		{"duplicate delivered ID", func(s *MemorySnapshot) { s.Items[1].DeliveredID = s.Items[0].DeliveredID }, "duplicate delivered_id"},
		{"bad content hash", func(s *MemorySnapshot) { s.Items[0].ContentSHA256 = "bad" }, "content_sha256"},
		{"holdout leakage", func(s *MemorySnapshot) { s.Items[0].SourceTaskID = "holdout-task" }, "holdout"},
		{"bad pool tag", func(s *MemorySnapshot) { s.Items[0].PoolMembership = []string{"hidden"} }, "pool_membership"},
		{"hidden answer provenance", func(s *MemorySnapshot) { s.Items[0].Provenance = "reference_solution: secret" }, "hidden-answer"},
		{"rekeyed without map", func(s *MemorySnapshot) { s.Rekeyed = true; s.IDMap = nil }, "rekeyed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			candidate.Items = append([]SnapshotItem(nil), base.Items...)
			tc.edit(&candidate)
			if err := candidate.Validate([]string{"holdout-task"}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want %q", err, tc.want)
			}
		})
	}
	badMap := base
	badMap.IDMap = map[string]string{"old": "observation:one", "old-2": "observation:one"}
	if err := badMap.Validate(nil); err == nil || !strings.Contains(err.Error(), "bijective") {
		t.Fatalf("non-bijective IDMap accepted: %v", err)
	}
	badHash := base
	badHash.SnapshotSHA256 = "0" + base.SnapshotSHA256[1:]
	data, err := badHash.Encode(nil)
	if err != nil {
		t.Fatalf("Encode bad hash: %v", err)
	}
	manifest := validManifest()
	manifest.CorpusProvenanceSHA256 = base.CorpusProvenanceSHA256
	if _, err := ImportSnapshot(data, manifest); err == nil || !strings.Contains(err.Error(), "snapshot_sha256") {
		t.Fatalf("bad snapshot hash accepted: %v", err)
	}
	manifest = validManifest()
	manifest.CorpusProvenanceSHA256 = base.CorpusProvenanceSHA256
	if _, err := ImportSnapshot(mustEncodeSnapshot(t, base), manifest); err == nil || !strings.Contains(err.Error(), "does not match manifest") {
		t.Fatalf("manifest snapshot hash mismatch accepted: %v", err)
	}
}

func TestSelectionAuditValidationAndStableHash(t *testing.T) {
	audit := SelectionAudit{Tasks: []SelectionAuditEntry{
		{TaskID: "task-b", ExpectedSelectedIDs: []string{"observation:b", "observation:a"}, ExpectedPostCompactionIDs: []string{"observation:a"}},
		{TaskID: "task-a", RetrievalMiss: true},
	}}
	if err := audit.ValidateFor([]TaskSpec{{ID: "task-a"}, {ID: "task-b"}}); err != nil {
		t.Fatalf("ValidateFor: %v", err)
	}
	hashOne := AuditSHA256(audit)
	reordered := SelectionAudit{Tasks: []SelectionAuditEntry{audit.Tasks[1], audit.Tasks[0]}}
	if hashOne != AuditSHA256(reordered) {
		t.Fatalf("audit hash changed with task order: %s != %s", hashOne, AuditSHA256(reordered))
	}
	badMiss := audit
	badMiss.Tasks = append([]SelectionAuditEntry(nil), audit.Tasks...)
	badMiss.Tasks[1].ExpectedSelectedIDs = []string{"observation:x"}
	if err := badMiss.Validate(); err == nil || !strings.Contains(err.Error(), "retrieval miss") {
		t.Fatalf("retrieval miss with selections accepted: %v", err)
	}
	unknown := SelectionAudit{Tasks: []SelectionAuditEntry{{TaskID: "unknown"}}}
	if err := unknown.ValidateFor([]TaskSpec{{ID: "known"}}); err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("unknown audit task accepted: %v", err)
	}
}

func TestMemoryWritePolicyClaimDenialAndTraceExemption(t *testing.T) {
	claim := MemoryWritePolicy{Mode: RunModeClaim}
	for _, kind := range []string{"observation", "exemplar", "OBSERVATION", "arbitrary"} {
		if err := claim.CheckUpsert(kind); err == nil || !strings.Contains(err.Error(), "memory_write_denied") || !strings.Contains(err.Error(), kind) {
			t.Fatalf("claim upsert %q error = %v", kind, err)
		}
		if err := claim.CheckMarkReviewed(kind); err == nil || !strings.Contains(err.Error(), "memory_write_denied") {
			t.Fatalf("claim review %q error = %v", kind, err)
		}
	}
	if err := claim.CheckUpsertTrace(); err != nil {
		t.Fatalf("claim trace upsert: %v", err)
	}
	if err := claim.CheckQueryTraces(); err != nil {
		t.Fatalf("claim trace query: %v", err)
	}
	development := MemoryWritePolicy{Mode: RunModeDevelopment}
	if err := development.CheckUpsert("observation"); err != nil {
		t.Fatalf("development observation: %v", err)
	}
	if err := development.CheckMarkReviewed("exemplar"); err != nil {
		t.Fatalf("development exemplar review: %v", err)
	}
	if err := development.CheckUpsert("other"); err == nil {
		t.Fatal("development unknown kind accepted")
	}
}

func TestVerifySelectionExactSetsAndSortedSymmetricDiff(t *testing.T) {
	audit := SelectionAudit{Tasks: []SelectionAuditEntry{{
		TaskID: "task-a", ExpectedSelectedIDs: []string{"id-b", "id-a"}, ExpectedPostCompactionIDs: []string{"id-a"},
	}}}
	if err := VerifySelection(SelectedDelivery{SelectedIDs: []string{"id-a", "id-b"}, PostCompactionIDs: []string{"id-a"}}, audit, "task-a"); err != nil {
		t.Fatalf("matching exact sets rejected: %v", err)
	}
	for _, tc := range []struct {
		name, want string
		actual     SelectedDelivery
	}{
		{"missing", "selected_missing=[id-b]", SelectedDelivery{SelectedIDs: []string{"id-a"}, PostCompactionIDs: []string{"id-a"}}},
		{"extra", "selected_extra=[id-c]", SelectedDelivery{SelectedIDs: []string{"id-a", "id-b", "id-c"}, PostCompactionIDs: []string{"id-a"}}},
		{"post drift", "post_compaction_extra=[id-c]", SelectedDelivery{SelectedIDs: []string{"id-a", "id-b"}, PostCompactionIDs: []string{"id-a", "id-c"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifySelection(tc.actual, audit, "task-a"); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "rule=invalidation") {
				t.Fatalf("VerifySelection error = %v, want %q", err, tc.want)
			}
		})
	}
	miss := SelectionAudit{Tasks: []SelectionAuditEntry{{TaskID: "task-miss", RetrievalMiss: true}}}
	if err := VerifySelection(SelectedDelivery{SelectedIDs: []string{"id"}}, miss, "task-miss"); err == nil || !strings.Contains(err.Error(), "selected_extra") {
		t.Fatalf("retrieval miss with delivery accepted: %v", err)
	}
}

func testMemorySnapshot(t *testing.T) MemorySnapshot {
	t.Helper()
	hash := strings.Repeat("a", 64)
	snapshot := MemorySnapshot{
		ManifestJSONSHA256: hash, CorpusProvenanceSHA256: hash, AdmissionPolicySHA256: hash, SelectorSHA256: hash,
		Items: []SnapshotItem{
			{DeliveredID: "observation:one", ContentSHA256: hash, Kind: SnapshotKindObservation, SourceTaskID: "task-a", RepositoryClass: "go", CreatedAtRFC3339: "2026-08-24T12:00:00Z", FreshnessLabel: FreshnessCurrent, Provenance: "source=task-a", PoolMembership: []string{"relevant"}},
			{DeliveredID: "exemplar:two", ContentSHA256: hash, Kind: SnapshotKindExemplar, SourceTaskID: "task-b", RepositoryClass: "go", CreatedAtRFC3339: "2026-08-24T12:00:01Z", FreshnessLabel: FreshnessStale, Provenance: "source=task-b", PoolMembership: []string{"placebo"}},
		},
	}
	snapshot.SnapshotSHA256 = SnapshotHash(snapshot)
	return snapshot
}

func mustEncodeSnapshot(t *testing.T, snapshot MemorySnapshot) []byte {
	t.Helper()
	data, err := snapshot.Encode(nil)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return data
}
