// Package warmcost implements the paired cold-versus-warm measurement design
// in tests/evals/cognition-families/MEASUREMENT_DESIGN.md.
//
// The package has unit tests, but they are provider-free and fast. The paid
// measurement itself is a release-cadence tool, never a CI test. The package
// never selects a provider or a model by itself; the operator supplies the
// exact approved exec command.
package warmcost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Arm is one measurement arm.
type Arm string

const (
	// ArmCold is the no-retained-experience control.
	ArmCold Arm = "cold"
	// ArmWarm has retained experience and both mechanisms enabled.
	ArmWarm Arm = "warm"
	// ArmRetrievalOnly retrieves and records but does not deliver, so
	// retrieval overhead is separated from delivery and work removal.
	ArmRetrievalOnly Arm = "warm-retrieval-only"
	// ArmWarmJev is W0 plus exactly the decision selector (M5): identical
	// cognition switches, the advisor flag on, active_experimental mode,
	// and manifest calibration and cap. The only delta from ArmWarm is the
	// selector, so the paired comparison isolates it.
	ArmWarmJev Arm = "warm-jev"
	// ArmColdWide is the cold control with a wider context expansion budget.
	ArmColdWide Arm = "cold-wide"
	// ArmLoop is the cold control using the plain agent tool loop.
	ArmLoop Arm = "loop"
)

// DefaultArms is the arm order the report uses.
var DefaultArms = []Arm{ArmCold, ArmWarm, ArmRetrievalOnly}

// flagsEnvPrefix starts the SPLICE_FLAGS entry of an arm.
const flagsEnvPrefix = "SPLICE_FLAGS="

// flagsClassifierOff is the SPLICE_FLAGS entry of every arm that has no
// classifier treatment. The trajectory classifier is on by default for
// users, so an eval arm must turn it off explicitly.
const flagsClassifierOff = flagsEnvPrefix + "-pipeline.trajectory_classifier"

// Env returns the explicit treatment environment for one arm. Every switch
// is always set so an ambient value can never leak into a measurement. The
// warm arm includes memory prefetch: files a related verified run needed
// are delivered in the first context round. SPLICE_FLAGS is set on every arm
// too. An arm that uses no flag sets it empty, because SPLICE_FLAGS outranks
// the legacy variables and an operator's shell value would otherwise leak in.
func (a Arm) Env() []string {
	switch a {
	case ArmWarm:
		// Memory v2: memory chooses what the first round reads and never
		// replaces reading. Evidence substitution and scope suppression stay
		// off (V2.1 measured substitution as harmful and suppression as
		// inactive); an --arm-env override can still switch them on.
		// The exemplar mode is explicit so an --arm-env override can ablate
		// the memory prose (retrieve-no-prompt) and keep the prefetch.
		return []string{flagsClassifierOff, "SPLICE_SCOPE_MODE=off", "SPLICE_EVIDENCE_SUBSTITUTION=off", "SPLICE_MEMORY_PREFETCH=on", "SPLICE_EXEMPLAR_MODE=both"}
	case ArmRetrievalOnly:
		return []string{
			flagsClassifierOff,
			"SPLICE_SCOPE_MODE=on",
			"SPLICE_EVIDENCE_SUBSTITUTION=off",
			"SPLICE_MEMORY_PREFETCH=off",
			"SPLICE_EXEMPLAR_MODE=retrieve-no-prompt",
		}
	case ArmCold:
		return []string{flagsClassifierOff, "SPLICE_SCOPE_MODE=off", "SPLICE_EVIDENCE_SUBSTITUTION=off", "SPLICE_MEMORY_PREFETCH=off"}
	case ArmColdWide:
		return append(ArmCold.Env(), "SPLICE_EXPANSION_ROUNDS=8")
	case ArmLoop:
		return ArmCold.Env()
	case ArmWarmJev:
		// The M5 manifest's preregistered values. The manifest is the
		// dispatch authority: the live campaign composes the env from
		// LoadJevManifest plus JevEnv, and the matrix test pins this
		// composition to those same values.
		// The warm base sets SPLICE_FLAGS to turn the classifier off. JevEnv
		// sets it to the Jev value, so the base entry is dropped to keep one
		// value per key.
		var env []string
		for _, kv := range ArmWarm.Env() {
			if !strings.HasPrefix(kv, flagsEnvPrefix) {
				env = append(env, kv)
			}
		}
		return append(env, JevEnv("0.9", "0.05")...)
	default:
		return nil
	}
}

