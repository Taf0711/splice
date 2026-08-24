package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// WorkspaceOptions selects the caller-owned run root and pinned binaries.
type WorkspaceOptions struct {
	Root          string
	SourceDir     string
	SpliceBinary  string
	SidecarBinary string
	GitBinary     string
}

// Workspace is the immutable directory layout for one experiment. Trial
// directories are created before a future runner starts any execution.
type Workspace struct {
	ExperimentID      string
	Root              string
	ExperimentRoot    string
	BinDir            string
	ConfigDir         string
	SidecarDir        string
	SessionsDir       string
	WorkspacesDir     string
	TrialsDir         string
	RawDir            string
	SpliceBinaryPath  string
	SidecarBinaryPath string
}

// BuildWorkspace creates or reuses the EV2-2 run-state layout. Existing
// directories and trial contents are preserved; use ResetWorkspace explicitly
// to clear one trial directory.
func BuildWorkspace(m Manifest, opts WorkspaceOptions) (Workspace, error) {
	if err := m.ValidateLocked(); err != nil {
		return Workspace{}, fmt.Errorf("manifest: %w", err)
	}
	root, err := filepath.Abs(strings.TrimSpace(opts.Root))
	if err != nil || strings.TrimSpace(opts.Root) == "" {
		return Workspace{}, fmt.Errorf("workspace root is required: %w", err)
	}
	if err := requireComponent(m.Protocol.ExperimentID, "experiment_id"); err != nil {
		return Workspace{}, err
	}
	if strings.TrimSpace(opts.SourceDir) == "" {
		return Workspace{}, fmt.Errorf("source directory is required")
	}
	if err := RequireCleanSourceWithGit(opts.SourceDir, opts.GitBinary); err != nil {
		return Workspace{}, err
	}
	if strings.TrimSpace(opts.SpliceBinary) == "" || strings.TrimSpace(opts.SidecarBinary) == "" {
		return Workspace{}, fmt.Errorf("splice and sidecar binary paths are required")
	}

	experimentRoot := filepath.Join(root, m.Protocol.ExperimentID)
	workspace := Workspace{
		ExperimentID:   m.Protocol.ExperimentID,
		Root:           root,
		ExperimentRoot: experimentRoot,
		BinDir:         filepath.Join(experimentRoot, "bin"),
		ConfigDir:      filepath.Join(experimentRoot, "config"),
		SidecarDir:     filepath.Join(experimentRoot, "sidecar"),
		SessionsDir:    filepath.Join(experimentRoot, "sessions"),
		WorkspacesDir:  filepath.Join(experimentRoot, "workspaces"),
		TrialsDir:      filepath.Join(experimentRoot, "trials"),
		RawDir:         filepath.Join(experimentRoot, "raw"),
	}
	for _, dir := range []string{workspace.BinDir, workspace.ConfigDir, workspace.SidecarDir,
		workspace.SessionsDir, workspace.WorkspacesDir, workspace.TrialsDir, workspace.RawDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Workspace{}, fmt.Errorf("create workspace directory %s: %w", dir, err)
		}
	}

	workspace.SpliceBinaryPath = filepath.Join(workspace.BinDir, "splice")
	workspace.SidecarBinaryPath = filepath.Join(workspace.BinDir, "splice-memd")
	if err := stageBinary(opts.SpliceBinary, workspace.SpliceBinaryPath, m.BinarySHA256); err != nil {
		return Workspace{}, fmt.Errorf("stage splice binary: %w", err)
	}
	if err := stageBinary(opts.SidecarBinary, workspace.SidecarBinaryPath, m.Sidecar.BinarySHA256); err != nil {
		return Workspace{}, fmt.Errorf("stage sidecar binary: %w", err)
	}

	for i, trial := range m.Schedule.Trials {
		if err := trial.ValidateFor(m.Protocol); err != nil {
			return Workspace{}, fmt.Errorf("schedule trials[%d]: %w", i, err)
		}
		if _, err := workspace.TrialPath(trial.Key); err != nil {
			return Workspace{}, fmt.Errorf("schedule trials[%d]: %w", i, err)
		}
	}
	return workspace, nil
}

// TrialPath returns and creates the directory for one scheduled trial.
func (w Workspace) TrialPath(key TrialKey) (string, error) {
	if err := key.Validate(); err != nil {
		return "", err
	}
	if w.ExperimentID != "" && key.ExperimentID != w.ExperimentID {
		return "", fmt.Errorf("trial experiment_id %q does not match workspace %q", key.ExperimentID, w.ExperimentID)
	}
	for name, value := range map[string]string{"task_id": key.TaskID, "arm": key.Arm, "experiment_id": key.ExperimentID} {
		if err := requireComponent(value, name); err != nil {
			return "", err
		}
	}
	path := filepath.Join(w.WorkspacesDir, key.TaskID, key.Arm,
		fmt.Sprintf("rep%d-env%d", key.RepetitionID, key.EnvironmentBlock))
	if !withinPath(w.WorkspacesDir, path) {
		return "", fmt.Errorf("trial %s escapes workspaces root", key.String())
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create trial workspace %s: %w", path, err)
	}
	return path, nil
}

