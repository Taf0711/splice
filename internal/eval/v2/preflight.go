package v2

import (
	"fmt"
	"sort"
	"strings"
)

var preflightToolClasses = []string{"file", "search", "shell", "symlink"}

// PreflightProbe describes one isolation probe that a future runner will
// execute against one hidden root.
type PreflightProbe struct {
	Name        string `json:"name"`
	ToolClass   string `json:"tool_class"`
	AttemptPath string `json:"attempt_path"`
	HiddenRoot  string `json:"hidden_root,omitempty"`
}

// PreflightResult records the outcome of one probe.
type PreflightResult struct {
	Probe  PreflightProbe `json:"probe"`
	Denied bool           `json:"denied"`
	Detail string         `json:"detail"`
}

// FixtureComparison records byte-level equality for one task's arm fixtures.
type FixtureComparison struct {
	TaskID      string `json:"task_id"`
	LeftPath    string `json:"left_path"`
	RightPath   string `json:"right_path"`
	LeftSHA256  string `json:"left_sha256"`
	RightSHA256 string `json:"right_sha256"`
}

// SidecarCheck is a stale-sidecar detection result. A socket without a live
// process is contamination and requires cleanup before a run.
type SidecarCheck struct {
	SocketPath   string `json:"socket_path"`
	SocketExists bool   `json:"socket_exists"`
	ProcessAlive bool   `json:"process_alive"`
}

// ProcessMetadata holds the future runner's argv and environment capture.
type ProcessMetadata struct {
	Argv        []string `json:"argv"`
	Environment []string `json:"environment"`
}

// PreflightReport is the complete isolation verification result. The runner
// fills the probes and contamination checks; this package validates them.
type PreflightReport struct {
	HiddenRoots        []string            `json:"hidden_roots"`
	Results            []PreflightResult   `json:"results"`
	FixtureComparisons []FixtureComparison `json:"fixture_comparisons,omitempty"`
	SidecarChecks      []SidecarCheck      `json:"sidecar_checks,omitempty"`
	ProcessMetadata    []ProcessMetadata   `json:"process_metadata,omitempty"`
}

// Validate requires every hidden root to have one denied probe for every tool
// class. Missing or passing probes are invalid isolation evidence.
func (r PreflightReport) Validate() error {
	if len(r.HiddenRoots) == 0 {
		return fmt.Errorf("preflight hidden_roots must not be empty")
	}
	roots := make(map[string]bool, len(r.HiddenRoots))
	for _, root := range r.HiddenRoots {
		if strings.TrimSpace(root) == "" {
			return fmt.Errorf("preflight hidden root must not be empty")
		}
		canonical, err := canonicalPath(root)
		if err != nil {
			return fmt.Errorf("preflight hidden root %q: %w", root, err)
		}
		if roots[canonical] {
			return fmt.Errorf("duplicate preflight hidden root %q", root)
		}
		roots[canonical] = true
	}
	seen := make(map[string]bool)
	for i, result := range r.Results {
		if err := result.validate(); err != nil {
			return fmt.Errorf("results[%d]: %w", i, err)
		}
		root, err := r.resultRoot(result.Probe, roots)
		if err != nil {
			return fmt.Errorf("results[%d]: %w", i, err)
		}
		key := root + "\x00" + result.Probe.ToolClass
		if seen[key] {
			return fmt.Errorf("results[%d]: duplicate probe for hidden root %q and tool class %q", i, root, result.Probe.ToolClass)
		}
		seen[key] = true
		if !result.Denied {
			return fmt.Errorf("results[%d]: probe %q was not denied", i, result.Probe.Name)
		}
	}
	for root := range roots {
		for _, class := range preflightToolClasses {
			if !seen[root+"\x00"+class] {
				return fmt.Errorf("missing denied preflight probe for hidden root %q and tool class %q", root, class)
			}
		}
	}
	for i, comparison := range r.FixtureComparisons {
		if err := comparison.Validate(); err != nil {
			return fmt.Errorf("fixture_comparisons[%d]: %w", i, err)
		}
	}
	for i, check := range r.SidecarChecks {
		if check.SocketExists && !check.ProcessAlive {
			return fmt.Errorf("sidecar_checks[%d]: stale sidecar socket %q exists without a live process; clean up before running", i, check.SocketPath)
		}
	}
	return nil
}

func (p PreflightProbe) Validate() error {
	if p.Name == "" || p.ToolClass == "" || p.AttemptPath == "" {
		return fmt.Errorf("probe needs name, tool_class, and attempt_path")
	}
	if !containsString(preflightToolClasses, p.ToolClass) {
		return fmt.Errorf("probe %q has unknown tool class %q", p.Name, p.ToolClass)
	}
	return nil
}

func (r PreflightResult) validate() error {
	if err := r.Probe.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Detail) == "" {
		return fmt.Errorf("probe %q detail is required", r.Probe.Name)
	}
	return nil
}

func (r PreflightReport) resultRoot(probe PreflightProbe, roots map[string]bool) (string, error) {
	if probe.HiddenRoot != "" {
		canonical, err := canonicalPath(probe.HiddenRoot)
		if err != nil {
			return "", fmt.Errorf("probe %q hidden_root: %w", probe.Name, err)
		}
		if !roots[canonical] {
			return "", fmt.Errorf("probe %q names undeclared hidden root %q", probe.Name, probe.HiddenRoot)
		}
		return canonical, nil
	}
	attempt, err := canonicalPath(probe.AttemptPath)
	if err != nil {
		return "", fmt.Errorf("probe %q attempt_path: %w", probe.Name, err)
	}
	matches := make([]string, 0, 1)
	for root := range roots {
		if withinPath(root, attempt) {
			matches = append(matches, root)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("probe %q attempt_path %q does not identify exactly one hidden root", probe.Name, probe.AttemptPath)
	}
	return matches[0], nil
}

// Validate checks that both fixture copies have matching SHA-256 bytes.
func (c FixtureComparison) Validate() error {
	if c.TaskID == "" || c.LeftPath == "" || c.RightPath == "" {
		return fmt.Errorf("fixture comparison needs task_id and both paths")
	}
	if !validHash(c.LeftSHA256) || !validHash(c.RightSHA256) {
		return fmt.Errorf("fixture comparison %s needs valid left and right sha256 hashes", c.TaskID)
	}
	if c.LeftSHA256 != c.RightSHA256 {
		return fmt.Errorf("fixture comparison %s hashes differ: %s != %s", c.TaskID, c.LeftSHA256, c.RightSHA256)
	}
	return nil
}

func containsString(values []string, want string) bool {
	return sort.SearchStrings(values, want) < len(values) && values[sort.SearchStrings(values, want)] == want
}
