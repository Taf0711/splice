package splice

import (
	"strings"
	"testing"
)

func e4Workspace(t *testing.T) string { return e1Workspace(t) }

func TestColdPlanReadsNamedTargetsAndSkipsFallback(t *testing.T) {
	ws := e4Workspace(t)
	cold := buildColdPlan("extend internal/audit/log.go with the EnforceRetention cap", ws, nil, 8)
	names := operationNames(cold.Operations)
	if !containsString(names, "read:internal/audit/log.go") {
		t.Fatalf("named target not read: %v", names)
	}
	if containsString(names, "read:internal/audit/retention.go") {
		t.Fatalf("fallback file read despite named target: %v", names)
	}
	if !containsString(names, "list:workspace") {
		t.Fatal("workspace listing must be retained by default")
	}
	// Justification present for the fallback skip.
	found := false
	for _, j := range cold.Justification {
		if len(j) >= 21 && j[:21] == "fallback sources skip" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fallback skip not justified: %v", cold.Justification)
	}
}

func TestColdPlanFallsBackWithoutNamedTargets(t *testing.T) {
	ws := e4Workspace(t)
	cold := buildColdPlan("make the audit hygiene visible", ws, nil, 1)
	readCount := 0
	for _, o := range cold.Operations {
		if o.Kind == "read" {
			readCount++
		}
	}
	if readCount != 1 {
		t.Fatalf("fallback reads = %d, want bounded 1", readCount)
	}
	if !containsString(operationNames(cold.Operations), "list:workspace") {
		t.Fatal("listing missing")
	}
}

func TestWarmPlanSubstitutesLocatedOperation(t *testing.T) {
	ws := e4Workspace(t)
	cold := buildColdPlan("reuse the EnforceRetention cutoff rules", ws, nil, 8)
	// The cold plan locates EnforceRetention through the symbol index; a
	// warm record for the SAME need gets NO credit (coldResolved).
	needs := deriveContextNeeds("reuse the EnforceRetention cutoff rules", ws, nil, nil)
	var coldNeed *ContextNeed
	for i := range needs {
		if needs[i].Kind == NeedLocateNamedOperation {
			n := needs[i]
			coldNeed = &n
		}
	}
	if coldNeed == nil || !coldNeed.coldResolved() {
		t.Fatalf("symbol-index need missing or not cold-resolved: %+v", coldNeed)
	}
	admitted, _ := admitCandidates([]nodeWithRecord{{NodeID: 1, NeedID: coldNeed.ID, Record: e3Record(map[string]string{})}},
		[]ContextNeed{*coldNeed}, admissionContext{Workspace: ws})
	if len(admitted) != 0 {
		// The record speaks about a different subject; and even a matching
		// one must not claim credit for a cold-resolved need.
		for _, a := range admitted {
			if a.Need.coldResolved() {
				t.Fatal("warm claimed credit for a cold-resolved need")
			}
		}
	}
	// A warm-admissible need (task-text origin, subject matching the cold
	// plan's symbol) IS substituted: the cold symbol operation disappears.
	warmNeed := ContextNeed{ID: "locate:internal/audit/log.go#EnforceRetention", Kind: NeedLocateNamedOperation,
		Subject: "internal/audit/log.go#EnforceRetention", Origin: NeedOriginTaskText, Required: true}
	_, digests := e3Workspace(t)
	rec := e3Record(digests)
	wsDigests := map[string]string{}
	for _, s := range rec.Supporting {
		d, err := currentFileDigest(ws, s.Path)
		if err == nil {
			wsDigests[s.Path] = d
		}
	}
	rec.Supporting[0].Digest = wsDigests[rec.Supporting[0].Path]
	admitted2, rejected2 := admitCandidates([]nodeWithRecord{{NodeID: 2, NeedID: warmNeed.ID, Record: rec}},
		[]ContextNeed{warmNeed}, admissionContext{Workspace: ws})
	if len(admitted2) != 1 {
		t.Fatalf("admission failed: %+v", rejected2)
	}
	warm := buildWarmPlan(cold, admitted2, []ContextNeed{})
	d := diffPlans(cold, warm)
	// The cold plan carries symbol:internal/audit/log.go#EnforceRetention
	// (from the index). The warm substitution removes the cold operation
	// the record replaces: the read of the record's subject is covered by
	// the delivered view. The elimination list must be non-empty and
	// structural.
	if len(d.Eliminated) == 0 {
		t.Fatalf("no eliminated operations: %+v", d)
	}
	if len(warm.Substitutions) == 0 {
		t.Fatal("substitution not recorded")
	}
	if warm.Substitutions[0].RecordRef == "" || warm.Substitutions[0].RecordDigest == "" {
		t.Fatalf("substitution provenance incomplete: %+v", warm.Substitutions[0])
	}
}

