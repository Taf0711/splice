package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Task is one held-out task in a paired-eval set. check is a shell command run
// in the repo after the run; exit 0 means the task succeeded. Success is
// deterministic, never LLM-judged.
type Task struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Check  string `json:"check"`
}

// TaskSet is a directory of tasks plus a fixture directory.
type TaskSet struct {
	// Dir is the absolute path to the taskset directory.
	Dir string
	// Name is the basename of the taskset directory.
	Name  string
	Tasks []Task
}

// FixtureDir returns the fixture directory inside the taskset (may not exist).
func (t TaskSet) FixtureDir() string {
	return filepath.Join(t.Dir, "fixture")
}

// LoadTaskSet reads a taskset directory: tasks/*.json (each {name, prompt,
// check}) plus an optional fixture/ directory. Validation is loud: a missing
// field names its task file.
func LoadTaskSet(dir string) (TaskSet, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return TaskSet{}, fmt.Errorf("resolve taskset dir: %w", err)
	}
	taskDir := filepath.Join(abs, "tasks")
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return TaskSet{}, fmt.Errorf("read tasks dir %s: %w", taskDir, err)
	}

	taskset := TaskSet{Dir: abs, Name: filepath.Base(abs)}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(taskDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return TaskSet{}, fmt.Errorf("read task %s: %w", path, err)
		}
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			return TaskSet{}, fmt.Errorf("parse task %s: %w", path, err)
		}
		if strings.TrimSpace(task.Name) == "" {
			return TaskSet{}, fmt.Errorf("task %s: name is required", path)
		}
		if strings.TrimSpace(task.Prompt) == "" {
			return TaskSet{}, fmt.Errorf("task %s: prompt is required", path)
		}
		if strings.TrimSpace(task.Check) == "" {
			return TaskSet{}, fmt.Errorf("task %s: check is required", path)
		}
		taskset.Tasks = append(taskset.Tasks, task)
	}
	if len(taskset.Tasks) == 0 {
		return TaskSet{}, fmt.Errorf("taskset %s has no tasks under tasks/*.json", abs)
	}
	return taskset, nil
}
