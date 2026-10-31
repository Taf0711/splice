package memd

import (
	"path/filepath"
)

// CanonicalProjectPath resolves a caller's project directory to the one
// stable identity the sidecar keys on: symlink-resolved when possible,
// absolute-cleaned otherwise.
//
// The identity matters end to end. macOS presents temp directories through
// /var while their resolved form is /private/var, and git resolves roots to
// the resolved spelling - so a capture imported from one spelling was
// invisible to retrieval and admission keyed from the other: the exact
// anchor query filtered by project_path, found nothing, and admission
// classified the record wrong-project. The fam-05 deepseek run showed the
// full signature: the record present in the sidecar, zero discovery
// telemetry, zero delivery, and a warm arm that re-read the anchored file.
//
// Canonicalizing inside the client means every producer and every consumer
// - capture, import, replay, exact retrieval, topic lookup, ranked search,
// semantic search, graph collect, admission's comparison input - speaks one
// identity regardless of the spelling its caller happened to hold. The
// form is stable within one machine session, which is the only scope the
// sidecar's project identity has (a per-project data dir, not a synced id).
func CanonicalProjectPath(path string) string {
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	// Unresolvable (missing or raced): clean the absolute form. Callers
	// canonicalize before writing and before querying, so both sides fall
	// back to the same spelling for the same missing directory.
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// canonicalStringPtr canonicalizes an optional project path in place
// semantics: nil stays nil ; hypa -c "a non-nil pointer is re-pointed at the
// canonical spelling so the caller's struct is not silently mutated.
func canonicalStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	canonical := CanonicalProjectPath(*p)
	return &canonical
}
