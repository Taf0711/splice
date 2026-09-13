package warmcost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Config is one measurement run.
type Config struct {
	RunID             string
	RepoDir           string
	Binary            string
	Model             string
	ModelSettings     ModelSettings
	Tasks             []Task
	Arms              []Arm
	Repeats           int
	OutDir            string
	Timeout           time.Duration
	BootstrapSamples  int
	BootstrapSeed     int64
	CorrectnessMargin float64
	SidecarRevision   string
	KeepWorkspaces    bool
	// Retention selects the sidecar lifetime protocol. Empty means
	// RetentionFresh.
	Retention RetentionMode
	// SidecarRoot is the directory the runner assigns per-class sidecar
	// sockets and databases under. Empty keeps the ambient operator sidecar.
	SidecarRoot string
}

// ExecRequest is one child process invocation.
type ExecRequest struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
}

// ExecResult is one child process result. A non-zero exit is not a Go error.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExecFunc runs one child process. It is a seam so the runner can be exercised
// offline with a scripted child and never calls a provider on its own.
type ExecFunc func(ctx context.Context, req ExecRequest) (ExecResult, error)

// PreRegistration is fixed before a run, never after.
type PreRegistration struct {
	PrimaryCorrectnessEndpoint string  `json:"primary_correctness_endpoint"`
	NoninferiorityMargin       float64 `json:"noninferiority_margin"`
	PrimaryCostEndpoint        string  `json:"primary_cost_endpoint"`
	SecondaryCostEndpoint      string  `json:"secondary_cost_endpoint"`
	WinRule                    string  `json:"win_rule"`
	StoppingRule               string  `json:"stopping_rule"`
}

// Provenance records the exact environment of a run.
type Provenance struct {
	RepoDir         string        `json:"repo_dir"`
	Binary          string        `json:"binary"`
	BinaryRevision  string        `json:"binary_revision"`
	SidecarRevision string        `json:"sidecar_revision"`
	ModelID         string        `json:"model_id"`
	ModelSettings   ModelSettings `json:"model_settings"`
	Arms            []Arm         `json:"arms"`
	Tasks           int           `json:"tasks"`
	Repeats         int           `json:"repeats"`
}

// Coverage states cost coverage over the run.
type Coverage struct {
	Attempts        int  `json:"attempts"`
	PartialAttempts int  `json:"partial_attempts"`
	Complete        bool `json:"complete"`
}

// Aggregate is the run-level report.
type Aggregate struct {
	Version         int             `json:"version"`
	RunID           string          `json:"run_id"`
	GeneratedAt     time.Time       `json:"generated_at"`
	PreRegistration PreRegistration `json:"pre_registration"`
	Provenance      Provenance      `json:"provenance"`
	Retention       RetentionReport `json:"retention"`
	ArmMetrics      []ArmMetrics    `json:"arm_metrics"`
	PerTask         []TaskEffect    `json:"per_task"`
	SkippedTasks    []string        `json:"skipped_tasks,omitempty"`
	Decomposition   Decomposition   `json:"decomposition"`
	Bootstrap       BootstrapResult `json:"bootstrap"`
	Claim           Claim           `json:"claim"`
	Coverage        Coverage        `json:"coverage"`
	Note            string          `json:"note"`
}

// reanchorFunc advances one run's capture set from one revision to another.
// It is a seam so a unit test can prove the commit-and-reanchor step runs
// after a passed write task and does not run after a failed one.
type reanchorFunc func(ctx context.Context, workspace, class, preHead, postHead, producerRunID string) error

// Runner executes the measurement.
type Runner struct {
	cfg  Config
	exec ExecFunc
	now  func() time.Time
	// reanchorFn is the capture-reanchor seam; it defaults to the sidecar
	// implementation and is replaced only in tests.
	reanchorFn reanchorFunc
}

