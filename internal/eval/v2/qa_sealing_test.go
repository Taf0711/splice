package v2

import (
	"strings"
	"testing"
)

// validIsolation builds a conforming isolation spec: the mandated hidden
// roots plus a grader-suffix artifact, disjoint from the visible set.
func validIsolation(taskID string) GraderIsolationSpec {
	return GraderIsolationSpec{
		TaskID:            taskID,
		HiddenPaths:       []string{"checks", "reference", "solution.reference", "fixture.expected"},
		AgentVisiblePaths: []string{"src", "README.md"},
	}
}

// validAcceptance builds a gate-conforming acceptance record for spec. The
// carried hash is the canonical content hash of the spec.
func validAcceptance(candidateID string, spec TaskSpec) TaskAcceptance {
	hash, _ := CanonicalTaskHash(spec)
	return TaskAcceptance{
		CandidateID:   candidateID,
		TaskID:        spec.ID,
		ContentSHA256: hash,
		QA: RunQAEvidence{
			FixtureHashMatchConfirmed:        true,
			BaselineCommand:                  "go test ./...",
			CheckRunSHA256:                   testHash,
			ReferenceSolutionPassConfirmed:   true,
			IndependentSolutionPassConfirmed: true,
			ProbePassed:                      true,
			GraderIsolation:                  validIsolation(spec.ID),
		},
	}
}

// TestCanonicalTaskHash pins the single content-identity helper: stable
// across calls, equal for equal content, different for different content.
func TestCanonicalTaskHash(t *testing.T) {
	task := validTask("task-a")
	first, err := CanonicalTaskHash(task)
	if err != nil {
		t.Fatalf("CanonicalTaskHash: %v", err)
	}
	second, err := CanonicalTaskHash(task)
	if err != nil {
		t.Fatalf("CanonicalTaskHash: %v", err)
	}
	if first != second {
		t.Fatalf("canonical hash not stable: %s != %s", first, second)
	}
	changed := validTask("task-a")
	changed.Language = "rust"
	third, err := CanonicalTaskHash(changed)
	if err != nil {
		t.Fatalf("CanonicalTaskHash: %v", err)
	}
	if third == first {
		t.Fatal("different content produced the same canonical hash")
	}
}

// TA1: a status transition must never change the content hash.
func TestTA1RegistryRejectsHashChangeAcrossTransition(t *testing.T) {
	reg := &CandidateRegistry{Entries: []CandidateEntry{
		{CandidateID: "task-a", RegisteredAtRFC3339: "2026-08-24T00:00:00Z", ContentSHA256: strings.Repeat("a", 64), Status: CandidateStatusRegistered},
		{CandidateID: "task-a", RegisteredAtRFC3339: "2026-08-24T01:00:00Z", ContentSHA256: strings.Repeat("b", 64), Status: CandidateStatusAccepted},
	}}
	err := reg.Validate()
	if err == nil || !strings.Contains(err.Error(), "task-a") {
		t.Fatalf("hash-changing transition accepted: %v", err)
	}
}

// TB1: acceptance must validate the full registry history first.
func TestTB1AcceptanceRefusesInvalidHistory(t *testing.T) {
	// registered -> accepted -> registered is an invalid history: accepted
	// is terminal. The old gate accepted it and appended a second acceptance.
	reg := &CandidateRegistry{Entries: []CandidateEntry{
		{CandidateID: "task-a", RegisteredAtRFC3339: "2026-08-24T00:00:00Z", ContentSHA256: testHash, Status: CandidateStatusRegistered},
		{CandidateID: "task-a", RegisteredAtRFC3339: "2026-08-24T01:00:00Z", ContentSHA256: testHash, Status: CandidateStatusAccepted},
		{CandidateID: "task-a", RegisteredAtRFC3339: "2026-08-24T02:00:00Z", ContentSHA256: testHash, Status: CandidateStatusRegistered},
	}}
	spec := validTask("task-a")
	err := AcceptTask(reg, spec, validAcceptance("task-a", spec), nil)
	if err == nil || !strings.Contains(err.Error(), "task-a") {
		t.Fatalf("invalid history accepted: %v", err)
	}
}

