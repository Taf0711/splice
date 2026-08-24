package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SnapshotKind identifies a frozen observation or exemplar item.
type SnapshotKind string

const (
	SnapshotKindObservation SnapshotKind = "observation"
	SnapshotKindExemplar    SnapshotKind = "exemplar"
)

// FreshnessLabel is an audit label, not a selector implementation.
type FreshnessLabel string

const (
	FreshnessCurrent  FreshnessLabel = "current"
	FreshnessStale    FreshnessLabel = "stale"
	FreshnessConflict FreshnessLabel = "conflict"
)

// SnapshotItem is one immutable delivered memory item. Provenance is metadata
// only; the actual corpus content arrives in a later corpus checkpoint.
type SnapshotItem struct {
	DeliveredID      string         `json:"delivered_id"`
	ContentSHA256    string         `json:"content_sha256"`
	Kind             SnapshotKind   `json:"kind"`
	SourceTaskID     string         `json:"source_task_id"`
	RepositoryClass  string         `json:"repository_class"`
	CreatedAtRFC3339 string         `json:"created_at_rfc3339"`
	FreshnessLabel   FreshnessLabel `json:"freshness_label"`
	Provenance       string         `json:"provenance"`
	PoolMembership   []string       `json:"pool_membership"`
}

// MemorySnapshot is the canonical frozen-corpus metadata container.
type MemorySnapshot struct {
	ManifestJSONSHA256     string            `json:"manifest_json_sha256"`
	Items                  []SnapshotItem    `json:"items"`
	CorpusProvenanceSHA256 string            `json:"corpus_provenance_sha256"`
	AdmissionPolicySHA256  string            `json:"admission_policy_sha256"`
	SelectorSHA256         string            `json:"selector_sha256"`
	IDMap                  map[string]string `json:"id_map"`
	SnapshotSHA256         string            `json:"snapshot_sha256"`
	Rekeyed                bool              `json:"rekeyed,omitempty"`
}

type memorySnapshotPreimage struct {
	ManifestJSONSHA256     string            `json:"manifest_json_sha256"`
	Items                  []SnapshotItem    `json:"items"`
	CorpusProvenanceSHA256 string            `json:"corpus_provenance_sha256"`
	AdmissionPolicySHA256  string            `json:"admission_policy_sha256"`
	SelectorSHA256         string            `json:"selector_sha256"`
	IDMap                  map[string]string `json:"id_map"`
	Rekeyed                bool              `json:"rekeyed,omitempty"`
}

// Validate checks snapshot integrity and rejects holdout-task leakage.
func (s MemorySnapshot) Validate(holdoutTaskIDs []string) error {
	for name, value := range map[string]string{
		"manifest_json_sha256":     s.ManifestJSONSHA256,
		"corpus_provenance_sha256": s.CorpusProvenanceSHA256,
		"admission_policy_sha256":  s.AdmissionPolicySHA256,
		"selector_sha256":          s.SelectorSHA256,
	} {
		if !validHash(value) {
			return fmt.Errorf("%s must be a sha256 hex digest", name)
		}
	}
	if s.Rekeyed && len(s.IDMap) == 0 {
		return fmt.Errorf("rekeyed snapshot requires id_map")
	}
	seenIDs := make(map[string]bool, len(s.Items))
	holdouts := make(map[string]bool, len(holdoutTaskIDs))
	for _, taskID := range holdoutTaskIDs {
		holdouts[taskID] = true
	}
	for i, item := range s.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
		if seenIDs[item.DeliveredID] {
			return fmt.Errorf("items[%d]: duplicate delivered_id %q", i, item.DeliveredID)
		}
		seenIDs[item.DeliveredID] = true
		if holdouts[item.SourceTaskID] {
			return fmt.Errorf("items[%d]: source_task_id %q is a holdout task", i, item.SourceTaskID)
		}
	}
	if len(s.IDMap) > 0 {
		seenValues := make(map[string]bool, len(s.IDMap))
		for snapshotID, deliveredID := range s.IDMap {
			if snapshotID == "" || deliveredID == "" {
				return fmt.Errorf("id_map contains an empty key or value")
			}
			if seenValues[deliveredID] {
				return fmt.Errorf("id_map is not bijective: duplicate delivered_id %q", deliveredID)
			}
			if !seenIDs[deliveredID] {
				return fmt.Errorf("id_map value %q has no snapshot item", deliveredID)
			}
			seenValues[deliveredID] = true
		}
		if len(seenValues) != len(seenIDs) {
			return fmt.Errorf("id_map is not bijective: mapped %d items, have %d", len(seenValues), len(seenIDs))
		}
	}
	return nil
}

// Validate checks one snapshot item.
func (i SnapshotItem) Validate() error {
	if !validDeliveredID(i.DeliveredID) {
		return fmt.Errorf("delivered_id %q must use observation: or exemplar: shape", i.DeliveredID)
	}
	if !validHash(i.ContentSHA256) {
		return fmt.Errorf("content_sha256 must be a sha256 hex digest")
	}
	if i.SourceTaskID == "" || i.RepositoryClass == "" {
		return fmt.Errorf("source_task_id and repository_class are required")
	}
	if _, err := time.Parse(time.RFC3339, i.CreatedAtRFC3339); err != nil {
		return fmt.Errorf("created_at_rfc3339 must be RFC3339: %w", err)
	}
	switch i.Kind {
	case SnapshotKindObservation, SnapshotKindExemplar:
	default:
		return fmt.Errorf("unknown snapshot kind %q", i.Kind)
	}
	switch i.FreshnessLabel {
	case FreshnessCurrent, FreshnessStale, FreshnessConflict:
	default:
		return fmt.Errorf("unknown freshness_label %q", i.FreshnessLabel)
	}
	seenPools := make(map[string]bool, len(i.PoolMembership))
	for _, pool := range i.PoolMembership {
		if pool != "relevant" && pool != "placebo" {
			return fmt.Errorf("unknown pool_membership %q", pool)
		}
		if seenPools[pool] {
			return fmt.Errorf("duplicate pool_membership %q", pool)
		}
		seenPools[pool] = true
	}
	provenance := strings.ToLower(i.Provenance)
	for _, forbidden := range []string{"answer", "solution", "reference_solution", "golden"} {
		if strings.Contains(provenance, forbidden) {
			return fmt.Errorf("provenance contains hidden-answer marker %q", forbidden)
		}
	}
	return nil
}

