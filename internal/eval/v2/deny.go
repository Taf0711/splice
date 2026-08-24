package v2

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultHiddenRoots names the protected corpus roots used by EV2 preflight.
var DefaultHiddenRoots = []string{"checks", "reference", "manifests", "private_corpus"}

// DenyRuleSet is the policy seam consumed by preflight probes and future
// sandbox wiring. Hidden roots are resolved relative to WorkspaceRoot.
type DenyRuleSet struct {
	WorkspaceRoot string
	HiddenRoots   []string
}

// NewDenyRuleSet builds a policy from one manifest and explicit hidden roots.
func NewDenyRuleSet(m Manifest, workspaceRoot string, hiddenRoots []string) (DenyRuleSet, error) {
	if err := m.Validate(); err != nil {
		return DenyRuleSet{}, fmt.Errorf("manifest: %w", err)
	}
	root, err := canonicalPath(workspaceRoot)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return DenyRuleSet{}, fmt.Errorf("workspace root is required: %w", err)
	}
	if len(hiddenRoots) == 0 {
		hiddenRoots = DefaultHiddenRoots
	}
	resolved := make([]string, 0, len(hiddenRoots))
	seen := make(map[string]bool, len(hiddenRoots))
	for _, hidden := range hiddenRoots {
		if strings.TrimSpace(hidden) == "" {
			return DenyRuleSet{}, fmt.Errorf("hidden root must not be empty")
		}
		candidate := hidden
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		candidate, err = canonicalPath(candidate)
		if err != nil {
			return DenyRuleSet{}, fmt.Errorf("resolve hidden root %q: %w", hidden, err)
		}
		if !withinPath(root, candidate) {
			return DenyRuleSet{}, fmt.Errorf("hidden root %q escapes workspace root", hidden)
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		resolved = append(resolved, candidate)
	}
	sort.Strings(resolved)
	return DenyRuleSet{WorkspaceRoot: root, HiddenRoots: resolved}, nil
}

// BuildDenyRuleSet is an explicit alias for callers that prefer builder naming.
func BuildDenyRuleSet(m Manifest, workspaceRoot string, hiddenRoots []string) (DenyRuleSet, error) {
	return NewDenyRuleSet(m, workspaceRoot, hiddenRoots)
}

// Check rejects direct reads, shell-mediated reads, search/glob reach, and
// symlink-resolved escapes. The tool-class labels in its errors are policy
// primitives for preflight reporting; they do not claim that real-path probe
// wiring or sandbox enforcement exists at this schema checkpoint.
// The error names the rule, tool class, and raw path.
func (d DenyRuleSet) Check(path string, resolution []string) error {
	if strings.TrimSpace(d.WorkspaceRoot) == "" {
		return fmt.Errorf("deny rule workspace_root is required")
	}
	candidates := append([]string{path}, resolution...)
	for _, candidate := range candidates {
		canonical, err := canonicalPath(candidate)
		if err != nil {
			return fmt.Errorf("deny rule path_resolution tool_class=symlink_escape raw_path=%q: %w", candidate, err)
		}
		if !withinPath(d.WorkspaceRoot, canonical) {
			return fmt.Errorf("deny rule workspace_escape tool_class=symlink_escape raw_path=%q resolved_path=%q", candidate, canonical)
		}
		for _, hidden := range d.HiddenRoots {
			if withinPath(hidden, canonical) {
				toolClass := "direct_read,shell_read,search_glob"
				if len(resolution) > 0 {
					toolClass += ",symlink_escape"
				}
				return fmt.Errorf("deny rule hidden_root tool_class=%s raw_path=%q resolved_path=%q hidden_root=%q", toolClass, path, canonical, hidden)
			}
		}
	}
	return nil
}
