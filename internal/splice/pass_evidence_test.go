package splice

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func writePassFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// requireSandboxGit skips when the stage sandbox that runs git reads is not
// available. The git pass tests depend on it through the HEAD summary.
func requireSandboxGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available {
		t.Skipf("host native sandbox backend unavailable: %s", backend.Message)
	}
}

// measureStep measures one judged pass the way runIterationLoop does: the HEAD
// summary, the pass builder, then commit to move the baseline to this pass end.
func measureStep(t *testing.T, tr *passEvidenceTracker, dir string, iteration int) passMeasure {
	t.Helper()
	ctx := context.Background()
	head := summarizeWorkspaceChanges(ctx, dir)
	m := tr.measure(ctx, iteration, head)
	tr.commit(ctx, m, head)
	return m
}

func statusOf(files []schemas.ChangedFile) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = f.Status
	}
	return out
}

// repoState returns the bytes of .git/index and the sorted list of files under
// .git/objects. Equal states mean nothing was written to the repository.
func repoState(t *testing.T, dir string) (string, []string) {
	t.Helper()
	index, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var objects []string
	err = filepath.WalkDir(filepath.Join(dir, ".git", "objects"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			objects = append(objects, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk objects: %v", err)
	}
	sort.Strings(objects)
	return string(index), objects
}

func TestPassEvidenceIgnoresPreexistingDirt(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "one\n")
	gitInit(t, dir)
	writePassFile(t, dir, "dirty.txt", "already dirty before the run\n")
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

	m := measureStep(t, tr, dir, 1)
	if !m.ok || len(m.summary.ChangedFiles) != 0 {
		t.Fatalf("pass with no edit: ok=%v files=%v, want none", m.ok, m.summary.ChangedFiles)
	}
	if head := summarizeWorkspaceChanges(ctx, dir); len(head.ChangedFiles) != 1 || head.ChangedFiles[0].Path != "dirty.txt" {
		t.Fatalf("HEAD builder = %v, want dirty.txt only", head.ChangedFiles)
	}
}

func TestPassEvidenceUnchangedEarlierEdit(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

	writePassFile(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	pass1 := measureStep(t, tr, dir, 1)
	if got := statusOf(pass1.summary.ChangedFiles); !slices.Equal(keysOf(got), []string{"a.go"}) {
		t.Fatalf("pass 1 = %v, want a.go", got)
	}

	pass2 := measureStep(t, tr, dir, 2)
	if !pass2.ok || len(pass2.summary.ChangedFiles) != 0 {
		t.Fatalf("pass 2 changed nothing but reported %v", pass2.summary.ChangedFiles)
	}
	if head := summarizeWorkspaceChanges(ctx, dir); len(head.ChangedFiles) != 1 {
		t.Fatalf("HEAD builder must still count the earlier edit, got %v", head.ChangedFiles)
	}
}

func TestPassEvidenceCountsOnlyTheLatestPass(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	writePassFile(t, dir, "b.go", "package b\n\nfunc B() {}\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

	writePassFile(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	measureStep(t, tr, dir, 1)

	// Pass 2 edits only b.go, which HEAD still holds as its previous text.
	// One line is removed and two are added.
	writePassFile(t, dir, "b.go", "package b\n\nfunc B2() {}\nfunc B3() {}\n")
	pass2 := measureStep(t, tr, dir, 2)
	if got := statusOf(pass2.summary.ChangedFiles); !slices.Equal(keysOf(got), []string{"b.go"}) || got["b.go"] != "modified" {
		t.Fatalf("pass 2 = %v, want exactly b.go modified", got)
	}
	if pass2.pair.pass.LinesAdded != 2 || pass2.pair.pass.LinesRemoved != 1 {
		t.Fatalf("pass 2 lines +%d -%d, want +2 -1", pass2.pair.pass.LinesAdded, pass2.pair.pass.LinesRemoved)
	}
}

func TestPassEvidenceRevertedFileIsReported(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

	writePassFile(t, dir, "a.go", "package a\n\nvar X = 1\n")
	measureStep(t, tr, dir, 1)
	// Pass 2 restores HEAD. The file changed in this pass, so it is reported.
	writePassFile(t, dir, "a.go", "package a\n")
	pass2 := measureStep(t, tr, dir, 2)
	if got := statusOf(pass2.summary.ChangedFiles); got["a.go"] != passStatusReverted {
		t.Fatalf("pass 2 = %v, want a.go reverted", got)
	}
	// The previous text had three lines and the reverted text has one.
	if pass2.pair.pass.LinesAdded != 0 || pass2.pair.pass.LinesRemoved != 2 {
		t.Fatalf("revert lines +%d -%d, want +0 -2", pass2.pair.pass.LinesAdded, pass2.pair.pass.LinesRemoved)
	}
}

func TestPassEvidenceLeavesRepositoryUntouched(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	gitInit(t, dir)
	ctx := context.Background()

	indexBefore, objectsBefore := repoState(t, dir)
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "a.go", "package a\n\nvar X = 1\n")
	writePassFile(t, dir, "new.go", "package n\n")
	measureStep(t, tr, dir, 1)
	measureStep(t, tr, dir, 2)

	indexAfter, objectsAfter := repoState(t, dir)
	if indexAfter != indexBefore {
		t.Fatal("the pass builder changed the repository index")
	}
	if strings.Join(objectsAfter, "\n") != strings.Join(objectsBefore, "\n") {
		t.Fatalf("the pass builder wrote repository objects:\nbefore %v\nafter  %v", objectsBefore, objectsAfter)
	}
}

// TestPassEvidenceRunsNoHostGitForTheTree puts a git shim first on PATH and
// measures one pass. The tree is never built by the builder, so the shim must
// see only cat-file reads, which go through the stage sandbox.
func TestPassEvidenceRunsNoHostGitForTheTree(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	writePassFile(t, dir, "b.go", "package b\n\nfunc B() {}\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "b.go", "package b\n\nfunc B2() {}\n")
	head := summarizeWorkspaceChanges(ctx, dir)

	logPath := installGitShim(t, dir)
	m := tr.measure(ctx, 1, head)
	if !m.ok {
		t.Fatalf("measure degraded: %s", m.pair.pass.DegradedReason)
	}
	calls, _ := os.ReadFile(logPath)
	if !strings.Contains(string(calls), "cat-file") {
		t.Fatalf("expected the sandboxed cat-file read to run, log = %q", calls)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if !strings.HasPrefix(line, "SANDBOX denied ") {
			t.Fatalf("a git call ran outside the stage sandbox (the probe write succeeded): %q", line)
		}
		for _, forbidden := range []string{"write-tree", "read-tree", " add ", "rev-parse", "hash-object"} {
			if strings.Contains(" "+line+" ", forbidden) {
				t.Fatalf("the pass builder ran %q on the host path: %q", forbidden, line)
			}
		}
	}
}

// TestPassEvidenceNeverRunsRepoFilters checks that the pass builder runs no
// repository clean filter. The HEAD summary is taken before the filter is
// installed, so the only git work left to the builder is the sandboxed read of
// HEAD blobs. A clean filter that leaves a marker outside the workspace must
// leave no marker after the pass.
func TestPassEvidenceNeverRunsRepoFilters(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "one\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "a.txt", "two\n")
	head := summarizeWorkspaceChanges(ctx, dir)

	marker := filepath.Join(t.TempDir(), "filter-ran")
	cfg, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(cfg, "\n[filter \"marker\"]\n\tclean = touch %s && cat\n", marker); err != nil {
		t.Fatal(err)
	}
	_ = cfg.Close()
	writePassFile(t, dir, ".gitattributes", "*.txt filter=marker\n")

	m := tr.measure(ctx, 1, head)
	if !m.ok {
		t.Fatalf("measure degraded: %s", m.pair.pass.DegradedReason)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the pass builder ran a repository clean filter")
	}
}

func TestPassEvidenceNonGitCreateModifyDelete(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "keep.txt", "a\nb\n")
	writePassFile(t, dir, "gone.txt", "x\ny\nz\n")
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

	writePassFile(t, dir, "keep.txt", "a\nc\n")
	writePassFile(t, dir, "fresh.txt", "new\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	pass1 := measureStep(t, tr, dir, 1)
	want := map[string]string{"keep.txt": "modified", "fresh.txt": "created", "gone.txt": "deleted"}
	if got := statusOf(pass1.summary.ChangedFiles); !equalStringMaps(got, want) {
		t.Fatalf("pass 1 statuses = %v, want %v", got, want)
	}
	// keep.txt: -b +c. fresh.txt: +new. gone.txt: -x -y -z.
	if pass1.pair.pass.LinesAdded != 2 || pass1.pair.pass.LinesRemoved != 4 {
		t.Fatalf("pass 1 lines +%d -%d, want +2 -4", pass1.pair.pass.LinesAdded, pass1.pair.pass.LinesRemoved)
	}

	pass2 := measureStep(t, tr, dir, 2)
	if len(pass2.summary.ChangedFiles) != 0 {
		t.Fatalf("pass 2 changed nothing but reported %v", pass2.summary.ChangedFiles)
	}
}

// TestPassEvidenceOverBudgetKeepsFileEvidence fills the kept-content budget.
// The changed file over the budget still gets file-level evidence, its line
// counts are skipped, and the summary is marked truncated. The pass does not
// degrade.
func TestPassEvidenceOverBudgetKeepsFileEvidence(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("line\n", 12*1024) // 60 KiB per file
	for i := 0; i < 5; i++ {
		writePassFile(t, dir, fmt.Sprintf("f%d.txt", i), big)
	}
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

	writePassFile(t, dir, "f4.txt", big+"more\n")
	m := measureStep(t, tr, dir, 1)
	if !m.ok || m.pair.pass.DegradedReason != "" {
		t.Fatalf("over budget must not degrade the pass: ok=%v reason=%q", m.ok, m.pair.pass.DegradedReason)
	}
	if got := statusOf(m.summary.ChangedFiles); got["f4.txt"] != "modified" {
		t.Fatalf("file-level evidence = %v, want f4.txt modified", got)
	}
	if !m.summary.Truncated || m.pair.pass.LinesAdded != 0 {
		t.Fatalf("truncated=%v lines +%d, want truncated with counts skipped", m.summary.Truncated, m.pair.pass.LinesAdded)
	}
	if again := measureStep(t, tr, dir, 2); len(again.summary.ChangedFiles) != 0 {
		t.Fatalf("pass 2 changed nothing but reported %v", again.summary.ChangedFiles)
	}
}

func TestPassEvidenceNoBaselineDegrades(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "a\n")
	tr := &passEvidenceTracker{workDir: dir}
	m := tr.measure(context.Background(), 1, schemas.ChangeSummary{})
	if m.ok || m.pair.pass.DegradedReason == "" {
		t.Fatalf("missing baseline must degrade the pass record, got ok=%v reason=%q", m.ok, m.pair.pass.DegradedReason)
	}
}

// TestPassEvidenceHeadDegradedDegradesThePass makes .git a directory that is
// not a repository, so the HEAD summary degrades. The pass record says so and
// the run is not affected.
func TestPassEvidenceHeadDegradedDegradesThePass(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "a\n")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	head := summarizeWorkspaceChanges(ctx, dir)
	if head.DegradedReason == "" {
		t.Skip("HEAD summary did not degrade for this fixture")
	}
	tr := newPassEvidenceTracker(ctx, dir, head)
	m := tr.measure(ctx, 1, head)
	if m.ok || m.pair.pass.DegradedReason == "" {
		t.Fatalf("degraded HEAD must degrade the pass, got ok=%v reason=%q", m.ok, m.pair.pass.DegradedReason)
	}
}

func TestPassEvidenceTrackerDegradesInsteadOfFailing(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "a\n")
	ctx := context.Background()
	tracker := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	if !tracker.haveBase {
		t.Fatal("tracker should capture a baseline for a plain directory")
	}

	writePassFile(t, dir, "a.txt", "a\nb\n")
	m := measureStep(t, tracker, dir, 1)
	if !m.ok || m.pair.pass.DegradedReason != "" {
		t.Fatalf("pass 1 degraded: ok=%v reason=%q", m.ok, m.pair.pass.DegradedReason)
	}
	if got := m.pair.pass.FilesChanged; len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("pass 1 files = %v, want a.txt", got)
	}

	tracker.haveBase = false
	m = tracker.measure(ctx, 2, summarizeWorkspaceChanges(ctx, dir))
	if m.ok || m.pair.pass.DegradedReason == "" {
		t.Fatalf("missing baseline should degrade the pass record, got ok=%v reason=%q", m.ok, m.pair.pass.DegradedReason)
	}
	if m.pair.pass.PassChanged != nil {
		t.Fatal("a degraded pass record must not claim a PassChanged value")
	}
}