// ResetWorkspace clears only one trial directory and leaves the journal and
// sibling trials intact.
func ResetWorkspace(w Workspace, key TrialKey) error {
	path, err := w.TrialPath(key)
	if err != nil {
		return fmt.Errorf("resolve trial reset %s: %w", key.String(), err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("reset trial workspace %s: %w", key.String(), err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("recreate trial workspace %s: %w", key.String(), err)
	}
	return nil
}

// RequireCleanSource rejects any tracked or untracked source-tree change.
func RequireCleanSource(dir string) error { return RequireCleanSourceWithGit(dir, "git") }

// RequireCleanSourceWithGit uses the caller-selected git executable so callers
// can pin the binary used for the cleanliness gate.
func RequireCleanSourceWithGit(dir, gitBinary string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("source directory is required")
	}
	if strings.TrimSpace(gitBinary) == "" {
		gitBinary = "git"
	}
	cmd := exec.Command(gitBinary, "-C", dir, "status", "--porcelain", "--untracked-files=all")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("check source cleanliness in %s: %w", dir, err)
	}
	if text := strings.TrimSpace(string(output)); text != "" {
		return fmt.Errorf("source tree %s is dirty: %s", dir, strings.ReplaceAll(text, "\n", "; "))
	}
	return nil
}

// ChildEnvironment filters a parent environment to the deterministic EV2
// allowlist and approved experiment-injected paths.
func ChildEnvironment(m Manifest, environ []string) ([]string, error) {
	if err := m.Protocol.Validate(); err != nil {
		return nil, fmt.Errorf("protocol: %w", err)
	}
	values := make(map[string]string)
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("malformed environment entry %q", entry)
		}
		values[key] = value
	}
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "TMPDIR": true, "LANG": true, "TZ": true, "TERM": true,
		"GOFLAGS": true, "GOMODCACHE": true, "GOCACHE": true, "GOTOOLCHAIN": true,
		"GOPROXY": true, "GONOSUMDB": true, "GOPRIVATE": true, "GO111MODULE": true,
		"CGO_ENABLED":      true,
		"SPLICE_EVAL_ROOT": true, "SPLICE_EVAL_SESSION_ROOT": true,
		"SPLICE_EVAL_SIDECAR_SOCKET": true, "SPLICE_EVAL_CONFIG": true,
	}
	root := values["SPLICE_EVAL_ROOT"]
	if root != "" {
		root, _ = canonicalPath(root)
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		if !allowed[key] {
			continue
		}
		if containsHiddenPath(value) {
			return nil, fmt.Errorf("environment variable %s points into a hidden root: %q", key, value)
		}
		if strings.HasPrefix(key, "SPLICE_EVAL_") && key != "SPLICE_EVAL_ROOT" && root != "" {
			resolved, err := canonicalPath(value)
			if err != nil || !withinPath(root, resolved) {
				return nil, fmt.Errorf("environment variable %s points outside experiment root: %q", key, value)
			}
		}
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out, nil
}

func stageBinary(source, destination, expected string) error {
	actual, err := hashFile(source)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("source hash %s does not match manifest hash %s", actual, expected)
	}
	if existing, err := hashFile(destination); err == nil {
		if existing == expected {
			return nil
		}
		return fmt.Errorf("staged hash %s does not match manifest hash %s", existing, expected)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source %s: %w", source, err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("create staged binary %s: %w", destination, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy binary %s: %w", source, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close staged binary %s: %w", destination, err)
	}
	staged, err := hashFile(destination)
	if err != nil {
		return err
	}
	if staged != expected {
		return fmt.Errorf("staged hash %s does not match manifest hash %s", staged, expected)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open binary %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash binary %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func requireComponent(value, field string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return fmt.Errorf("%s %q must be a single safe path component", field, value)
	}
	return nil
}

func withinPath(root, candidate string) bool {
	rootCanonical, err := canonicalPath(root)
	if err != nil {
		return false
	}
	candidateCanonical, err := canonicalPath(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootCanonical, candidateCanonical)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func canonicalPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	probe := absolute
	suffix := make([]string, 0)
	for {
		resolved, resolveErr := filepath.EvalSymlinks(probe)
		if resolveErr == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return absolute, nil
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func containsHiddenPath(value string) bool {
	for _, part := range strings.FieldsFunc(filepath.ToSlash(value), func(r rune) bool { return r == '/' || r == ':' }) {
		switch strings.ToLower(part) {
		case "checks", "reference", "manifests", "private_corpus":
			return true
		}
	}
	return false
}
