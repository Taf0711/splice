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
	"syscall"
	"time"

	"github.com/Taf0711/splice/internal/errhint"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Config is one measurement run.
type Config struct {
	RunID   string
	RepoDir string
	Binary  string
	// BinaryRevision is the revision of the binary under test. When set it
	// overrides the repo-derived revision, so an Arm A and an Arm B run record
	// the revision that actually ran instead of the harness repo's HEAD.
	BinaryRevision    string
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
	// NoCommitWrites skips the commit and reanchor after a verified write,
	// so the read runs on the write's uncommitted changes. That is how a
	// user works between two runs, and it is the case where capture
	// anchoring decides whether memory survives from one run to the next.
	NoCommitWrites bool
	// NoMemoryPrefetch turns memory prefetch off in every arm. It is the
	// ablation that isolates what delivering remembered files contributes:
	// the same arms and memory, without the prefetch.
	NoMemoryPrefetch bool
	// ArmEnvOverrides are KEY=VALUE switches that replace the same key in
	// every arm that sets it: the general ablation. A key no arm sets is
	// ignored, so an override never adds a treatment to the cold arm.
	ArmEnvOverrides []string
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
	// OutputPath, when set, receives the child's stdout incrementally as it
	// streams, so a killed or timed-out attempt still leaves a partial raw
	// transcript in the run directory. The full buffer is still returned in
	// ExecResult.Stdout.
	OutputPath string
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
	// BootstrapContrast names the pair that PerTask, Decomposition, and
	// Bootstrap compare. They are always warm minus cold, also in a run
	// that has other arms.
	BootstrapContrast string          `json:"bootstrap_contrast"`
	PerTask           []TaskEffect    `json:"per_task"`
	SkippedTasks      []string        `json:"skipped_tasks,omitempty"`
	Decomposition     Decomposition   `json:"decomposition"`
	Bootstrap         BootstrapResult `json:"bootstrap"`
	// Contrasts compare each arm that extends a base arm (warm-jev extends
	// warm) with that base arm, when the run has both arms.
	Contrasts []ArmContrast `json:"contrasts,omitempty"`
	Claim     Claim         `json:"claim"`
	Coverage  Coverage      `json:"coverage"`
	// CacheLayout is the run-level prefix layout summary (Option B telemetry).
	CacheLayout CacheLayoutReport `json:"cache_layout"`
	Note        string            `json:"note"`
}

// ArmContrast is one treatment arm compared with its control arm, task by
// task. In PerTask, the Cold* fields hold the control and the Warm* fields
// hold the treatment.
type ArmContrast struct {
	Treatment    Arm             `json:"treatment"`
	Control      Arm             `json:"control"`
	PerTask      []TaskEffect    `json:"per_task"`
	SkippedTasks []string        `json:"skipped_tasks,omitempty"`
	Bootstrap    BootstrapResult `json:"bootstrap"`
}

// armContrasts builds one contrast for each configured arm whose base arm is
// also configured. Without it, a run with cold, warm, and warm-jev never
// compares warm-jev with warm; the M6 powered report read the warm-minus-cold
// interval as the warm-jev result.
func armContrasts(arms []Arm, attempts []Attempt, samples int, seed int64) []ArmContrast {
	configured := map[Arm]bool{}
	for _, arm := range arms {
		configured[arm] = true
	}
	var out []ArmContrast
	for _, arm := range arms {
		base := arm.Base()
		if base == arm || !configured[base] {
			continue
		}
		effects, skipped := ComputeTaskEffects(attempts, base, arm)
		out = append(out, ArmContrast{
			Treatment:    arm,
			Control:      base,
			PerTask:      effects,
			SkippedTasks: skipped,
			Bootstrap:    BootstrapTaskDeltas(effects, samples, seed),
		})
	}
	return out
}

