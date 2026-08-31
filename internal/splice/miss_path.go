package splice

import (
	"context"

	"github.com/Taf0711/splice/internal/splice/cognition"
	"github.com/Taf0711/splice/internal/splice/memoryrank"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// rerankedMissPath performs the C1c miss-path retrieval for one stage:
//
//  1. If the MemoryStore also implements RankedSearchStore, fetch candidates
//     WITH their FTS BM25 ranks (16-32 candidates, report section 28) and
//     order them with the deterministic reranker.
//  2. Otherwise (or on any ranked-search error, including an old sidecar
//     without the endpoint), fall back to plain Memory.Search and keep its
//     BM25 order. Admission is order-agnostic, so both paths admit the same
//     way; only the candidate order differs.
//
// The returned bundle is what memoryreason.Admit receives. Error means
// retrieval itself failed (the caller degrades memory status); a ranked
// fallback to Search returning zero results is NOT an error.
func (p stageInputPreparation) rerankedMissPath(ctx context.Context, input schemas.HarnessStageInput, root string) (schemas.MemoryBundle, error) {
	query := newMemoryQuery(input.StageName, input.RequestIntent, root)
	rankedStore, ok := p.Memory.(RankedSearchStore)
	if !ok || rankedStore == nil {
		return p.Memory.Search(ctx, query)
	}
	candidates, _, err := rankedStore.SearchRanked(ctx, query)
	if err != nil {
		// Old sidecar or transient ranked failure: fall back, do not fail
		// the run's memory on an enhancement.
		return p.Memory.Search(ctx, query)
	}
	if len(candidates) == 0 {
		return schemas.MemoryBundle{}, nil
	}
	// Widen the candidate pool for reranking: FTS candidates are cheap and
	// the reranker + token budget pick the small final set. The plain path
	// keeps its historic Limit so an un-reranked store changes nothing.
	if query.Limit < 24 {
		query.Limit = 24
		if widened, _, werr := rankedStore.SearchRanked(ctx, query); werr == nil && len(widened) > len(candidates) {
			candidates = widened
		}
	}
	obs := make([]schemas.MemoryObservation, len(candidates))
	ranks := make([]float64, len(candidates))
	for i, c := range candidates {
		obs[i] = c.Observation
		ranks[i] = c.Rank
	}
	keys := cognition.DeriveKeys(cognition.DeriveInput{
		RequestIntent:        input.RequestIntent,
		PriorChangedFiles:    input.PriorChangedFiles,
		VerificationCommands: acceptanceFactCommands(input.AcceptanceFacts),
	})
	ordered := memoryrank.Rank(memoryrank.Candidates{Observations: obs, Ranks: ranks}, memoryrank.Context{
		TopicKeys:   keys,
		Intent:      input.RequestIntent,
		ProjectPath: root,
		StageName:   input.StageName,
		NowUnix:     p.NowUnix,
	})
	bundle := schemas.MemoryBundle{}
	for _, s := range ordered {
		bundle.Observations = append(bundle.Observations, s.Observation)
	}
	return bundle, nil
}
