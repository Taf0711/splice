package stages

// C1/C3: the base-snapshot registry. When a context bundle is fulfilled
// for a stage invocation, the delivered source views ARE the base the
// model may edit against. This registry records, per path, the
// concatenation of what the model actually received (dedupe by identity
// happened in formatContextBundle) plus the full content sha256, keyed by
// a short handle (the first 12 hex chars of the digest). The parser's
// base_ref resolves here: an unknown handle, or a handle whose recorded
// digest no longer matches the file at write time (C2 recheck), fails
// loudly rather than applying edits to unseen bytes.
//
// One registry per stage Run invocation (carried on StageOptions by the
// orchestrator's stageOptions constructor); the model cannot influence
// its content except by receiving views.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// ProposalBaseRegistry holds the delivered-source bases for one stage run.
type ProposalBaseRegistry struct {
	mu        sync.Mutex
	byHandle  map[string]ProposalSnapshot
	byPath    map[string]string // path -> handle
	seenViews map[string]bool   // view identity guard
}

func NewProposalBaseRegistry() *ProposalBaseRegistry {
	return &ProposalBaseRegistry{
		byHandle:  map[string]ProposalSnapshot{},
		byPath:    map[string]string{},
		seenViews: map[string]bool{},
	}
}

// contentDigest is the shared sha256-hex helper (independent from the
// splice package's HashBytes so the stages package stays self-contained).
func contentDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ContentDigest is the exported shared sha256-hex helper (D tests resolve
// base handles the same way the registry does).
func ContentDigest(s string) string {
	return contentDigest(s)
}

// HandleFor returns the 12-char handle for a content digest.
func HandleFor(digest string) string {
	if len(digest) < 12 {
		return digest
	}
	return digest[:12]
}

// RecordFromBundle registers every source-bearing item of a fulfilled
// bundle. Per the planner steer on base_ref identity: the handle keys on
// the DELIVERED VIEW text (what the model actually saw, display text
// included), so a proposal edit can only ever match inside delivered
// views - the unseen-span rule is enforced by construction. The snapshot
// records BOTH digests: ViewDigest over the delivered text (matching
// identity for base_ref) and ContentDigest over the raw file bytes (for
// the C2 boundary recheck). Raw bytes ride the snapshot host-side only.
func (r *ProposalBaseRegistry) RecordFromBundle(bundle *schemas.ContextBundle) {
	if r == nil || bundle == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Group delivered text per path, in bundle order.
	pathText := map[string]*strings.Builder{}
	rawByPath := map[string]string{}
	var order []string
	for _, item := range bundle.Items {
		if item.Error != nil {
			continue
		}
		path, _ := item.Payload["path"].(string)
		version, _ := item.Payload["version"].(string)
		text, _ := item.Payload["text"].(string)
		raw, hasRaw := item.Payload["raw"].(string)
		if path == "" || version == "" || text == "" {
			continue
		}
		identity := fmt.Sprintf("%s@%s", path, version)
		if r.seenViews[identity] {
			continue
		}
		r.seenViews[identity] = true
		if _, exists := pathText[path]; !exists {
			order = append(order, path)
			pathText[path] = &strings.Builder{}
		}
		pathText[path].WriteString(text)
		if hasRaw {
			rawByPath[path] = raw
		}
	}
	for _, path := range order {
		text := pathText[path].String()
		viewDigest := contentDigest(text)
		handle := HandleFor(viewDigest)
		snap := ProposalSnapshot{
			Path:       path,
			Version:    viewDigest,
			ViewDigest: viewDigest,
			Base:       text,
		}
		if raw, ok := rawByPath[path]; ok {
			snap.ContentDigest = contentDigest(raw)
			snap.Raw = raw
		}
		r.byHandle[handle] = snap
		r.byPath[path] = handle
	}
}

// Resolve finds the snapshot for a model-supplied base_ref. The second
// return is false when the handle is unknown: the parser then fails the
// proposal loudly instead of guessing a base.
func (r *ProposalBaseRegistry) Resolve(baseRef string) (ProposalSnapshot, bool) {
	if r == nil {
		return ProposalSnapshot{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snap, ok := r.byHandle[strings.TrimSpace(baseRef)]
	return snap, ok
}

// CurrentProposalSnapshot resolves the base_ref for the parser. It reads
// the run-local registry wired onto StageOptions; a nil registry (or an
// unset resolver) makes every compact modify/delete fail with unknown
// base_ref, which is the loud, honest outcome.
func currentProposalSnapshot(baseRef string) (ProposalSnapshot, bool) {
	return currentProposalBases.Resolve(baseRef)
}

// currentProposalBases is the run-local registry for the invocation
// currently executing. Stage runs are sequential within a pipeline
// iteration and the orchestrator sets it in stageOptions before the stage
// runs; tests may set it directly.
var currentProposalBases = NewProposalBaseRegistry()

// ProbeBasePaths lists the paths with recorded bases (test seam).
func ProbeBasePaths() []string {
	currentProposalBases.mu.Lock()
	defer currentProposalBases.mu.Unlock()
	out := make([]string, 0, len(currentProposalBases.byPath))
	for path := range currentProposalBases.byPath {
		out = append(out, path)
	}
	return out
}

// CurrentBaseForProbe returns the recorded delivered text for one path
// (test seam for gate mocks that must reference base_ref handles exactly
// as the model would).
func CurrentBaseForProbe(path string) (string, bool) {
	currentProposalBases.mu.Lock()
	defer currentProposalBases.mu.Unlock()
	handle, ok := currentProposalBases.byPath[path]
	if !ok {
		return "", false
	}
	snap, ok := currentProposalBases.byHandle[handle]
	if !ok {
		return "", false
	}
	return snap.Base, true
}

// RecordProposalBases registers a fulfilled context bundle as the
// proposal base for the current stage invocation (C1). Run-stage-locally:
// the model may only propose edits against text it actually received.
func RecordProposalBases(bundle *schemas.ContextBundle) {
	currentProposalBases.RecordFromBundle(bundle)
}

// SetProposalBases installs the registry for the upcoming stage
// invocation and returns a reset func.
func SetProposalBases(reg *ProposalBaseRegistry) func() {
	prev := currentProposalBases
	currentProposalBases = reg
	return func() { currentProposalBases = prev }
}

// ProbeDefaultRequestFor is the test seam for the default context request.
func ProbeDefaultRequestFor(intent, workDir, language string) schemas.ContextRequest {
	return defaultContextRequest(intent, workDir, language)
}