// TestPassEvidencePeekReadsChangeSoFar covers the repair
// boundary. The peek measures the change so far in the current pass, and the
// flag-on value replaces the HEAD value there.
func TestPassEvidencePeekReadsChangeSoFar(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "repair.txt", "fixed\n")

	m := tr.measure(ctx, 3, summarizeWorkspaceChanges(ctx, dir))
	if !m.ok {
		t.Fatalf("peek degraded: %s", m.pair.pass.DegradedReason)
	}
	if got := m.pair.pass.FilesChanged; !slices.Equal(got, []string{"repair.txt"}) {
		t.Fatalf("repair peek pass files = %v, want only repair.txt", got)
	}
	if got := m.pair.head.FilesChanged; !slices.Contains(got, "seed.txt") {
		t.Fatalf("repair peek HEAD files = %v, want the cumulative list", got)
	}
	// The peek did not move the baseline, so the pass end still sees repair.txt.
	end := measureStep(t, tr, dir, 3)
	if got := end.pair.pass.FilesChanged; !slices.Equal(got, []string{"repair.txt"}) {
		t.Fatalf("pass end after a peek = %v, want repair.txt", got)
	}
}

// repairFileWriter writes repair.txt on every re-entry, so a repair changes the
// workspace in the middle of its pass.
type repairFileWriter struct {
	dir   string
	calls int
}

func (*repairFileWriter) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: false, Description: "writing code changes"}
}

func (w *repairFileWriter) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	w.calls++
	if err := os.WriteFile(filepath.Join(w.dir, "repair.txt"), []byte(fmt.Sprintf("fix %d\n", w.calls)), 0o644); err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	return schemas.HarnessStageOutput{Summary: "revision applied", Confidence: 0.9, ChangedFiles: []string{"repair.txt"}}, nil
}

// TestPassEvidenceRepairBoundaryReadsTheFlaggedBuilder runs a repair whose
// test runner keeps failing, under the classifier. The repair boundary's state
// reads the pass builder's change so far with the flag on, and the HEAD
// builder with it off. The other builder is the shadow.
func TestPassEvidenceRepairBoundaryReadsTheFlaggedBuilder(t *testing.T) {
	for _, usePass := range []bool{false, true} {
		t.Run(fmt.Sprintf("flag=%v", usePass), func(t *testing.T) {
			dir := t.TempDir()
			writePassFile(t, dir, "seed.txt", "seed\n")
			ctx := context.Background()
			tracker := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

			eval := &trajectoryEvaluator{label: "continue"}
			advisor := classifierAdvisor(t, eval)
			options := PipelineRunConfig{
				Advisor:          advisor,
				Flags:            flags.Set{flags.PassEvidence: usePass},
				passEvidence:     tracker,
				ClassifierPolicy: newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false),
			}
			options.ClassifierPolicy.maxIterations = defaultMaxIterations
			options.ClassifierPolicy.tokenBudget = 1000
			plan := schemas.ExecutionPlan{
				Tier:          schemas.TierLight,
				RequestIntent: "repair boundary reads the flagged builder",
				Stages:        []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}},
				TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
			}
			writer := &repairFileWriter{dir: dir}
			registry := stageRegistry{"code_writer": writer, "test_runner": &identicalFailureRunner{}}
			var outputs []schemas.HarnessStageOutput
			var records []schemas.StageRecord
			priorSummaries := map[string]string{}
			priorChangedFiles := map[string][]string{}
			initial := (&identicalFailureRunner{}).current()

			if _, _, err := attemptLocalRepair(
				ctx, "repair-run", 1, plan, registry, nil,
				options, dir, nil, nil, nil, nil,
				time.Now().Add(time.Minute), NewStageExecutionBudget(0), &records, &outputs,
				&priorSummaries, &priorChangedFiles, initial,
			); err != nil {
				t.Fatalf("repair: %v", err)
			}
			if writer.calls == 0 {
				t.Fatal("the repair writer never ran")
			}

			var repairRecord *schemas.DecisionDispositionRecord
			dispositions := advisor.Records()
			for i := range dispositions {
				if dispositions[i].Snapshot.Trajectory != nil && dispositions[i].Snapshot.Trajectory.Boundary.Repair >= 1 {
					repairRecord = &dispositions[i]
				}
			}
			if repairRecord == nil {
				t.Fatal("no repair boundary was classified")
			}
			history := repairRecord.Snapshot.Trajectory.Boundary.History
			if len(repairRecord.EvidenceShadow) != len(history) {
				t.Fatalf("shadow has %d entries for a %d-entry History", len(repairRecord.EvidenceShadow), len(history))
			}
			stateFiles := history[len(history)-1].FilesChanged
			shadow := repairRecord.EvidenceShadow[len(repairRecord.EvidenceShadow)-1]
			if usePass {
				if !slices.Equal(stateFiles, []string{"repair.txt"}) {
					t.Fatalf("flag on: repair state files = %v, want only repair.txt", stateFiles)
				}
				if shadow.Builder != passBuilderHead || !slices.Contains(shadow.FilesChanged, "seed.txt") {
					t.Fatalf("flag on: repair shadow = %+v, want the HEAD record", shadow)
				}
			} else {
				if !slices.Contains(stateFiles, "seed.txt") || !slices.Contains(stateFiles, "repair.txt") {
					t.Fatalf("flag off: repair state files = %v, want the HEAD-relative list", stateFiles)
				}
				if shadow.Builder != passBuilderPass || !slices.Equal(shadow.FilesChanged, []string{"repair.txt"}) {
					t.Fatalf("flag off: repair shadow = %+v, want the pass record", shadow)
				}
			}
		})
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// workspaceWritingFailureStage writes out.txt on every call and then fails, so
// each pass changes the workspace and the run takes the failed-pass path.
type workspaceWritingFailureStage struct {
	dir   string
	calls int
	body  func(call int) string
}

