package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TrustDecision records the user's recorded stance toward a workspace.
type TrustDecision int

const (
	// TrustUndecided means no explicit decision has been recorded for the path or
	// any ancestor; the caller should prompt the user.
	TrustUndecided TrustDecision = iota
	// TrustTrusted means the workspace or an ancestor is trusted.
	TrustTrusted
	// TrustDeclined means the workspace or an ancestor was explicitly declined.
	TrustDeclined
)

// trustRecord is the on-disk JSON shape for one workspace entry.
type trustRecord struct {
	Trusted   bool      `json:"trusted"`
	DecidedAt time.Time `json:"decidedAt"`
}

// trustFile is the top-level JSON document stored at the trust store path.
type trustFile struct {
	Workspaces map[string]trustRecord `json:"workspaces"`
}

// TrustStore persists per-workspace trust decisions on disk. It tolerates a
// missing or corrupt backing file so that a trust prompt never blocks startup.
type TrustStore struct {
	path string
	mu   sync.RWMutex
	data trustFile
}

// LoadTrustStore reads the trust store at path.
func LoadTrustStore(path string) (*TrustStore, error) {
	s := &TrustStore{path: path, data: trustFile{Workspaces: map[string]trustRecord{}}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, fmt.Errorf("read trust store %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		// Corrupt store: start empty so the user gets re-prompted next startup.
		fmt.Fprintf(os.Stderr, "warning: trust store %s is corrupt, resetting: %v\n", path, err)
		s.data.Workspaces = map[string]trustRecord{}
		return s, nil
	}
	if s.data.Workspaces == nil {
		s.data.Workspaces = map[string]trustRecord{}
	}
	return s, nil
}

// IsTrusted reports the closest recorded trust decision for workspacePath or
// any ancestor. Parent trust applies to its children.
func (s *TrustStore) IsTrusted(workspacePath string) TrustDecision {
	canonical, err := canonicalizeTrustPath(workspacePath)
	if err != nil {
		return TrustUndecided
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for cur := canonical; cur != ""; cur = parentTrustPath(cur) {
		if rec, ok := s.data.Workspaces[cur]; ok {
			if rec.Trusted {
				return TrustTrusted
			}
			return TrustDeclined
		}
		if cur == filepath.Dir(cur) {
			// Reached the filesystem root and no parent.
			break
		}
	}
	return TrustUndecided
}

// SetTrusted records a trust decision for path.
func (s *TrustStore) SetTrusted(path string, trusted bool) error {
	canonical, err := canonicalizeTrustPath(path)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Workspaces[canonical] = trustRecord{
		Trusted:   trusted,
		DecidedAt: time.Now().UTC(),
	}
	return nil
}

// Save atomically writes the trust store to disk with 0600 permissions.
func (s *TrustStore) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("encode trust store %s: %w", s.path, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create trust directory %s: %w", dir, err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".splice-trust-*.tmp")
	if err != nil {
		return fmt.Errorf("write trust store %s: %w", s.path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure trust store permissions %s: %w", s.path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write trust store %s: %w", s.path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write trust store %s: %w", s.path, err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("write trust store %s: %w", s.path, err)
	}
	return nil
}

func canonicalizeTrustPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("canonicalize trust path %s: %w", p, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// The path or an intermediate symlink may be missing; fall back to the
		// absolute path so trust decisions can still be recorded.
		return abs, nil
	}
	return resolved, nil
}

func parentTrustPath(p string) string {
	parent := filepath.Dir(p)
	if parent == p {
		return ""
	}
	return parent
}
