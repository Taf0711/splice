package splice

import (
	"context"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/memoryreason"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// rankedFakeStore implements MemoryStore + RankedSearchStore. SearchRanked
// returns candidates in BM25 order with ranks; Search returns the same
// candidates in the same order (so the fallback comparison isolates the
// reranker's effect).
type rankedFakeStore struct {
	cands []schemas.MemoryRanked
}

func (f *rankedFakeStore) Search(ctx context.Context, q schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	b := schemas.MemoryBundle{}
	for _, c := range f.cands {
		b.Observations = append(b.Observations, c.Observation)
	}
	return b, nil
}
func (f *rankedFakeStore) Upsert(ctx context.Context, obs schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	return obs, nil
}
func (f *rankedFakeStore) SearchRanked(ctx context.Context, q schemas.MemoryQuery) ([]schemas.MemoryRanked, bool, error) {
	return f.cands, false, nil
}

// plainFakeStore implements only MemoryStore (the old-sidecar shape).
type plainFakeStore struct {
	obs []schemas.MemoryObservation
}

func (f *plainFakeStore) Search(ctx context.Context, q schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	b := schemas.MemoryBundle{}
	b.Observations = append(b.Observations, f.obs...)
	return b, nil
}
func (f *plainFakeStore) Upsert(ctx context.Context, obs schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	return obs, nil
}

func mkObs(id int64, title, content string) schemas.MemoryObservation {
	return schemas.MemoryObservation{ID: id, OwnerAgent: "a", Title: title, Content: content,
		MemoryType: "pattern", Scope: "global", Visibility: "private"}
}

func prep(store MemoryStore) stageInputPreparation {
	return stageInputPreparation{Memory: store, NowUnix: 1_800_000_000}
}

// The reranker reorders miss-path candidates: an identifier-overlapping
// observation overtakes a BM25-stronger generic one.
func TestRerankedMissPathReorders(t *testing.T) {
	generic := schemas.MemoryRanked{Observation: mkObs(1, "Release checklist", "tag build publish artifacts"), Rank: -9.0}
	relevant := schemas.MemoryRanked{Observation: mkObs(2, "Session notes", "InvalidateSession clears the session store cache"), Rank: -1.0}
	p := prep(&rankedFakeStore{cands: []schemas.MemoryRanked{generic, relevant}})
	input := schemas.HarnessStageInput{RequestIntent: "fix InvalidateSession in the session store"}

	bundle, err := p.rerankedMissPath(context.Background(), input, "/repo")
	if err != nil {
		t.Fatalf("reranked miss path: %v", err)
	}
	if len(bundle.Observations) != 2 {
		t.Fatalf("got %d observations, want 2", len(bundle.Observations))
	}
	if bundle.Observations[0].ID != 2 {
		t.Fatalf("reranker did not promote the relevant observation: first id %d", bundle.Observations[0].ID)
	}
}

// A store WITHOUT the ranked capability returns Search order unchanged:
// the fallback is byte-identical to the pre-C1c path.
func TestMissPathFallsBackToPlainSearchOrder(t *testing.T) {
	o1 := mkObs(1, "Release checklist", "tag build publish artifacts")
	o2 := mkObs(2, "Session notes", "InvalidateSession clears the session store cache")
	p := prep(&plainFakeStore{obs: []schemas.MemoryObservation{o1, o2}})
	input := schemas.HarnessStageInput{RequestIntent: "fix InvalidateSession in the session store"}

	bundle, err := p.rerankedMissPath(context.Background(), input, "/repo")
	if err != nil {
		t.Fatalf("fallback miss path: %v", err)
	}
	if len(bundle.Observations) != 2 || bundle.Observations[0].ID != 1 || bundle.Observations[1].ID != 2 {
		t.Fatalf("fallback order changed: %+v", bundle.Observations)
	}
}

// A ranked-store error (old sidecar) falls back to Search instead of failing.
func TestMissPathRankedErrorFallsBack(t *testing.T) {
	failing := &errRankedStore{obs: mkObs(9, "fallback content", "content")}
	p := prep(failing)
	input := schemas.HarnessStageInput{RequestIntent: "anything"}

	bundle, err := p.rerankedMissPath(context.Background(), input, "/repo")
	if err != nil {
		t.Fatalf("ranked error must fall back, got: %v", err)
	}
	if len(bundle.Observations) != 1 || bundle.Observations[0].ID != 9 {
		t.Fatalf("fallback did not return Search results: %+v", bundle.Observations)
	}
}

type errRankedStore struct{ obs schemas.MemoryObservation }

func (f *errRankedStore) Search(ctx context.Context, q schemas.MemoryQuery) (schemas.MemoryBundle, error) {
	return schemas.MemoryBundle{Observations: []schemas.MemoryObservation{f.obs}}, nil
}
func (f *errRankedStore) Upsert(ctx context.Context, obs schemas.MemoryObservation) (schemas.MemoryObservation, error) {
	return obs, nil
}
func (f *errRankedStore) SearchRanked(ctx context.Context, q schemas.MemoryQuery) ([]schemas.MemoryRanked, bool, error) {
	return nil, false, context.DeadlineExceeded
}

// End-to-end: rerank + budget admission together deliver the most relevant
// small observation and drop the over-budget filler.
func TestRerankThenAdmitDeliversTopRelevant(t *testing.T) {
	filler := schemas.MemoryRanked{Observation: mkObs(1, "Filler", strings.Repeat("z", 400)), Rank: -9.0}
	relevant := schemas.MemoryRanked{Observation: mkObs(2, "Session", "InvalidateSession clears the store"), Rank: -2.0}
	p := prep(&rankedFakeStore{cands: []schemas.MemoryRanked{filler, relevant}})
	input := schemas.HarnessStageInput{RequestIntent: "fix InvalidateSession in the session store"}

	bundle, err := p.rerankedMissPath(context.Background(), input, "/repo")
	if err != nil {
		t.Fatalf("miss path: %v", err)
	}
	bundle.RequestingAgent = "code_writer"
	admitted := memoryreason.Admit(&bundle, "/repo", p.NowUnix)
	if admitted.Bundle == nil || len(admitted.Bundle.Observations) == 0 {
		t.Fatal("nothing admitted")
	}
	if admitted.Bundle.Observations[0].ID != 2 {
		t.Fatalf("top admitted id = %d, want the reranked-relevant 2", admitted.Bundle.Observations[0].ID)
	}
}