func (*workspaceWritingFailureStage) Capabilities() stages.Capabilities {
	return stages.Capabilities{}
}

func (stage *workspaceWritingFailureStage) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	stage.calls++
	if err := os.WriteFile(filepath.Join(stage.dir, "out.txt"), []byte(stage.body(stage.calls)), 0o644); err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	return schemas.HarnessStageOutput{}, fmt.Errorf("failure %d", stage.calls)
}

// TestPassEvidenceShadowsRideTheClassifierBoundary runs the classifier path
// over passes that change the workspace in pass 1 and not in pass 2. Each
// boundary's shadow holds one record per History entry, and the flag picks
// which builder is the state source and which is the shadow.
func TestPassEvidenceShadowsRideTheClassifierBoundary(t *testing.T) {
	for _, usePass := range []bool{false, true} {
		t.Run(fmt.Sprintf("flag=%v", usePass), func(t *testing.T) {
			dir := t.TempDir()
			writePassFile(t, dir, "seed.txt", "seed\n")
			eval := &trajectoryEvaluator{label: "continue"}
			advisor := classifierAdvisor(t, eval)
			// Pass 1 creates out.txt. Pass 2 writes the same bytes: no change.
			stage := &workspaceWritingFailureStage{dir: dir, body: func(int) string { return "one\n" }}
			plan := schemas.ExecutionPlan{
				Tier:          schemas.TierLight,
				RequestIntent: "shadow evidence on the classifier boundary",
				Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
				TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
			}
			options := PipelineRunConfig{Advisor: advisor, Flags: flags.Set{flags.PassEvidence: usePass}}
			trace := newRunTraceAccumulator(nil, "shadow-run", "session", dir, plan, "off", nil)
			if _, err := runIterationLoop(context.Background(), "shadow-run", plan, stageRegistry{"code_writer": stage}, nil, options, dir, nil, nil, nil, trace, nil, nil); err != nil {
				t.Fatalf("runIterationLoop: %v", err)
			}

			records := advisor.Records()
			if len(records) == 0 {
				t.Fatal("no classifier disposition records were written")
			}
			wantShadow := passBuilderPass
			if usePass {
				wantShadow = passBuilderHead
			}
			for _, record := range records {
				if record.Snapshot.Trajectory == nil {
					t.Fatalf("record has no trajectory snapshot: %+v", record.Snapshot)
				}
				history := record.Snapshot.Trajectory.Boundary.History
				if len(record.EvidenceShadow) != len(history) {
					t.Fatalf("shadow has %d entries for a %d-entry History", len(record.EvidenceShadow), len(history))
				}
				for _, shadow := range record.EvidenceShadow {
					if shadow.Builder != wantShadow {
						t.Fatalf("shadow builder = %q, want %q", shadow.Builder, wantShadow)
					}
				}
			}
			last := records[len(records)-1].EvidenceShadow
			current := last[len(last)-1]
			if usePass {
				// A non-git HEAD summary lists every file, so seed.txt appears too.
				if !slices.Contains(current.FilesChanged, "out.txt") || !slices.Contains(current.FilesChanged, "seed.txt") {
					t.Fatalf("HEAD shadow for pass 2 = %v, want out.txt and seed.txt (cumulative)", current.FilesChanged)
				}
			} else if !slices.Equal(current.FilesChanged, []string{"out.txt"}) {
				// Pass 1 failed and was not judged, so its edit is still in the
				// change measured for pass 2.
				t.Fatalf("pass shadow for pass 2 = %v, want out.txt from the unjudged pass 1", current.FilesChanged)
			}

			if len(trace.passEvidence) != 2*stage.calls {
				t.Fatalf("trace has %d pass records for %d passes, want two per pass", len(trace.passEvidence), stage.calls)
			}
			// The pass attribution reaches the trace: the failing writer is a
			// node with a stage_error and no changed files.
			if nodes := trace.passEvidence[0].Nodes; len(nodes) != 1 || nodes[0].Node != "code_writer" || nodes[0].Outcome.Failure != schemas.NodeFailureStageError {
				t.Fatalf("pass 1 nodes = %+v, want one code_writer stage_error", nodes)
			}
		})
	}
}

// TestPassEvidenceStateReadsTheFlaggedBuilder checks the judged state that the
// trajectory reads. Pass 2 changes nothing, so with the flag on its state
// reports no change, and with the flag off it keeps the HEAD-relative change.
func TestPassEvidenceStateReadsTheFlaggedBuilder(t *testing.T) {
	for _, usePass := range []bool{false, true} {
		t.Run(fmt.Sprintf("flag=%v", usePass), func(t *testing.T) {
			dir := t.TempDir()
			writePassFile(t, dir, "seed.txt", "seed\n")
			eval := &trajectoryEvaluator{label: "step_back"}
			advisor := classifierAdvisor(t, eval)
			stage := &workspaceWritingFailureStage{dir: dir, body: func(int) string { return "one\n" }}
			plan := schemas.ExecutionPlan{
				Tier:          schemas.TierLight,
				RequestIntent: "judged state reads the flagged builder",
				Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
				TokenBudget:   schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"},
			}
			options := PipelineRunConfig{Advisor: advisor, Flags: flags.Set{flags.PassEvidence: usePass}}
			trace := newRunTraceAccumulator(nil, "state-run", "session", dir, plan, "off", nil)
			if _, err := runIterationLoop(context.Background(), "state-run", plan, stageRegistry{"code_writer": stage}, nil, options, dir, nil, nil, nil, trace, nil, nil); err != nil {
				t.Fatalf("runIterationLoop: %v", err)
			}
			if len(trace.history) < 2 {
				t.Fatalf("want two judged states, got %d", len(trace.history))
			}
			pass2 := trace.history[1]
			if usePass {
				if len(pass2.FilesChanged) != 0 || pass2.PassChanged == nil || *pass2.PassChanged {
					t.Fatalf("flag on: pass 2 state = files %v pass_changed %v, want none and false", pass2.FilesChanged, pass2.PassChanged)
				}
			} else if !slices.Contains(pass2.FilesChanged, "out.txt") {
				t.Fatalf("flag off: pass 2 state = %v, want the HEAD-relative change", pass2.FilesChanged)
			}
		})
	}
}

// TestPassEvidenceDegradesWithoutFailingTheRun makes the HEAD summary degrade
// for every pass. Each pass records why the pass builder degraded, the run
// still ends on its own terms, and the flag-on state falls back to HEAD.
func TestPassEvidenceDegradesWithoutFailingTheRun(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	stage := &workspaceWritingFailureStage{dir: dir, body: func(call int) string { return fmt.Sprintf("%d\n", call) }}
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "degrade without failing",
		Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
	}
	var progress strings.Builder
	options := PipelineRunConfig{Flags: flags.Set{flags.PassEvidence: true}, OnReasoning: func(s string) { progress.WriteString(s) }}
	trace := newRunTraceAccumulator(nil, "degrade-run", "session", dir, plan, "off", nil)
	result, err := runIterationLoop(context.Background(), "degrade-run", plan, stageRegistry{"code_writer": stage}, nil, options, dir, nil, nil, nil, trace, nil, nil)
	if err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed from the stage, not from the evidence", result.Status)
	}
	if len(trace.passEvidence) == 0 {
		t.Fatal("no pass records traced")
	}
	for _, rec := range trace.passEvidence {
		if rec.Builder == passBuilderPass && rec.DegradedReason == "" {
			t.Fatalf("pass record for iteration %d has no DegradedReason: %+v", rec.Iteration, rec)
		}
	}
	if !strings.Contains(progress.String(), "[evidence] pass builder degraded") {
		t.Fatalf("flag-on degradation must emit a progress notice, got %q", progress.String())
	}
}