// TC1: acceptance binds the candidate content hash.
func TestTC1AcceptanceBindsCandidateContent(t *testing.T) {
	spec := validTask("task-a")
	canonical, _ := CanonicalTaskHash(spec)

	t.Run("matching content succeeds and records the hash", func(t *testing.T) {
		reg := &CandidateRegistry{}
		if err := reg.Register("cand-a", canonical); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := AcceptTask(reg, spec, validAcceptance("cand-a", spec), nil); err != nil {
			t.Fatalf("acceptance failed: %v", err)
		}
		latest, ok := reg.Latest("cand-a")
		if !ok || latest.Status != CandidateStatusAccepted || latest.ContentSHA256 != canonical {
			t.Fatalf("acceptance not recorded with bound hash: %+v", latest)
		}
	})

	t.Run("mismatched registered content fails", func(t *testing.T) {
		reg := &CandidateRegistry{}
		if err := reg.Register("cand-a", strings.Repeat("e", 64)); err != nil {
			t.Fatalf("register: %v", err)
		}
		err := AcceptTask(reg, spec, validAcceptance("cand-a", spec), nil)
		if err == nil || !strings.Contains(err.Error(), "registered hash") {
			t.Fatalf("mismatched registered content accepted: %v", err)
		}
		latest, _ := reg.Latest("cand-a")
		if latest.Status != CandidateStatusRegistered {
			t.Fatalf("partial acceptance: %+v", latest)
		}
	})

	t.Run("carried hash must match canonical content", func(t *testing.T) {
		reg := &CandidateRegistry{}
		if err := reg.Register("cand-a", canonical); err != nil {
			t.Fatalf("register: %v", err)
		}
		qa := validAcceptance("cand-a", spec)
		qa.ContentSHA256 = strings.Repeat("f", 64)
		err := AcceptTask(reg, spec, qa, nil)
		if err == nil || !strings.Contains(err.Error(), "canonical hash") {
			t.Fatalf("forged carried hash accepted: %v", err)
		}
	})
}

// TD1: acceptance refuses an unsealed spec.
func TestTD1AcceptanceRefusesUnsealedSpec(t *testing.T) {
	spec := validTask("task-a")
	canonical, _ := CanonicalTaskHash(spec)
	reg := &CandidateRegistry{}
	if err := reg.Register("cand-a", canonical); err != nil {
		t.Fatalf("register: %v", err)
	}
	unsealed := spec
	unsealed.Sealed = false
	unsealed.ReferenceSolutionSHA256 = ""
	unsealed.IndependentSolutionSHA256 = ""
	unsealed.MutationProbeSHA256 = ""
	err := AcceptTask(reg, unsealed, validAcceptance("cand-a", spec), nil)
	if err == nil || !strings.Contains(err.Error(), "not sealed") {
		t.Fatalf("unsealed spec accepted: %v", err)
	}
}

// TE1: a forged manifest task hash fails locked validation with the task
// named.
func TestTE1LockedManifestRecomputesTaskHashes(t *testing.T) {
	m := validManifest()
	m.TaskHashes[0].SHA256 = strings.Repeat("f", 64)
	err := m.ValidateLocked()
	if err == nil || !strings.Contains(err.Error(), "task-a") {
		t.Fatalf("forged hash accepted: %v", err)
	}
}