// NewRunner validates the configuration and returns a runner. A nil exec seam
// uses the real subprocess runner.
func NewRunner(cfg Config, seam ExecFunc) (*Runner, error) {
	if strings.TrimSpace(cfg.Binary) == "" {
		return nil, errors.New("warmcost: binary is required")
	}
	if strings.TrimSpace(cfg.OutDir) == "" {
		return nil, errors.New("warmcost: out dir is required")
	}
	if len(cfg.Tasks) == 0 {
		return nil, errors.New("warmcost: at least one task is required")
	}
	if cfg.Repeats <= 0 {
		return nil, fmt.Errorf("warmcost: repeats %d must be >= 1", cfg.Repeats)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Minute
	}
	if cfg.BootstrapSamples <= 0 {
		cfg.BootstrapSamples = 10000
	}
	if cfg.BootstrapSeed == 0 {
		cfg.BootstrapSeed = 1
	}
	if len(cfg.Arms) == 0 {
		cfg.Arms = append([]Arm(nil), DefaultArms...)
	}
	for _, a := range cfg.Arms {
		if !a.Valid() {
			return nil, fmt.Errorf("warmcost: unknown arm %q", a)
		}
	}
	if cfg.Retention == "" {
		cfg.Retention = RetentionFresh
	}
	if !cfg.Retention.Valid() {
		return nil, fmt.Errorf("warmcost: unknown retention mode %q (want fresh or shared)", cfg.Retention)
	}
	if cfg.Retention == RetentionShared && strings.TrimSpace(cfg.SidecarRoot) == "" {
		return nil, errors.New("warmcost: retention shared requires a sidecar root, because the warm arms must share one persistent sidecar")
	}
	for _, t := range cfg.Tasks {
		if !t.ValidPhase() {
			return nil, fmt.Errorf("warmcost: task %s has unknown phase %q (want write, read, or empty)", t.ID, t.Phase)
		}
	}
	if cfg.RunID == "" {
		cfg.RunID = "warmcost-" + time.Now().UTC().Format("20060102T150405Z")
	}
	if seam == nil {
		seam = runExec
	}
	runner := &Runner{cfg: cfg, exec: seam, now: time.Now}
	runner.reanchorFn = runner.reanchorSequence
	return runner, nil
}

// sequence is one ordered task group that shares ONE workspace. The runner
// runs a sequence's tasks in order in the same directory, so a write-phase
// task's bytes persist for the read-phase task that follows it. A taskset
// with no write-phase task keeps one sequence per task, which preserves the
// per-task workspace behavior of the earlier protocol versions.
type sequence struct {
	Index   int
	Tasks   []Task
	Fixture string
	// GitInit makes the sequence workspace a git repository with an initial
	// commit. The capture path anchors evidence at a git revision
	// (internal/splice/run.go verifiedRevision falls back to HEAD), so a
	// sequence that is meant to retain evidence needs a repository. Plain
	// tasksets keep the previous workspace behavior.
	GitInit bool
}

// groupSequences orders the tasks and groups them into shared workspaces. A
// write-phase task starts a new sequence; the tasks after it, up to the next
// write-phase task, run in its workspace. With no write-phase task, every
// task is its own sequence.
func groupSequences(tasks []Task) ([]sequence, error) {
	ordered := orderTasks(tasks)
	hasWrite := false
	for _, t := range ordered {
		if t.Phase == PhaseWrite {
			hasWrite = true
			break
		}
	}
	var groups [][]Task
	if !hasWrite {
		for _, t := range ordered {
			groups = append(groups, []Task{t})
		}
	} else {
		var current []Task
		for _, t := range ordered {
			if t.Phase == PhaseWrite && len(current) > 0 {
				groups = append(groups, current)
				current = nil
			}
			current = append(current, t)
		}
		if len(current) > 0 {
			groups = append(groups, current)
		}
	}
	sequences := make([]sequence, 0, len(groups))
	for i, group := range groups {
		fixture := group[0].Fixture
		gitInit := false
		for _, t := range group {
			if t.Fixture != fixture {
				return nil, fmt.Errorf("warmcost: sequence %d mixes fixtures %q and %q (task %s)", i, fixture, t.Fixture, t.ID)
			}
			if t.Phase == PhaseWrite {
				gitInit = true
			}
		}
		sequences = append(sequences, sequence{Index: i, Tasks: group, Fixture: fixture, GitInit: gitInit})
	}
	return sequences, nil
}

// sequenceSession is the sidecar session id for one (sequence, arm, repeat).
// Shared retention still collapses the warm arms onto one sidecar class; the
// id distinguishes fresh sidecars.
func sequenceSession(runID string, seqIndex int, arm Arm, repeat int) string {
	return fmt.Sprintf("wc-%s-seq%02d-%s-r%d", runID, seqIndex, arm, repeat)
}

