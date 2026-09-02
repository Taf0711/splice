package splice

import (
	"context"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TopicLookupStore is the OPTIONAL capability that enables the cognition
// fast path: a MemoryStore whose backing store can answer a deterministic
// exact topic-key lookup. It is deliberately separate from MemoryStore so
// existing fakes and stores keep working unchanged; at runtime the pipeline
// type-asserts p.Memory to this interface and falls back to the existing
// broad Search path when it is absent. *memd.Client satisfies it.
//
// C0 invariant: this path is observability and retrieval only. A hit still
// passes memoryreason.Admit and compactStageInput unchanged; a miss, stale,
// unknown, or unsupported store falls back byte-identically to Memory.Search.
type TopicLookupStore interface {
	LookupTopic(ctx context.Context, q schemas.MemoryTopicQuery) (schemas.MemoryBundle, error)
}

// RankedSearchStore is the OPTIONAL capability that enables the C1c miss-path
// reranker: a MemoryStore whose backing store exposes FTS ranks with its
// search results. It is deliberately separate from MemoryStore so existing
// fakes and stores keep working unchanged; at runtime the pipeline type-
// asserts p.Memory to this interface and falls back to plain Memory.Search
// ordering when it is absent. *memd.Client satisfies it.
type RankedSearchStore interface {
	SearchRanked(ctx context.Context, q schemas.MemoryQuery) ([]schemas.MemoryRanked, bool, error)
}
