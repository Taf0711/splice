package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTaskset(t *testing.T, tasks map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	for name, body := range tasks {
		if err := os.WriteFile(filepath.Join(dir, "tasks", name+".json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write task %s: %v", name, err)
		}
	}
	return dir
}

func TestLoadTaskSetValid(t *testing.T) {
	dir := writeTaskset(t, map[string]string{
		"one": `{"name": "one", "prompt": "add a thing", "check": "true"}`,
		"two": `{"name": "two", "prompt": "fix a thing", "check": "go test ./..."}`,
	})
	taskset, err := LoadTaskSet(dir)
	if err != nil {
		t.Fatalf("LoadTaskSet: %v", err)
	}
	if len(taskset.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(taskset.Tasks))
	}
	if taskset.Name != filepath.Base(dir) {
		t.Fatalf("name = %q", taskset.Name)
	}
}

func TestLoadTaskSetValidationNamesFile(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"missing name", `{"prompt": "p", "check": "true"}`, "name is required"},
		{"missing prompt", `{"name": "n", "check": "true"}`, "prompt is required"},
		{"missing check", `{"name": "n", "prompt": "p"}`, "check is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTaskset(t, map[string]string{"task": tc.body})
			_, err := LoadTaskSet(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "task.json") {
				t.Fatalf("err = %v, want it to name the task file", err)
			}
		})
	}
}

func TestLoadTaskSetNoTasks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	if _, err := LoadTaskSet(dir); err == nil || !strings.Contains(err.Error(), "no tasks") {
		t.Fatalf("err = %v, want no-tasks error", err)
	}
}