func TestNoEligibleRecordMeansWarmEqualsCold(t *testing.T) {
	ws := e4Workspace(t)
	cold := buildColdPlan("polish the audit hygiene", ws, nil, 8)
	warm := buildWarmPlan(cold, nil, deriveContextNeeds("polish the audit hygiene", ws, nil, nil))
	d := diffPlans(cold, warm)
	if len(d.Eliminated) != 0 {
		t.Fatalf("warm eliminated operations with no record: %+v", d.Eliminated)
	}
	if len(d.BeforeOps) != len(d.AfterOps) {
		t.Fatalf("operation counts differ: %d vs %d", len(d.BeforeOps), len(d.AfterOps))
	}
	if len(warm.DeliveredNotes) != 0 {
		t.Fatalf("no-hit warm added narration: %v", warm.DeliveredNotes)
	}
	// The policy decision is recorded instead.
	if len(warm.PolicyDecisions) == 0 {
		t.Fatal("no-advantage decision not recorded")
	}
}

func TestOpenNeedSurvivesSubstitution(t *testing.T) {
	ws := e4Workspace(t)
	cold := buildColdPlan("reuse the EnforceRetention rules", ws, nil, 8)
	unresolved := deriveContextNeeds("reuse the EnforceRetention rules", ws, nil, nil)
	_, digests := e3Workspace(t)
	rec := e3Record(digests)
	need := ContextNeed{ID: "locate:internal/audit/log.go#Enforce", Kind: NeedLocateNamedOperation,
		Subject: "internal/audit/log.go#Enforce", Origin: NeedOriginTaskText, Required: true}
	d, err := currentFileDigest(ws, rec.Supporting[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	rec.Supporting[0].Digest = d
	admitted, _ := admitCandidates([]nodeWithRecord{{NodeID: 1, NeedID: need.ID, Record: rec}},
		[]ContextNeed{need}, admissionContext{Workspace: ws})
	if len(admitted) != 1 {
		t.Fatal("fixture admission failed")
	}
	warm := buildWarmPlan(cold, admitted, unresolved)
	open := 0
	for _, n := range warm.UnresolvedNeeds {
		if n.Kind == NeedOpenDiscovery {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("open needs = %d, want 1: %+v", open, warm.UnresolvedNeeds)
	}
	decisionRecorded := false
	for _, p := range warm.PolicyDecisions {
		if strings.Contains(p, "open-ended discovery need survives") {
			decisionRecorded = true
		}
	}
	if !decisionRecorded {
		t.Fatalf("open-need policy not recorded: %v", warm.PolicyDecisions)
	}
}

func TestLocationOnlyRecordDeliversNoProse(t *testing.T) {
	ws, digests := e3Workspace(t)
	cold := ColdPlan{Operations: []ColdOperation{{Name: "list:workspace", Kind: "list", Subject: "workspace"}}}
	rec := e3Record(digests) // conclusion is location phrasing ("defines")
	need := e3Need()
	admitted, _ := admitCandidates([]nodeWithRecord{{NodeID: 1, NeedID: need.ID, Record: rec}},
		[]ContextNeed{need}, admissionContext{Workspace: ws})
	if len(admitted) != 1 {
		t.Fatal("fixture admission failed")
	}
	warm := buildWarmPlan(cold, admitted, nil)
	if len(warm.DeliveredNotes) != 0 {
		t.Fatalf("location-only record delivered prose: %v", warm.DeliveredNotes)
	}
	// A behavioral constraint may justify one note.
	recB := e3Record(digests)
	recB.Conclusion = "EnforceRetention must not mutate the trail; cutoff applies before the cap"
	recB.VerificationStatus = VerificationStatusPassed
	admittedB, _ := admitCandidates([]nodeWithRecord{{NodeID: 2, NeedID: need.ID, Record: recB}},
		[]ContextNeed{need}, admissionContext{Workspace: ws})
	if len(admittedB) != 1 {
		t.Fatal("behavioral fixture admission failed")
	}
	warmB := buildWarmPlan(cold, admittedB, nil)
	if len(warmB.DeliveredNotes) != 1 {
		t.Fatalf("behavioral note not delivered: %v", warmB.DeliveredNotes)
	}
}

func TestDiffPlansIsStructural(t *testing.T) {
	cold := ColdPlan{Operations: []ColdOperation{
		{Name: "read:a.go", Kind: "read", Subject: "a.go"},
		{Name: "list:workspace", Kind: "list", Subject: "workspace"},
	}}
	warm := WarmPlan{Operations: []ColdOperation{{Name: "list:workspace", Kind: "list", Subject: "workspace"}}}
	d := diffPlans(cold, warm)
	if len(d.Eliminated) != 1 || d.Eliminated[0] != "read:a.go" {
		t.Fatalf("eliminated = %v", d.Eliminated)
	}
}