// Run executes every arm, every sequence, every repeat, and returns the
// aggregate. One workspace serves every task of one (arm, sequence); the
// workspace contents are reset once per repeat and never between the tasks
// of a sequence.
func (r *Runner) Run(ctx context.Context) (Aggregate, error) {
	runDir := filepath.Join(r.cfg.OutDir, r.cfg.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return Aggregate{}, fmt.Errorf("warmcost: create run dir: %w", err)
	}

	provenance := Provenance{
		RepoDir:         r.cfg.RepoDir,
		Binary:          r.cfg.Binary,
		BinaryRevision:  r.binaryRevision(),
		SidecarRevision: r.cfg.SidecarRevision,
		ModelID:         r.cfg.Model,
		ModelSettings:   r.cfg.ModelSettings,
		Arms:            append([]Arm(nil), r.cfg.Arms...),
		Tasks:           len(r.cfg.Tasks),
		Repeats:         r.cfg.Repeats,
	}

	sequences, err := groupSequences(r.cfg.Tasks)
	if err != nil {
		return Aggregate{}, err
	}
	// seqBase holds one stable workspace path per (arm, sequence). The path is
	// stable across repeats, which keeps the runtime memory project identity
	// stable, and its CONTENTS are reset at the start of every repeat. A
	// stable path is what lets a later read task retrieve an earlier write
	// task's captured evidence.
	seqBase, err := os.MkdirTemp("", "warmcost-seq-base-")
	if err != nil {
		return Aggregate{}, fmt.Errorf("warmcost: create sequence base: %w", err)
	}
	if !r.cfg.KeepWorkspaces {
		defer os.RemoveAll(seqBase)
	}

	var attempts []Attempt

	// Interleave arm order across repeats to reduce drift.
	for repeat := 0; repeat < r.cfg.Repeats; repeat++ {
		arms := append([]Arm(nil), r.cfg.Arms...)
		if repeat%2 == 1 {
			for i, j := 0, len(arms)-1; i < j; i, j = i+1, j-1 {
				arms[i], arms[j] = arms[j], arms[i]
			}
		}
		for _, seq := range sequences {
			for _, arm := range arms {
				seqAttempts, seqErr := r.runSequence(ctx, seq, arm, repeat, seqBase, provenance)
				for _, attempt := range seqAttempts {
					attempts = append(attempts, attempt)
					if err := writeAttempt(runDir, attempt); err != nil {
						return Aggregate{}, err
					}
				}
				if seqErr != nil {
					// Record the tasks the sequence never reached, so a
					// failed sequence stays in total spend honestly.
					for ordinal := len(seqAttempts); ordinal < len(seq.Tasks); ordinal++ {
						attempt := r.failedAttempt(seq.Tasks[ordinal], arm, repeat, seq.Index, ordinal, len(seq.Tasks), provenance, seqErr)
						attempts = append(attempts, attempt)
						if err := writeAttempt(runDir, attempt); err != nil {
							return Aggregate{}, err
						}
					}
				}
			}
		}
	}

	agg, err := r.aggregate(provenance, attempts)
	if err != nil {
		return Aggregate{}, err
	}
	data, err := json.MarshalIndent(agg, "", "  ")
	if err != nil {
		return Aggregate{}, fmt.Errorf("warmcost: marshal aggregate: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "aggregate.json"), append(data, '\n'), 0o644); err != nil {
		return Aggregate{}, fmt.Errorf("warmcost: write aggregate: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "report.md"), []byte(renderMarkdown(agg)), 0o644); err != nil {
		return Aggregate{}, fmt.Errorf("warmcost: write report: %w", err)
	}
	return agg, nil
}

// aggregate builds the run-level report from the captured attempts. It fails
// loud when the per-source cost split disagrees with the billed total.
func (r *Runner) aggregate(prov Provenance, attempts []Attempt) (Aggregate, error) {
	byArm := map[Arm][]Attempt{}
	for _, a := range attempts {
		byArm[a.Arm] = append(byArm[a.Arm], a)
	}
	var metrics []ArmMetrics
	for _, arm := range r.cfg.Arms {
		metrics = append(metrics, ComputeArmMetrics(arm, byArm[arm]))
	}
	metricByArm := map[Arm]ArmMetrics{}
	for _, m := range metrics {
		metricByArm[m.Arm] = m
	}

	effects, skipped := ComputeTaskEffects(attempts, ArmCold, ArmWarm)

	decomp, err := Decompose(metricByArm[ArmCold], metricByArm[ArmWarm])
	if err != nil {
		return Aggregate{}, err
	}
	boot := BootstrapTaskDeltas(effects, r.cfg.BootstrapSamples, r.cfg.BootstrapSeed)

	coverage := Coverage{Attempts: len(attempts)}
	for _, a := range attempts {
		if !a.CompleteCoverage() {
			coverage.PartialAttempts++
		}
	}
	coverage.Complete = coverage.PartialAttempts == 0
	claim := EvaluateClaim(decomp, boot, r.cfg.CorrectnessMargin, !coverage.Complete, r.cfg.Retention == RetentionFresh)

	agg := Aggregate{
		Version:     Version,
		RunID:       r.cfg.RunID,
		GeneratedAt: r.now(),
		PreRegistration: PreRegistration{
			PrimaryCorrectnessEndpoint: "verifier pass or fail per attempt",
			NoninferiorityMargin:       r.cfg.CorrectnessMargin,
			PrimaryCostEndpoint:        "billed USD per verified completion",
			SecondaryCostEndpoint:      "billed USD per attempt",
			WinRule:                    "warm is a win only when the matched-pair cost delta is negative with a task-clustered interval that excludes zero, and correctness is noninferior to the margin",
			StoppingRule:               "if warm does not reduce provider requests per verified completion on a corpus that triggers expansions, stop",
		},
		Provenance:    prov,
		Retention:     r.retentionReport(),
		ArmMetrics:    metrics,
		PerTask:       effects,
		SkippedTasks:  skipped,
		Decomposition: decomp,
		Bootstrap:     boot,
		Claim:         claim,
		Coverage:      coverage,
		Note:          "Every number comes from the captured authoritative request ledger. Tokens are provider-reported. A partial run withholds the total-cost claim.",
	}
	return agg, nil
}

// SidecarAssignment names the sidecar pattern one arm uses.
type SidecarAssignment struct {
	Arm     Arm    `json:"arm"`
	Pattern string `json:"pattern"`
}

// RetentionReport states the sidecar lifetime protocol of a run, so a reader
// can tell whether the warm arm had any retained experience.
type RetentionReport struct {
	Mode         RetentionMode       `json:"mode"`
	SidecarRoot  string              `json:"sidecar_root"`
	Assignment   []SidecarAssignment `json:"assignment"`
	OrderingRule string              `json:"ordering_rule"`
	Note         string              `json:"note,omitempty"`
}

// retentionReport records the protocol the run used.
func (r *Runner) retentionReport() RetentionReport {
	rep := RetentionReport{
		Mode:         r.cfg.Retention,
		SidecarRoot:  r.cfg.SidecarRoot,
		OrderingRule: "one workspace per (arm, sequence); the write-phase task and the read-phase tasks that follow it run in that workspace in order, and the runner interleaves arm order per repeat, so a write on the shared sidecar and in the workspace precedes its read",
	}
	for _, arm := range r.cfg.Arms {
		rep.Assignment = append(rep.Assignment, SidecarAssignment{Arm: arm, Pattern: sidecarPattern(r.cfg.Retention, arm)})
	}
	if strings.TrimSpace(r.cfg.SidecarRoot) == "" {
		rep.Note = "no sidecar root is set, so every attempt uses the ambient operator sidecar"
	}
	return rep
}

// sidecarClass names the sidecar a (mode, arm, session) uses. Shared mode
// gives the warm arms ONE class so a later attempt can retrieve an earlier
// attempt's evidence. The cold arm always gets a per-attempt class so it stays
// a clean control. Fresh mode gives every attempt its own class.
func sidecarClass(mode RetentionMode, arm Arm, sessionID string) string {
	if mode == RetentionShared && arm != ArmCold {
		return "shared-warm"
	}
	return "fresh-" + sessionID
}

// sidecarPattern describes a sidecar class in words for the report.
func sidecarPattern(mode RetentionMode, arm Arm) string {
	if mode == RetentionShared && arm != ArmCold {
		return "one shared sidecar for every attempt: <sidecar-root>/shared-warm"
	}
	return "a fresh sidecar per attempt: <sidecar-root>/fresh-<session-id>"
}

// sidecarEnv returns the sidecar environment overrides for one class. With no
// sidecar root the ambient operator sidecar is used unchanged.
func (r *Runner) sidecarEnv(class string) []string {
	if strings.TrimSpace(r.cfg.SidecarRoot) == "" {
		return nil
	}
	dir := filepath.Join(r.cfg.SidecarRoot, class)
	return []string{
		"SPLICE_MEMD_SOCKET=" + filepath.Join(dir, "mem.sock"),
		"SPLICE_MEMD_DB=" + filepath.Join(dir, "mem.db"),
	}
}

// prepareSidecar creates the sidecar directory for one class. The sidecar
// daemon owns the socket and database files.
func (r *Runner) prepareSidecar(class string) error {
	if strings.TrimSpace(r.cfg.SidecarRoot) == "" {
		return nil
	}
	dir := filepath.Join(r.cfg.SidecarRoot, class)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("warmcost: create sidecar dir %s: %w", dir, err)
	}
	return nil
}

// orderTasks puts write-phase tasks before every other task while keeping the
// taskset order inside each group, so the taskset owns the sequence and the
// runner only enforces the phase rule.
func orderTasks(tasks []Task) []Task {
	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Phase == PhaseWrite {
			out = append(out, t)
		}
	}
	for _, t := range tasks {
		if t.Phase != PhaseWrite {
			out = append(out, t)
		}
	}
	return out
}