// reanchorFunc advances one run's capture set from one revision to another.
// It is a seam so a unit test can prove the commit-and-reanchor step runs
// after a passed write task and does not run after a failed one.
type reanchorFunc func(ctx context.Context, workspace, class, preHead, postHead, producerRunID string) error

// harnessError identifies a failure in campaign setup. It is separate from
// an attempt error because setup runs before the model and must stop the
// campaign rather than count as a model failure.
type harnessError struct {
	taskID string
	err    error
}

func (e *harnessError) Error() string {
	return fmt.Sprintf("warmcost: task %s setup failed: %v", e.taskID, e.err)
}

func (e *harnessError) Unwrap() error { return e.err }

func isHarnessError(err error) bool {
	var target *harnessError
	return errors.As(err, &target)
}

// Runner executes the measurement.
type Runner struct {
	cfg  Config
	exec ExecFunc
	now  func() time.Time
	// reanchorFn is the capture-reanchor seam; it defaults to the sidecar
	// implementation and is replaced only in tests.
	reanchorFn reanchorFunc
	// sleep is the wait seam for network retries; nil uses time.Sleep.
	sleep func(time.Duration)
	// retry is the network retry of the sequence now running; 0 is the
	// first run.
	retry int
	// startSidecarFn starts the sidecar daemon of one class and returns the
	// function that stops it. It defaults to startMemd and is replaced only
	// in tests.
	startSidecarFn func(ctx context.Context, socket string, env []string) (func() error, error)
	// sidecarStops holds the stop function of each sidecar the runner
	// started, by class.
	sidecarStops map[string]func() error
}

// A sequence with an attempt whose requests never reached the model runs
// again after a wait. The campaign stops when the upstream stays
// unreachable, because the remaining attempts would measure the outage.
const (
	networkRetries   = 12
	networkRetryWait = 5 * time.Minute
)

// endedOnConnectivity reports a run whose error event is a provider
// connection drop that outlasted splice's own replays. Such a run measures
// the network, not the arm (P6: a reset in a repair was counted as a wrong
// answer). A timeout is not a drop: a model that stalls is a result, and a
// rerun of the sequence would hide it.
func endedOnConnectivity(stdout []byte) bool {
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		var ev struct{ Type, Message string }
		if json.Unmarshal(line, &ev) != nil || ev.Type != "error" || errhint.Classify(errors.New(ev.Message)) != errhint.Connectivity {
			continue
		}
		msg := strings.ToLower(ev.Message)
		if !strings.Contains(msg, "timeout") && !strings.Contains(msg, "deadline exceeded") && !strings.Contains(msg, "timed out") {
			return true
		}
	}
	return false
}

// maxSocketPath is the longest unix socket path that connects on macOS
// (sun_path is 104 bytes including the terminating NUL).
const maxSocketPath = 103