// JevEnv returns the selector switches the warm-jev arm adds on top of the
// warm arm's env. Calibration and cap are campaign-manifest values, not
// code constants: they arrive through the runner's arm-env extension point
// and the manifest validates them before dispatch.
func JevEnv(pUseful, capUSD string) []string {
	return []string{
		flagsEnvPrefix + "pipeline.decision_advisor=true,-pipeline.trajectory_classifier",
		"SPLICE_DECISION_ADVISOR_MODE=active_experimental",
		"SPLICE_DECISION_ADVISOR_MODEL=typesafe/jev-1.13",
		"SPLICE_DECISION_ADVISOR_PUSEFUL=" + pUseful,
		"SPLICE_DECISION_ADVISOR_CAP_USD=" + capUSD,
	}
}

// MemoryMode returns the --memory value for one arm. Cold disables the
// sidecar; every other arm enables it.
func (a Arm) MemoryMode() string {
	if a == ArmCold || a == ArmColdWide || a == ArmLoop {
		return "off"
	}
	return "on"
}

// Base reports the cognition arm a Jev arm extends.
func (a Arm) Base() Arm {
	if a == ArmWarmJev {
		return ArmWarm
	}
	if a == ArmColdWide || a == ArmLoop {
		return ArmCold
	}
	return a
}

// Valid reports whether the arm is one of the known arms.
func (a Arm) Valid() bool {
	switch a {
	case ArmCold, ArmWarm, ArmRetrievalOnly, ArmWarmJev, ArmColdWide, ArmLoop:
		return true
	}
	return false
}

// RetentionMode selects the sidecar lifetime protocol for a run.
type RetentionMode string

const (
	// RetentionFresh gives every attempt its own sidecar. The warm arms then
	// have no retained experience, so the run measures the enabled mechanisms
	// at cold memory and cannot support a memory-effect claim.
	RetentionFresh RetentionMode = "fresh"
	// RetentionShared gives the warm arms ONE sidecar that persists across the
	// tasks and repeats, so an earlier attempt's captured evidence can be
	// retrieved by a later attempt. The cold arm keeps a per-attempt sidecar
	// so it stays a clean control.
	RetentionShared RetentionMode = "shared"
)

// Valid reports whether the retention mode is known.
func (m RetentionMode) Valid() bool {
	switch m {
	case RetentionFresh, RetentionShared:
		return true
	}
	return false
}

// ParseRetention parses a retention mode. An unset value means fresh. An
// unknown value is a loud configuration error naming the offender.
func ParseRetention(raw string) (RetentionMode, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return RetentionFresh, nil
	}
	mode := RetentionMode(trimmed)
	if !mode.Valid() {
		return "", fmt.Errorf("unknown retention mode %q (want fresh or shared)", trimmed)
	}
	return mode, nil
}

// Task phases order the corpus so a write precedes a read on a shared sidecar.
const (
	// PhaseWrite marks a task that captures experience for later tasks.
	PhaseWrite = "write"
	// PhaseRead marks a task that reads retained experience.
	PhaseRead = "read"
)

// Task is one task in the measurement corpus.
type Task struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Check  string `json:"check"`
	// Setup is an optional shell script that prepares the attempt workspace.
	// The runner executes it before the splice process, so its work has no
	// provider cost. A non-zero exit is a harness error.
	Setup   string `json:"setup,omitempty"`
	Fixture string `json:"fixture,omitempty"`
	// Phase is write, read, or empty. A write-phase task starts a workspace
	// sequence; the tasks that depend on it run after it in that workspace.
	Phase string `json:"phase,omitempty"`
	// Precursor is the ID of the write-phase task whose workspace this task
	// runs in. It is required on every non-write task when the taskset has
	// two or more write tasks, because the pairing is otherwise ambiguous.
	Precursor string `json:"precursor,omitempty"`
}