func (r *Runner) binaryRevision() string {
	if r.cfg.RepoDir == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", r.cfg.RepoDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// execArgs mirrors the production headless invocation used by the eval seam.
func execArgs(arm Arm, model, sessionID, prompt string) []string {
	args := []string{
		"--no-trust", "exec",
		"--output-format", "stream-json",
		"--memory", arm.MemoryMode(),
		"--init-session-id", sessionID,
	}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	return append(args, prompt)
}

// runExec is the production subprocess seam.
func runExec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, req.Binary, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return res, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, fmt.Errorf("run %s: %w", req.Binary, err)
}

type streamFinalEvent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// parsePipelineResult finds the final stream-json event and parses its text as
// the pipeline result. The text IS the JSON produced after applyRequestLedger.
func parsePipelineResult(stdout []byte) (schemas.PipelineResult, error) {
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)
	finalText := ""
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev streamFinalEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type == "final" && ev.Text != "" {
			finalText = ev.Text
		}
	}
	if err := scanner.Err(); err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("scan exec output: %w", err)
	}
	if finalText == "" {
		return schemas.PipelineResult{}, errors.New("no final event with a pipeline result in exec output")
	}
	var result schemas.PipelineResult
	if err := json.Unmarshal([]byte(finalText), &result); err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("parse final pipeline result: %w", err)
	}
	if err := result.Validate(); err != nil {
		return schemas.PipelineResult{}, fmt.Errorf("final pipeline result invalid: %w", err)
	}
	return result, nil
}