func upstreamUnreachable(attempts []Attempt) bool {
	for _, a := range attempts {
		if a.UpstreamUnreachable {
			return true
		}
	}
	return false
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
	// A unix socket path longer than the platform limit (104 bytes on
	// macOS, NUL included) fails to connect, and splice then runs silently
	// without memory: P6c and P6d lost memory on 218 of 235 warm runs
	// because a longer run id pushed the path over. Refuse to start.
	if strings.TrimSpace(cfg.SidecarRoot) != "" {
		for _, a := range cfg.Arms {
			class := sidecarClass(cfg.Retention, a, sequenceSession(cfg.RunID, 99, a, cfg.Repeats-1))
			if sock := filepath.Join(cfg.SidecarRoot, class, "mem.sock"); len(sock) > maxSocketPath {
				return nil, fmt.Errorf("warmcost: sidecar socket path %q is %d bytes, over the %d-byte limit; use a shorter --run-id or --sidecar-root", sock, len(sock), maxSocketPath)
			}
		}
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
	runner.startSidecarFn = func(ctx context.Context, socket string, env []string) (func() error, error) {
		return startMemd(ctx, cfg.Binary, socket, env)
	}
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

// groupSequences groups the tasks into shared workspaces. Each write-phase
// task starts one sequence, in taskset order. Each other task runs after its
// precursor write in that write's workspace, in taskset order. With one write
// task, a task with no declared precursor depends on that write. With two or
// more write tasks, every other task must declare its precursor: the M6
// powered campaign put all ten reads behind the last write, so nine reads ran
// without their precursor's code or memory. With no write-phase task, every
// task is its own sequence.
func groupSequences(tasks []Task) ([]sequence, error) {
	var groups [][]Task
	writeGroup := map[string]int{}
	for _, t := range tasks {
		if t.Phase != PhaseWrite {
			continue
		}
		if t.Precursor != "" {
			return nil, fmt.Errorf("warmcost: write task %s declares precursor %q; only a non-write task can have a precursor", t.ID, t.Precursor)
		}
		writeGroup[t.ID] = len(groups)
		groups = append(groups, []Task{t})
	}
	if len(groups) == 0 {
		for _, t := range tasks {
			if t.Precursor != "" {
				return nil, fmt.Errorf("warmcost: task %s declares precursor %q, but the taskset has no write task", t.ID, t.Precursor)
			}
			groups = append(groups, []Task{t})
		}
	} else {
		for _, t := range tasks {
			if t.Phase == PhaseWrite {
				continue
			}
			precursor := t.Precursor
			if precursor == "" {
				if len(groups) > 1 {
					return nil, fmt.Errorf("warmcost: task %s declares no precursor and the taskset has %d write tasks; the pairing is ambiguous", t.ID, len(groups))
				}
				precursor = groups[0][0].ID
			}
			g, ok := writeGroup[precursor]
			if !ok {
				return nil, fmt.Errorf("warmcost: task %s names precursor %q, which is not a write task in the taskset", t.ID, precursor)
			}
			groups[g] = append(groups[g], t)
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
	// Stop every sidecar the runner started, on every exit path. A shared
	// sidecar lives until here.
	defer r.stopSidecars()

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
				for try := 1; upstreamUnreachable(seqAttempts); try++ {
					if try > networkRetries {
						return Aggregate{}, fmt.Errorf("warmcost: sequence %d arm %s repeat %d: the model upstream stayed unreachable after %d retries", seq.Index, arm, repeat, networkRetries)
					}
					fmt.Fprintf(os.Stderr, "warmcost: sequence %d arm %s repeat %d: model upstream unreachable; retry %d of %d in %s\n", seq.Index, arm, repeat, try, networkRetries, networkRetryWait)
					if r.sleep != nil {
						r.sleep(networkRetryWait)
					} else {
						time.Sleep(networkRetryWait)
					}
					r.retry = try
					seqAttempts, seqErr = r.runSequence(ctx, seq, arm, repeat, seqBase, provenance)
				}
				r.retry = 0
				// A fresh sidecar serves only this sequence.
				if r.cfg.Retention == RetentionFresh {
					if err := r.stopSidecar(sidecarClass(r.cfg.Retention, arm, sequenceSession(r.cfg.RunID, seq.Index, arm, repeat))); err != nil {
						fmt.Fprintf(os.Stderr, "warmcost: %v\n", err)
					}
				}
				for _, attempt := range seqAttempts {
					attempts = append(attempts, attempt)
					if err := writeAttempt(runDir, attempt); err != nil {
						return Aggregate{}, err
					}
					// Without credentials every later attempt fails the same
					// way. P6e and the D5 pilot lost 146 and 8 attempts when
					// the key stopped resolving at 00:47 (the keychain locked).
					// A memory arm without its sidecar measures nothing (P6c,
					// P6d, P6g ran their warm arms without memory).
					if attempt.MemoryUnavailable {
						return Aggregate{}, fmt.Errorf("warmcost: %s %s: the memory sidecar was unavailable in a memory arm; stopping the campaign (%d attempts written)", attempt.TaskID, attempt.Arm, len(attempts))
					}
					if attempt.CredentialsMissing {
						return Aggregate{}, fmt.Errorf("warmcost: %s %s: the provider reported a missing or invalid API key; stopping the campaign (%d attempts written)", attempt.TaskID, attempt.Arm, len(attempts))
					}
				}
				if seqErr != nil {
					if isHarnessError(seqErr) {
						return Aggregate{}, seqErr
					}
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

	// Keep the historical warm-minus-cold primary pair when warm is present.
	// For loop and expansion campaigns, use the first configured non-cold arm so
	// the aggregate remains defined while Contrasts reports every arm versus cold.
	primary := ArmWarm
	if !metricByArmHas(r.cfg.Arms, primary) {
		primary = firstNonColdArm(r.cfg.Arms)
	}
	if primary == "" {
		primary = ArmCold
	}
	effects, skipped := ComputeTaskEffects(attempts, ArmCold, primary)

	decomp, err := Decompose(metricByArm[ArmCold], metricByArm[primary])
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
		Provenance:        prov,
		Retention:         r.retentionReport(),
		ArmMetrics:        metrics,
		BootstrapContrast: string(primary) + " - " + string(ArmCold),
		PerTask:           effects,
		SkippedTasks:      skipped,
		Decomposition:     decomp,
		Bootstrap:         boot,
		Contrasts:         armContrasts(r.cfg.Arms, attempts, r.cfg.BootstrapSamples, r.cfg.BootstrapSeed),
		Claim:             claim,
		Coverage:          coverage,
		CacheLayout:       computeCacheLayout(attempts),
		Note:              "Every number comes from the captured authoritative request ledger. Tokens are provider-reported. A partial run withholds the total-cost claim.",
	}
	return agg, nil
}

func metricByArmHas(arms []Arm, want Arm) bool {
	for _, arm := range arms {
		if arm == want {
			return true
		}
	}
	return false
}

func firstNonColdArm(arms []Arm) Arm {
	for _, arm := range arms {
		if arm != ArmCold {
			return arm
		}
	}
	return ""
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
		"SPLICE_MEMD_MAINTENANCE=0",
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

// ensureSidecar starts the sidecar of one class unless the runner already
// started it. The runner owns the daemon, so it can stop it. Without a
// sidecar root the ambient operator sidecar is used and nothing starts.
func (r *Runner) ensureSidecar(ctx context.Context, class string, env []string) error {
	if strings.TrimSpace(r.cfg.SidecarRoot) == "" || r.startSidecarFn == nil {
		return nil
	}
	if _, ok := r.sidecarStops[class]; ok {
		return nil
	}
	stop, err := r.startSidecarFn(ctx, filepath.Join(r.cfg.SidecarRoot, class, "mem.sock"), env)
	if err != nil {
		return fmt.Errorf("start sidecar %s: %w", class, err)
	}
	if r.sidecarStops == nil {
		r.sidecarStops = map[string]func() error{}
	}
	r.sidecarStops[class] = stop
	return nil
}

// stopSidecar stops the sidecar of one class if the runner started it. A
// second call does nothing.
func (r *Runner) stopSidecar(class string) error {
	stop, ok := r.sidecarStops[class]
	if !ok {
		return nil
	}
	delete(r.sidecarStops, class)
	if err := stop(); err != nil {
		return fmt.Errorf("stop sidecar %s: %w", class, err)
	}
	return nil
}

// stopSidecars stops every sidecar the runner started and still runs.
func (r *Runner) stopSidecars() {
	classes := make([]string, 0, len(r.sidecarStops))
	for class := range r.sidecarStops {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		if err := r.stopSidecar(class); err != nil {
			fmt.Fprintf(os.Stderr, "warmcost: %v\n", err)
		}
	}
}

// startMemd starts `splice-memd --serve` with the attempt environment, the
// binary that splice at spliceBinary would spawn, and waits until the socket
// answers. splice then finds a healthy sidecar and does not spawn its own,
// detached one. The returned function stops the daemon with SIGTERM, and
// kills it when it does not exit in time.
func startMemd(ctx context.Context, spliceBinary, socket string, env []string) (func() error, error) {
	binary := memd.BinaryFor(envGetter(env), spliceBinary)
	if binary == "" {
		return nil, fmt.Errorf("no splice-memd binary resolves for %s; set SPLICE_MEMD_BIN", spliceBinary)
	}
	cmd := exec.Command(binary, "--serve")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}
	// exited closes when the daemon has exited, so every wait on it returns
	// and a second stop does nothing.
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	stop := func() error {
		select {
		case <-exited:
			return nil
		default:
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
			return nil
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
			return fmt.Errorf("%s did not stop on SIGTERM in 10s; killed", binary)
		}
	}
	client := memd.NewClient(socket)
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := client.Health(ctx)
		if err == nil {
			return stop, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			_ = stop()
			return nil, fmt.Errorf("%s at %s did not become healthy: %w", binary, socket, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// envGetter looks a key up in an environment list. The last entry wins.
func envGetter(env []string) func(string) string {
	return func(key string) string {
		value := ""
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && k == key {
				value = v
			}
		}
		return value
	}
}

// binaryRevision is the revision recorded on the run and every attempt. An
// explicit Config.BinaryRevision is authoritative, so an operator-supplied
// binary records the revision it was built from rather than the harness repo's
// HEAD. The repo derivation is the fallback when the override is empty.
func (r *Runner) binaryRevision() string {
	if rev := strings.TrimSpace(r.cfg.BinaryRevision); rev != "" {
		return rev
	}
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
	if arm == ArmLoop {
		args = append(args, "--loop")
	}
	return append(args, prompt)
}

// runExec is the production subprocess seam.
func runExec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, req.Binary, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	// A bounded WaitDelay keeps Run from blocking on a grandchild that holds
	// the stderr pipe after the direct child is killed at the deadline.
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var stdout []byte
	var outFile *os.File
	var stdoutBuf *bytes.Buffer
	if req.OutputPath != "" {
		// Give the child the raw file as a direct fd (no copier goroutine).
		// Output is therefore flushed to disk as it streams and survives a
		// kill or timeout, and Run returns as soon as the child dies.
		f, ferr := os.Create(req.OutputPath)
		if ferr != nil {
			return ExecResult{}, fmt.Errorf("warmcost: create raw stream %s: %w", req.OutputPath, ferr)
		}
		outFile = f
		cmd.Stdout = f
	} else {
		buf := new(bytes.Buffer)
		stdoutBuf = buf
		cmd.Stdout = buf
	}
	err := cmd.Run()
	if outFile != nil {
		_ = outFile.Close()
		if data, rerr := os.ReadFile(req.OutputPath); rerr == nil {
			stdout = data
		}
	} else if stdoutBuf != nil {
		stdout = append(stdout, stdoutBuf.Bytes()...)
	}
	res := ExecResult{Stdout: stdout, Stderr: stderr.Bytes()}
	// A context cancellation (per-attempt timeout) must be an error even when
	// the killed process also produced an *exec.ExitError; otherwise a timeout
	// would be misread as a normal non-zero exit.
	if cerr := ctx.Err(); cerr != nil {
		return res, fmt.Errorf("run %s: %w", req.Binary, cerr)
	}
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

// finalEventText finds the last final stream-json event and returns its text,
// which IS the JSON produced after applyRequestLedger.
func finalEventText(stdout []byte) (string, error) {
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
		return "", fmt.Errorf("scan exec output: %w", err)
	}
	if finalText == "" {
		return "", errors.New("no final event with a pipeline result in exec output")
	}
	return finalText, nil
}

// parsePipelineResult parses the final stream-json event text as the pipeline
// result.
func parsePipelineResult(stdout []byte) (schemas.PipelineResult, error) {
	finalText, err := finalEventText(stdout)
	if err != nil {
		return schemas.PipelineResult{}, err
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

// runSetup executes the optional task setup script in the attempt workspace.
// Setup is harness work. Its output is included in the error to make a bad
// checkout or configuration visible to the campaign operator.
func runSetup(ctx context.Context, taskID, dir, setup string) error {
	if strings.TrimSpace(setup) == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", setup)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
		return &harnessError{taskID: taskID, err: err}
	}
	return nil
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
			if isHarnessError(taskErr) {
				return attempts, taskErr
			}
			// Preserve the partial attempt's identity, timings, and raw
			// stream: a timeout or kill still leaves a partial transcript,
			// and dropping it here was why two killed glm attempts left no
			// artifact. Fall back to a synthetic failure only when the
			// attempt never started.
			failed := r.failedAttempt(task, arm, repeat, seq.Index, ordinal, len(seq.Tasks), prov, taskErr)
			if attempt.TaskID == "" {
				attempt = failed
			} else {
				if attempt.RunStatus == "" {
					attempt.RunStatus = failed.RunStatus
				}
				if attempt.LedgerError == "" {
					attempt.LedgerError = failed.LedgerError
				}
				attempt.VerifierResult = false
				attempt.CostCoverage = failed.CostCoverage
				if attempt.StartedAt.IsZero() {
					attempt.StartedAt = failed.StartedAt
				}
				if attempt.EndedAt.IsZero() {
					attempt.EndedAt = failed.EndedAt
					attempt.DurationMS = failed.DurationMS
				}
			}
		}
		attempts = append(attempts, attempt)
		// The documented eval contract: once a write-phase task's verifier
		// passes, the harness commits the verified tree and advances that
		// run's captured nodes from the pre-verify HEAD to the post-verify
		// commit. The stage sandbox refuses the write-shaped stash create,
		// so in-run capture anchors at the pre-verify HEAD; without this
		// step the read task's freshness diff sees the write's edit and
		// rejects the nodes. A failed write is never committed.
		if taskErr == nil && seq.GitInit && task.Phase == PhaseWrite && attempt.VerifierResult && !r.cfg.NoCommitWrites {
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
	if err := runSetup(ctx, task.ID, workspace, task.Setup); err != nil {
		return Attempt{}, err
	}
	sessionID := fmt.Sprintf("wc-%s-%s-%s-r%d", r.cfg.RunID, task.ID, arm, repeat)
	if r.retry > 0 {
		// A rerun after an outage needs its own session: splice refuses to
		// start a session id that already exists (P3: the retry of one
		// sequence failed all six tasks on "splice session already exists").
		sessionID += fmt.Sprintf("-retry%d", r.retry)
	}
	attempt := Attempt{
		TaskID:          task.ID,
		Arm:             arm,
		Repeat:          repeat,
		Sequence:        seqIndex,
		SequenceOrdinal: seqOrdinal,
		SequenceTasks:   seqTasks,
		Workspace:       workspace,
		Binary:          prov.Binary,
		BinaryRevision:  prov.BinaryRevision,
		SidecarRevision: prov.SidecarRevision,
		FixtureDigest:   fixtureDigest,
		ModelID:         r.cfg.Model,
		ModelSettings:   r.cfg.ModelSettings,
		TreatmentEnv:    r.armEnv(arm),
		SessionID:       sessionID,
		StartedAt:       started,
	}

	class := sidecarClass(r.cfg.Retention, arm, sequenceSession(r.cfg.RunID, seqIndex, arm, repeat))
	if err := r.prepareSidecar(class); err != nil {
		return attempt, err
	}
	env := envWithOverrides(os.Environ(), append(r.armEnv(arm), r.sidecarEnv(class)...)...)
	if arm.MemoryMode() == "on" {
		if err := r.ensureSidecar(ctx, class, env); err != nil {
			return attempt, &harnessError{taskID: task.ID, err: err}
		}
	}
	// Name the raw stream BEFORE launch and stream into it, so a kill or a
	// timeout leaves the partial transcript on disk even when the harness
	// cannot finish the attempt.
	rawName := rawStreamName(task.ID, arm, repeat)
	rawPath := filepath.Join(filepath.Join(r.cfg.OutDir, r.cfg.RunID), rawName)
	attempt.RawStream = rawName
	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	res, err := r.exec(runCtx, ExecRequest{
		Binary:     r.cfg.Binary,
		Args:       execArgs(arm, r.cfg.Model, sessionID, task.Prompt),
		Env:        env,
		Dir:        workspace,
		OutputPath: rawPath,
	})
	ended := r.now()
	attempt.EndedAt = ended
	attempt.DurationMS = ended.Sub(started).Milliseconds()
	attempt.UpstreamUnreachable = bytes.Contains(res.Stdout, []byte("upstream unreachable: ")) || endedOnConnectivity(res.Stdout)
	attempt.MemoryUnavailable = arm.MemoryMode() == "on" && bytes.Contains(res.Stdout, []byte("memory sidecar unavailable"))
	attempt.CredentialsMissing = bytes.Contains(res.Stdout, []byte("auth error: your API key is missing or invalid"))
	if arm.MemoryMode() == "on" {
		attempt.Trace, attempt.TraceUnavailable = r.exportTrace(ctx, class, workspace, sessionID)
	}
	if err != nil {
		// Record why the attempt ended. A per-attempt timeout is an
		// infrastructure outcome, never a model failure, and it keeps the
		// partial transcript that streamed to disk.
		if runCtx.Err() == context.DeadlineExceeded {
			attempt.RunStatus = "timeout"
			attempt.LedgerError = fmt.Sprintf("attempt timeout after %s; partial raw stream retained", r.cfg.Timeout)
		} else {
			attempt.RunStatus = "infrastructure_failed"
			attempt.LedgerError = fmt.Sprintf("exec failed: %v", err)
		}
		if len(res.Stdout) > 0 {
			_ = os.WriteFile(rawPath, res.Stdout, 0o644)
		}
		return attempt, fmt.Errorf("warmcost: exec task %s arm %s: %w", task.ID, arm, err)
	}
	// Additive raw artifact: keep the child's full stream-json so the report
	// can quote model output and the expansion reason. The attempt JSON is a
	// projection of the final PipelineResult, not the whole stream.
	if werr := os.WriteFile(rawPath, res.Stdout, 0o644); werr != nil {
		return attempt, fmt.Errorf("warmcost: write raw stream for task %s: %w", task.ID, werr)
	}

	if parsed, perr := parsePipelineResult(res.Stdout); perr != nil {
		attempt.LedgerError = perr.Error()
	} else {
		attempt.Requests = requestRecords(parsed.UsageRecords)
		attempt.RunID = parsed.RunID
		attempt.RunStatus = parsed.Status
		attempt.CostCoverage = parsed.CostCoverage
		attempt.Totals = totalsFromResult(parsed)
		// Option B: decode the cache telemetry from the same final event with
		// harness-local structs and merge it onto the typed records. A decode
		// failure leaves the telemetry unreported, never reported as zero.
		if tel, terr := parseCacheTelemetry(res.Stdout); terr != nil {
			attempt.LedgerError = terr.Error()
		} else {
			attempt.Requests = mergeCacheTelemetry(attempt.Requests, tel)
			attempt.CacheTelemetryReported = tel.Reported
			attempt.PromptLayoutFlips = tel.Flips
		}
		attempt.Rounds = computeRounds(attempt.Requests)
		attempt.LayoutStability = computeLayoutStability(attempt.Requests)
	}

	verified, output, verr := runVerifier(runCtx, workspace, task.Check)
	if verr != nil {
		return attempt, fmt.Errorf("warmcost: verifier task %s: %w", task.ID, verr)
	}
	attempt.VerifierResult = verified
	attempt.VerifierOutput = output
	attempt.Memory = parseMemoryHealth(res.Stdout)
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
		// Capture anchors at a host snapshot of the verified worktree
		// (worktrees.SnapshotCommit), not at the pre-verify HEAD. The commit
		// above records the same bytes, so those nodes are already fresh and
		// there is nothing to advance.
		return nil
	}
	if _, err := client.ReanchorGraphByIDs(ctx, project, ids, preHead, postHead); err != nil {
		return fmt.Errorf("warmcost: reanchor %d node(s) %s -> %s: %w", len(ids), shortRevision(preHead), shortRevision(postHead), err)
	}
	return nil
}

// exportTrace writes the attempt's stored RunOutcome trace to
// <run dir>/traces/<session>-trace.json, before the runner stops the sidecar,
// so decision-replay --traces reads it directly. It returns the path relative
// to the run directory, or the reason no trace was written.
func (r *Runner) exportTrace(ctx context.Context, class, workspace, sessionID string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// The export reads a sidecar that the attempt used. It never spawns one,
	// so the ambient case reads the default socket without a spawn.
	socket := memd.DefaultSocketPath()
	if strings.TrimSpace(r.cfg.SidecarRoot) != "" {
		socket = filepath.Join(r.cfg.SidecarRoot, class, "mem.sock")
	}
	client := memd.NewClient(socket)
	if err := client.Health(ctx); err != nil {
		return "", fmt.Sprintf("memory sidecar unavailable at %s: %v", socket, err)
	}
	roots := []string{workspace}
	if canonical := canonicalWorkspacePath(workspace); canonical != workspace {
		roots = append(roots, canonical)
	}
	trace, err := client.TraceForSession(ctx, roots, sessionID)
	if err != nil {
		return "", fmt.Sprintf("trace query failed: %v", err)
	}
	if trace == nil {
		return "", "no stored trace for this session"
	}
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return "", fmt.Sprintf("encode trace: %v", err)
	}
	rel := filepath.Join("traces", sessionID+"-trace.json")
	path := filepath.Join(r.cfg.OutDir, r.cfg.RunID, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Sprintf("create trace dir: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Sprintf("write trace: %v", err)
	}
	return rel, ""
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
		Binary:          prov.Binary,
		BinaryRevision:  prov.BinaryRevision,
		SidecarRevision: prov.SidecarRevision,
		ModelID:         r.cfg.Model,
		ModelSettings:   r.cfg.ModelSettings,
		TreatmentEnv:    r.armEnv(arm),
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

// withClassifierOff keeps the default trajectory classifier out of an eval arm.
// A SPLICE_FLAGS override replaces the arm's own flags, so when the override
// does not name pipeline.trajectory_classifier, the flag is turned off.
func withClassifierOff(override string) string {
	value := strings.TrimPrefix(override, flagsEnvPrefix)
	if strings.Contains(value, "pipeline.trajectory_classifier") {
		return override
	}
	if value == "" {
		return flagsEnvPrefix + "-pipeline.trajectory_classifier"
	}
	return override + ",-pipeline.trajectory_classifier"
}

// armEnv is the arm's treatment environment with the run-level ablations
// applied.
func (r *Runner) armEnv(arm Arm) []string {
	env := append([]string{}, arm.Env()...)
	overrides := append([]string{}, r.cfg.ArmEnvOverrides...)
	if r.cfg.NoMemoryPrefetch {
		overrides = append(overrides, "SPLICE_MEMORY_PREFETCH=off")
	}
	for _, o := range overrides {
		key, _, ok := strings.Cut(o, "=")
		if !ok {
			continue
		}
		if key == "SPLICE_FLAGS" {
			o = withClassifierOff(o)
		}
		for i, kv := range env {
			if strings.HasPrefix(kv, key+"=") {
				env[i] = o
			}
		}
	}
	return env
}