// ValidPhase reports whether the task phase is one of the known values. An
// empty phase means the task keeps its taskset position.
func (t Task) ValidPhase() bool {
	switch t.Phase {
	case "", PhaseWrite, PhaseRead:
		return true
	}
	return false
}

// ModelSettings records the model configuration an attempt used.
type ModelSettings struct {
	Provider        string `json:"provider,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	MaxTurns        string `json:"max_turns,omitempty"`
}

// Totals is the per-attempt total copied from the authoritative ledger
// fields. It is never recomputed from stage rows.
type Totals struct {
	Requests      int     `json:"requests"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	CachedTokens  int     `json:"cached_input_tokens"`
	CacheWrite    int     `json:"cache_write_tokens"`
	Reasoning     int     `json:"reasoning_tokens"`
	BilledUSD     float64 `json:"billed_usd"`
	PricedRecords int     `json:"priced_records"`
}

// RequestRecord is one provider request in the attempt artifact. It mirrors
// the authoritative ledger record, but the identity fields carry no omitempty,
// so a zero InvocationOrdinal or ContextRound is still recorded explicitly.
type RequestRecord struct {
	Sequence          int      `json:"sequence"`
	Stage             string   `json:"stage"`
	Iteration         int      `json:"iteration"`
	InvocationOrdinal int      `json:"invocation_ordinal"`
	ContextRound      int      `json:"context_round"`
	SpendSource       string   `json:"spend_source"`
	InputTokens       int      `json:"input_tokens"`
	OutputTokens      int      `json:"output_tokens"`
	CachedTokens      int      `json:"cached_input_tokens"`
	CacheWrite        int      `json:"cache_write_tokens"`
	Reasoning         int      `json:"reasoning_tokens"`
	CostUSD           *float64 `json:"cost_usd"`
	CostStatus        string   `json:"cost_status"`
	// PromptLayoutHash is the cacheable prefix layout hash (system prompt plus
	// tool schema) the telemetry branch reports per request. Empty means the
	// binary emitted no telemetry for this request, which is unknown, not a
	// layout change. Option B decodes it from the raw JSON, not the product
	// schema.
	PromptLayoutHash string `json:"prompt_layout_hash,omitempty"`
	// CacheHit is the provider's stated cache read: true, false, or nil when no
	// usage was reported. Nil is unknown and is never read as a miss.
	CacheHit *bool `json:"cache_hit"`
	// MemoryPosition states where dynamic content sits relative to the
	// cacheable prefix. Empty means the binary emitted no telemetry.
	MemoryPosition string `json:"memory_position,omitempty"`
}

// requestRecords maps the authoritative ledger into the explicit artifact
// shape. Nothing is recomputed. The three cache telemetry fields
// (PromptLayoutHash, CacheHit, MemoryPosition) are not on the harness branch's
// product schema; they are decoded from the raw final result JSON and copied on
// afterward by mergeCacheTelemetry (Option B), so this function stays the one
// place the typed ledger fields are projected.
func requestRecords(recs []schemas.PipelineUsageRecord) []RequestRecord {
	out := make([]RequestRecord, 0, len(recs))
	for _, r := range recs {
		out = append(out, RequestRecord{
			Sequence:          r.Sequence,
			Stage:             r.Stage,
			Iteration:         r.Iteration,
			InvocationOrdinal: r.InvocationOrdinal,
			ContextRound:      r.ContextRound,
			SpendSource:       r.SpendSource,
			InputTokens:       r.InputTokens,
			OutputTokens:      r.OutputTokens,
			CachedTokens:      r.CachedTokens,
			CacheWrite:        r.CacheWrite,
			Reasoning:         r.Reasoning,
			CostUSD:           r.CostUSD,
			CostStatus:        r.CostStatus,
		})
	}
	return out
}

