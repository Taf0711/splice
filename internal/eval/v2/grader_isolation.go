package v2

import (
	"fmt"
	"path"
	"strings"
)

// graderSuffixes are file extensions whose presence in the agent-visible
// set would leak grader material to the model.
var graderSuffixes = []string{".check", ".expected", ".reference"}

// GraderIsolationSpec declares the split between hidden grader material and
// agent-visible workspace paths for one task. The hidden set is a full
// task-file inventory: every grader file must be listed. Validate checks
// that no hidden path is reachable from an agent-visible path via directory
// containment, and that known grader-suffix files are not agent-visible.
//
// Approach: inventory-based prefix containment analysis. The package is
// filesystem-free and deterministic, so establishing the prefix relationship
// between normalized paths is the only correct containment check without
// walking the disk. Every hidden path is compared against every visible
// path in both directions.
type GraderIsolationSpec struct {
	TaskID            string   `json:"task_id"`
	HiddenPaths       []string `json:"hidden_paths"`
	AgentVisiblePaths []string `json:"agent_visible_paths"`
}

func hasGraderSuffix(name string) bool {
	for _, suffix := range graderSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// pathContains reports whether parent is a directory-component prefix of
// child (e.g. "src" contains "src/task.expected"). Exact equality is not
// containment; the caller checks that separately.
func pathContains(parent, child string) bool {
	return strings.HasPrefix(child, parent+"/")
}

// Validate enforces all grader isolation rules:
//   - hidden and visible paths are canonical relative workspace paths
//   - hidden paths must include "checks" and "reference" (mandated roots)
//   - no agent-visible path basename matches a grader suffix
//   - no hidden path equals or is reachable from an agent-visible path
//   - no agent-visible path sits inside a hidden path
//
// Errors name the offending paths.
func (s GraderIsolationSpec) Validate() error {
	if s.TaskID == "" {
		return fmt.Errorf("task_id is required")
	}
	hidden := make([]string, 0, len(s.HiddenPaths))
	seenHidden := make(map[string]bool, len(s.HiddenPaths))
	for i, p := range s.HiddenPaths {
		normalized, ok := normalizePath(p)
		if !ok || normalized != p {
			return fmt.Errorf("hidden_paths[%d] must be a canonical relative workspace path", i)
		}
		if seenHidden[normalized] {
			return fmt.Errorf("hidden_paths[%d] duplicates %q", i, normalized)
		}
		seenHidden[normalized] = true
		hidden = append(hidden, normalized)
	}
	visible := make([]string, 0, len(s.AgentVisiblePaths))
	seenVisible := make(map[string]bool, len(s.AgentVisiblePaths))
	for i, p := range s.AgentVisiblePaths {
		normalized, ok := normalizePath(p)
		if !ok || normalized != p {
			return fmt.Errorf("agent_visible_paths[%d] must be a canonical relative workspace path", i)
		}
		if seenVisible[normalized] {
			return fmt.Errorf("agent_visible_paths[%d] duplicates %q", i, normalized)
		}
		seenVisible[normalized] = true
		visible = append(visible, normalized)
	}
	// Mandated hidden roots: every isolation spec must hide the checks/
	// and reference/ directories.
	for _, required := range []string{"checks", "reference"} {
		if !seenHidden[required] {
			return fmt.Errorf("hidden_paths must include %s/", required)
		}
	}
	// Grader-suffix files must not be agent-visible.
	for _, v := range visible {
		base := path.Base(v)
		if hasGraderSuffix(base) {
			return fmt.Errorf("agent-visible path %q matches a grader suffix; grader material cannot be agent-visible", v)
		}
	}
	// Disjointness and containment, both directions.
	for _, h := range hidden {
		for _, v := range visible {
			if h == v {
				return fmt.Errorf("path %q is both hidden and agent-visible", h)
			}
			if pathContains(v, h) {
				return fmt.Errorf("hidden path %q is reachable from agent-visible directory %q", h, v)
			}
			if pathContains(h, v) {
				return fmt.Errorf("agent-visible path %q sits inside hidden path %q", v, h)
			}
		}
	}
	return nil
}
