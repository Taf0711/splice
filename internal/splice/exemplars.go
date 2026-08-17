package splice

import (
	"context"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/splice/learn"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Exemplar retrieval bounds. exemplarMinRank is the deterministic bm25 score
// gate: a match ranked above this (weaker) is discarded, so an empty bundle
// beats a junk bundle. Silence on a below-threshold result is correct, not a
// bug. Distillates are size-bounded and compete with memory items for the same
// stage budget.
const (
	exemplarMaxResults = 3
	exemplarMaxChars   = 400  // per-exemplar distillate cap
	exemplarTotalChars = 1200 // across all exemplars
	exemplarMinRank    = -1.0 // FTS5 bm25 score gate (more negative = better)
)

// retrieveExemplars queries kept past runs for the repo and distilled intent
// and returns up to exemplarMaxResults distilled exemplars, score-gated and
// size-bounded. Retrieval failure is returned to the caller, which skips
// silently; exemplars never fail a run.
func retrieveExemplars(ctx context.Context, querier learn.TraceQuerier, projectRoot, intent string) ([]schemas.Exemplar, error) {
	results, err := querier.QueryTraces(ctx, schemas.TraceQueryFilter{
		RepoRoot: projectRoot,
		Status:   "completed",
		Verdict:  schemas.VerdictKept,
		Query:    boundRunes(intent, 200),
		Limit:    exemplarMaxResults,
	})
	if err != nil {
		return nil, err
	}

	exemplars := make([]schemas.Exemplar, 0, exemplarMaxResults)
	total := 0
	for _, result := range results {
		if len(exemplars) >= exemplarMaxResults {
			break
		}
		// Ordered by rank ascending (most relevant first); once a match is
		// weaker than the gate, every later one is too.
		if result.Rank > exemplarMinRank {
			break
		}
		content := boundRunes(distillExemplar(result.Trace), exemplarMaxChars)
		if total+len(content) > exemplarTotalChars {
			break
		}
		exemplars = append(exemplars, schemas.Exemplar{RunID: result.Trace.RunID, Content: content})
		total += len(content)
	}
	return exemplars, nil
}

// distillExemplar renders only the approved, bounded fields of a kept run:
// intent, tier, stage sequence, iteration count, top changed files, and token
// total. Raw prompts, transcripts, and stage output bodies never appear.
func distillExemplar(trace schemas.RunOutcome) string {
	var b strings.Builder
	b.WriteString("intent: ")
	b.WriteString(boundRunes(trace.Intent, 120))
	b.WriteString("\ntier: ")
	b.WriteString(trace.Tier)
	if trace.Plan != nil {
		names := make([]string, len(trace.Plan.Stages))
		for i, s := range trace.Plan.Stages {
			names[i] = s.Name
		}
		b.WriteString("\nstages: ")
		b.WriteString(strings.Join(names, ", "))
	}
	b.WriteString("\niterations: ")
	b.WriteString(strconv.Itoa(len(trace.Iterations)))
	if len(trace.Outcome.ChangedFiles) > 0 {
		files := trace.Outcome.ChangedFiles
		if len(files) > 5 {
			files = files[:5]
		}
		b.WriteString("\nchanged: ")
		b.WriteString(strings.Join(files, ", "))
	}
	total := 0
	for _, s := range trace.Stages {
		total += s.TokensInput + s.TokensOutput
	}
	b.WriteString("\ntokens: ")
	b.WriteString(strconv.Itoa(total))
	return b.String()
}

// boundRunes bounds s to n runes without an ellipsis, matching the memory
// query truncation discipline.
func boundRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