// Attempt is the per-attempt JSON artifact. Every number comes from the
// captured ledger or the verifier, never from a hand sum.
type Attempt struct {
	TaskID string `json:"task_id"`
	Arm    Arm    `json:"arm"`
	Repeat int    `json:"repeat"`
	// Sequence identifies the shared workspace this attempt ran in. Tasks in
	// one sequence run one after another in the same directory, so a
	// write-phase task's bytes persist for the read-phase task that follows.
	Sequence int `json:"sequence"`
	// SequenceOrdinal is the task's position inside its sequence.
	SequenceOrdinal int `json:"sequence_ordinal"`
	// SequenceTasks is the number of tasks in the sequence.
	SequenceTasks int `json:"sequence_tasks"`
	// Workspace is the shared sequence workspace this attempt ran in. It is
	// also the runtime memory project identity, so a write and a read in one
	// sequence share it.
	Workspace string `json:"workspace,omitempty"`
	// RunID is the pipeline run id from the final result. The harness passes
	// it as the producer run id when it reanchors that run's capture set.
	RunID string `json:"run_id,omitempty"`
	// Binary is the path of the binary under test. It is recorded so a reader
	// can tie a run to the artifact that produced it, not only to a revision.
	Binary          string          `json:"binary"`
	BinaryRevision  string          `json:"binary_revision"`
	SidecarRevision string          `json:"sidecar_revision"`
	FixtureDigest   string          `json:"fixture_digest"`
	ModelID         string          `json:"model_id"`
	ModelSettings   ModelSettings   `json:"model_settings"`
	TreatmentEnv    []string        `json:"treatment_env"`
	SessionID       string          `json:"session_id"`
	RunStatus       string          `json:"run_status"`
	CostCoverage    string          `json:"cost_coverage"`
	VerifierResult  bool            `json:"verifier_result"`
	VerifierOutput  string          `json:"verifier_output,omitempty"`
	Requests        []RequestRecord `json:"requests"`
	Totals          Totals          `json:"totals"`
	// CacheTelemetryReported is true when the binary emitted at least one
	// telemetry value for this attempt. False means the telemetry fields are
	// unknown, not zero.
	CacheTelemetryReported bool `json:"cache_telemetry_reported"`
	// PromptLayoutFlips is the binary's mid-run prompt layout flip count. Nil
	// means the binary emitted no cache telemetry, so the count is unknown and
	// must not be reported as zero.
	PromptLayoutFlips *int `json:"prompt_layout_flips,omitempty"`
	// Rounds is the per-(stage, iteration) cache view required by SPEC section
	// 8 item 3. A round with no hit is a full-priced round.
	Rounds []RoundCache `json:"rounds,omitempty"`
	// LayoutStability is the per-stage distinct prompt layout hash view.
	LayoutStability []StageLayout `json:"layout_stability,omitempty"`
	LedgerError     string        `json:"ledger_error,omitempty"`
	// RawStream names the additive raw stream-json artifact for this
	// attempt, when one was captured. Empty means no raw artifact exists.
	RawStream string `json:"raw_stream,omitempty"`
	// Trace names the exported RunOutcome trace of a memory-arm attempt,
	// relative to the run directory (traces/<session>-trace.json).
	// TraceUnavailable says why a memory-arm attempt has no trace.
	Trace            string `json:"trace,omitempty"`
	TraceUnavailable string `json:"trace_unavailable,omitempty"`
	// UpstreamUnreachable marks a run whose model requests never reached
	// the model (a network failure). Such an attempt measures the network,
	// not the arm, so the runner retries its sequence.
	UpstreamUnreachable bool `json:"upstream_unreachable,omitempty"`
	// MemoryUnavailable marks a memory-arm run whose sidecar could not be
	// reached: splice ran it without memory, so it does not measure the arm.
	MemoryUnavailable bool `json:"memory_unavailable,omitempty"`
	// CredentialsMissing marks a run the provider refused for a missing or
	// invalid API key. The campaign stops at the first one.
	CredentialsMissing bool      `json:"credentials_missing,omitempty"`
	StartedAt          time.Time `json:"started_at"`
	EndedAt            time.Time `json:"ended_at"`
	DurationMS         int64     `json:"duration_ms"`
	// Memory is the run's memory health line (memory v2 Section 3.6):
	// files memory delivered, how many the run changed, and how many files
	// the model requested that memory did not deliver. Nil when the run
	// printed no memory line (no memory arm, or nothing delivered or
	// missed).
	Memory *MemoryHealth `json:"memory,omitempty"`
}