// totalsFromResult copies the authoritative ledger totals. Nothing is summed
// by hand here.
func totalsFromResult(result schemas.PipelineResult) Totals {
	return Totals{
		Requests:      len(result.UsageRecords),
		InputTokens:   result.TotalTokensInput,
		OutputTokens:  result.TotalTokensOutput,
		CachedTokens:  result.TotalTokensCached,
		CacheWrite:    result.TotalTokensCacheWrite,
		Reasoning:     result.TotalTokensReasoning,
		BilledUSD:     result.TotalCostUSD,
		PricedRecords: result.PricedRequestCount,
	}
}

// runVerifier runs the task check in the attempt workspace.
func runVerifier(ctx context.Context, dir, check string) (bool, string, error) {
	if strings.TrimSpace(check) == "" {
		return false, "", errors.New("warmcost: task check is empty")
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", check)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, string(out), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, string(out), nil
	}
	return false, string(out), fmt.Errorf("verifier start: %w", err)
}

// envWithOverrides removes any existing keys, then appends the overrides, so an
// ambient switch can never win over the arm's explicit value.
func envWithOverrides(base []string, overrides ...string) []string {
	keys := map[string]bool{}
	for _, o := range overrides {
		if i := strings.IndexByte(o, '='); i > 0 {
			keys[o[:i]] = true
		}
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i > 0 && keys[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, overrides...)
}

// runSequence prepares one workspace for the sequence and runs every task in
// it, in order. Each task still produces its own attempt, verifier, and
// ledger. A task failure does not stop the sequence: the workspace keeps
// whatever the failed task left and the next task runs in it.
func (r *Runner) runSequence(ctx context.Context, seq sequence, arm Arm, repeat int, seqBase string, prov Provenance) ([]Attempt, error) {
	dir := filepath.Join(seqBase, fmt.Sprintf("%s-seq%02d", arm, seq.Index))
	if err := prepareWorkspaceAt(dir, seq.Fixture); err != nil {
		return nil, err
	}
	if seq.GitInit {
		if err := initGitWorkspace(dir); err != nil {
			return nil, err
		}
	}
	attempts := make([]Attempt, 0, len(seq.Tasks))
	class := sidecarClass(r.cfg.Retention, arm, sequenceSession(r.cfg.RunID, seq.Index, arm, repeat))
	for ordinal, task := range seq.Tasks {
		attempt, taskErr := r.runTaskInWorkspace(ctx, task, arm, repeat, seq.Index, ordinal, len(seq.Tasks), dir, prov)
		if taskErr != nil {
			attempt = r.failedAttempt(task, arm, repeat, seq.Index, ordinal, len(seq.Tasks), prov, taskErr)
		}
		attempts = append(attempts, attempt)
		// The documented eval contract: once a write-phase task's verifier
		// passes, the harness commits the verified tree and advances that
		// run's captured nodes from the pre-verify HEAD to the post-verify
		// commit. The stage sandbox refuses the write-shaped stash create,
		// so in-run capture anchors at the pre-verify HEAD; without this
		// step the read task's freshness diff sees the write's edit and
		// rejects the nodes. A failed write is never committed.
		if taskErr == nil && seq.GitInit && task.Phase == PhaseWrite && attempt.VerifierResult {
			if cerr := r.commitAndReanchor(ctx, dir, class, arm, attempt); cerr != nil {
				return attempts, fmt.Errorf("warmcost: sequence %d task %s: %w", seq.Index, task.ID, cerr)
			}
		}
	}
	return attempts, nil
}

// runTaskInWorkspace runs one task in an already-prepared sequence workspace
// and captures its ledger, raw stream, and verifier result.
func (r *Runner) runTaskInWorkspace(ctx context.Context, task Task, arm Arm, repeat, seqIndex, seqOrdinal, seqTasks int, workspace string, prov Provenance) (Attempt, error) {
	started := r.now()
	fixtureDigest, err := digestFixture(task.Fixture)
	if err != nil {
		return Attempt{}, err
	}
	sessionID := fmt.Sprintf("wc-%s-%s-%s-r%d", r.cfg.RunID, task.ID, arm, repeat)
	attempt := Attempt{
		TaskID:          task.ID,
		Arm:             arm,
		Repeat:          repeat,
		Sequence:        seqIndex,
		SequenceOrdinal: seqOrdinal,
		SequenceTasks:   seqTasks,
		Workspace:       workspace,
		BinaryRevision:  prov.BinaryRevision,
		SidecarRevision: prov.SidecarRevision,
		FixtureDigest:   fixtureDigest,
		ModelID:         r.cfg.Model,
		ModelSettings:   r.cfg.ModelSettings,
		TreatmentEnv:    arm.Env(),
		SessionID:       sessionID,
		StartedAt:       started,
	}

	class := sidecarClass(r.cfg.Retention, arm, sequenceSession(r.cfg.RunID, seqIndex, arm, repeat))
	if err := r.prepareSidecar(class); err != nil {
		return attempt, err
	}
	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	res, err := r.exec(runCtx, ExecRequest{
		Binary: r.cfg.Binary,
		Args:   execArgs(arm, r.cfg.Model, sessionID, task.Prompt),
		Env:    envWithOverrides(os.Environ(), append(append([]string{}, arm.Env()...), r.sidecarEnv(class)...)...),
		Dir:    workspace,
	})
	ended := r.now()
	attempt.EndedAt = ended
	attempt.DurationMS = ended.Sub(started).Milliseconds()
	if err != nil {
		return attempt, fmt.Errorf("warmcost: exec task %s arm %s: %w", task.ID, arm, err)
	}
	// Additive raw artifact: keep the child's full stream-json so the report
	// can quote model output and the expansion reason. The attempt JSON is a
	// projection of the final PipelineResult, not the whole stream.
	rawName := rawStreamName(task.ID, arm, repeat)
	if werr := os.WriteFile(filepath.Join(filepath.Join(r.cfg.OutDir, r.cfg.RunID), rawName), res.Stdout, 0o644); werr != nil {
		return attempt, fmt.Errorf("warmcost: write raw stream for task %s: %w", task.ID, werr)
	}
	attempt.RawStream = rawName

	if parsed, perr := parsePipelineResult(res.Stdout); perr != nil {
		attempt.LedgerError = perr.Error()
	} else {
		attempt.Requests = requestRecords(parsed.UsageRecords)
		attempt.RunID = parsed.RunID
		attempt.RunStatus = parsed.Status
		attempt.CostCoverage = parsed.CostCoverage
		attempt.Totals = totalsFromResult(parsed)
	}

	verified, output, verr := runVerifier(runCtx, workspace, task.Check)
	if verr != nil {
		return attempt, fmt.Errorf("warmcost: verifier task %s: %w", task.ID, verr)
	}
	attempt.VerifierResult = verified
	attempt.VerifierOutput = output
	return attempt, nil
}

// prepareWorkspaceAt resets dir to a fresh copy of the fixture. An empty
// fixture yields an empty workspace, which is correct for a cold task.
func prepareWorkspaceAt(dir, fixture string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("warmcost: reset workspace %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("warmcost: create workspace %s: %w", dir, err)
	}
	if strings.TrimSpace(fixture) == "" {
		return nil
	}
	return copyTree(fixture, dir)
}

// initGitWorkspace makes one sequence workspace a git repository with one
// initial commit and a local identity. The runtime capture path anchors
// evidence at a git revision (verifiedRevision falls back to HEAD), and a
// workspace with no repository produces no reusable record. The initial
// commit gives HEAD a revision; the write task's edits then appear as an
// uncommitted diff, which is what capture snapshots and what a later
// freshness diff compares.
func initGitWorkspace(dir string) error {
	steps := [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=warmcost@example.invalid", "-c", "user.name=warmcost", "commit", "-q", "-m", "warmcost sequence fixture"},
		{"config", "user.email", "warmcost@example.invalid"},
		{"config", "user.name", "warmcost"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("warmcost: git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// commitAndReanchor applies the documented eval contract after a write-phase
// task's verifier passes: commit the verified tree, then advance the write
// run's captured nodes from the pre-verify HEAD to the post-verify commit.
// The stage sandbox refuses the write-shaped `git stash create`, so in-run
// capture anchors at the pre-verify HEAD; the commit does not change what the
// run verified, it only renames those bytes, so the freshness contract holds.
// Both arms commit, so the workspace treatment is identical. Only arms with
// memory on reanchor, because a cold arm captures nothing.
func (r *Runner) commitAndReanchor(ctx context.Context, dir, class string, arm Arm, attempt Attempt) error {
	preHead, err := gitHeadRevision(ctx, dir)
	if err != nil {
		return err
	}
	dirty, err := gitTreeDirty(ctx, dir)
	if err != nil {
		return err
	}
	if err := runGit(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	if dirty {
		if err := runGit(ctx, dir, "-c", "user.email=warmcost@example.invalid", "-c", "user.name=warmcost",
			"commit", "-q", "-m", "warmcost: verified sequence tree"); err != nil {
			return err
		}
	}
	postHead, err := gitHeadRevision(ctx, dir)
	if err != nil {
		return err
	}
	if dirty && postHead == preHead {
		return fmt.Errorf("warmcost: verified tree commit in %s produced no new revision", dir)
	}
	if postHead == preHead || arm.MemoryMode() != "on" {
		return nil
	}
	if r.reanchorFn == nil {
		return nil
	}
	return r.reanchorFn(ctx, dir, class, preHead, postHead, attempt.RunID)
}

// reanchorSequence is the sidecar implementation of the reanchor seam. It
// scopes the update to the write run's capture set when the producer run id is
// known, matching the MVP eval contract.
func (r *Runner) reanchorSequence(ctx context.Context, workspace, class, preHead, postHead, producerRunID string) error {
	if preHead == "" || postHead == "" || preHead == postHead {
		return fmt.Errorf("warmcost: reanchor %s: two distinct revisions are required", workspace)
	}
	client, err := r.sidecarClient(ctx, class)
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("warmcost: reanchor %s: memory sidecar unavailable", workspace)
	}
	project := canonicalWorkspacePath(workspace)
	ids, err := client.CaptureSetIDsForRun(ctx, project, preHead, producerRunID)
	if err != nil {
		return fmt.Errorf("warmcost: resolve capture set for run %q at %s: %w", producerRunID, shortRevision(preHead), err)
	}
	if len(ids) == 0 {
		if producerRunID != "" {
			return fmt.Errorf("warmcost: no capture set for run %q at revision %s in %s", producerRunID, shortRevision(preHead), project)
		}
		return nil
	}
	if _, err := client.ReanchorGraphByIDs(ctx, project, ids, preHead, postHead); err != nil {
		return fmt.Errorf("warmcost: reanchor %d node(s) %s -> %s: %w", len(ids), shortRevision(preHead), shortRevision(postHead), err)
	}
	return nil
}

// sidecarClient resolves the sidecar for one sidecar class. With no sidecar
// root the ambient operator sidecar is used.
func (r *Runner) sidecarClient(ctx context.Context, class string) (*memd.Client, error) {
	if strings.TrimSpace(r.cfg.SidecarRoot) == "" {
		return memd.Resolve(ctx)
	}
	socket := filepath.Join(r.cfg.SidecarRoot, class, "mem.sock")
	client := memd.NewClient(socket)
	if err := client.Health(ctx); err != nil {
		return nil, fmt.Errorf("warmcost: sidecar health at %s: %w", socket, err)
	}
	return client, nil
}

// canonicalWorkspacePath resolves symlinks so the path matches the project
// path the runtime canonicalized when it persisted the capture.
func canonicalWorkspacePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func shortRevision(rev string) string {
	if len(rev) > 10 {
		return rev[:10]
	}
	return rev
}

func gitHeadRevision(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("warmcost: git rev-parse HEAD in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitTreeDirty(ctx context.Context, dir string) (bool, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("warmcost: git status in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("warmcost: git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// failedAttempt records an infrastructure failure honestly instead of dropping
// it, so failed attempts stay in total spend.
func (r *Runner) failedAttempt(task Task, arm Arm, repeat, seqIndex, seqOrdinal, seqTasks int, prov Provenance, cause error) Attempt {
	now := r.now()
	return Attempt{
		TaskID:          task.ID,
		Arm:             arm,
		Repeat:          repeat,
		Sequence:        seqIndex,
		SequenceOrdinal: seqOrdinal,
		SequenceTasks:   seqTasks,
		BinaryRevision:  prov.BinaryRevision,
		SidecarRevision: prov.SidecarRevision,
		ModelID:         r.cfg.Model,
		ModelSettings:   r.cfg.ModelSettings,
		TreatmentEnv:    arm.Env(),
		RunStatus:       "infrastructure_failed",
		CostCoverage:    schemas.CostCoveragePartial,
		VerifierResult:  false,
		LedgerError:     cause.Error(),
		StartedAt:       now,
		EndedAt:         now,
	}
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("warmcost: stat fixture %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("warmcost: fixture %s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// digestFixture hashes the fixture tree in sorted path order. An empty fixture
// hashes the empty string.
func digestFixture(fixture string) (string, error) {
	if strings.TrimSpace(fixture) == "" {
		return "sha256:" + hex.EncodeToString(sha256.New().Sum(nil)), nil
	}
	var files []string
	err := filepath.WalkDir(fixture, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("warmcost: walk fixture: %w", err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		rel, err := filepath.Rel(fixture, f)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, rel)
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// rawStreamName is the additive raw-stream artifact name for one attempt.
func rawStreamName(taskID string, arm Arm, repeat int) string {
	return fmt.Sprintf("raw-%s-%s-r%d.jsonl", taskID, arm, repeat)
}

// writeAttempt writes one attempt JSON artifact.
func writeAttempt(runDir string, attempt Attempt) error {
	data, err := json.MarshalIndent(attempt, "", "  ")
	if err != nil {
		return fmt.Errorf("warmcost: marshal attempt: %w", err)
	}
	name := fmt.Sprintf("attempt-%s-%s-r%d.json", attempt.TaskID, attempt.Arm, attempt.Repeat)
	if err := os.WriteFile(filepath.Join(runDir, name), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("warmcost: write attempt %s: %w", name, err)
	}
	return nil
}
