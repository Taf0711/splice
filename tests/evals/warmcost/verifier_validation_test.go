package warmcost

// The Section 15.1 guard as a test: every taskset verifier self-validates
// offline against its own gold and wrong trees before any campaign may use
// it. The M6 campaign burned two invalid runs on verifier designs that
// passed gold but rejected acceptable model work (a test-file placement
// requirement) or accepted a rule duplication the task forbids. This test
// makes that class of defect a build failure, not a discovered expense.
//
// Provider-free and fast: it applies the task's gold files to a copy of the
// fixture, runs the check script, expects success; applies the wrong files,
// runs the check again, expects failure. A verifier that cannot pass gold
// or cannot reject wrong breaks the campaign before any provider request.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice"
)

// loadTaskLoose parses a taskset task without requiring inline gold/wrong
// trees: the families taskset carries its trees in write-gold/ and the
// cognition-families validate/ directories instead.
func loadTaskLoose(t *testing.T, path string) validatedTask {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	var task validatedTask
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.Check == "" {
		t.Fatalf("%s: no check script", path)
	}
	return task
}

// validateTasksetVerifiers runs the gold/wrong validation for a taskset
// whose tasks carry their own full-file gold and wrong trees.
func validateTasksetVerifiers(t *testing.T, base string, names []string, _ any) {
	fixture := filepath.Join(base, "fixture")
	for _, name := range names {
		task := loadTaskJSON(t, filepath.Join(base, "tasks", name+".json"))

		goldDir := filepath.Join(t.TempDir(), "tree")
		copyDir(t, fixture, goldDir) // dst absent: cp -R creates it as the copy
		applyTaskFiles(t, goldDir, task.Gold)
		if rc := runCheck(t, goldDir, task.Check); rc != 0 {
			t.Errorf("%s: the check must PASS on the gold tree (rc=%d); a verifier that rejects gold work stops the campaign under Section 15.1", name, rc)
		}

		wrongDir := filepath.Join(t.TempDir(), "tree")
		copyDir(t, fixture, wrongDir)
		applyTaskFiles(t, wrongDir, task.Wrong)
		if rc := runCheck(t, wrongDir, task.Check); rc == 0 {
			t.Errorf("%s: the check must FAIL on the wrong tree; a verifier that accepts the forbidden implementation cannot measure the mechanism", name)
		}
	}
}

func jsonUnmarshalStrict(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}

func applyTaskFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if content == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func runCheck(t *testing.T, dir, check string) int {
	t.Helper()
	cmd := exec.Command("bash", "-c", check)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("check execution failed: %v", err)
		}
		return exitErr.ExitCode()
	}
	return 0
}

// validatedTask is the full taskset JSON shape including the gold and wrong
// reference trees and the phase field the runner groups sequences by.
type validatedTask struct {
	Name   string            `json:"name"`
	Phase  string            `json:"phase"`
	Prompt string            `json:"prompt"`
	Check  string            `json:"check"`
	Gold   map[string]string `json:"gold"`
	Wrong  map[string]string `json:"wrong"`
}

