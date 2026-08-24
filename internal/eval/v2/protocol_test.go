package v2

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validProtocol() Protocol {
	return Protocol{
		Family:       ProtocolFamily,
		Version:      ProtocolVersion,
		ExperimentID: "exp-2026-q3",
		Kind:         ExperimentPrimary,
		Mode:         RunModeClaim,
		Arms: []ArmSpec{
			{Name: ArmEmptyControl, DeliveredMemory: "none"},
			{Name: ArmHardPlacebo, DeliveredMemory: "matched_placebo"},
			{Name: ArmRelevantFrozen, DeliveredMemory: "relevant_frozen"},
		},
		Margins:         &DecisionMargins{DeltaSuccess: 0.05, MinTokenGainEmpty: 0.10, MinTokenGainPlacebo: 0.00, ConfidenceLevel: 0.95, AlphaTarget: 0.05},
		GateOrder:       LockedGateOrder(),
		IntervalMethod:  IntervalBCa,
		Resamples:       MinBootstrapResamples,
		SecondaryHolm:   true,
		Seed:            20260824,
		Repetitions:     6,
		HardSpendCapUSD: 400,
	}
}

func validSafetyProtocol() Protocol {
	p := validProtocol()
	p.Kind = ExperimentSafety
	p.Arms = []ArmSpec{
		{Name: ArmSafetyNone, DeliveredMemory: "none"},
		{Name: ArmSafetyCurrentOnly, DeliveredMemory: "current_only"},
		{Name: ArmSafetyStaleOnly, DeliveredMemory: "stale_only"},
		{Name: ArmSafetyCurrentThenStale, DeliveredMemory: "current_then_stale"},
		{Name: ArmSafetyStaleThenCurrent, DeliveredMemory: "stale_then_current"},
		{Name: ArmSafetyConflicting, DeliveredMemory: "conflicting_current"},
		{Name: ArmSafetyInstructionLike, DeliveredMemory: "instruction_like"},
	}
	p.Margins = nil
	p.SafetyMargins = &SafetyMargins{DeltaSuccess: 0.05, MaxStaleReferenceRate: 0.05,
		MaxForbiddenActionRate: 0.05, ConfidenceLevel: 0.95, AlphaTarget: 0.05}
	p.GateOrder = LockedSafetyGateOrder()
	return p
}

func validTask(id string) TaskSpec {
	task := TaskSpec{
		ID:                      id,
		Sealed:                  true,
		RepositoryClass:         "go-core",
		Language:                "go",
		Family:                  "pipeline",
		Tier:                    "light",
		Difficulty:              "medium",
		MemoryCompetency:        "api-conventions",
		PromptSHA256:            testHash,
		FixtureArchiveSHA256:    testHash,
		BaselineCommandSHA256:   testHash,
		SetupSHA256:             testHash,
		CheckSHA256:             testHash,
		ReferenceSolutionSHA256: strings.Repeat("b", 64),
		ExpectedChangedFiles:    []string{"main.go"},
		ForbiddenChangedFiles:   []string{"go.mod"},
		NetworkPolicy:           "offline",
		Author:                  "curator",
		Auditor:                 "auditor",
		ApprovalDate:            "2026-08-24T00:00:00Z",
	}
	task.ContextChecks.RequiredFiles = []string{"README.md"}
	return task
}

func validSchedule(p Protocol, taskIDs []string) Schedule {
	var s Schedule
	for _, task := range taskIDs {
		for _, arm := range p.Arms {
			for rep := 1; rep <= p.Repetitions; rep++ {
				s.Trials = append(s.Trials, TrialSpec{Key: TrialKey{
					ExperimentID: p.ExperimentID, TaskID: task, Arm: arm.Name,
					RepetitionID: rep, EnvironmentBlock: 1,
				}})
			}
		}
	}
	return s
}

func validManifest() Manifest {
	p := validProtocol()
	tasks := []TaskSpec{validTask("task-a"), validTask("task-b")}
	return Manifest{
		Protocol:          p,
		SourceCommit:      "f6c2bf7",
		SourceClean:       true,
		BinarySHA256:      testHash,
		ProtocolHash:      testHash,
		OS:                "darwin",
		Arch:              "arm64",
		HardwareLabel:     "owner-laptop",
		ToolchainVersions: []ToolchainVersion{{Name: "go", Version: "1.25"}},
		Sidecar:           SidecarIdentity{Commit: "abc123", BinarySHA256: testHash, Version: "0.4", Capabilities: []string{"trace_write", "memory_read"}},
		ProviderProfile:   "claim-default",
		StageRoutes: []StageRoute{
			{Stage: "code_writer", Provider: "openrouter", Model: "gpt-5.6-sol"},
			{Stage: "test_generator", Provider: "openrouter", Model: "gpt-5.6-sol"},
		},
		TaskHashes:             []NamedHash{{Name: "task-a", SHA256: testHash}, {Name: "task-b", SHA256: testHash}},
		FixtureSHA256:          testHash,
		SnapshotSHA256:         testHash,
		SelectionAuditSHA256:   testHash,
		CorpusProvenanceSHA256: testHash,
		PromptSchemaHash:       testHash,
		ToolHash:               testHash,
		TopologyHash:           testHash,
		CompactionHash:         testHash,
		BudgetHash:             testHash,
		AnalysisCodeHash:       testHash,
		RuleHashes: []NamedHash{{Name: "invalidation", SHA256: testHash}, {Name: "retry", SHA256: testHash},
			{Name: "stopping", SHA256: testHash}, {Name: "security-review", SHA256: testHash}},
		Tasks:            tasks,
		Schedule:         validSchedule(p, []string{"task-a", "task-b"}),
		SampleSize:       len(p.Arms) * p.Repetitions * 2,
		ExpectedCalls:    36,
		EstimatedCostUSD: 120,
	}
}

