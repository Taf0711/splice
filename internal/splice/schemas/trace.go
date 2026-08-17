package schemas

import (
	"errors"
	"fmt"
	"time"
)

// TraceSchemaVersion is the current run-outcome trace schema version. The
// trace store decodes unknown fields tolerantly so additive schema evolution
// never breaks older rows; this version gates structural Validate().
const TraceSchemaVersion = "1"

// Verdict vocabulary. A verdict is written as a separate, append-only record
// after the run ends; the effective verdict is the latest record at query time.
const (
	VerdictKept     = "kept"
	VerdictRejected = "rejected"
)

// OutcomeRecord is the terminal state of a run.
type OutcomeRecord struct {
	Status       string   `json:"status"` // completed / aborted / failed
	AbortReason  string   `json:"abort_reason,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

// Validate checks the outcome record.
func (o OutcomeRecord) Validate() error {
	switch o.Status {
	case "completed", "aborted", "failed":
	default:
		return fmt.Errorf("invalid outcome status %q", o.Status)
	}
	return nil
}

// MemoryRecord summarizes the run's memory sidecar state. The three-state
// status matters: a warm run whose retrieval failed must not be recorded as a
// cold run.
type MemoryRecord struct {
	Status string `json:"status"` // active / off / unavailable
	Items  int    `json:"items"`
	Chars  int    `json:"chars"`
}

// Validate checks the memory record.
func (m MemoryRecord) Validate() error {
	switch m.Status {
	case "active", "off", "unavailable":
	default:
		return fmt.Errorf("invalid memory status %q", m.Status)
	}
	if m.Items < 0 || m.Chars < 0 {
		return errors.New("memory items and chars must be non-negative")
	}
	return nil
}

// InputMeta records per-stage input provenance as sizes only, never content.
// LN2/LN3 use it to tell "spiral with thin input" from "spiral with fat input".
type InputMeta struct {
	ContextItems     int `json:"context_items"`
	ContextChars     int `json:"context_chars"`
	MemoryItems      int `json:"memory_items"`
	MemoryChars      int `json:"memory_chars"`
	ExemplarItems    int `json:"exemplar_items"`
	EdgePayloadBytes int `json:"edge_payload_bytes"`
}

// Validate checks the input metadata.
func (m InputMeta) Validate() error {
	if m.ContextItems < 0 || m.ContextChars < 0 || m.MemoryItems < 0 || m.MemoryChars < 0 || m.ExemplarItems < 0 || m.EdgePayloadBytes < 0 {
		return errors.New("input metadata counts must be non-negative")
	}
	return nil
}

// Cache records a per-stage semantic-cache result. The zero value means the
// cache was not consulted; Veritas response headers are not wired yet.
type Cache struct {
	Hit        bool    `json:"hit"`
	Similarity float64 `json:"similarity"`
}

// Validate checks the cache record.
func (c Cache) Validate() error {
	if c.Similarity < 0 {
		return errors.New("cache similarity must be non-negative")
	}
	return nil
}

// TracedStage is a StageRecord plus the per-stage input metadata and cache
// state the trace needs. StageRecord is embedded so the trace keeps the
// resolved provider/model strings that close model-version drift.
type TracedStage struct {
	StageRecord
	InputMeta InputMeta `json:"input_meta"`
	Cache     Cache     `json:"cache"`
	// PromptHash is the short sha256 of the stage's embedded prompt file. It is
	// empty on traces written before this field existed (the legacy bucket).
	PromptHash string `json:"prompt_hash,omitempty"`
}

// Validate checks the traced stage.
func (t TracedStage) Validate() error {
	if err := t.StageRecord.Validate(); err != nil {
		return err
	}
	if err := t.InputMeta.Validate(); err != nil {
		return err
	}
	if err := t.Cache.Validate(); err != nil {
		return err
	}
	return nil
}

// Intervention types. Weight is the charter-metric weight: tap=1,
// clarification=2, guidance=4, review/reject=8. Guidance is TUI-level and out
// of LN1 scope; the type is reserved so later runs do not need a backfill.
const (
	InterventionPermissionTap = "permission_tap"
	InterventionClarification = "clarification"
	InterventionReview        = "review"
)

// InterventionRecord is a typed, weighted human intervention during a run.
type InterventionRecord struct {
	Type      string `json:"type"`             // permission_tap / clarification / review
	Weight    int    `json:"weight"`           // 1 / 2 / 4 / 8
	Stage     string `json:"stage,omitempty"`  // empty for run-level interventions
	Iteration int    `json:"iteration"`        // 0 for run-level interventions
	Summary   string `json:"summary"`          // what was asked
	Choice    string `json:"choice,omitempty"` // what the user chose
}

// Validate checks the intervention record.
func (i InterventionRecord) Validate() error {
	switch i.Type {
	case InterventionPermissionTap, InterventionClarification, InterventionReview:
	default:
		return fmt.Errorf("invalid intervention type %q", i.Type)
	}
	switch i.Weight {
	case 1, 2, 4, 8:
	default:
		return fmt.Errorf("invalid intervention weight %d", i.Weight)
	}
	if i.Summary == "" {
		return errors.New("intervention summary is required")
	}
	if i.Iteration < 0 {
		return errors.New("intervention iteration must be >= 0")
	}
	return nil
}

// RunOutcome is a self-contained trace of one pipeline run. The embedded plan
// and stage records make the trace reconstructable with zero external
// references. Traces are append-only; nothing updates or backfills a trace.
type RunOutcome struct {
	SchemaVersion string               `json:"schema_version"`
	RunID         string               `json:"run_id"`
	SessionID     string               `json:"session_id,omitempty"`
	RepoRoot      string               `json:"repo_root"`
	Intent        string               `json:"intent"` // distilled, never the raw prompt
	Tier          string               `json:"tier"`
	Plan          *ExecutionPlan       `json:"plan"`
	Iterations    []IterationState     `json:"iterations"`
	Stages        []TracedStage        `json:"stages"`
	Outcome       OutcomeRecord        `json:"outcome"`
	Memory        MemoryRecord         `json:"memory"`
	Interventions []InterventionRecord `json:"interventions,omitempty"`
	// ToolFingerprint is the short sha256 of the deterministic verification-tool
	// identities. TopologyHash is the short sha256 of the compiled plan's
	// stage/edge structure (not code version). Both are empty on legacy traces.
	ToolFingerprint string `json:"tool_fingerprint,omitempty"`
	TopologyHash    string `json:"topology_hash,omitempty"`
	// BudgetProvenance records the per-stage budget provenance string applied at
	// run start (calibrated / default / refusal). Written at trace time so the
	// applied budgets always carry their origin.
	BudgetProvenance map[string]string `json:"budget_provenance,omitempty"`
}

// Validate checks the run outcome. It is strict: a malformed trace is a
// schema bug and fails loudly rather than being written silently.
func (r RunOutcome) Validate() error {
	if r.SchemaVersion != TraceSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", r.SchemaVersion)
	}
	if r.RunID == "" {
		return errors.New("run_id is required")
	}
	if r.RepoRoot == "" {
		return errors.New("repo_root is required")
	}
	if r.Intent == "" {
		return errors.New("intent is required")
	}
	if r.Tier == "" {
		return errors.New("tier is required")
	}
	if r.Plan == nil {
		return errors.New("plan is required")
	}
	if err := r.Plan.Validate(); err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	for i, it := range r.Iterations {
		if err := it.Validate(); err != nil {
			return fmt.Errorf("iterations[%d]: %w", i, err)
		}
	}
	for i, s := range r.Stages {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("stages[%d]: %w", i, err)
		}
	}
	if err := r.Outcome.Validate(); err != nil {
		return err
	}
	if err := r.Memory.Validate(); err != nil {
		return err
	}
	for i, iv := range r.Interventions {
		if err := iv.Validate(); err != nil {
			return fmt.Errorf("interventions[%d]: %w", i, err)
		}
	}
	return nil
}

// VerdictRecord is the append-only second record that lands after a run ends.
// It is written only for kept or rejected; absence means the verdict is
// unknown. RunOutcome carries no verdict field.
type VerdictRecord struct {
	RunID            string    `json:"run_id"`
	Verdict          string    `json:"verdict"` // kept / rejected
	RejectReason     string    `json:"reject_reason,omitempty"`
	MergeCommitSHA   string    `json:"merge_commit_sha,omitempty"`
	MergeBranch      string    `json:"merge_branch,omitempty"`
	KeptWorktreePath string    `json:"kept_worktree_path,omitempty"`
	DecidedAt        time.Time `json:"decided_at"`
}

// Validate checks the verdict record.
func (v VerdictRecord) Validate() error {
	if v.RunID == "" {
		return errors.New("run_id is required")
	}
	switch v.Verdict {
	case VerdictKept, VerdictRejected:
	default:
		return fmt.Errorf("invalid verdict %q", v.Verdict)
	}
	if v.DecidedAt.IsZero() {
		return errors.New("decided_at is required")
	}
	return nil
}

// TraceQueryFilter is the client-side filter for querying stored traces.
// Zero-valued fields are ignored.
type TraceQueryFilter struct {
	RepoRoot string `json:"repo_root"`
	Tier     string `json:"tier"`
	Status   string `json:"status"`
	Verdict  string `json:"verdict"`
	Query    string `json:"query"`
	Since    int64  `json:"since"`
	Limit    int    `json:"limit"`
}

// TraceQueryResult is one trace joined with its latest verdict. Verdict is nil
// when none has been recorded (unknown). Rank is the FTS bm25 score when a
// Query filter was supplied (more negative = more relevant), else 0.
type TraceQueryResult struct {
	Trace   RunOutcome     `json:"trace"`
	Verdict *VerdictRecord `json:"verdict,omitempty"`
	Rank    float64        `json:"rank,omitempty"`
}