// TestPassNodeAttributionNamesEachWriter runs one pass with two writing nodes,
// a checker that reports failing tests, and a stage that fails. Each node's
// evidence names its own files and its own closed failure kind.
func TestPassNodeAttributionNamesEachWriter(t *testing.T) {
	plan := schemas.ExecutionPlan{
		Tier:          schemas.TierLight,
		RequestIntent: "attribute each node",
		Stages:        []schemas.ExecutionStage{{Name: "writer_a"}, {Name: "writer_b"}, {Name: "checker"}, {Name: "broken"}},
	}
	registry := stageRegistry{
		"writer_a": outputStage{output: schemas.HarnessStageOutput{Summary: "wrote a", ChangedFiles: []string{"a.go"}}},
		"writer_b": outputStage{output: schemas.HarnessStageOutput{Summary: "wrote b", ChangedFiles: []string{"b.go", "b_test.go"}}},
		"checker":  &identicalFailureRunner{},
		"broken":   terminalDetailStage{},
	}
	var nodes []schemas.NodeEvidence
	options := PipelineRunConfig{passNodes: &nodes}
	if _, _, _, err := runPass(context.Background(), "attr-run", 2, plan, registry, runFakeProvider{}, options, t.TempDir(), nil, time.Time{}, nil, nil, nil, NewStageExecutionBudget(0)); err != nil {
		t.Fatalf("runPass: %v", err)
	}
	byNode := map[string]schemas.NodeEvidence{}
	for _, n := range nodes {
		if n.Iteration != 2 {
			t.Fatalf("node %s carries iteration %d, want 2", n.Node, n.Iteration)
		}
		byNode[n.Node] = n
	}
	if got := byNode["writer_a"].ChangedFiles; !slices.Equal(got, []string{"a.go"}) {
		t.Fatalf("writer_a files = %v, want a.go", got)
	}
	if got := byNode["writer_b"].ChangedFiles; !slices.Equal(got, []string{"b.go", "b_test.go"}) {
		t.Fatalf("writer_b files = %v, want b.go and b_test.go", got)
	}
	if n := byNode["checker"]; len(n.ChangedFiles) != 0 || n.Outcome.Failure != schemas.NodeFailureTestFailure || n.Outcome.Status != schemas.StageCompleted {
		t.Fatalf("checker = %+v, want completed with a test_failure and no files", n)
	}
	if n := byNode["broken"]; n.Outcome.Status != schemas.StageFailed || n.Outcome.Failure != schemas.NodeFailureStageError {
		t.Fatalf("broken = %+v, want failed with a stage_error", n)
	}
	if n := byNode["writer_a"]; n.Outcome.Failure != schemas.NodeFailureNone {
		t.Fatalf("writer_a failure = %q, want none", n.Outcome.Failure)
	}
}

func TestPassNodeDriftCrossCheck(t *testing.T) {
	head := schemas.ChangeSummary{IsRepo: true, ChangedFiles: []schemas.ChangedFile{{Path: "a.go"}, {Path: "sneaky.go"}}}
	nodes := []schemas.NodeEvidence{{Node: "code_writer", ChangedFiles: []string{"a.go", "ghost.go"}}}
	unclaimed, unseen := passNodeDrift(head, []string{"a.go", "sneaky.go"}, nodes)
	if !slices.Equal(unclaimed, []string{"sneaky.go"}) {
		t.Fatalf("unclaimed = %v, want sneaky.go", unclaimed)
	}
	if !slices.Equal(unseen, []string{"ghost.go"}) {
		t.Fatalf("unseen = %v, want ghost.go", unseen)
	}

	// A capped HEAD list does not hold beyond.go, so the builder cannot judge
	// it and it is not reported as unseen. f1.go is listed, so it is.
	var capped schemas.ChangeSummary
	capped.IsRepo = true
	for i := 0; i < defaultMaxSummaryFiles; i++ {
		capped.ChangedFiles = append(capped.ChangedFiles, schemas.ChangedFile{Path: fmt.Sprintf("f%d.go", i)})
	}
	claims := []schemas.NodeEvidence{{Node: "code_writer", ChangedFiles: []string{"beyond.go", "f1.go"}}}
	_, unseen = passNodeDrift(capped, nil, claims)
	if !slices.Equal(unseen, []string{"f1.go"}) {
		t.Fatalf("capped unseen = %v, want only f1.go", unseen)
	}
}

func TestUnclaimedNoticeIsBounded(t *testing.T) {
	files := []string{"f0.go", "f1.go", "f2.go", "f3.go", "f4.go", "f5.go", "f6.go"}
	got := unclaimedNotice(3, files)
	want := "[evidence] pass 3: 7 changed file(s) no node reported: f0.go, f1.go, f2.go, f3.go, f4.go, +2 more"
	if got != want {
		t.Fatalf("notice = %q\nwant    %q", got, want)
	}
}

// TestUnclaimedFileNoticeFollowsTheFlag runs a writer that changes a file and
// reports nothing. The drift is recorded either way, and the notice names the
// file only with the pass evidence flag on.
func TestUnclaimedFileNoticeFollowsTheFlag(t *testing.T) {
	for _, usePass := range []bool{false, true} {
		t.Run(fmt.Sprintf("flag=%v", usePass), func(t *testing.T) {
			dir := t.TempDir()
			writePassFile(t, dir, "seed.txt", "seed\n")
			stage := &workspaceWritingFailureStage{dir: dir, body: func(int) string { return "quiet\n" }}
			plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "drift", Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
			var notices []string
			options := PipelineRunConfig{OnNotice: func(s string) { notices = append(notices, s) }, Flags: flags.Set{flags.PassEvidence: usePass}}
			trace := newRunTraceAccumulator(nil, "drift-run", "session", dir, plan, "off", nil)
			if _, err := runIterationLoop(context.Background(), "drift-run", plan, stageRegistry{"code_writer": stage}, nil, options, dir, nil, nil, nil, trace, nil, nil); err != nil {
				t.Fatalf("runIterationLoop: %v", err)
			}
			found := false
			for _, n := range notices {
				if strings.Contains(n, "out.txt") && strings.Contains(n, "no node reported") {
					found = true
				}
			}
			if found != usePass {
				t.Fatalf("notice shown = %v with flag=%v, notices %q", found, usePass, notices)
			}
			// The drift is recorded on every run as shadow evidence.
			// The trace holds the head record, then the pass record, per pass.
			if drift := trace.passEvidence[1].Unclaimed; !slices.Equal(drift, []string{"out.txt"}) {
				t.Fatalf("pass 1 unclaimed = %v, want out.txt", drift)
			}
		})
	}
}

// TestNoProgressReasonNamesWriterOnlyWhenNamed pins the rule text. An empty
// writer keeps the reason the flag-off path has always produced.
func TestNoProgressReasonNamesWriterOnlyWhenNamed(t *testing.T) {
	no := false
	stall := []schemas.IterationState{
		{Iteration: 1, PassChanged: &no, StateHash: "h1"},
		{Iteration: 2, PassChanged: &no, StateHash: "h2"},
		{Iteration: 3, PassChanged: &no, StateHash: "h3"},
	}
	plain := evaluateTrajectoryJudgmentsFor(stall, nil, "")
	if plain.Reason != "The last three iterations produced no workspace change." {
		t.Fatalf("flag-off reason = %q, want the unchanged text", plain.Reason)
	}
	named := evaluateTrajectoryJudgmentsFor(stall, nil, "code_writer")
	want := "The last three iterations produced no workspace change. code_writer changed no files this pass."
	if named.Reason != want {
		t.Fatalf("named reason = %q, want %q", named.Reason, want)
	}
}