func uintp(n uint64) *uint64 { return &n }
func boolp(b bool) *bool     { return &b }

func validTelemetry() TelemetryRecord {
	total, input, cached, cacheWrite, output, reasoning := uint64(9000), uint64(7000), uint64(0), uint64(0), uint64(2000), uint64(0)
	pass, complete := true, true
	zero, wall, latency := uint64(0), int64(60000), int64(10)
	count := 0
	cost, webCost := 0.08, 0.0
	usage := TokenUsage{TotalTokens: &total, InputTokens: &input, CachedInputTokens: &cached,
		CacheWriteTokens: &cacheWrite, OutputTokens: &output, ReasoningTokens: &reasoning}
	request := ProviderRequestTelemetry{
		RequestID: "request-1", Stage: "code_writer", Usage: usage,
		WebSearchRequests: &zero, WallTimeMs: &wall,
		ProviderCostUSD: &cost, WebSearchCostUSD: &webCost, PricingCoverage: PricingFull,
	}
	fingerprints := StageFingerprints{PromptSHA256: testHash, ToolSHA256: testHash,
		SchemaSHA256: testHash, TopologySHA256: testHash, BudgetSHA256: testHash}
	stage := StageTelemetry{Route: StageRoute{Stage: "code_writer", Provider: "openrouter", Model: "gpt-5.6-sol"},
		Fingerprints: fingerprints, Requests: []ProviderRequestTelemetry{request},
		LatencyMs: &latency, ToolCallCount: &count, PermissionCount: &count,
		RepairCount: &count, CompactionCalls: &count, Abort: &pass, InterventionCount: &count}
	return TelemetryRecord{
		Source: TelemetrySourceTrace, RunID: "run-1", SessionID: "session-1",
		Stages: []StageTelemetry{stage}, Tokens: usage,
		SelectedMemoryIDs: []string{}, DeliveredMemoryIDs: []string{}, Dispositions: []MemoryDisposition{},
		DispositionsComplete: &complete, InvalidClaimCount: &count,
		DeterministicCheckPassed: &pass, DeterministicCheckOutputSHA256: testHash,
		RetryCount: &count, RepairCount: &count, CompactionCalls: &count,
		WallTimeMs: &wall, MemoryQueryLatencyMs: &latency,
		ProviderCostUSD: &cost, WebSearchCostUSD: &webCost, WebSearchRequests: &zero,
		WebSearchEngines: []string{}, PricingCoverage: PricingFull,
	}
}

func TestProtocolAcceptsValidDefinition(t *testing.T) {
	if err := validProtocol().Validate(); err != nil {
		t.Fatalf("valid protocol rejected: %v", err)
	}
	if err := validSafetyProtocol().Validate(); err != nil {
		t.Fatalf("valid safety protocol rejected: %v", err)
	}
}

func TestSafetyProtocolRejectsEfficacyShape(t *testing.T) {
	p := validSafetyProtocol()
	p.Arms = p.Arms[:6]
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "exactly 7 arms") {
		t.Fatalf("incomplete safety matrix accepted: %v", err)
	}
	p = validSafetyProtocol()
	p.SafetyMargins = nil
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "safety_margins") {
		t.Fatalf("missing safety margins accepted: %v", err)
	}
	p = validSafetyProtocol()
	p.Margins = &DecisionMargins{DeltaSuccess: 0.01}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "efficacy margins") {
		t.Fatalf("efficacy margins accepted in safety protocol: %v", err)
	}
	p = validSafetyProtocol()
	p.GateOrder = LockedGateOrder()
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "gate_order") {
		t.Fatalf("efficacy gate order accepted for safety: %v", err)
	}
}

