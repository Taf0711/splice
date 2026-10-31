package warmcost

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCompetingTasksetRunsEachTrioInOneWorkspace is the P3 corpus gate: each
// trio is one sequence, its three writes run before its three reads, so
// every read runs with a work record from each write.
func TestCompetingTasksetRunsEachTrioInOneWorkspace(t *testing.T) {
	for _, dir := range []string{"competing-taskset", "competing-large-taskset"} {
		checkTrioSequences(t, dir)
	}
}

// TestFutileTasksetPairsEachWriteWithItsRead: the D5 futile corpus (8
// futile families plus the 4 chain families as the correctness guard), one
// write-then-read sequence per family.
func TestFutileTasksetPairsEachWriteWithItsRead(t *testing.T) {
	tasks, err := LoadTaskset(filepath.Join("futile-taskset"))
	if err != nil {
		t.Fatalf("load futile taskset: %v", err)
	}
	seqs, err := groupSequences(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 12 {
		t.Fatalf("%d sequences, want 12", len(seqs))
	}
	for _, seq := range seqs {
		if len(seq.Tasks) != 2 || !strings.HasSuffix(seq.Tasks[0].ID, "-write") || !strings.HasSuffix(seq.Tasks[1].ID, "-read") {
			t.Fatalf("sequence %v is not one write then its read", seq.Tasks)
		}
	}
}

func checkTrioSequences(t *testing.T, dir string) {
	t.Helper()
	tasks, err := LoadTaskset(filepath.Join(dir))
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}
	seqs, err := groupSequences(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 2 {
		t.Fatalf("%d sequences, want 2 (one per trio)", len(seqs))
	}
	for _, seq := range seqs {
		var order []string
		for _, task := range seq.Tasks {
			order = append(order, task.ID[len(task.ID)-len("write"):])
		}
		if got := strings.Join(order, ","); got != "write,write,write,-read,-read,-read" {
			t.Fatalf("sequence order %s, want three writes then three reads", got)
		}
	}
}