// TF1: sealed tasks require complete QA evidence collections.
func TestTF1SealedTaskRequiresQAEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*TaskSpec)
		want   string
	}{
		{"forbidden_changed_files", func(t *TaskSpec) { t.ForbiddenChangedFiles = nil }, "forbidden_changed_files"},
		{"context_checks.required_files", func(t *TaskSpec) { t.ContextChecks.RequiredFiles = nil }, "required_files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := validTask("task-a")
			tc.mutate(&task)
			err := task.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TG1: unsealed candidates may omit all three solution hashes together;
// partial presence fails; sealed tasks may not omit any.
func TestTG1UnsealedCandidateSolutionHashRules(t *testing.T) {
	unseal := func(task TaskSpec) TaskSpec {
		task.Sealed = false
		task.Author, task.Auditor, task.ApprovalDate = "", "", ""
		return task
	}

	t.Run("all three omitted passes", func(t *testing.T) {
		task := unseal(validTask("task-a"))
		task.ReferenceSolutionSHA256 = ""
		task.IndependentSolutionSHA256 = ""
		task.MutationProbeSHA256 = ""
		if err := task.Validate(); err != nil {
			t.Fatalf("candidate rejected: %v", err)
		}
	})

	t.Run("partially present still fails", func(t *testing.T) {
		task := unseal(validTask("task-a"))
		task.IndependentSolutionSHA256 = ""
		task.MutationProbeSHA256 = ""
		// reference_solution_sha256 remains set: 1 of 3 is inconsistent.
		err := task.Validate()
		if err == nil || !strings.Contains(err.Error(), "all present or all omitted") {
			t.Fatalf("partial solution hashes accepted: %v", err)
		}
	})

	t.Run("sealed with omissions still fails", func(t *testing.T) {
		task := validTask("task-a")
		task.IndependentSolutionSHA256 = ""
		err := task.Validate()
		if err == nil || !strings.Contains(err.Error(), "solution hashes") {
			t.Fatalf("sealed task with omitted solution hash accepted: %v", err)
		}
	})
}

// TH1: the baseline command must be a real command after trimming.
func TestTH1BaselineCommandMustBeReal(t *testing.T) {
	spec := validTask("task-a")
	canonical, _ := CanonicalTaskHash(spec)

	t.Run("whitespace baseline fails", func(t *testing.T) {
		reg := &CandidateRegistry{}
		if err := reg.Register("cand-a", canonical); err != nil {
			t.Fatalf("register: %v", err)
		}
		qa := validAcceptance("cand-a", spec)
		qa.QA.BaselineCommand = "   "
		err := AcceptTask(reg, spec, qa, nil)
		if err == nil || !strings.Contains(err.Error(), "baseline command") {
			t.Fatalf("whitespace baseline accepted: %v", err)
		}
	})

	t.Run("trimmed non-empty passes", func(t *testing.T) {
		reg := &CandidateRegistry{}
		if err := reg.Register("cand-a", canonical); err != nil {
			t.Fatalf("register: %v", err)
		}
		qa := validAcceptance("cand-a", spec)
		qa.QA.BaselineCommand = "  go test ./...  "
		if err := AcceptTask(reg, spec, qa, nil); err != nil {
			t.Fatalf("trimmed baseline rejected: %v", err)
		}
	})
}

// TI1: the mutation probe description must state what the mutation breaks.
func TestTI1MutationProbeDescriptionContract(t *testing.T) {
	t.Run("hello rejected", func(t *testing.T) {
		probe := MutationProbe{TaskID: "task-a", ProbeDescription: "hello", ExpectedCheckResult: MutationResultMustFailOnMutant}
		if err := probe.Validate(); err == nil {
			t.Fatal("label accepted as a mutation description")
		}
	})

	t.Run("spec-conforming description passes", func(t *testing.T) {
		probe := MutationProbe{TaskID: "task-a",
			ProbeDescription:    "reverses the authorization check so an invalid token is accepted",
			ExpectedCheckResult: MutationResultMustFailOnMutant}
		if err := probe.Validate(); err != nil {
			t.Fatalf("conforming description rejected: %v", err)
		}
	})
}

// TJ1: grader material nested under an agent-visible directory is detected.
func TestTJ1GraderIsolationDetectsNestedMaterial(t *testing.T) {
	spec := GraderIsolationSpec{
		TaskID:            "task-a",
		HiddenPaths:       []string{"checks", "reference", "src/task.expected"},
		AgentVisiblePaths: []string{"src"},
	}
	err := spec.Validate()
	if err == nil || !strings.Contains(err.Error(), "src/task.expected") {
		t.Fatalf("nested grader material not detected: %v", err)
	}
}