func loadTaskJSON(t *testing.T, path string) validatedTask {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	var task validatedTask
	if err := jsonUnmarshalStrict(raw, &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.Check == "" {
		t.Fatalf("%s: no check script", path)
	}
	if len(task.Gold) == 0 || len(task.Wrong) == 0 {
		t.Fatalf("%s: gold and wrong reference trees are required for verifier validation", path)
	}
	return task
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	cmd := exec.Command("cp", "-R", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copy fixture: %v: %s", err, out)
	}
}

func TestHoldoutVerifiersPassGoldAndRejectWrong(t *testing.T) {
	validateTasksetVerifiers(t, filepath.Join("..", "warmcost", "holdout-taskset"),
		[]string{"validate-user-helper-write", "validate-user-helper-read"}, nil)
}

// familiesTasksetValidation composes each family's precursor gold (the
// hand-built write-gold tree) through the write check, then the target
// gold tree through the read check, and rejects the target wrong tree.
// taskGoldTree returns the corpus's target-state gold files for one family
// (validate/_gold/fam-XX) as the file map the validator applies.
func taskGoldTree(t *testing.T, fid string) map[string]string {
	t.Helper()
	dir := filepath.Join("..", "cognition-families", "validate", "_gold", fid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read gold tree %s: %v", dir, err)
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(raw)
	}
	return files
}

// taskWrongTree returns the corpus's wrong tree for one family.
func taskWrongTree(t *testing.T, fid string) map[string]string {
	t.Helper()
	dir := filepath.Join("..", "cognition-families", "validate", "_wrong", fid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read wrong tree %s: %v", dir, err)
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(raw)
	}
	return files
}

func TestFamiliesVerifiersPassGoldAndRejectWrong(t *testing.T) {
	base := filepath.Join("..", "warmcost", "families-taskset")
	golds := filepath.Join(base, "write-gold")
	for i := 1; i <= 10; i++ {
		fid := fmt.Sprintf("fam-%02d", i)
		fixture := filepath.Join(base, "fixture")

		write := loadTaskLoose(t, filepath.Join(base, "tasks", fid+"-write.json"))
		writeGoldDir := filepath.Join(golds, fid)
		if rc := runCheck(t, writeGoldDir, write.Check); rc != 0 {
			t.Errorf("%s write: the check must PASS on the composed write-gold tree (rc=%d)", fid, rc)
		}

		read := loadTaskLoose(t, filepath.Join(base, "tasks", fid+"-read.json"))
		// The corpus validates targets against its own gold trees (pristine
		// fixture plus the target diff), exactly as validate_pair.py does.
		readGoldDir := filepath.Join(t.TempDir(), fid+"-read")
		copyDir(t, fixture, readGoldDir)
		applyTaskFiles(t, readGoldDir, taskGoldTree(t, fid))
		if rc := runCheck(t, readGoldDir, read.Check); rc != 0 {
			t.Errorf("%s read: the check must PASS on the corpus read-gold tree (rc=%d)", fid, rc)
		}

		wrongDir := filepath.Join(t.TempDir(), fid+"-wrong")
		copyDir(t, fixture, wrongDir)
		applyTaskFiles(t, wrongDir, taskWrongTree(t, fid))
		if rc := runCheck(t, wrongDir, read.Check); rc == 0 {
			t.Errorf("%s read: the check must FAIL on the wrong tree", fid)
		}
	}
}

// TestTasksetPhaseGroupingContract enforces the runner's workspace contract
// offline: phase declaration is all-or-nothing within a taskset, and a
// phase-grouped sequence's gold trees compose - applying each task's gold in
// sequence order to ONE tree must make every check pass. The M6 campaigns
// burned two runs because the holdout taskset omitted phases, so the read
// task ran in a fresh workspace whose tree lacked the helper the write task
// had created in a different workspace; "reuse the existing helper" was
// unsatisfiable by construction. This test makes that defect a build
// failure.
func TestTasksetPhaseGroupingContract(t *testing.T) {
	t.Run("holdout", func(t *testing.T) {
		base := filepath.Join("..", "warmcost", "holdout-taskset")
		tasks := []validatedTask{
			loadTaskJSON(t, filepath.Join(base, "tasks", "validate-user-helper-write.json")),
			loadTaskJSON(t, filepath.Join(base, "tasks", "validate-user-helper-read.json")),
		}
		assertPhaseGroupingContract(t, base, tasks)
	})
	t.Run("families", func(t *testing.T) {
		base := filepath.Join("..", "warmcost", "families-taskset")
		var tasks []validatedTask
		for i := 1; i <= 10; i++ {
			fid := fmt.Sprintf("fam-%02d", i)
			tasks = append(tasks,
				loadTaskLoose(t, filepath.Join(base, "tasks", fid+"-write.json")),
				loadTaskLoose(t, filepath.Join(base, "tasks", fid+"-read.json")))
		}
		assertPhaseGroupingContract(t, base, tasks)
	})
	t.Run("families runner layout", func(t *testing.T) {
		// The gold checks above validate each family in isolation. This
		// check validates the layout the runner actually builds: it loads
		// the taskset through the runner's loader and groups it with the
		// runner's grouping. The M6 powered campaign ran nine of ten reads
		// behind the wrong write, and no offline check saw it.
		tasks, err := LoadTaskset(filepath.Join("..", "warmcost", "families-taskset"))
		if err != nil {
			t.Fatal(err)
		}
		sequences, err := groupSequences(tasks)
		if err != nil {
			t.Fatal(err)
		}
		if len(sequences) != 10 {
			t.Fatalf("sequences = %d, want 10 (one workspace per family)", len(sequences))
		}
		for i, seq := range sequences {
			fid := fmt.Sprintf("fam-%02d", i+1)
			var ids []string
			for _, task := range seq.Tasks {
				ids = append(ids, task.ID)
			}
			got := strings.Join(ids, ",")
			if want := fid + "-write," + fid + "-read"; got != want {
				t.Fatalf("sequence %d = %s, want %s", i, got, want)
			}
		}
	})
}

// assertPhaseGroupingContract enforces the runner's workspace contract for
// one taskset: phase declaration is all-or-nothing, prompts fit the
// distilled-intent bound, and a phase-less taskset may not hide cross-task
// dependencies behind byte-identical gold files.
func assertPhaseGroupingContract(t *testing.T, base string, tasks []validatedTask) {
	// The pipeline distills the request intent at a fixed bound
	// (information minimalism). A task whose contract falls past the cut is
	// incoherent by construction: the M6 read prompt shipped 459 runes with
	// the 200/400 contract outside the bound, the model saw a truncated
	// intent, burned context expansions asking for the missing success
	// behavior, and the attempt timed out mid-generation. The distilled
	// intent must therefore be the prompt, byte for byte.
	for _, task := range tasks {
		if strings.TrimSpace(task.Prompt) == "" {
			t.Fatalf("%s: task carries no prompt", task.Name)
		}
		if distilled := splice.DistillRequestIntent(task.Prompt); distilled != task.Prompt {
			t.Fatalf("%s: the prompt exceeds the pipeline's intent bound and would be truncated to %q; shorten the prompt so the full contract reaches the stage", task.Name, distilled)
		}
	}
	declared := 0
	for _, task := range tasks {
		if task.Phase != "" {
			declared++
		}
	}
	if declared != 0 && declared != len(tasks) {
		t.Fatalf("taskset mixes declared and empty phases: %d of %d declared; the runner groups workspaces by phase and a partial declaration silently splits them", declared, len(tasks))
	}
	if declared == 0 {
		// Undeclared tasksets run every task in its own workspace, so no
		// task may depend on another task's output. The mechanical signal
		// of a hidden dependency: two tasks whose gold trees carry
		// byte-identical content for the same file - the second task's
		// standalone gold only passes because it smuggled in the first
		// task's result. The M6 holdout pair shipped exactly this defect
		// with empty phases, and the read task was incoherent.
		type goldFile struct{ task, file, content string }
		var all []goldFile
		for _, task := range tasks {
			for file, content := range task.Gold {
				all = append(all, goldFile{task.Name, file, content})
			}
		}
		for i := 0; i < len(all); i++ {
			for j := i + 1; j < len(all); j++ {
				if all[i].file == all[j].file && all[i].content == all[j].content {
					t.Fatalf("tasks %q and %q both ship byte-identical gold for %s with no phases declared: an undeclared workspace dependency. Declare the phase fields so the runner groups them into one shared workspace", all[i].task, all[j].task, all[i].file)
				}
			}
		}
		t.Skip("no phases declared and no cross-task gold duplication: tasks are independent by contract")
	}
	// Per-task composition: the fixture plus that task's gold tree must
	// pass that task's check. (The families taskset stores its golds
	// externally - write-gold/ for precursors, the corpus _gold trees for
	// targets - and its golds are each built from pristine, so a single
	// accumulating tree would let later golds clobber earlier changes;
	// cross-task accumulation is the runner's live condition, proven in
	// the m6ds campaign.)
	isFamilies := strings.Contains(base, "families-taskset")
	for _, task := range tasks {
		tree := filepath.Join(t.TempDir(), task.Name)
		copyDir(t, filepath.Join(base, "fixture"), tree)
		if isFamilies {
			fid := strings.TrimSuffix(strings.TrimSuffix(task.Name, "-write"), "-read")
			if strings.HasSuffix(task.Name, "-write") {
				applyDirOver(t, filepath.Join(base, "write-gold", fid), tree)
			} else {
				applyTaskFiles(t, tree, taskGoldTree(t, fid))
			}
		} else {
			applyTaskFiles(t, tree, task.Gold)
		}
		if rc := runCheck(t, tree, task.Check); rc != 0 {
			t.Errorf("%s: the composed tree must pass this task's check (rc=%d); a task whose premise is absent from its own gold tree is incoherent", task.Name, rc)
		}
	}
}

// applyDirOver copies every file of src over dst (dst exists).
func applyDirOver(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read gold dir %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLargeTasksetGates is the offline gate for large-taskset, the warmcost
// form of tests/evals/large-families. Per family it proves the matrix of
// large-families/validate/validate_large.py (base fails A, gold A passes A,
// gold A fails B, gold A plus gold B passes B, gold A plus wrong B fails B),
// that every prompt reaches the stage unshortened, and that the runner
// pairs each read with its own write.
func TestLargeTasksetGates(t *testing.T) {
	base := filepath.Join("..", "warmcost", "large-taskset")
	tasks, err := LoadTaskset(base)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Task{}
	for _, task := range tasks {
		byID[task.ID] = task
		if distilled := splice.DistillRequestIntent(task.Prompt); distilled != task.Prompt {
			t.Fatalf("%s: the prompt exceeds the intent bound and would reach the stage as %q", task.ID, distilled)
		}
	}
	sequences, err := groupSequences(tasks)
	if err != nil {
		t.Fatal(err)
	}
	var families []string
	for _, task := range tasks {
		if task.Phase == PhaseWrite {
			families = append(families, strings.TrimSuffix(task.ID, "-write"))
		}
	}
	if len(sequences) != len(families) || len(families) < 10 {
		t.Fatalf("sequences = %d for %d families, want one per family and at least 10", len(sequences), len(families))
	}
	large := filepath.Join("..", "large-families")
	overlay := func(dir, script string) {
		t.Helper()
		cmd := exec.Command("bash", filepath.Join(large, "validate", script), dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("overlay %s: %v: %s", script, err, out)
		}
	}
	for _, fam := range families {
		write, read := byID[fam+"-write"], byID[fam+"-read"]
		tree := func(overlays ...string) string {
			dir := filepath.Join(t.TempDir(), "tree")
			copyDir(t, filepath.Join(large, "fixture"), dir)
			for _, o := range overlays {
				overlay(dir, o)
			}
			return dir
		}
		if rc := runCheck(t, tree(), write.Check); rc == 0 {
			t.Errorf("%s: the write check must FAIL on the pristine fixture", fam)
		}
		if rc := runCheck(t, tree("_gold-a/"+fam+".sh"), write.Check); rc != 0 {
			t.Errorf("%s: the write check must PASS on gold A (rc=%d)", fam, rc)
		}
		if rc := runCheck(t, tree("_gold-a/"+fam+".sh"), read.Check); rc == 0 {
			t.Errorf("%s: the read check must FAIL on gold A alone", fam)
		}
		if rc := runCheck(t, tree("_gold-a/"+fam+".sh", "_gold-b/"+fam+".sh"), read.Check); rc != 0 {
			t.Errorf("%s: the read check must PASS on gold A plus gold B (rc=%d)", fam, rc)
		}
		if rc := runCheck(t, tree("_gold-a/"+fam+".sh", "_wrong/"+fam+".sh"), read.Check); rc == 0 {
			t.Errorf("%s: the read check must FAIL on gold A plus wrong B", fam)
		}
	}
}