// TestAttributedWriterFollowsTheFlag pins the gate every rule call site uses.
// The flag decides whether the writer name reaches a rule reason, and a plan
// without a code writer names no writer.
func TestAttributedWriterFollowsTheFlag(t *testing.T) {
	defaultPlan := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "code_writer", Type: "code_writer"}, {Name: "test_runner", Type: "test_runner"}}}
	renamed := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "author", Type: "code_writer"}}}
	agentOnly := schemas.ExecutionPlan{Stages: []schemas.ExecutionStage{{Name: "fixer", Type: "agent"}, {Name: "test_runner", Type: "test_runner"}}}
	if got := attributedWriter(PipelineRunConfig{}, defaultPlan); got != "" {
		t.Fatalf("flag off names %q, want none", got)
	}
	on := PipelineRunConfig{Flags: flags.Set{flags.PassEvidence: true}}
	for _, tc := range []struct {
		name          string
		plan          schemas.ExecutionPlan
		writer, label string
	}{
		{"default", defaultPlan, "code_writer", "code_writer"},
		{"renamed", renamed, "author", "author"},
		{"agent only", agentOnly, "", "pass"},
	} {
		if got := attributedWriter(on, tc.plan); got != tc.writer {
			t.Errorf("%s: flag on names %q, want %q", tc.name, got, tc.writer)
		}
		if got := boundaryStage(tc.plan); got != tc.label {
			t.Errorf("%s: boundary stage = %q, want %q", tc.name, got, tc.label)
		}
	}
	stall := []schemas.IterationState{{Iteration: 1, PassChanged: new(bool), StateHash: "h1"}, {Iteration: 2, PassChanged: new(bool), StateHash: "h2"}, {Iteration: 3, PassChanged: new(bool), StateHash: "h3"}}
	if reason := evaluateTrajectoryJudgmentsFor(stall, nil, attributedWriter(on, agentOnly)).Reason; strings.Contains(reason, "code_writer") {
		t.Fatalf("agent-only reason names a missing stage: %q", reason)
	}
}

// TestPassEvidenceFailedPassEditsSurviveToNextPass: a pass that fails and does
// not enter History must not move the baseline. Its edit appears in the next
// pass's change.
func TestPassEvidenceFailedPassEditsSurviveToNextPass(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))

	writePassFile(t, dir, "a.go", "package a\n")
	failed := tr.measure(ctx, 1, summarizeWorkspaceChanges(ctx, dir))
	if !failed.ok || !slices.Equal(failed.pair.pass.FilesChanged, []string{"a.go"}) {
		t.Fatalf("pass 1 = %v, want a.go", failed.pair.pass.FilesChanged)
	}
	// Pass 1 is not judged, so no commit. Pass 2 changes nothing.
	pass2 := tr.measure(ctx, 2, summarizeWorkspaceChanges(ctx, dir))
	if !slices.Equal(pass2.pair.pass.FilesChanged, []string{"a.go"}) {
		t.Fatalf("pass 2 after an uncommitted failed pass = %v, want a.go", pass2.pair.pass.FilesChanged)
	}
	tr.commit(ctx, pass2, summarizeWorkspaceChanges(ctx, dir))
	pass3 := tr.measure(ctx, 3, summarizeWorkspaceChanges(ctx, dir))
	if len(pass3.pair.pass.FilesChanged) != 0 {
		t.Fatalf("pass 3 after a committed pass = %v, want none", pass3.pair.pass.FilesChanged)
	}
}

// TestPassNodeDriftNormalizesClaimedPaths: a claim written as "./a.go" or
// "sub//x.go" names the same file as the pass builder's "a.go" and "sub/x.go".
func TestPassNodeDriftNormalizesClaimedPaths(t *testing.T) {
	nodes := []schemas.NodeEvidence{{Node: "code_writer", ChangedFiles: []string{"./a.go", "sub//x.go"}}}
	unclaimed, unseen := passNodeDrift(schemas.ChangeSummary{}, []string{"a.go", "sub/x.go"}, nodes)
	if len(unclaimed) != 0 || len(unseen) != 0 {
		t.Fatalf("normalized claims disagree: unclaimed=%v unseen=%v", unclaimed, unseen)
	}
}

// TestRepairReentryRecordsNodeOutcome: a repair re-entry of the writer and the
// re-run test runner each add a node result to the pass attribution.
func TestRepairReentryRecordsNodeOutcome(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	ctx := context.Background()
	plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "repair nodes", Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}
	writer := &repairFileWriter{dir: dir}
	registry := stageRegistry{"code_writer": writer, "test_runner": &identicalFailureRunner{}}
	var nodes []schemas.NodeEvidence
	options := PipelineRunConfig{passNodes: &nodes}
	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	if _, _, err := attemptLocalRepair(ctx, "repair-nodes", 1, plan, registry, nil, options, dir, nil, nil, nil, nil,
		time.Now().Add(time.Minute), NewStageExecutionBudget(0), &records, &outputs,
		&priorSummaries, &priorChangedFiles, (&identicalFailureRunner{}).current()); err != nil {
		t.Fatalf("repair: %v", err)
	}
	var writerNode, runnerNode *schemas.NodeEvidence
	for i := range nodes {
		switch nodes[i].Node {
		case "code_writer":
			writerNode = &nodes[i]
		case "test_runner":
			runnerNode = &nodes[i]
		}
	}
	if writerNode == nil || !slices.Equal(writerNode.ChangedFiles, []string{"repair.txt"}) {
		t.Fatalf("writer re-entry node = %+v, want repair.txt", writerNode)
	}
	if runnerNode == nil || runnerNode.Outcome.Failure != schemas.NodeFailureTestFailure {
		t.Fatalf("test re-run node = %+v, want a test_failure", runnerNode)
	}
}

// changedFilesError is a stage error that still reports changed files.
type changedFilesError struct{ files []string }

func (e changedFilesError) Error() string               { return "writer failed after a write" }
func (e changedFilesError) StageChangedFiles() []string { return e.files }

// failingRepairWriter fails every re-entry after it writes repair.txt.
type failingRepairWriter struct{ dir string }

func (*failingRepairWriter) Capabilities() stages.Capabilities {
	return stages.Capabilities{ModelFree: false, Description: "writing code changes"}
}

func (w *failingRepairWriter) Run(context.Context, schemas.HarnessStageInput, zeroruntime.Provider, stages.StageOptions) (schemas.HarnessStageOutput, error) {
	if err := os.WriteFile(filepath.Join(w.dir, "repair.txt"), []byte("half\n"), 0o644); err != nil {
		return schemas.HarnessStageOutput{}, err
	}
	return schemas.HarnessStageOutput{}, changedFilesError{files: []string{"repair.txt"}}
}

// TestRepairFailedReentryRecordsNodeOutcome: a writer re-entry that fails
// still adds a failed node result, with the files it changed, to the pass
// attribution.
func TestRepairFailedReentryRecordsNodeOutcome(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	ctx := context.Background()
	plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "repair nodes", Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}}}
	registry := stageRegistry{"code_writer": &failingRepairWriter{dir: dir}, "test_runner": &identicalFailureRunner{}}
	var nodes []schemas.NodeEvidence
	options := PipelineRunConfig{passNodes: &nodes}
	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	if _, _, err := attemptLocalRepair(ctx, "repair-nodes", 1, plan, registry, nil, options, dir, nil, nil, nil, nil,
		time.Now().Add(time.Minute), NewStageExecutionBudget(0), &records, &outputs,
		&priorSummaries, &priorChangedFiles, (&identicalFailureRunner{}).current()); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Node != "code_writer" || nodes[0].Outcome.Status != schemas.StageFailed ||
		nodes[0].Outcome.Failure != schemas.NodeFailureStageError || !slices.Equal(nodes[0].ChangedFiles, []string{"repair.txt"}) {
		t.Fatalf("nodes = %+v, want one failed code_writer node with repair.txt", nodes)
	}
}

// TestRepairBoundaryPassChangedFollowsTheFlag: a flag-on repair boundary sets
// PassChanged from the pass builder's change so far. A flag-off boundary keeps
// it unset, as before.
func TestRepairBoundaryPassChangedFollowsTheFlag(t *testing.T) {
	for _, usePass := range []bool{false, true} {
		t.Run(fmt.Sprintf("flag=%v", usePass), func(t *testing.T) {
			dir := t.TempDir()
			writePassFile(t, dir, "seed.txt", "seed\n")
			ctx := context.Background()
			plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "repair progress", Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}},
				TokenBudget: schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"}}
			advisor := classifierAdvisor(t, &trajectoryEvaluator{label: "continue"})
			options := PipelineRunConfig{Advisor: advisor, Flags: flags.Set{flags.PassEvidence: usePass}, passEvidence: newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))}
			options.ClassifierPolicy = newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
			options.ClassifierPolicy.maxIterations = defaultMaxIterations
			options.ClassifierPolicy.tokenBudget = 1000
			writer := &repairFileWriter{dir: dir}
			registry := stageRegistry{"code_writer": writer, "test_runner": &identicalFailureRunner{}}
			var outputs []schemas.HarnessStageOutput
			var records []schemas.StageRecord
			priorSummaries := map[string]string{}
			priorChangedFiles := map[string][]string{}
			if _, _, err := attemptLocalRepair(ctx, "repair-progress", 1, plan, registry, nil, options, dir, nil, nil, nil, nil,
				time.Now().Add(time.Minute), NewStageExecutionBudget(0), &records, &outputs,
				&priorSummaries, &priorChangedFiles, (&identicalFailureRunner{}).current()); err != nil {
				t.Fatalf("repair: %v", err)
			}
			var repairRecord *schemas.DecisionDispositionRecord
			dispositions := advisor.Records()
			for i := range dispositions {
				if dispositions[i].Snapshot.Trajectory != nil && dispositions[i].Snapshot.Trajectory.Boundary.Repair >= 1 {
					repairRecord = &dispositions[i]
				}
			}
			if repairRecord == nil {
				t.Fatal("no repair boundary was classified")
			}
			history := repairRecord.Snapshot.Trajectory.Boundary.History
			last := history[len(history)-1]
			if usePass {
				if last.PassChanged == nil || !*last.PassChanged {
					t.Fatalf("flag on: repair state PassChanged = %v, want true (repair.txt changed)", last.PassChanged)
				}
			} else if last.PassChanged != nil {
				t.Fatalf("flag off: repair state PassChanged = %v, want unset", *last.PassChanged)
			}
		})
	}
}