// CompleteCoverage reports whether every captured request carries a priced
// cost. A missing price is unknown, never zero.
func (a Attempt) CompleteCoverage() bool {
	if a.CostCoverage == schemas.CostCoverageComplete {
		return true
	}
	if a.CostCoverage != "" {
		return false
	}
	if len(a.Requests) == 0 {
		return false
	}
	for _, r := range a.Requests {
		if r.CostStatus != schemas.CostStatusPriced || r.CostUSD == nil {
			return false
		}
	}
	return true
}

// Version is the report schema version.
const Version = 1

// LoadTaskset reads tasks/*.json plus an optional fixture/ directory. The "id"
// field is required; "name" is accepted as an alias for compatibility with the
// existing eval tasksets. The runner and the offline taskset tests share
// this loader, so a field the runner reads is a field the tests see.
func LoadTaskset(dir string) ([]Task, error) {
	taskDir := filepath.Join(dir, "tasks")
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir %s: %w", taskDir, err)
	}
	defaultFixture := filepath.Join(dir, "fixture")
	if _, statErr := os.Stat(defaultFixture); statErr != nil {
		defaultFixture = ""
	}
	var tasks []Task
	// A second file with the same task id runs every attempt twice under
	// one session id (a sync tool's "name 2.json" copy did this to P2d).
	seenIDs := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(taskDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read task %s: %w", path, err)
		}
		var raw struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Prompt    string `json:"prompt"`
			Check     string `json:"check"`
			Setup     string `json:"setup"`
			Fixture   string `json:"fixture"`
			Phase     string `json:"phase"`
			Precursor string `json:"precursor"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse task %s: %w", path, err)
		}
		id := strings.TrimSpace(raw.ID)
		if id == "" {
			id = strings.TrimSpace(raw.Name)
		}
		if id == "" {
			return nil, fmt.Errorf("task %s: id is required", path)
		}
		if first, dup := seenIDs[id]; dup {
			return nil, fmt.Errorf("task %s: id %q is also declared by %s", path, id, first)
		}
		seenIDs[id] = path
		if strings.TrimSpace(raw.Prompt) == "" {
			return nil, fmt.Errorf("task %s: prompt is required", path)
		}
		if strings.TrimSpace(raw.Check) == "" {
			return nil, fmt.Errorf("task %s: check is required", path)
		}
		fixture := raw.Fixture
		if fixture != "" && !filepath.IsAbs(fixture) {
			fixture = filepath.Join(dir, fixture)
		}
		if fixture == "" {
			fixture = defaultFixture
		}
		tasks = append(tasks, Task{ID: id, Prompt: raw.Prompt, Check: raw.Check, Setup: raw.Setup, Fixture: fixture, Phase: strings.TrimSpace(raw.Phase), Precursor: strings.TrimSpace(raw.Precursor)})
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("taskset %s has no tasks under tasks/*.json", dir)
	}
	return tasks, nil
}

// MemoryHealth is one run's memory delivery outcome.
type MemoryHealth struct {
	Delivered int `json:"delivered"`
	Hits      int `json:"hits"`
	Misses    int `json:"misses"`
}

// memoryLinePattern matches the pipeline's "[memory] delivered ..." line.
var memoryLinePattern = regexp.MustCompile(`\[memory\] delivered (\d+) file\(s\), (\d+) changed by the run, (\d+) requested in expansions`)

// parseMemoryHealth reads the last memory health line from a raw stream.
func parseMemoryHealth(raw []byte) *MemoryHealth {
	matches := memoryLinePattern.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil
	}
	m := matches[len(matches)-1]
	atoi := func(b []byte) int { n, _ := strconv.Atoi(string(b)); return n }
	return &MemoryHealth{Delivered: atoi(m[1]), Hits: atoi(m[2]), Misses: atoi(m[3])}
}
