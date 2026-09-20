package splice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// retentionFixture copies the real large-02 fixture's audit package into a
// temp worktree: log.go (Trail) and retention.go (Task A's actual output
// shape: an Apply helper plus policy types, NOT a method on Trail).
func retentionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	logGo := "package audit\n\nimport (\n\t\"sync\"\n\t\"time\"\n)\n\n// Event is one audit record.\ntype Event struct {\n\tAt      time.Time\n\tActor   string\n\tAction  string\n\tObject  string\n\tOutcome string\n}\n\n// Trail stores events in memory.\ntype Trail struct {\n\tmu     sync.Mutex\n\tevents []Event\n\tclock  func() time.Time\n}\n\n// NewTrail wires an empty audit trail.\nfunc NewTrail() *Trail {\n\treturn &Trail{clock: time.Now}\n}\n\n// Record appends one event.\nfunc (t *Trail) Record(actor, action, object, outcome string) {\n\tt.mu.Lock()\n\tdefer t.mu.Unlock()\n\tt.events = append(t.events, Event{\n\t\tAt: t.clock(), Actor: actor, Action: action,\n\t\tObject: object, Outcome: outcome,\n\t})\n}\n\n// Since returns events recorded after the given time.\nfunc (t *Trail) Since(cutoff time.Time) []Event {\n\tt.mu.Lock()\n\tdefer t.mu.Unlock()\n\tvar out []Event\n\tfor _, e := range t.events {\n\t\tif e.At.After(cutoff) {\n\t\t\tout = append(out, e)\n\t\t}\n\t}\n\treturn out\n}\n\n// Count returns how many events are stored.\nfunc (t *Trail) Count() int {\n\tt.mu.Lock()\n\tdefer t.mu.Unlock()\n\treturn len(t.events)\n}\n"
	// Task A's ACTUAL output in this repo's fixture: a package-level Apply
	// helper plus RetentionPolicy, not a Trail method. Capture must record
	// what exists, never assume a pure reusable helper.
	retentionGo := "package audit\n\nimport \"time\"\n\n// RetentionPolicy bounds how long audit events are kept.\ntype RetentionPolicy struct {\n\tMaxAge   time.Duration\n\tMaxCount int\n}\n\n// DefaultRetention keeps 30 days of events capped at ten thousand.\nvar DefaultRetention = RetentionPolicy{MaxAge: 30 * 24 * time.Hour, MaxCount: 10000}\n\n// Apply drops events outside the retention policy and returns how\n// many remain.\nfunc Apply(t *Trail, p RetentionPolicy, now time.Time) int {\n\tkept := t.Since(now.Add(-p.MaxAge))\n\tif len(kept) > p.MaxCount {\n\t\tkept = kept[len(kept)-p.MaxCount:]\n\t}\n\treturn len(kept)\n}\n"
	for rel, body := range map[string]string{
		"internal/audit/log.go":       logGo,
		"internal/audit/retention.go": retentionGo,
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestRetentionVerticalSlice runs the offline mechanism gate: natural Task A
// capture, restart, Task B needs, and the three-condition comparison. The
// exit gate asks for ONE natural A-to-B fixture with an actually eliminated
// discovery operation and independent correctness retained.
func TestRetentionVerticalSlice(t *testing.T) {
	// --- Task A: a verified run changed retention.go and its test. ---
	workspaceA := retentionFixture(t)
	if err := os.WriteFile(filepath.Join(workspaceA, "internal", "audit", "retention_test.go"),
		[]byte("package audit\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestApplyCutoffAndCap(t *testing.T) {\n\ttrail := NewTrail()\n\tnow := time.Now()\n\tfor i := 0; i < 5; i++ {\n\t\ttrail.Record(\"u\", \"read\", \"o\", \"ok\")\n\t}\n\tgot := Apply(trail, RetentionPolicy{MaxAge: time.Hour, MaxCount: 3}, now)\n\tif got != 3 {\n\t\tt.Fatalf(\"got %d want 3\", got)\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake, client := newFakeSidecar(t)
	taskA := SliceTaskA{
		ChangedFiles: []string{"internal/audit/retention.go", "internal/audit/retention_test.go"},
		Verification: captureVerification{
			TestStageRan:      true,
			TestsExecuted:     2,
			TestsFailed:       0,
			TestCommand:       "go test ./internal/audit/...",
			EnvironmentStdLib: true,
			ObservedResult:    "executed 2 test(s), 0 failed",
		},
		Revision: "rev-task-a",
		RunID:    "run-a-001",
	}
	ids, err := CaptureTaskA(context.Background(), client, workspaceA, taskA)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("Task A captured nothing")
	}

	// --- Restart boundary: reload the records from the stored node
	// metadata. Nothing after this line touches the original captures. ---
	var saved []nodeWithRecord
	for id, node := range fake.nodes {
		if node.MetadataJSON == nil {
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(*node.MetadataJSON), &meta); err != nil {
			t.Fatalf("node %d metadata: %v", id, err)
		}
		rec := parseReuseRecord(meta)
		if rec == nil {
			continue
		}
		saved = append(saved, nodeWithRecord{NodeID: id, Record: rec})
	}
	if len(saved) == 0 {
		t.Fatal("restart lost every record")
	}

	// --- Task B: the target task on a FRESH worktree with the SAME bytes
	// (the eval harness commits Task A's tree; the worktree identity is
	// the committed revision naming those bytes). ---
	workspaceB := retentionFixture(t)
	intentB := "Add RetentionDeficit(trail *Trail, maxAge time.Duration, maxCount int) int in internal/audit/retention.go that reuses the EnforceRetention cutoff and cap rules without mutating the trail. Reference the Apply policy in internal/audit/retention.go."

	needs := deriveContextNeeds(intentB, workspaceB, nil, nil)
	// Retrieval: match each saved record to a Task B need by subject (the
	// deterministic stand-in for the graph retrieval hop; the records are
	// exported from the runtime, never regenerated by the evaluator).
	records := matchRecordsToNeeds(saved, needs)
	results := RunRetentionSlice(intentB, workspaceB, records, needs)
	if len(results) != 3 {
		t.Fatalf("conditions = %d, want 3", len(results))
	}
	byCond := map[SliceCondition]SliceResult{}
	for _, r := range results {
		byCond[r.Condition] = r
	}

	// Improved cold: the baseline plan.
	cold := byCond[ConditionImprovedCold]
	if len(cold.AfterOps) == 0 {
		t.Fatal("cold plan empty")
	}

	// Automatic: the mechanism gate needs at least one structurally
	// eliminated discovery operation with correctness retained
	// independently (the fixture verifier, not the record).
	auto := byCond[ConditionAutomatic]
	t.Logf("slice trace:\n%s", sliceTraceJSON(results))
	if len(auto.Eliminated) == 0 {
		// The honest outcome is reported either way; this fixture SHOULD
		// eliminate the retention.go read or symbol lookup because Task A
		// captured exactly that file's bytes and Task B needs them.
		t.Fatalf("mechanism gate unmet: no eliminated operation. trace:\n%s", sliceTraceJSON(results))
	}
	// Correctness is independent of the memory path: the fixture's own
	// verifier (validate/_gold-a, _gold-b scripts) judges Task B's output.
	// The slice records what was eliminated, never certifies behavior.
	if auto.IncrementalBenefit == false {
		t.Fatal("elimination recorded without incremental benefit flag")
	}
	// No-hit hygiene: the cold plan and the automatic plan differ ONLY by
	// the recorded eliminations and bounded delivery.
	for _, op := range cold.AfterOps {
		eliminated := false
		for _, e := range auto.Eliminated {
			if e == op {
				eliminated = true
				break
			}
		}
		if eliminated {
			continue
		}
		found := false
		for _, a := range auto.AfterOps {
			if a == op {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("cold op %s vanished from the warm plan without a recorded elimination", op)
		}
	}
	// The listing survives unless its exclusion carries its own
	// justification.
	listKept := false
	for _, a := range auto.AfterOps {
		if a == "list:workspace" {
			listKept = true
		}
	}
	if !listKept {
		justified := false
		for _, p := range auto.PolicyDecisions {
			if p != "" {
				justified = true
			}
		}
		if !justified {
			t.Fatal("listing dropped without recorded justification")
		}
	}
}

// matchRecordsToNeeds pairs each saved record with the Task B need whose
// subject the record addresses. This is the retrieval stand-in: subject
// equality only, no ranking tricks, and no record is invented.
func matchRecordsToNeeds(saved []nodeWithRecord, needs []ContextNeed) []nodeWithRecord {
	var out []nodeWithRecord
	for _, s := range saved {
		for _, n := range needs {
			if n.Kind == NeedOpenDiscovery {
				continue
			}
			if recordSpeaksOfSubject(s.Record, n.Subject) {
				out = append(out, nodeWithRecord{NodeID: s.NodeID, NeedID: n.ID, Record: s.Record})
				break
			}
		}
	}
	return out
}

// taskBNeed maps a reloaded record to the Task B need it can answer: the
// location need for the record's supporting subject. The manual condition
// uses the same mapping (information actually available from Task A).
func taskBNeed(_ []nodeWithRecord, rec *ReuseRecord) ContextNeed {
	subject := ""
	if len(rec.Supporting) > 0 {
		s := rec.Supporting[0]
		if s.Symbol != "" {
			subject = s.Path + "#" + s.Symbol
		} else {
			subject = s.Path
		}
	}
	return ContextNeed{
		ID:       needID("locate", subject),
		Kind:     NeedLocateNamedOperation,
		Subject:  subject,
		Origin:   NeedOriginTaskText,
		Required: true,
	}
}