// TestRepairBoundaryDegradedPassIsNoticed: a flag-on repair boundary whose
// pass builder degrades tells the user, as a pass boundary does.
func TestRepairBoundaryDegradedPassIsNoticed(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	ctx := context.Background()
	plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "repair degraded", Stages: []schemas.ExecutionStage{{Name: "code_writer"}, {Name: "test_runner"}},
		TokenBudget: schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"}}
	advisor := classifierAdvisor(t, &trajectoryEvaluator{label: "continue"})
	var progress strings.Builder
	options := PipelineRunConfig{Advisor: advisor, Flags: flags.Set{flags.PassEvidence: true}, passEvidence: &passEvidenceTracker{workDir: dir},
		OnReasoning: func(text string) { progress.WriteString(text) }}
	options.ClassifierPolicy = newHostTrajectoryPolicy(advisor, config.TrajectoryConfig{}, false)
	options.ClassifierPolicy.maxIterations = defaultMaxIterations
	options.ClassifierPolicy.tokenBudget = 1000
	registry := stageRegistry{"code_writer": &repairFileWriter{dir: dir}, "test_runner": &identicalFailureRunner{}}
	var outputs []schemas.HarnessStageOutput
	var records []schemas.StageRecord
	priorSummaries := map[string]string{}
	priorChangedFiles := map[string][]string{}
	if _, _, err := attemptLocalRepair(ctx, "repair-degraded", 1, plan, registry, nil, options, dir, nil, nil, nil, nil,
		time.Now().Add(time.Minute), NewStageExecutionBudget(0), &records, &outputs,
		&priorSummaries, &priorChangedFiles, (&identicalFailureRunner{}).current()); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !strings.Contains(progress.String(), "[evidence] pass builder degraded") {
		t.Fatalf("no degraded notice in progress:\n%s", progress.String())
	}
}

// TestTraceHeadRecordCarriesJudgedProgress: a judged pass's head record in the
// trace carries the digest progress, as the HEAD-relative state does. A pass
// that is not judged leaves it unset.
func TestTraceHeadRecordCarriesJudgedProgress(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	eval := &trajectoryEvaluator{label: "step_back"}
	advisor := classifierAdvisor(t, eval)
	stage := &workspaceWritingFailureStage{dir: dir, body: func(int) string { return "one\n" }}
	plan := schemas.ExecutionPlan{Tier: schemas.TierLight, RequestIntent: "trace progress", Stages: []schemas.ExecutionStage{{Name: "code_writer"}},
		TokenBudget: schemas.TokenBudget{TotalInputBudget: 1000, TotalOutputBudget: 1000, OverflowPolicy: "abort"}}
	trace := newRunTraceAccumulator(nil, "trace-progress", "session", dir, plan, "off", nil)
	options := PipelineRunConfig{Advisor: advisor}
	if _, err := runIterationLoop(context.Background(), "trace-progress", plan, stageRegistry{"code_writer": stage}, nil, options, dir, nil, nil, nil, trace, nil, nil); err != nil {
		t.Fatalf("runIterationLoop: %v", err)
	}
	if len(trace.history) == 0 {
		t.Fatal("no judged pass in the trace")
	}
	// Pass 1 is the first judged pass. Its head record is the first entry.
	if trace.passEvidence[0].Builder != passBuilderHead || trace.passEvidence[0].PassChanged == nil {
		t.Fatalf("judged pass 1 head record = %+v, want PassChanged set", trace.passEvidence[0])
	}
}

// TestPassEvidenceOneBatchReadPerMeasure: a pass that edits several tracked
// files reads their HEAD text through one git cat-file --batch process, under
// the sandbox. The shim counts the cat-file processes.
func TestPassEvidenceOneBatchReadPerMeasure(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	writePassFile(t, dir, "b.go", "package b\n")
	writePassFile(t, dir, "c.go", "package c\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		writePassFile(t, dir, name, "package x\n\nfunc X() {}\n")
	}
	head := summarizeWorkspaceChanges(ctx, dir)
	logPath := installGitShim(t, dir)
	m := tr.measure(ctx, 1, head)
	if !m.ok || len(m.summary.ChangedFiles) != 3 {
		t.Fatalf("measure ok=%v files=%v, want three changed files", m.ok, m.summary.ChangedFiles)
	}
	calls, _ := os.ReadFile(logPath)
	if n := strings.Count(string(calls), "cat-file --batch"); n != 1 {
		t.Fatalf("cat-file --batch ran %d times, want once per measure. log:\n%s", n, calls)
	}
}

// installGitShim puts a git shim first on PATH. The shim logs each call to the
// returned file in logDir, prefixed with SANDBOX denied or SANDBOX open.
// logDir must be writable inside the stage sandbox (the workspace): a git
// read can write no system temp directory. The prefix comes
// from a probe write under HOME, which lies outside the workspace and TMPDIR.
// The stage sandbox denies that write. A host process does not, so a host
// spawn shows as "open". The shim then runs the real git.
func installGitShim(t *testing.T, logDir string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	logPath := filepath.Join(logDir, "calls.log")
	probe := filepath.Join(home, ".splice-pass-sandbox-probe")
	script := fmt.Sprintf("#!/bin/sh\n"+
		"if touch %[1]q 2>/dev/null; then rm -f %[1]q; state=open; else state=denied; fi\n"+
		"echo \"SANDBOX $state $*\" >> %[2]q\n"+
		"exec %[3]q \"$@\"\n", probe, logPath, realGit)
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// TestPassEvidenceExactNamesInGitWorkspace: a name with a space and a name with
// non-ASCII bytes, edited in pass 2, are reported under their exact paths with
// correct line counts.
func TestPassEvidenceExactNamesInGitWorkspace(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "my file.go", "package m\n")
	writePassFile(t, dir, "café.go", "package c\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "my file.go", "package m\n\nfunc M() {}\n")
	writePassFile(t, dir, "café.go", "package c\n\nfunc C() {}\n")
	m := measureStep(t, tr, dir, 1)
	want := map[string]string{"my file.go": "modified", "café.go": "modified"}
	if got := statusOf(m.summary.ChangedFiles); !equalStringMaps(got, want) {
		t.Fatalf("pass 1 statuses = %v, want %v", got, want)
	}
	if m.pair.pass.LinesAdded != 4 || m.pair.pass.LinesRemoved != 0 {
		t.Fatalf("pass 1 lines +%d -%d, want +4 -0", m.pair.pass.LinesAdded, m.pair.pass.LinesRemoved)
	}
}

// TestPassEvidenceDiffStopsAtTheCap: the line-count text stops growing at the
// diff cap, and the summary is truncated. Each file below adds one line of
// about 200 KiB, so the first few fill the cap.
func TestPassEvidenceDiffStopsAtTheCap(t *testing.T) {
	acc := &passAccumulator{}
	big := strings.Repeat("x", 200*1024)
	for i := 0; i < 10; i++ {
		acc.add(fmt.Sprintf("f%d.txt", i), "modified", "", true, big+"\n", true)
	}
	if acc.diff.Len() > 2*defaultMaxDiffBytes {
		t.Fatalf("diff grew to %d bytes, want it held near the %d cap", acc.diff.Len(), defaultMaxDiffBytes)
	}
	if !acc.summary(false).Truncated {
		t.Fatal("a capped diff must mark the summary truncated")
	}
}

// TestPassEvidenceManyShortLinesGetNoCounts: about 100,000 one-character
// lines fit the diff budget as text, but their count lines ("+ x\n") do not.
// The file is listed with no counts, and the summary is truncated.
func TestPassEvidenceManyShortLinesGetNoCounts(t *testing.T) {
	acc := &passAccumulator{}
	text := strings.Repeat("x\n", 100000)
	if len(text) > defaultMaxDiffBytes {
		t.Fatalf("text is %d bytes, want it under the %d budget", len(text), defaultMaxDiffBytes)
	}
	acc.add("lines.txt", "created", "", true, text, true)
	summary := acc.summary(false)
	if !summary.Truncated || summary.DiffText != "" || len(summary.ChangedFiles) != 1 {
		t.Fatalf("truncated = %v, diff = %d bytes, files = %d, want truncated with no counts and one file", summary.Truncated, len(summary.DiffText), len(summary.ChangedFiles))
	}
}

// TestPassEvidenceLargeFileIsCappedNotRead: a file over the hash cap is marked
// capped and compared by size. Its content is not held.
func TestPassEvidenceLargeFileIsCappedNotRead(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("a", maxPassHashBytes+10)
	writePassFile(t, dir, "huge.bin", big)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	entry, skip := scanPassPath(root, "huge.bin")
	if skip || !entry.capped || entry.size != int64(len(big)) {
		t.Fatalf("scan = %+v skip=%v, want a capped entry of %d bytes", entry, skip, len(big))
	}
	if text, known := readPassText(root, "huge.bin", entry); known || text != "" {
		t.Fatal("a capped file's text must be unknown and empty")
	}
}

// TestPassEvidenceUnreadableFileIsUnknownNotDegraded: a file that cannot be
// read is listed, its counts are unknown, and the pass is not degraded.
func TestPassEvidenceUnreadableFileIsUnknownNotDegraded(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "ok.txt", "one\n")
	writePassFile(t, dir, "locked.txt", "two\n")
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, schemas.ChangeSummary{})
	if err := os.Chmod(filepath.Join(dir, "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked.txt"), 0o644) })
	if f, err := os.Open(filepath.Join(dir, "locked.txt")); err == nil {
		_ = f.Close()
		t.Skip("the file is readable by this user (root?), so unreadability cannot be set up")
	}
	m := tr.measure(ctx, 1, schemas.ChangeSummary{})
	if !m.ok || m.pair.pass.DegradedReason != "" {
		t.Fatalf("an unreadable file degraded the pass: ok=%v reason=%q", m.ok, m.pair.pass.DegradedReason)
	}
	if got := statusOf(m.summary.ChangedFiles); got["locked.txt"] == "" {
		t.Fatalf("unreadable file not listed: %v", got)
	}
	if !m.summary.Truncated {
		t.Fatal("an unreadable file must mark the summary truncated")
	}
}

