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
	partialByPath := map[string]bool{}
	lastEnd := map[string]int{}
	var order []string
	for _, item := range bundle.Items {
		if item.Error != nil {
			continue
		}
		path, _ := item.Payload["path"].(string)
		if redacted, _ := item.Payload["redacted_source"].(bool); redacted && path != "" {
			// No exact bytes for this path: record it so an edit is refused
			// with the reason, unless a view of it was already registered.
			if _, exists := r.byPath[path]; !exists {
				handle := HandleFor(contentDigest("redacted\x00" + path))
				r.byHandle[handle] = ProposalSnapshot{Path: path, Redacted: true}
				r.byPath[path] = handle
			}
			continue
		}
		version, _ := item.Payload["version"].(string)
		text, _ := item.Payload["text"].(string)
		raw, hasRaw := item.Payload["raw"].(string)
		if path == "" || version == "" || text == "" {
			continue
		}
		// The identity includes the view's own text: a different view of
		// the same file version (a partial record first, the full file
		// later) must register, so the base is the latest view the model
		// saw. An identical view delivered again stays one registration.
		identity := fmt.Sprintf("%s@%s@%s", path, version, contentDigest(text))
		if r.seenViews[identity] {
			continue
		}
		r.seenViews[identity] = true
		if _, exists := pathText[path]; !exists {
			order = append(order, path)
			pathText[path] = &strings.Builder{}
		}
		start := payloadLine(item.Payload["start"])
		if pathText[path].Len() > 0 {
			// Views with a gap between them get an elision marker. A plain
			// newline would let an edit's old text span the gap and match.
			if start > lastEnd[path]+1 {
				pathText[path].WriteString("\n" + elisionMarker + "\n")
			} else {
				pathText[path].WriteString("\n")
			}
		}
		pathText[path].WriteString(text)
		lastEnd[path] = payloadLine(item.Payload["end"])
		if viewIsPartial(item, raw) {
			partialByPath[path] = true
		}
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
		snap.Partial = partialByPath[path]
		if raw, ok := rawByPath[path]; ok {
			snap.ContentDigest = contentDigest(raw)
			snap.Raw = raw
		}
		r.byHandle[handle] = snap
		r.byPath[path] = handle
	}
}

// Resolve finds the snapshot for a model-supplied base_ref. Two forms are
// accepted: the host-minted view handle, and the delivered view's PATH as
// an alias for the same snapshot. The path form exists because a live
// model reliably cites the file it saw but cannot compute a hash of text
// it only read; the recorded P4 smoke failure was two guessed handles
// burning format retries before the modify was abandoned. The second
// return is false when neither form is known: the parser then fails the
// proposal loudly instead of guessing a base.
func (r *ProposalBaseRegistry) Resolve(baseRef string) (ProposalSnapshot, bool) {
	if r == nil {
		return ProposalSnapshot{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.TrimSpace(baseRef)
	if snap, ok := r.byHandle[key]; ok {
		return snap, true
	}
	// Models frequently echo the path from the delivered view instead of
	// the short content handle. A path that was actually delivered is
	// unambiguous and resolves to the same snapshot; anything not
	// delivered still fails loudly as an unknown base_ref.
	if handle, ok := r.byPath[key]; ok {
		if snap, ok := r.byHandle[handle]; ok {
			return snap, true
		}
	}
	return ProposalSnapshot{}, false
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

// ResolveByPath finds the snapshot recorded for a path (the by-path
// index). It backs normalizeProposal's fallback for modify proposals
// whose model omitted base_ref entirely: the delivered base for that
// path is unambiguous within one invocation.
func (r *ProposalBaseRegistry) ResolveByPath(path string) (ProposalSnapshot, bool) {
	if r == nil {
		return ProposalSnapshot{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	handle, ok := r.byPath[path]
	if !ok {
		return ProposalSnapshot{}, false
	}
	snap, ok := r.byHandle[handle]
	return snap, ok
}

// elisionMarker stands in for lines a ranged read did not deliver between
// two views of one file.
const elisionMarker = "[lines not delivered]"

// viewIsPartial reports whether a delivered view leaves part of the file
// unseen: it was truncated, or its line range does not cover the file's
// first and last lines. A view that carries no range at all is whole-file,
// as before ranges existed. Fulfilled reads always carry their range.
func viewIsPartial(item schemas.ContextItem, raw string) bool {
	if item.Truncated {
		return true
	}
	start, end := payloadLine(item.Payload["start"]), payloadLine(item.Payload["end"])
	if start == 0 && end == 0 {
		return false
	}
	return start > 1 || end < rawLineCount(raw)
}

// payloadLine reads a line number from a payload value (JSON decodes it as a
// float64; in-process it is an int).
func payloadLine(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// rawLineCount counts the lines of raw bytes the way the splice package does:
// one more than the newline count, and 0 for an empty file.
func rawLineCount(raw string) int {
	if raw == "" {
		return 0
	}
	return strings.Count(raw, "\n") + 1
}
