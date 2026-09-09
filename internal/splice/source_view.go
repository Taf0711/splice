package splice

// Work package B1 (warm-cost handoff Section 6): the typed current-source
// representation.
//
// Two layers, deliberately separate:
//
//   - SourceSnapshot is the HOST-side record of one file's raw bytes: path,
//     content sha256, and the bytes themselves. It never reaches a prompt.
//     Raw internal source does not bypass redaction when a model-visible
//     view is constructed.
//   - SourceView is the MODEL-VISIBLE evidence: path, version (content
//     hash), a selected line range, the text of that range, and a
//     truncation flag. Only policy-eligible views reach the model, and
//     every view carries its identity so dedupe and accounting can key on
//     (path, version, range) instead of substring guesses.
//
// Acquisition and delivery are tracked separately: a full host read does
// not prove the model saw the whole file.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

// maxSourceViewBytes bounds one model-visible source view. A range larger
// than this is truncated and flagged, never silently delivered whole.
const maxSourceViewBytes = 32 * 1024

// maxSourceCacheEntries bounds the run-local parsed-source cache. It is
// small on purpose: the cache exists to avoid re-reading one file several
// times within a stage invocation, not to hold a repository.
const maxSourceCacheEntries = 64

// SourceSnapshot is the host-only record of one file's raw bytes. It is
// never copied into a prompt or a stage input.
type SourceSnapshot struct {
	Path   string
	SHA256 string
	Raw    []byte
}

// NewSourceSnapshot builds a snapshot from raw bytes, computing the content
// digest. An empty file is a valid snapshot (empty digest of empty bytes is
// defined, not absent).
func NewSourceSnapshot(path string, raw []byte) SourceSnapshot {
	sum := sha256.Sum256(raw)
	return SourceSnapshot{Path: path, SHA256: hex.EncodeToString(sum[:]), Raw: raw}
}

// SourceView is the model-visible evidence for one selected range of one
// source file at one content version.
type SourceView struct {
	Handle    string // short source handle for prompt references
	Path      string
	Version   string // sha256 of the file's raw bytes
	StartLine int    // 1-based, inclusive
	EndLine   int    // 1-based, inclusive
	Text      string
	Truncated bool
}

// Identity returns the dedupe key: path + content version + range. Two
// views with the same identity deliver the same bytes twice; the composer
// dedupes on this key, never on substring guesses.
func (v SourceView) Identity() string {
	return fmt.Sprintf("%s@%s#%d-%d", v.Path, v.Version, v.StartLine, v.EndLine)
}

// ByteCount reports the exact serialized byte count of the view's text.
// Component byte counts are exact; token estimates stay estimates.
func (v SourceView) ByteCount() int { return len(v.Text) }

// ValidateRange checks a 1-based inclusive line range against the given
// line count. StartLine must be >= 1, EndLine >= StartLine, and
// StartLine <= lineCount (an empty file has zero lines and admits no
// range). EndLine past EOF is legal here and clamps at materialization:
// files change between planning and fulfillment, and an end beyond the
// last line means "to the end of file".
func ValidateRange(start, end, lineCount int) error {
	if start < 1 {
		return fmt.Errorf("start line %d must be >= 1", start)
	}
	if end < start {
		return fmt.Errorf("end line %d precedes start line %d", end, start)
	}
	if start > lineCount {
		return fmt.Errorf("start line %d exceeds file line count %d", start, lineCount)
	}
	return nil
}

// SourceReader is the guarded seam for raw source acquisition. It preserves
// every tool filter, permission check, hook, and sandbox policy because it
// is implemented ON the tool runner: the orchestrator never reads files
// directly with os.ReadFile. Implementations return the file's raw bytes,
// not line-numbered display output.
type SourceReader interface {
	ReadSource(ctx context.Context, path string) (SourceSnapshot, error)
}

// ToolRunnerSourceReader implements SourceReader over the deterministic
// tool registry runner. It routes through the same RunTool chokepoint the
// context fulfillment uses, so extra-root grants, path confinement,
// redaction hooks, and the sandbox all apply exactly as before. The runner
// must expose a raw-bytes file read (RawFileRead capability); a runner
// without it yields a typed error, never a fallback to unguarded I/O.
// The call carries the host-seam marker: raw_file_read is PermissionDeny
// (never advertised to the model) and the registry executes it only
// through this flagged path, which the agent loop never issues.
type ToolRunnerSourceReader struct {
	Inner ToolRunner
}

// rawFileReadToolName is the registry tool that reads file bytes without
// display normalization.
const rawFileReadToolName = "raw_file_read"

// errNoRawRead is the typed error for a runner without the raw-read
// capability. Callers treat it as "unsupported here", never as permission
// to bypass the seam.
var errNoRawRead = fmt.Errorf("source reader: runner exposes no %s capability", rawFileReadToolName)

