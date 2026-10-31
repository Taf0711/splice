package warmcost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// TestCoupledTasksetNeedsMemoryForTheCoupledFile is the P2 corpus gate:
// without memory, the first round of every coupled task must not read the
// coupled file (else the prompt already carries what memory would add), and
// with the related write's files as memory it must.
func TestCoupledTasksetNeedsMemoryForTheCoupledFile(t *testing.T) {
	dir := filepath.Join("coupled-taskset")
	tasks, err := LoadTaskset(dir)
	if err != nil {
		t.Fatalf("load coupled taskset: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "coupled.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest []struct{ ID, Coupled string }
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	coupled := map[string]string{}
	for _, m := range manifest {
		coupled[m.ID] = m.Coupled
	}
	fixture := filepath.Join("..", "coupled-families", "fixture")
	reads := func(prompt string, memory []string) []string {
		var out []string
		for _, q := range stages.DefaultContextRequestPreferring(prompt, "", fixture, "go", memory).Queries {
			if q.QueryType == schemas.ContextReadFile && q.Path != nil {
				out = append(out, *q.Path)
			}
		}
		return out
	}
	if len(tasks) != 2*len(manifest) {
		t.Fatalf("%d tasks for %d families", len(tasks), len(manifest))
	}
	for _, task := range tasks {
		family := task.ID[:len("cpl-00")]
		target := coupled[family]
		cold := reads(task.Prompt, nil)
		t.Logf("%s cold: %v", task.ID, cold)
		if slices.Contains(cold, target) {
			t.Errorf("%s: the cold first round already reads %s: %v", task.ID, target, cold)
		}
		if task.Phase == PhaseRead {
			if warm := reads(task.Prompt, []string{target}); !slices.Contains(warm, target) {
				t.Errorf("%s: memory naming %s does not reach the first round: %v", task.ID, target, warm)
			}
		}
	}
}
