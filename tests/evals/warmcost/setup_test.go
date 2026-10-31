package warmcost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTasksetReadsSetup(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := map[string]string{"id": "task", "prompt": "p", "check": "true", "setup": "echo setup"}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(filepath.Join(dir, "tasks", "task.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := LoadTaskset(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Setup != "echo setup" {
		t.Fatalf("setup = %q, want echo setup", tasks[0].Setup)
	}
}

func TestRunSetupSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := runSetup(context.Background(), "task", dir, "printf ready > marker"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ready" {
		t.Fatalf("marker = %q", data)
	}
}

func TestRunSetupFailureIsHarnessError(t *testing.T) {
	err := runSetup(context.Background(), "task", t.TempDir(), "echo bad; exit 7")
	if err == nil || !isHarnessError(err) {
		t.Fatalf("runSetup error = %v, want harness error", err)
	}
	if !strings.Contains(err.Error(), "task setup failed") || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error = %v, want task and output", err)
	}
}

func TestSetupFailureStopsRunBeforeModel(t *testing.T) {
	called := false
	r, err := NewRunner(Config{
		Binary:  "/bin/true",
		OutDir:  t.TempDir(),
		Arms:    []Arm{ArmCold},
		Tasks:   []Task{{ID: "task", Prompt: "p", Setup: "exit 9", Check: "true"}},
		Repeats: 1,
	}, func(context.Context, ExecRequest) (ExecResult, error) {
		called = true
		return ExecResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err == nil || !isHarnessError(err) {
		t.Fatalf("Run error = %v, want harness error", err)
	}
	if called {
		t.Fatal("model process ran after setup failed")
	}
}