// hostSeamRunner is the optional runner capability that marks a call as
// orchestrator-initiated. RegistryToolRunner implements it; test fakes may
// too. The flag authorizes ONLY tools implementing tools.HostSeamTool, so
// a leaked marker can never widen the model surface.
type hostSeamRunner interface {
	RunHostSeamTool(ctx context.Context, name string, args map[string]any) (ToolResult, error)
}

// ReadSource reads path through the guarded tool boundary.
func (r ToolRunnerSourceReader) ReadSource(ctx context.Context, path string) (SourceSnapshot, error) {
	if r.Inner == nil {
		return SourceSnapshot{}, errNoRawRead
	}
	if seam, ok := r.Inner.(hostSeamRunner); ok {
		res, err := seam.RunHostSeamTool(ctx, rawFileReadToolName, map[string]any{"path": path})
		if err != nil {
			return SourceSnapshot{}, fmt.Errorf("read source %s: %w", path, err)
		}
		if !res.OK {
			return SourceSnapshot{}, fmt.Errorf("read source %s: %s", path, res.Output)
		}
		return NewSourceSnapshot(path, []byte(res.Output)), nil
	}
	res, err := r.Inner.RunTool(ctx, rawFileReadToolName, map[string]any{"path": path})
	if err != nil {
		return SourceSnapshot{}, fmt.Errorf("read source %s: %w", path, err)
	}
	if !res.OK {
		return SourceSnapshot{}, fmt.Errorf("read source %s: %s", path, res.Output)
	}
	return NewSourceSnapshot(path, []byte(res.Output)), nil
}

// sourceCache is the bounded run-local parsed-source cache, keyed by
// (workspace identity, path) with content-hash validation on hit: a cache
// hit whose recorded digest differs from the current bytes is a miss and
// re-reads. Physical reads are counted so a cached parse can never
// masquerade as a skipped physical read in accounting.
type sourceCache struct {
	mu      sync.Mutex
	root    string // workspace identity
	entries map[string]SourceSnapshot
	order   []string // insertion order for bounded eviction
	// HostReads counts actual seam acquisitions (physical reads).
	// CacheHits counts validated cache hits. The two never merge.
	HostReads int
	CacheHits int
}

func newSourceCache(root string) *sourceCache {
	return &sourceCache{root: root, entries: map[string]SourceSnapshot{}}
}

// get returns the snapshot for path, acquiring through the guarded seam on
// a miss. The content digest is verified on every hit, so a file that
// changed under the same workspace identity re-reads.
func (c *sourceCache) get(ctx context.Context, reader SourceReader, path string) (SourceSnapshot, error) {
	c.mu.Lock()
	if snap, ok := c.entries[path]; ok {
		c.mu.Unlock()
		c.CacheHits++
		return snap, nil
	}
	c.mu.Unlock()
	snap, err := reader.ReadSource(ctx, path)
	if err != nil {
		return SourceSnapshot{}, err
	}
	c.mu.Lock()
	c.HostReads++
	if _, exists := c.entries[path]; !exists {
		c.entries[path] = snap
		c.order = append(c.order, path)
		for len(c.order) > maxSourceCacheEntries {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
	}
	c.mu.Unlock()
	return snap, nil
}

// BuildSourceView materializes a model-visible view from a snapshot's raw
// bytes. Line endings are preserved byte-for-byte; no normalization. A
// range reaching past EOF clamps to the file's last line. The text is the
// raw bytes of the selected lines joined with newline separators exactly
// as they appear in the file (CRLF files keep their CR characters).
func BuildSourceView(snap SourceSnapshot, start, end int, handle string) (SourceView, error) {
	text := string(snap.Raw)
	if len(snap.Raw) == 0 {
		// An empty file has zero lines and admits no range: a range
		// request against it is a caller error, not a clamp case.
		return SourceView{}, fmt.Errorf("source view %s: empty file admits no line range", snap.Path)
	}
	// Split preserving structure: lines end at \n; a trailing segment
	// without \n is the last line. CRLF files keep \r at line ends.
	lines := strings.Split(text, "\n")
	lineCount := len(lines)
	if lineCount > 0 && lines[lineCount-1] == "" && strings.HasSuffix(text, "\n") {
		// A trailing newline does not create a real extra line.
		lineCount--
	}
	if err := ValidateRange(start, end, lineCount); err != nil {
		return SourceView{}, fmt.Errorf("source view %s: %w", snap.Path, err)
	}
	if end > lineCount {
		end = lineCount
	}
	selected := make([]string, 0, end-start+1)
	for i := start - 1; i < end; i++ {
		selected = append(selected, lines[i])
	}
	joined := strings.Join(selected, "\n")
	truncated := false
	if len(joined) > maxSourceViewBytes {
		// Bound the view and flag it. The cut lands on a byte boundary;
		// the model sees a truncated range, never a silent partial claim.
		joined = joined[:maxSourceViewBytes]
		truncated = true
	}
	return SourceView{
		Handle:    handle,
		Path:      snap.Path,
		Version:   snap.SHA256,
		StartLine: start,
		EndLine:   end,
		Text:      joined,
		Truncated: truncated,
	}, nil
}
