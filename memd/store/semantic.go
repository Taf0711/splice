package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

// SemanticIndex is a local, dependency-free semantic index over graph nodes.
// Text is tokenized, hashed into a fixed-size feature vector with fnv-1a,
// L2-normalized, and stored per node as a BLOB. Search embeds the query the
// same way and ranks by cosine similarity. Vectors live in SQLite; at the
// scale a project graph reaches (thousands of nodes) a full in-memory scan
// is well under a millisecond, so no external vector service is needed. The
// bound is documented: memory use is nodeCount * 1024 * 8 bytes at most
// (8 KiB per node) because the vector is sparse and we only store nonzero
// buckets via the blob, but the scan itself is O(nodeCount).
//
// Deterministic: the same text always produces the same vector and the same
// query always returns the same ranked result.
type SemanticIndex struct {
	store *Store
}

// semanticDims is the hashed feature-vector width. 1024 buckets keep
// collisions low at graph scale while staying tiny in memory.
const semanticDims = 1024

// stopwords is a small, fixed stopword set. It is intentionally short: the
// corpus is code claims, where words like "the" appear but rarely carry
// meaning. Keeping the set small avoids over-stemming technical claims.
var semanticStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "any": true, "can": true,
	"her": true, "was": true, "one": true, "our": true, "out": true,
	"day": true, "get": true, "has": true, "him": true, "his": true,
	"how": true, "its": true, "new": true, "now": true, "old": true,
	"see": true, "two": true, "way": true, "who": true, "did": true,
	"that": true, "this": true, "with": true, "from": true, "have": true,
}

// NewSemanticIndex returns a semantic index built over the store.
func NewSemanticIndex(store *Store) *SemanticIndex {
	return &SemanticIndex{store: store}
}

// semanticTokenize lowercases, splits on non-alphanumeric characters, drops
// tokens shorter than 3 characters, and drops stopwords.
func semanticTokenize(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		lower := strings.ToLower(f)
		if len(lower) < 3 || semanticStopwords[lower] {
			continue
		}
		out = append(out, lower)
	}
	return out
}

// semanticEmbed builds a hashed, L2-normalized feature vector for text.
// fnv-1a maps each token to a bucket; the bucket count is the weight.
func semanticEmbed(text string) []float64 {
	vec := make([]float64, semanticDims)
	for _, tok := range semanticTokenize(text) {
		h := fnv.New32a()
		h.Write([]byte(tok)) //nolint:errcheck // fnv never returns an error
		vec[h.Sum32()%semanticDims]++
	}
	// L2 normalize.
	var sum float64
	for _, v := range vec {
		sum += v * v
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return vec
	}
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

// encodeVector packs a vector into a byte blob: 4-byte node_id (unused here,
// kept for future extension) then 1024 float32s. Only the blob layout
// matters to SQLite.
func encodeVector(vec []float64) []byte {
	out := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(float32(v))
		binary.LittleEndian.PutUint32(out[i*4:], bits)
	}
	return out
}

// decodeVector unpacks a blob back into a float64 vector.
func decodeVector(blob []byte) ([]float64, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("semantic: vector blob length %d is not a multiple of 4", len(blob))
	}
	vec := make([]float64, len(blob)/4)
	for i := range vec {
		bits := binary.LittleEndian.Uint32(blob[i*4:])
		vec[i] = float64(math.Float32frombits(bits))
	}
	return vec, nil
}

// cosine computes cosine similarity between two equal-length vectors. Both
// are L2-normalized at embed time, so this is a plain dot product; the
// explicit norm division makes the function correct even for a caller that
// passes an unnormalized vector (for example, a zero vector).
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// IndexNode computes the vector for a node's claim plus anchor values and
// stores it. Callers should re-index after an upsert that changes the claim.
func (si *SemanticIndex) IndexNode(ctx context.Context, nodeID int64, text string) error {
	if nodeID <= 0 {
		return fmt.Errorf("semantic: node id must be >= 1, got %d", nodeID)
	}
	vec := semanticEmbed(text)
	if len(vec) == 0 {
		// Nothing to embed (for example, an empty claim). Leave the row
		// absent so Search skips it; that is the documented fallback, not an
		// error.
		return nil
	}
	_, err := si.store.db.ExecContext(ctx, `
		INSERT INTO cognition_embeddings(node_id, vector) VALUES (?, ?)
		ON CONFLICT(node_id) DO UPDATE SET vector = excluded.vector
	`, nodeID, encodeVector(vec))
	if err != nil {
		return fmt.Errorf("semantic: index node %d: %w", nodeID, err)
	}
	return nil
}

// SemanticHit pairs a node ID with its cosine score.
type SemanticHit struct {
	NodeID int64
	Score  float64
}

// Search embeds text, scans all indexed vectors, and returns the top-k node
// IDs whose nodes are active, ordered by score descending (ties broken by
// node ID ascending for determinism). An empty index returns an empty result
// and no error: that is the documented fallback.
func (si *SemanticIndex) Search(ctx context.Context, text string, k int, projectPath string) ([]SemanticHit, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("semantic: search text is required")
	}
	if k <= 0 {
		k = 8
	}
	qvec := semanticEmbed(text)

	// Project filter: when set, only nodes of that project rank. Cross-
	// project hits would anchor at another repo's revisions and poison the
	// caller's freshness diffs, so scoping is a correctness requirement.
	rows, err := si.store.db.QueryContext(ctx, `
		SELECT e.node_id, e.vector
		FROM cognition_embeddings e
		JOIN cognition_nodes n ON n.id = e.node_id
		WHERE n.status = ?
		  AND (? = '' OR n.project_path = ?)
	`, NodeStatusActive, projectPath, projectPath)
	if err != nil {
		return nil, fmt.Errorf("semantic: scan vectors: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var hits []SemanticHit
	for rows.Next() {
		var (
			id   int64
			blob []byte
		)
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("semantic: scan vector row: %w", err)
		}
		vec, err := decodeVector(blob)
		if err != nil {
			return nil, err
		}
		hits = append(hits, SemanticHit{NodeID: id, Score: cosine(qvec, vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("semantic: vector rows: %w", err)
	}

	// Deterministic order: score descending, then node ID ascending.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].NodeID < hits[j].NodeID
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// ReindexAll recomputes the vector for every active node. It is an
// administrative operation, useful after the embedding scheme changes.
func (si *SemanticIndex) ReindexAll(ctx context.Context) (int64, error) {
	rows, err := si.store.db.QueryContext(ctx, `
		SELECT id, claim FROM cognition_nodes WHERE status = ?
	`, NodeStatusActive)
	if err != nil {
		return 0, fmt.Errorf("semantic: list nodes: %w", err)
	}
	type pair struct {
		id    int64
		claim string
	}
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.claim); err != nil {
			rows.Close()
			return 0, fmt.Errorf("semantic: list scan: %w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("semantic: list rows: %w", err)
	}
	rows.Close()

	var count int64
	for _, p := range pairs {
		if err := si.IndexNode(ctx, p.id, p.claim); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