// TestPassEvidenceBinaryFileCountsZeroLines: a binary file (NUL in its first
// bytes) is listed and counts zero lines, as git does.
func TestPassEvidenceBinaryFileCountsZeroLines(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "data.bin", "one\x00two\n")
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "data.bin", "one\x00two\nthree\nfour\n")
	m := measureStep(t, tr, dir, 1)
	if got := statusOf(m.summary.ChangedFiles); got["data.bin"] != "modified" {
		t.Fatalf("binary file not listed: %v", got)
	}
	if m.pair.pass.LinesAdded != 0 || m.pair.pass.LinesRemoved != 0 {
		t.Fatalf("binary counts +%d -%d, want zero", m.pair.pass.LinesAdded, m.pair.pass.LinesRemoved)
	}
}

// TestHeadBlobReadNamesItsFailure: a path that the batch cannot carry is a
// named error, so the pass degrades with a reason instead of reading wrongly.
func TestHeadBlobReadNamesItsFailure(t *testing.T) {
	_, err := openHeadBlobs(context.Background(), t.TempDir(), []string{"bad\nname.go"})
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("openHeadBlobs error = %v, want a named line-break error", err)
	}
}

// TestPassEvidenceDoesNotFollowSymlinkedParent: pass 2 replaces the directory
// of an edited file with a symbolic link to a directory outside the workspace.
// The pass builder must not read the outside file for its line counts.
func TestPassEvidenceDoesNotFollowSymlinkedParent(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "sub/a.txt", "one\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "sub/a.txt", "two\n")
	measureStep(t, tr, dir, 1)

	outside := t.TempDir()
	writePassFile(t, outside, "a.txt", "secret\nsecret\nsecret\n")
	if err := os.RemoveAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	pass2 := measureStep(t, tr, dir, 2)
	if !pass2.ok {
		t.Fatalf("measure degraded: %s", pass2.pair.pass.DegradedReason)
	}
	// The new link counts one line, its target text. The outside file holds
	// three lines that must not count.
	if strings.Contains(pass2.summary.DiffText, "+ secret") || !pass2.summary.Truncated {
		t.Fatalf("pass 2 truncated=%v, diff:\n%s\nthe builder read a file outside the workspace", pass2.summary.Truncated, pass2.summary.DiffText)
	}
}

// TestHeadSummaryDoesNotReadOutsideTheWorkspace: an untracked symbolic link to
// a file outside the workspace must not put that file's text in the HEAD diff.
func TestHeadSummaryDoesNotReadOutsideTheWorkspace(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	gitInit(t, dir)
	outside := t.TempDir()
	writePassFile(t, outside, "secret.txt", "outside-marker\n")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	head := summarizeWorkspaceChanges(context.Background(), dir)
	if head.DegradedReason != "" {
		t.Fatalf("HEAD summary degraded: %s", head.DegradedReason)
	}
	if strings.Contains(head.DiffText, "outside-marker") {
		t.Fatalf("the HEAD summary read a file outside the workspace:\n%s", head.DiffText)
	}
}

// TestWalkSummaryDoesNotReadOutsideTheWorkspace: in a workspace without Git, a
// relative symbolic link that leads out of the workspace must not put the
// outside file's text in the summary. A regular file inside it still counts.
func TestWalkSummaryDoesNotReadOutsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "a.go", "package a\n")
	outside := t.TempDir()
	writePassFile(t, outside, "secret.txt", "outside-marker\n")
	target, err := filepath.Rel(dir, filepath.Join(outside, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	summary := walkChangeSummary(dir)
	if strings.Contains(summary.DiffText, "outside-marker") {
		t.Fatalf("the walk summary read a file outside the workspace:\n%s", summary.DiffText)
	}
	if summary.DiffText != "# file: a.go\npackage a\n" || len(summary.ChangedFiles) != 1 {
		t.Fatalf("summary = %+v, want only the regular file a.go", summary)
	}
}

// TestPassDigestDoesNotReadOutsideTheWorkspace: the digest of a listed
// symbolic link to an outside file must not change when that file changes.
func TestPassDigestDoesNotReadOutsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writePassFile(t, outside, "secret.txt", "one\n")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	summary := schemas.ChangeSummary{ChangedFiles: []schemas.ChangedFile{{Path: "link", Status: "created"}}}
	before := passDigest(dir, summary)
	writePassFile(t, outside, "secret.txt", "two\n")
	if after := passDigest(dir, summary); after != before {
		t.Fatal("the pass digest read a file outside the workspace")
	}
}

// TestPassEvidenceLockedFileIsNotProgress: a file that is unreadable in both
// passes is not a change, so no_progress can still fire. The summary is marked
// truncated, because the file's state is unknown.
func TestPassEvidenceLockedFileIsNotProgress(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "ok.txt", "one\n")
	writePassFile(t, dir, "locked.txt", "two\n")
	if err := os.Chmod(filepath.Join(dir, "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked.txt"), 0o644) })
	if f, err := os.Open(filepath.Join(dir, "locked.txt")); err == nil {
		_ = f.Close()
		t.Skip("the file is readable by this user (root?), so unreadability cannot be set up")
	}
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	m := measureStep(t, tr, dir, 1)
	if !m.ok || len(m.summary.ChangedFiles) != 0 {
		t.Fatalf("pass with no edit: ok=%v files=%v, want none", m.ok, m.summary.ChangedFiles)
	}
	if m.pair.pass.PassChanged == nil || *m.pair.pass.PassChanged {
		t.Fatalf("PassChanged = %v, want false", m.pair.pass.PassChanged)
	}
	if !m.summary.Truncated {
		t.Fatal("an unreadable file must mark the summary truncated")
	}
}

