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

// Runner executes the measurement.
type Runner struct {
	cfg  Config
	exec ExecFunc
	now  func() time.Time
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
	return &Runner{cfg: cfg, exec: seam, now: time.Now}, nil
}

// Run executes every arm, every task, every repeat, and returns the aggregate.
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

	var attempts []Attempt

	// orderTasks puts write-phase tasks first so a shared sidecar receives a
	// write before a read.
	tasks := orderTasks(r.cfg.Tasks)

	// Interleave arm order across repeats to reduce drift.
	for repeat := 0; repeat < r.cfg.Repeats; repeat++ {
		arms := append([]Arm(nil), r.cfg.Arms...)
		if repeat%2 == 1 {
			for i, j := 0, len(arms)-1; i < j; i, j = i+1, j-1 {
				arms[i], arms[j] = arms[j], arms[i]
			}
		}
		for _, task := range tasks {
			for _, arm := range arms {
				attempt, err := r.runAttempt(ctx, task, arm, repeat, provenance)
				if err != nil {
					attempt = r.failedAttempt(task, arm, repeat, provenance, err)
				}
				attempts = append(attempts, attempt)
				if err := writeAttempt(runDir, attempt); err != nil {
					return Aggregate{}, err
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

// runAttempt runs one task/arm/repeat and captures the authoritative ledger.
func (r *Runner) runAttempt(ctx context.Context, task Task, arm Arm, repeat int, prov Provenance) (Attempt, error) {
	started := r.now()
	workspace, cleanup, err := prepareWorkspace(task.Fixture)
	if err != nil {
		return Attempt{}, err
	}
	if !r.cfg.KeepWorkspaces {
		defer cleanup()
	}
	fixtureDigest, err := digestFixture(task.Fixture)
	if err != nil {
		return Attempt{}, err
	}
	sessionID := fmt.Sprintf("wc-%s-%s-%s-r%d", r.cfg.RunID, task.ID, arm, repeat)
	attempt := Attempt{
		TaskID:          task.ID,
		Arm:             arm,
		Repeat:          repeat,
		BinaryRevision:  prov.BinaryRevision,
		SidecarRevision: prov.SidecarRevision,
		FixtureDigest:   fixtureDigest,
		ModelID:         r.cfg.Model,
		ModelSettings:   r.cfg.ModelSettings,
		TreatmentEnv:    arm.Env(),
		SessionID:       sessionID,
		StartedAt:       started,
	}

	class := sidecarClass(r.cfg.Retention, arm, sessionID)
	if err := r.prepareSidecar(class); err != nil {
		return Attempt{}, err
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

// failedAttempt records an infrastructure failure honestly instead of dropping
// it, so failed attempts stay in total spend.
func (r *Runner) failedAttempt(task Task, arm Arm, repeat int, prov Provenance, cause error) Attempt {
	now := r.now()
	return Attempt{
		TaskID:          task.ID,
		Arm:             arm,
		Repeat:          repeat,
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
		OrderingRule: "write-phase tasks run before read-phase tasks, and the runner interleaves arm order per repeat, so a write on the shared sidecar precedes a read of it",
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

// prepareWorkspace copies the fixture into a fresh temp directory. An empty
// fixture yields an empty workspace, which is correct for a cold task.
func prepareWorkspace(fixture string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "warmcost-ws-")
	if err != nil {
		return "", nil, fmt.Errorf("warmcost: create workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if strings.TrimSpace(fixture) == "" {
		return dir, cleanup, nil
	}
	if err := copyTree(fixture, dir); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
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