func TestProtocolRejectsInvalid(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Protocol)
		wantSub string
	}{
		{"unknown family", func(p *Protocol) { p.Family = "splice.eval.v1" }, "family"},
		{"unknown version", func(p *Protocol) { p.Version = "splice.eval.v1" }, "version"},
		{"missing experiment id", func(p *Protocol) { p.ExperimentID = "" }, "experiment_id"},
		{"unknown kind", func(p *Protocol) { p.Kind = "exploratory" }, "invalid experiment kind"},
		{"unknown mode", func(p *Protocol) { p.Mode = "yolo" }, "invalid run mode"},
		{"unknown arm name", func(p *Protocol) { p.Arms[0].Name = "vibes" }, "unknown arm"},
		{"duplicate arms", func(p *Protocol) { p.Arms[1].Name = ArmEmptyControl }, "duplicate arm"},
		{"missing primary arm", func(p *Protocol) { p.Arms = p.Arms[:2] }, "exactly 3 arms"},
		{"arm without delivered memory shape", func(p *Protocol) { p.Arms[0].DeliveredMemory = "" }, "delivered_memory"},
		{"negative margin", func(p *Protocol) { p.Margins.DeltaSuccess = -0.01 }, "delta_success"},
		{"confidence out of range", func(p *Protocol) { p.Margins.ConfidenceLevel = 1.5 }, "confidence_level"},
		{"reordered gate sequence", func(p *Protocol) {
			p.GateOrder = []string{GateArtifactValidity, GateSampleCompleteness, GateNetEfficiency,
				GateSuccessNonInferiority, GateContentRelevance}
		}, "locked"},
		{"short gate sequence", func(p *Protocol) { p.GateOrder = p.GateOrder[:2] }, "exactly 5 gates"},
		{"unknown interval method", func(p *Protocol) { p.IntervalMethod = "vibes" }, "invalid interval method"},
		{"too few resamples", func(p *Protocol) { p.Resamples = 100 }, "resamples"},
		{"zero seed", func(p *Protocol) { p.Seed = 0 }, "seed"},
		{"zero repetitions", func(p *Protocol) { p.Repetitions = 0 }, "repetitions"},
		{"zero spend cap", func(p *Protocol) { p.HardSpendCapUSD = 0 }, "hard_spend_cap_usd"},
		{"primary cannot include safety arm", func(p *Protocol) {
			p.Arms = append(p.Arms, ArmSpec{Name: ArmSafetyNone, DeliveredMemory: "none"})
		}, "exactly 3 arms"},
		{"nonfinite spend cap", func(p *Protocol) { p.HardSpendCapUSD = math.Inf(1) }, "finite"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validProtocol()
			tc.mutate(&p)
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestTaskSetValidation(t *testing.T) {
	t.Run("rejects empty set", func(t *testing.T) {
		if err := (TaskSet{}).Validate(); err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rejects malformed approval date", func(t *testing.T) {
		task := validTask("t1")
		task.ApprovalDate = "tomorrow"
		if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "RFC3339") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rejects unsafe or duplicate file paths", func(t *testing.T) {
		task := validTask("t1")
		task.ExpectedChangedFiles = []string{"../main.go"}
		if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "relative workspace path") {
			t.Fatalf("err = %v", err)
		}
		task = validTask("t1")
		task.ExpectedChangedFiles = []string{"main.go", "main.go"}
		if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "duplicates") {
			t.Fatalf("err = %v", err)
		}
		for _, unsafe := range []string{" main.go", "main.go ", "dir\\file.go"} {
			task = validTask("t1")
			task.ExpectedChangedFiles = []string{unsafe}
			if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "canonical") {
				t.Fatalf("non-canonical path %q accepted: %v", unsafe, err)
			}
		}
		task = validTask("t1")
		task.ExpectedChangedFiles = []string{"main\x00.go"}
		if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "relative workspace path") {
			t.Fatalf("NUL path accepted: %v", err)
		}
	})
	t.Run("rejects overlapping context files", func(t *testing.T) {
		task := validTask("t1")
		task.ContextChecks.ForbiddenFiles = []string{"README.md"}
		if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "both required and forbidden") {
			t.Fatalf("overlap accepted: %v", err)
		}
	})
	t.Run("accepts sealed task", func(t *testing.T) {
		set := TaskSet{Tasks: []TaskSpec{validTask("t1"), validTask("t2")}}
		if err := set.Validate(); err != nil {
			t.Fatalf("valid set rejected: %v", err)
		}
	})
	t.Run("rejects duplicate task identity", func(t *testing.T) {
		set := TaskSet{Tasks: []TaskSpec{validTask("t1"), validTask("t1")}}
		if err := set.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate task id") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rejects missing grader hash on sealed task", func(t *testing.T) {
		task := validTask("t1")
		task.CheckSHA256 = ""
		err := task.Validate()
		if err == nil || !strings.Contains(err.Error(), "check_sha256") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rejects malformed hash", func(t *testing.T) {
		task := validTask("t1")
		task.PromptSHA256 = "deadbeef"
		if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "prompt_sha256") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("sealed task needs independent auditor", func(t *testing.T) {
		task := validTask("t1")
		task.Auditor = task.Author
		if err := task.Validate(); err == nil || !strings.Contains(err.Error(), "auditor") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unsealed candidate may omit approvers", func(t *testing.T) {
		task := validTask("t1")
		task.Sealed = false
		task.Author, task.Auditor, task.ApprovalDate = "", "", ""
		if err := (TaskSet{Tasks: []TaskSpec{task}}).Validate(); err != nil {
			t.Fatalf("candidate rejected: %v", err)
		}
	})
}

func TestScheduleValidation(t *testing.T) {
	p := validProtocol()
	tasks := []string{"task-a", "task-b"}

	t.Run("complete schedule passes", func(t *testing.T) {
		if err := validSchedule(p, tasks).CompleteFor(p, tasks); err != nil {
			t.Fatalf("complete schedule rejected: %v", err)
		}
	})
	t.Run("missing cell detected", func(t *testing.T) {
		s := validSchedule(p, tasks)
		s.Trials = s.Trials[1:]
		err := s.CompleteFor(p, tasks)
		if err == nil || !strings.Contains(err.Error(), "missing cell") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("extra cell detected", func(t *testing.T) {
		s := validSchedule(p, tasks)
		// A distinct extra cell has a unique identity, so Validate alone
		// cannot reject it; CompleteFor pins schedule completeness.
		s.Trials = append(s.Trials, TrialSpec{Key: TrialKey{
			ExperimentID: p.ExperimentID, TaskID: "task-a", Arm: ArmHardPlacebo,
			RepetitionID: 99, EnvironmentBlock: 1,
		}})
		if err := s.Validate(); err != nil {
			t.Fatalf("unique extra cell must pass identity validation: %v", err)
		}
		if err := s.CompleteFor(p, tasks); err == nil || !strings.Contains(err.Error(), "unexpected trial") {
			t.Fatalf("CompleteFor err = %v", err)
		}
	})
	t.Run("wrong arm detected", func(t *testing.T) {
		s := validSchedule(p, tasks)
		s.Trials[0].Key.Arm = ArmSafetyNone
		err := s.CompleteFor(p, tasks)
		if err == nil || !strings.Contains(err.Error(), "not declared") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("paired arms require one environment block", func(t *testing.T) {
		s := validSchedule(p, tasks)
		s.Trials[1].Key.EnvironmentBlock = 2
		err := s.CompleteFor(p, tasks)
		if err == nil || !strings.Contains(err.Error(), "environment blocks") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("nonpositive repetition invalid", func(t *testing.T) {
		s := Schedule{Trials: []TrialSpec{{Key: TrialKey{ExperimentID: "e", TaskID: "t", Arm: ArmEmptyControl, RepetitionID: 0}}}}
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "repetition_id") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestManifestLockedValidation(t *testing.T) {
	t.Run("fully locked manifest passes", func(t *testing.T) {
		if err := validManifest().ValidateLocked(); err != nil {
			t.Fatalf("valid manifest rejected: %v", err)
		}
	})
	badCases := []struct {
		name    string
		mutate  func(*Manifest)
		wantSub string
	}{
		{"dirty tree", func(m *Manifest) { m.SourceClean = false }, "clean source tree"},
		{"missing binary hash", func(m *Manifest) { m.BinarySHA256 = "" }, "binary_sha256"},
		{"missing route map", func(m *Manifest) { m.StageRoutes = nil }, "resolved stage routes"},
		{"route missing model", func(m *Manifest) { m.StageRoutes[0].Model = "" }, "routes[0]"},
		{"sidecar without capabilities", func(m *Manifest) { m.Sidecar.Capabilities = nil }, "capability set"},
		{"sidecar hash malformed", func(m *Manifest) { m.Sidecar.BinarySHA256 = "xyz" }, "sidecar identity"},
		{"unsealed task in lock", func(m *Manifest) { m.Tasks[0].Sealed = false },
			"is not sealed"},
		{"rule hashes absent", func(m *Manifest) { m.RuleHashes = nil }, "rule hashes"},
		{"required rule hash absent", func(m *Manifest) { m.RuleHashes = m.RuleHashes[:1] }, "rule hashes"},
		{"extra rule hash rejected", func(m *Manifest) { m.RuleHashes = append(m.RuleHashes, NamedHash{Name: "other", SHA256: testHash}) }, "exactly four"},
		{"sample size drift", func(m *Manifest) { m.SampleSize = 3 }, "sample_size"},
		{"schedule cell dropped", func(m *Manifest) { m.Schedule.Trials = m.Schedule.Trials[:len(m.Schedule.Trials)-1] },
			"missing cell"},
		{"cost over cap", func(m *Manifest) { m.EstimatedCostUSD = 100000 }, "spend cap"},
		{"nonfinite estimated cost", func(m *Manifest) { m.EstimatedCostUSD = math.NaN() }, "finite"},
		{"no expected calls", func(m *Manifest) { m.ExpectedCalls = 0 }, "expected_calls"},
		{"selection audit absent", func(m *Manifest) { m.SelectionAuditSHA256 = "zz" }, "selection_audit_sha256"},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.mutate(&m)
			err := m.ValidateLocked()
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func uint64p(n uint64) *uint64  { return &n }
func intptr(n int) *int         { return &n }
func floatp(f float64) *float64 { return &f }

func TestTelemetryRecordValidation(t *testing.T) {
	t.Run("complete record passes", func(t *testing.T) {
		r := validTelemetry()
		if err := r.Validate(); err != nil {
			t.Fatalf("valid telemetry rejected: %v", err)
		}
	})
	t.Run("missing total is never zero-filled", func(t *testing.T) {
		r := validTelemetry()
		r.Tokens.TotalTokens = nil
		err := r.Validate()
		if err == nil || !strings.Contains(err.Error(), "never zero-filled") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("total must equal input plus output", func(t *testing.T) {
		r := validTelemetry()
		r.Tokens.TotalTokens = uint64p(1)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "input plus output") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("cache subsets are disjoint", func(t *testing.T) {
		r := validTelemetry()
		r.Tokens.CachedInputTokens = uint64p(5000)
		r.Tokens.CacheWriteTokens = uint64p(3000)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "cache write tokens") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("cached subset cannot exceed input", func(t *testing.T) {
		r := validTelemetry()
		r.Tokens.CachedInputTokens = uint64p(999999)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "cached input tokens") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("reasoning subset cannot exceed output", func(t *testing.T) {
		r := validTelemetry()
		r.Tokens.ReasoningTokens = uint64p(5000)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "reasoning tokens") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("absent routes fail loudly", func(t *testing.T) {
		r := validTelemetry()
		r.Stages = nil
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "stage telemetry") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("incomplete dispositions need a claim count", func(t *testing.T) {
		r := validTelemetry()
		r.DispositionsComplete = boolp(false)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "dispositions_complete") {
			t.Fatalf("err = %v", err)
		}
		r.InvalidClaimCount = intptr(2)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "dispositions_complete") {
			t.Fatalf("incomplete dispositions accepted: %v", err)
		}
	})
	t.Run("unknown source rejected", func(t *testing.T) {
		r := validTelemetry()
		r.Source = "vibes"
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "source") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("aggregate request fields must reconcile", func(t *testing.T) {
		r := validTelemetry()
		request := &r.Stages[0].Requests[0]
		request.Usage.CachedInputTokens = uint64p(1)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "aggregate cached_input_tokens") {
			t.Fatalf("cached-input aggregate mismatch accepted: %v", err)
		}

		r = validTelemetry()
		request = &r.Stages[0].Requests[0]
		request.ProviderCostUSD = floatp(0.09)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "provider cost") {
			t.Fatalf("cost aggregate mismatch accepted: %v", err)
		}

		r = validTelemetry()
		request = &r.Stages[0].Requests[0]
		request.PricingCoverage = PricingNone
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "pricing coverage") {
			t.Fatalf("pricing aggregate mismatch accepted: %v", err)
		}

		r = validTelemetry()
		request = &r.Stages[0].Requests[0]
		request.WebSearchRequests = uint64p(1)
		request.WebSearchEngine = "exa"
		r.WebSearchRequests = uint64p(1)
		r.WebSearchEngines = []string{"wrong"}
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "engine") {
			t.Fatalf("web-search engine aggregate mismatch accepted: %v", err)
		}

		r = validTelemetry()
		r.Stages[0].RepairCount = intptr(1)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "repair_count") {
			t.Fatalf("repair aggregate mismatch accepted: %v", err)
		}
	})
}

func TestTrialResultValidation(t *testing.T) {
	key := TrialKey{ExperimentID: "e", TaskID: "t", Arm: ArmRelevantFrozen, RepetitionID: 1, EnvironmentBlock: 1}
	tel := validTelemetry()

	t.Run("valid result requires telemetry", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialValid, ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "telemetry") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("valid result with telemetry passes", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialValid, CheckPassed: true, Telemetry: &tel, DeterministicCheckOutputSHA256: testHash, ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err != nil {
			t.Fatalf("rejected: %v", err)
		}
	})
	t.Run("invalid result names its failure rule", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialInvalid, FailureRuleID: FailureRuleInvalidation, ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err != nil {
			t.Fatalf("rejected: %v", err)
		}
		missing := r
		missing.FailureRuleID = ""
		if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "failure rule") {
			t.Fatalf("missing failure rule accepted: %v", err)
		}
	})
	t.Run("security invalid names its rule too", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialSecurityInvalid, FailureRuleID: FailureRuleSecurityReview, ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err != nil {
			t.Fatalf("rejected: %v", err)
		}
		missing := r
		missing.FailureRuleID = "security"
		if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "security-review") {
			t.Fatalf("wrong security rule accepted: %v", err)
		}
	})
	t.Run("arbitrary infrastructure rule is rejected", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialInvalid, FailureRuleID: "provider-outage", ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "invalidation, retry, or stopping") {
			t.Fatalf("arbitrary failure rule accepted: %v", err)
		}
	})
	t.Run("valid result cannot carry a failure rule", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialValid, CheckPassed: true, FailureRuleID: "should-not-exist", Telemetry: &tel, DeterministicCheckOutputSHA256: testHash, ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "failure rule") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("check result must match telemetry", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialValid, CheckPassed: false, Telemetry: &tel, DeterministicCheckOutputSHA256: testHash, ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "does not match telemetry") {
			t.Fatalf("mismatched check result accepted: %v", err)
		}
	})
	t.Run("deterministic failure remains a valid product outcome", func(t *testing.T) {
		failureTelemetry := tel
		passed := false
		failureTelemetry.DeterministicCheckPassed = &passed
		r := TrialResult{Key: key, Status: TrialValid, CheckPassed: false, Telemetry: &failureTelemetry,
			DeterministicCheckOutputSHA256: testHash, ChangedFileHashes: []NamedHash{}}
		if err := r.Validate(); err != nil {
			t.Fatalf("valid deterministic failure rejected: %v", err)
		}
	})
	t.Run("changed file hash names are canonical paths", func(t *testing.T) {
		r := TrialResult{Key: key, Status: TrialInvalid, FailureRuleID: FailureRuleInvalidation,
			ChangedFileHashes: []NamedHash{{Name: "../main.go", SHA256: testHash}}}
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "canonical relative workspace path") {
			t.Fatalf("unsafe changed-file path accepted: %v", err)
		}
	})
	t.Run("context rejects an opposite-family arm", func(t *testing.T) {
		safety := validSafetyProtocol()
		r := TrialResult{Key: key, Status: TrialValid, CheckPassed: true, Telemetry: &tel, DeterministicCheckOutputSHA256: testHash, ChangedFileHashes: []NamedHash{}}
		r.Key.ExperimentID = safety.ExperimentID
		if err := r.ValidateFor(safety); err == nil || !strings.Contains(err.Error(), "not declared") {
			t.Fatalf("primary arm accepted by safety protocol: %v", err)
		}
	})
}

func TestAnalysisPlanAndReport(t *testing.T) {
	p := validProtocol()
	t.Run("matching plan passes", func(t *testing.T) {
		plan := AnalysisPlan{IntervalMethod: p.IntervalMethod, ConfidenceLevel: p.Margins.ConfidenceLevel,
			Resamples: p.Resamples, Seed: p.Seed, GateOrder: LockedGateOrder(), SecondaryHolm: true,
			Sensitivity: LockedEfficacySensitivityChecks()}
		if err := plan.Validate(p); err != nil {
			t.Fatalf("plan rejected: %v", err)
		}
	})
	t.Run("drifted resamples rejected", func(t *testing.T) {
		plan := AnalysisPlan{IntervalMethod: p.IntervalMethod, ConfidenceLevel: p.Margins.ConfidenceLevel,
			Resamples: p.Resamples + 1, Seed: p.Seed, GateOrder: LockedGateOrder(), SecondaryHolm: true,
			Sensitivity: LockedEfficacySensitivityChecks()}
		if err := plan.Validate(p); err == nil || !strings.Contains(err.Error(), "resamples") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("drifted seed rejected", func(t *testing.T) {
		plan := AnalysisPlan{IntervalMethod: p.IntervalMethod, ConfidenceLevel: p.Margins.ConfidenceLevel,
			Resamples: p.Resamples, Seed: p.Seed + 1, GateOrder: LockedGateOrder(), SecondaryHolm: true,
			Sensitivity: LockedEfficacySensitivityChecks()}
		if err := plan.Validate(p); err == nil || !strings.Contains(err.Error(), "seed") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("duplicate sensitivity check rejected", func(t *testing.T) {
		plan := AnalysisPlan{IntervalMethod: p.IntervalMethod, ConfidenceLevel: p.Margins.ConfidenceLevel,
			Resamples: p.Resamples, Seed: p.Seed, GateOrder: LockedGateOrder(), SecondaryHolm: true,
			Sensitivity: []SensitivityCheck{SensitivityRepositoryDriver, SensitivityRepositoryDriver}}
		if err := plan.Validate(p); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("efficacy report needs gates", func(t *testing.T) {
		r := Report{ExperimentID: p.ExperimentID, Kind: ExperimentPrimary, ManifestHash: testHash, Verdict: VerdictEfficacySupported}
		if err := r.ValidateFor(p); err == nil || !strings.Contains(err.Error(), "gate outcomes") {
			t.Fatalf("err = %v", err)
		}
		gates := []GateOutcome{
			{Name: GateArtifactValidity, Passed: true, Estimate: 1, LowerBound: 1, UpperBound: 1, ConfidenceLevel: 0.95},
			{Name: GateSampleCompleteness, Passed: true, Estimate: 1, LowerBound: 1, UpperBound: 1, ConfidenceLevel: 0.95},
			{Name: GateSuccessNonInferiority, Passed: true, Estimate: 0, LowerBound: -0.01, UpperBound: 0.01, ConfidenceLevel: 0.95},
			{Name: GateNetEfficiency, Passed: true, Estimate: 0.8, LowerBound: 0.7, UpperBound: 0.89, ConfidenceLevel: 0.95},
			{Name: GateContentRelevance, Passed: true, Estimate: 0.9, LowerBound: 0.8, UpperBound: 0.95, ConfidenceLevel: 0.95},
		}
		r.Gates = gates
		if err := r.ValidateFor(p); err != nil {
			t.Fatalf("report with gates rejected: %v", err)
		}
		gates[4].Passed = false
		gates[4].UpperBound = 1.1
		if err := (Report{ExperimentID: p.ExperimentID, Kind: ExperimentPrimary, ManifestHash: testHash,
			Verdict: VerdictEfficacySupported, Gates: gates}).ValidateFor(p); err == nil || !strings.Contains(err.Error(), "every gate") {
			t.Fatalf("failed support gate accepted: %v", err)
		}
		gates[4].Passed = true
		gates[4].UpperBound = 0.95
		if err := (Report{ExperimentID: p.ExperimentID, Kind: ExperimentPrimary, ManifestHash: testHash,
			Verdict: VerdictEfficacyNotSupported, Gates: gates}).ValidateFor(p); err == nil || !strings.Contains(err.Error(), "contradicts") {
			t.Fatalf("negative all-passed report accepted: %v", err)
		}
		gates[2].LowerBound = -0.1
		if err := (Report{ExperimentID: p.ExperimentID, Kind: ExperimentPrimary, ManifestHash: testHash,
			Verdict: VerdictEfficacySupported, Gates: gates}).ValidateFor(p); err == nil || !strings.Contains(err.Error(), "threshold") {
			t.Fatalf("support report with a failed locked threshold accepted: %v", err)
		}
	})
	t.Run("safety plan uses safety diagnostics", func(t *testing.T) {
		safety := validSafetyProtocol()
		plan := AnalysisPlan{IntervalMethod: safety.IntervalMethod, ConfidenceLevel: safety.SafetyMargins.ConfidenceLevel,
			Resamples: safety.Resamples, Seed: safety.Seed, GateOrder: LockedSafetyGateOrder(), SecondaryHolm: true,
			Sensitivity: LockedSafetySensitivityChecks()}
		if err := plan.Validate(safety); err != nil {
			t.Fatalf("safety plan rejected: %v", err)
		}
		plan.Sensitivity = LockedEfficacySensitivityChecks()
		if err := plan.Validate(safety); err == nil || !strings.Contains(err.Error(), "safety analysis") {
			t.Fatalf("efficacy diagnostics accepted for safety plan: %v", err)
		}
	})
	t.Run("excluded trials require protocol-family context", func(t *testing.T) {
		excluded := TrialResult{Key: TrialKey{ExperimentID: p.ExperimentID, TaskID: "task-a", Arm: ArmSafetyNone,
			RepetitionID: 1, EnvironmentBlock: 1}, Status: TrialInvalid, FailureRuleID: FailureRuleInvalidation,
			ChangedFileHashes: []NamedHash{}}
		r := Report{ExperimentID: p.ExperimentID, Kind: ExperimentPrimary, ManifestHash: testHash,
			Verdict: VerdictInvalid, ExcludedTrials: []TrialResult{excluded}}
		if err := r.ValidateFor(p); err == nil || !strings.Contains(err.Error(), "not declared") {
			t.Fatalf("opposite-family excluded trial accepted: %v", err)
		}
	})
	t.Run("integrity verdict may omit gates but not the manifest hash", func(t *testing.T) {
		r := Report{ExperimentID: "e", Kind: ExperimentPrimary, Verdict: VerdictIncomplete}
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "manifest_hash") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestManifestTaskHashPairing(t *testing.T) {
	m := validManifest()
	// Same count, but the second entry names a task that does not exist:
	// only the pairing rule can catch this.
	m.TaskHashes[1].Name = "task-zzz"
	err := m.ValidateLocked()
	if err == nil || !strings.Contains(err.Error(), "no entry for task task-b") {
		t.Fatalf("err = %v, want missing task-b pairing", err)
	}
}

func TestGenerateSchedule(t *testing.T) {
	p := validProtocol()
	tasks := []string{"task-a", "task-b", "task-c", "task-d"}

	first, err := GenerateSchedule(p, tasks)
	if err != nil {
		t.Fatalf("GenerateSchedule returned error: %v", err)
	}
	if err := first.CompleteFor(p, tasks); err != nil {
		t.Fatalf("generated schedule is incomplete: %v", err)
	}
	second, err := GenerateSchedule(p, tasks)
	if err != nil {
		t.Fatalf("repeat GenerateSchedule returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("equal inputs produced different schedules")
	}

	for repetition := 1; repetition <= p.Repetitions; repetition++ {
		blocks := make(map[int]map[string]bool)
		for _, trial := range first.Trials {
			if trial.Key.RepetitionID != repetition {
				continue
			}
			if blocks[trial.Key.EnvironmentBlock] == nil {
				blocks[trial.Key.EnvironmentBlock] = make(map[string]bool)
			}
			if blocks[trial.Key.EnvironmentBlock][trial.Key.Arm] {
				t.Fatalf("repetition %d block %d repeats arm %q", repetition, trial.Key.EnvironmentBlock, trial.Key.Arm)
			}
			blocks[trial.Key.EnvironmentBlock][trial.Key.Arm] = true
		}
		for block, arms := range blocks {
			if len(arms) != len(p.Arms) {
				t.Fatalf("repetition %d block %d has %d arms, want %d", repetition, block, len(arms), len(p.Arms))
			}
		}
	}

	changedSeed := p
	changedSeed.Seed++
	third, err := GenerateSchedule(changedSeed, tasks)
	if err != nil {
		t.Fatalf("changed-seed GenerateSchedule returned error: %v", err)
	}
	if reflect.DeepEqual(first, third) {
		t.Fatal("changed seed produced the same schedule")
	}
	changedOrder, err := GenerateSchedule(p, []string{"task-d", "task-b", "task-c", "task-a"})
	if err != nil {
		t.Fatalf("changed-order GenerateSchedule returned error: %v", err)
	}
	if reflect.DeepEqual(first, changedOrder) {
		t.Fatal("changed task order produced the same schedule")
	}
}

func TestGenerateScheduleRejectsTaskIDs(t *testing.T) {
	p := validProtocol()
	for _, tc := range []struct {
		name    string
		taskIDs []string
		want    string
	}{
		{name: "empty", taskIDs: []string{"task-a", ""}, want: "empty"},
		{name: "duplicate", taskIDs: []string{"task-a", "task-a"}, want: "duplicates"},
		{name: "arm collision", taskIDs: []string{ArmEmptyControl}, want: "collides"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := GenerateSchedule(p, tc.taskIDs); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestTrialJournalResumeAndCodec(t *testing.T) {
	p := validProtocol()
	schedule, err := GenerateSchedule(p, []string{"task-a", "task-b"})
	if err != nil {
		t.Fatalf("GenerateSchedule returned error: %v", err)
	}
	var journal TrialJournal
	for i, trial := range schedule.Trials[:3] {
		if err := journal.Append(JournalEntry{Key: trial.Key, PersistedAt: "2026-08-24T00:00:00Z", Status: "scheduled"}); err != nil {
			t.Fatalf("append scheduled entry %d: %v", i, err)
		}
	}
	incompleteBefore := IncompleteTrials(journal, schedule)
	if len(incompleteBefore) != len(schedule.Trials) {
		t.Fatalf("incomplete count after scheduled crash = %d, want %d", len(incompleteBefore), len(schedule.Trials))
	}
	if err := journal.Append(JournalEntry{Key: schedule.Trials[0].Key, PersistedAt: "2026-08-24T00:01:00Z", Status: "started"}); err != nil {
		t.Fatalf("status advance failed: %v", err)
	}
	if err := journal.Append(JournalEntry{Key: schedule.Trials[0].Key, PersistedAt: "2026-08-24T00:02:00Z", Status: "scheduled"}); err == nil || !strings.Contains(err.Error(), "regression") {
		t.Fatalf("status regression error = %v", err)
	}
	if err := journal.Append(JournalEntry{Key: schedule.Trials[0].Key, PersistedAt: "2026-08-24T00:03:00Z", Status: "started"}); err != nil {
		t.Fatalf("replayed status failed: %v", err)
	}
	if incomplete := IncompleteTrials(journal, schedule); len(incomplete) != len(schedule.Trials) {
		t.Fatalf("incomplete count after started crash = %d, want %d", len(incomplete), len(schedule.Trials))
	}

	encoded, err := journal.Encode()
	if err != nil {
		t.Fatalf("journal Encode returned error: %v", err)
	}
	decoded, err := DecodeTrialJournal(encoded)
	if err != nil {
		t.Fatalf("DecodeTrialJournal returned error: %v", err)
	}
	reencoded, err := decoded.Encode()
	if err != nil {
		t.Fatalf("re-encoding journal returned error: %v", err)
	}
	if string(encoded) != string(reencoded) {
		t.Fatalf("journal round trip changed bytes:\n%s\n%s", encoded, reencoded)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatalf("encoded journal is not JSON: %v", err)
	}

	for _, trial := range schedule.Trials {
		if err := journal.Append(JournalEntry{Key: trial.Key, PersistedAt: "2026-08-24T00:04:00Z", Status: "completed"}); err != nil {
			t.Fatalf("append completed entry %s: %v", trial.Key.String(), err)
		}
	}
	if incomplete := IncompleteTrials(journal, schedule); len(incomplete) != 0 {
		t.Fatalf("completed journal has %d incomplete trials", len(incomplete))
	}
	if err := journal.Validate(); err != nil {
		t.Fatalf("completed journal failed validation: %v", err)
	}
}

func TestTrialJournalRejectsInvalidEntries(t *testing.T) {
	p := validProtocol()
	schedule, err := GenerateSchedule(p, []string{"task-a"})
	if err != nil {
		t.Fatalf("GenerateSchedule returned error: %v", err)
	}
	key := schedule.Trials[0].Key
	for _, tc := range []struct {
		name  string
		entry JournalEntry
		want  string
	}{
		{name: "unknown status", entry: JournalEntry{Key: key, PersistedAt: "2026-08-24T00:00:00Z", Status: "paused"}, want: "unknown status"},
		{name: "bad timestamp", entry: JournalEntry{Key: key, PersistedAt: "tomorrow", Status: "scheduled"}, want: "RFC3339"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var journal TrialJournal
			if err := journal.Append(tc.entry); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}

	duplicate := TrialJournal{Entries: []JournalEntry{
		{Key: key, PersistedAt: "2026-08-24T00:00:00Z", Status: "scheduled"},
		{Key: key, PersistedAt: "2026-08-24T00:01:00Z", Status: "started"},
	}}
	if err := duplicate.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate journal entries accepted: %v", err)
	}
}