// TestPassEvidenceLargeRewriteHasNoPartialCounts: a pass rewrites every line
// of a tracked file larger than the diff budget. The line counts of that file
// cannot fit, so it gets none and the summary is truncated. Counts that stop
// part way through the file would be wrong.
func TestPassEvidenceLargeRewriteHasNoPartialCounts(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	var before, after strings.Builder
	for i := 0; i < 400000; i++ {
		fmt.Fprintf(&before, "line-%06d\n", i)
		fmt.Fprintf(&after, "LINE-%06d\n", i)
	}
	writePassFile(t, dir, "lock.txt", before.String())
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	writePassFile(t, dir, "lock.txt", after.String())
	head := summarizeWorkspaceChanges(ctx, dir)
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	m := tr.measure(ctx, 1, head)
	runtime.ReadMemStats(&ms1)
	// Neither the HEAD text nor the new text fits the budget, so neither is
	// held in memory.
	if grew := ms1.TotalAlloc - ms0.TotalAlloc; grew > uint64(before.Len()) {
		t.Fatalf("measure allocated %d bytes, want less than the %d byte file", grew, before.Len())
	}
	if !m.ok || !slices.Equal(m.pair.pass.FilesChanged, []string{"lock.txt"}) {
		t.Fatalf("pass 1 ok=%v files=%v, want lock.txt", m.ok, m.pair.pass.FilesChanged)
	}
	if !m.summary.Truncated || m.pair.pass.LinesAdded != 0 || m.pair.pass.LinesRemoved != 0 {
		t.Fatalf("pass 1 truncated=%v lines +%d -%d, want truncated with no counts", m.summary.Truncated, m.pair.pass.LinesAdded, m.pair.pass.LinesRemoved)
	}
}

// TestPassAccumulatorDoesNotDiffOverTheBudget: texts larger than the diff
// budget are not diffed, so the memory cost stays near zero.
func TestPassAccumulatorDoesNotDiffOverTheBudget(t *testing.T) {
	var before, after strings.Builder
	for i := 0; i < 200000; i++ {
		fmt.Fprintf(&before, "line-%06d\n", i)
		fmt.Fprintf(&after, "LINE-%06d\n", i)
	}
	oldText, curText := before.String(), after.String()
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	acc := &passAccumulator{}
	acc.add("lock.txt", "modified", oldText, true, curText, true)
	runtime.ReadMemStats(&ms1)
	if grew := ms1.TotalAlloc - ms0.TotalAlloc; grew > uint64(defaultMaxDiffBytes) {
		t.Fatalf("add allocated %d bytes for %d bytes of text, want at most the %d byte budget", grew, len(oldText)+len(curText), defaultMaxDiffBytes)
	}
	if !acc.summary(false).Truncated || acc.diff.Len() != 0 {
		t.Fatalf("truncated=%v diff=%d bytes, want truncated with no counts", acc.truncated, acc.diff.Len())
	}
}

// TestPassEvidenceLargeCreatedFileIsNotRead: a created file larger than the
// diff budget is listed with no counts. Its text is not read into memory.
func TestPassEvidenceLargeCreatedFileIsNotRead(t *testing.T) {
	dir := t.TempDir()
	writePassFile(t, dir, "seed.txt", "seed\n")
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	big := strings.Repeat("line-000000\n", 400000)
	writePassFile(t, dir, "lock.txt", big)
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	m := tr.measure(ctx, 1, schemas.ChangeSummary{})
	runtime.ReadMemStats(&ms1)
	if grew := ms1.TotalAlloc - ms0.TotalAlloc; grew > uint64(len(big)) {
		t.Fatalf("measure allocated %d bytes, want less than the %d byte file", grew, len(big))
	}
	if !slices.Equal(m.pair.pass.FilesChanged, []string{"lock.txt"}) || !m.summary.Truncated || m.pair.pass.LinesAdded != 0 {
		t.Fatalf("pass 1 files=%v truncated=%v +%d, want lock.txt truncated with no counts", m.pair.pass.FilesChanged, m.summary.Truncated, m.pair.pass.LinesAdded)
	}
}

// TestPassEvidenceListsAPathOnce: after git rm --cached, git status lists the
// path twice ("D" and "??"). The pass lists it once.
func TestPassEvidenceListsAPathOnce(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "one\ntwo\n")
	gitInit(t, dir)
	ctx := context.Background()
	tr := newPassEvidenceTracker(ctx, dir, summarizeWorkspaceChanges(ctx, dir))
	if out, err := exec.Command("git", "-C", dir, "rm", "-q", "--cached", "a.txt").CombinedOutput(); err != nil {
		t.Fatalf("git rm --cached: %v: %s", err, out)
	}
	m := measureStep(t, tr, dir, 1)
	if !m.ok || !slices.Equal(m.pair.pass.FilesChanged, []string{"a.txt"}) {
		t.Fatalf("pass 1 ok=%v files=%v, want a.txt once", m.ok, m.pair.pass.FilesChanged)
	}
	// The text equals HEAD, so no line changed.
	if m.pair.pass.LinesAdded != 0 || m.pair.pass.LinesRemoved != 0 {
		t.Fatalf("pass 1 lines +%d -%d, want +0 -0", m.pair.pass.LinesAdded, m.pair.pass.LinesRemoved)
	}
}

// TestHeadBlobReadFailureNamesGitStderr: a batch process that fails before
// its first answer gives a named error with git's stderr. The stderr is read
// after the process ends, so the race detector sees no data race.
func TestHeadBlobReadFailureNamesGitStderr(t *testing.T) {
	requireSandboxGit(t)
	shimDir := t.TempDir()
	script := "#!/bin/sh\necho 'fatal: shim refused' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	reader, err := openHeadBlobs(context.Background(), t.TempDir(), []string{"a.go"})
	if err != nil {
		t.Fatalf("openHeadBlobs: %v", err)
	}
	_, err = reader.Next()
	_ = reader.Close()
	if err == nil || !strings.Contains(err.Error(), "shim refused") {
		t.Fatalf("Next error = %v, want git's stderr in it", err)
	}
}

// TestGitReadContainsRepoFilterTemp: a repository clean filter runs inside
// Splice's own git status read, as git-lfs needs. Its TMPDIR is a private
// directory that is removed after the read, and it cannot write the system
// temp directory. The status stays correct.
func TestGitReadContainsRepoFilterTemp(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "a.txt", "one\n")
	writePassFile(t, dir, ".gitignore", "seen.txt\n")
	gitInit(t, dir)
	sysMarker := filepath.Join(os.TempDir(), fmt.Sprintf("splice-filter-marker-%d", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(sysMarker) })
	seen := filepath.Join(dir, "seen.txt")
	filter := fmt.Sprintf(`touch "$TMPDIR/tmpdir-marker"; echo "$TMPDIR" > %q; ls "$TMPDIR" >> %q; touch %q 2>/dev/null; cat`, seen, seen, sysMarker)
	for _, args := range [][]string{{"config", "filter.mark.clean", filter}, {"config", "filter.mark.smudge", "cat"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	writePassFile(t, dir, ".gitattributes", "a.txt filter=mark\n")
	writePassFile(t, dir, "a.txt", "two\n")

	head := summarizeWorkspaceChanges(context.Background(), dir)
	if head.DegradedReason != "" {
		t.Fatalf("HEAD summary degraded: %s", head.DegradedReason)
	}
	if got := statusOf(head.ChangedFiles); got["a.txt"] != "modified" {
		t.Fatalf("status with the filter = %v, want a.txt modified", got)
	}
	data, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the clean filter did not run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	private := lines[0]
	if !strings.Contains(string(data), "tmpdir-marker") || filepath.Clean(private) == filepath.Clean(os.TempDir()) {
		t.Fatalf("filter TMPDIR = %q with listing %q, want a private directory that holds the marker", private, lines[1:])
	}
	if _, err := os.Stat(private); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the private temp directory %s is still there: %v", private, err)
	}
	if _, err := os.Stat(sysMarker); err == nil {
		t.Fatal("the clean filter wrote the system temp directory")
	}
}

// TestWorktreeChangedFilesExactNames: the trace workspace capture lists a
// name with a space and a non-ASCII name under their exact paths. The old
// parse dropped names that git quoted.
func TestWorktreeChangedFilesExactNames(t *testing.T) {
	requireSandboxGit(t)
	dir := t.TempDir()
	writePassFile(t, dir, "my file.go", "package m\n")
	writePassFile(t, dir, "café.go", "package c\n")
	gitInit(t, dir)
	writePassFile(t, dir, "my file.go", "package m\n\nfunc M() {}\n")
	writePassFile(t, dir, "café.go", "package c\n\nfunc C() {}\n")
	writePassFile(t, dir, "new file.go", "package n\n")
	got := worktreeChangedFiles(context.Background(), dir)
	sort.Strings(got)
	want := []string{"café.go", "my file.go", "new file.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("worktreeChangedFiles = %q, want %q", got, want)
	}
}