// Encode validates and returns canonical snapshot JSON.
func (s MemorySnapshot) Encode(holdoutTaskIDs []string) ([]byte, error) {
	if err := s.Validate(holdoutTaskIDs); err != nil {
		return nil, err
	}
	return json.Marshal(s.canonical())
}

// DecodeMemorySnapshot decodes and validates canonical snapshot JSON.
func DecodeMemorySnapshot(data []byte, holdoutTaskIDs []string) (MemorySnapshot, error) {
	var snapshot MemorySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return MemorySnapshot{}, fmt.Errorf("decode memory snapshot: %w", err)
	}
	if err := snapshot.Validate(holdoutTaskIDs); err != nil {
		return MemorySnapshot{}, fmt.Errorf("validate memory snapshot: %w", err)
	}
	return snapshot, nil
}

// SnapshotPreimage returns the exact canonical bytes hashed by SnapshotHash.
func SnapshotPreimage(s MemorySnapshot) ([]byte, error) {
	if err := s.Validate(nil); err != nil {
		return nil, err
	}
	return json.Marshal(s.canonicalPreimage())
}

// SnapshotHash returns the SHA-256 hash over all snapshot fields except
// SnapshotSHA256 itself.
func SnapshotHash(s MemorySnapshot) string {
	data, err := SnapshotPreimage(s)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ImportedSnapshot exposes read-only snapshot accessors for the future runner.
type ImportedSnapshot struct {
	snapshot      MemorySnapshot
	workspacePath string
}

// ImportSnapshot verifies a frozen snapshot against the locked manifest and
// returns a value with no exported mutation path. The workspace path is empty
// until a future runner materializes the read-only file.
func ImportSnapshot(data []byte, m Manifest) (ImportedSnapshot, error) {
	var snapshot MemorySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ImportedSnapshot{}, fmt.Errorf("decode memory snapshot: %w", err)
	}
	if err := snapshot.Validate(nil); err != nil {
		return ImportedSnapshot{}, fmt.Errorf("validate memory snapshot: %w", err)
	}
	if snapshot.SnapshotSHA256 == "" || snapshot.SnapshotSHA256 != SnapshotHash(snapshot) {
		return ImportedSnapshot{}, fmt.Errorf("snapshot_sha256 %q does not match recomputed snapshot hash %q", snapshot.SnapshotSHA256, SnapshotHash(snapshot))
	}
	if snapshot.CorpusProvenanceSHA256 != m.CorpusProvenanceSHA256 {
		return ImportedSnapshot{}, fmt.Errorf("snapshot corpus provenance %q does not match manifest %q", snapshot.CorpusProvenanceSHA256, m.CorpusProvenanceSHA256)
	}
	return ImportedSnapshot{snapshot: snapshot}, nil
}

// Items returns a copy of the immutable item list.
func (i ImportedSnapshot) Items() []SnapshotItem {
	items := append([]SnapshotItem(nil), i.snapshot.Items...)
	for index := range items {
		items[index].PoolMembership = append([]string(nil), items[index].PoolMembership...)
	}
	return items
}

// Item returns one immutable item by delivered ID.
func (i ImportedSnapshot) Item(deliveredID string) (SnapshotItem, bool) {
	for _, item := range i.snapshot.Items {
		if item.DeliveredID == deliveredID {
			item.PoolMembership = append([]string(nil), item.PoolMembership...)
			return item, true
		}
	}
	return SnapshotItem{}, false
}

// WorkspacePath returns the future runner's read-only materialization path.
func (i ImportedSnapshot) WorkspacePath() string { return i.workspacePath }

func (s MemorySnapshot) canonical() MemorySnapshot {
	copy := s
	copy.Items = append([]SnapshotItem(nil), s.Items...)
	sort.Slice(copy.Items, func(i, j int) bool { return copy.Items[i].DeliveredID < copy.Items[j].DeliveredID })
	for index := range copy.Items {
		copy.Items[index].PoolMembership = append([]string(nil), copy.Items[index].PoolMembership...)
		sort.Strings(copy.Items[index].PoolMembership)
	}
	if s.IDMap != nil {
		copy.IDMap = make(map[string]string, len(s.IDMap))
		for key, value := range s.IDMap {
			copy.IDMap[key] = value
		}
	}
	return copy
}

func (s MemorySnapshot) canonicalPreimage() memorySnapshotPreimage {
	canonical := s.canonical()
	return memorySnapshotPreimage{
		ManifestJSONSHA256:     canonical.ManifestJSONSHA256,
		Items:                  canonical.Items,
		CorpusProvenanceSHA256: canonical.CorpusProvenanceSHA256,
		AdmissionPolicySHA256:  canonical.AdmissionPolicySHA256,
		SelectorSHA256:         canonical.SelectorSHA256,
		IDMap:                  canonical.IDMap,
		Rekeyed:                canonical.Rekeyed,
	}
}

func validDeliveredID(id string) bool {
	return strings.HasPrefix(id, "observation:") && len(id) > len("observation:") ||
		strings.HasPrefix(id, "exemplar:") && len(id) > len("exemplar:")
}
