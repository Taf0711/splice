package splice

// The prefetch-need selection (D2, pack prefetch_need_v1). Code ranks the
// files the work records name (rankedMemoryFiles); Jev judges each ranked
// file with one noul over the task and the file's first lines: must the
// task's change edit or match this file? Shadow records the probabilities
// and changes nothing. active_experimental keeps the files at or above the
// threshold, in probability order, and may keep none. A failed or declined
// decision keeps the code-ranked prefetch.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const (
	// DecisionPackEnvVar selects the pack the advisor runs at stage
	// preparation. Unset keeps the M4 dependency selector.
	DecisionPackEnvVar = "SPLICE_DECISION_PACK"
	// QuestionPackPrefetchNeedV1 is the prefetch-need pack.
	QuestionPackPrefetchNeedV1 = "prefetch_need_v1"
	// PrefetchNeedThresholdEnvVar is the true probability a file needs to
	// stay in the prefetch in active_experimental mode. Unset: no action.
	PrefetchNeedThresholdEnvVar = "SPLICE_PREFETCH_NEED_THRESHOLD"

	// PrefetchNeedGateEnvVar asks Jev only when the best work record's
	// task overlap is below this value; at or above it, the code ranking
	// keeps the prefetch. Unset asks Jev every time. (P5b: Jev saved on
	// tasks whose memory held other features' records only, and cost
	// requests on tasks whose own kind of task was recorded.)
	PrefetchNeedGateEnvVar = "SPLICE_PREFETCH_NEED_GATE"

	// maxPrefetchNeedCandidates is how many ranked files Jev judges: more
	// than the prefetch keeps, so the judgment can choose, not only drop.
	maxPrefetchNeedCandidates = 6
	// prefetchNeedHeadChars bounds the file head each candidate carries.
	prefetchNeedHeadChars = 1200
)

// prefetchNeedScore is one judged candidate.
type prefetchNeedScore struct {
	Path string
	P    float64
}

// prefetchNeedQuestions asks one noul per candidate. The false criterion
// names the near miss (a neighboring feature), as the rerank cookbook
// does, because the distractors here are sibling features.
func prefetchNeedQuestions(n int) []zeroruntime.DecisionQuestion {
	out := make([]zeroruntime.DecisionQuestion, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, zeroruntime.DecisionQuestion{
			ID:   fmt.Sprintf("need_%d", i),
			Kind: zeroruntime.DecisionKindNoul,
			Instructions: fmt.Sprintf("`need` is a coding task in this repository. `candidates[%d].Ref` is a file an earlier task used; "+
				"`candidates[%d].Claim` names that earlier task and shows the file's first lines. "+
				"To complete `need` correctly, must the change edit this file, or match a list, signature, or rule that this file holds?", i, i),
			Criteria: []zeroruntime.DecisionCriterion{
				{Label: "false", Description: "The file belongs to a similar or neighboring feature. The task can be completed correctly without reading it."},
				{Label: "true", Description: "The task's change must edit this file, or must match a list, signature, or rule that this file holds."},
			},
		})
	}
	return out
}

// fileHead returns up to limit bytes of the file, cut at a line end.
func fileHead(workDir, path string, limit int) string {
	data, err := os.ReadFile(filepath.Join(workDir, path))
	if err != nil {
		return ""
	}
	if len(data) > limit {
		data = data[:limit]
		if i := strings.LastIndexByte(string(data), '\n'); i > 0 {
			data = data[:i]
		}
	}
	return string(data)
}

// runPrefetchNeed buys one decision over the candidates and returns the
// scores in candidate order. ok is false when no decision was bought or no
// answer came back; the disposition records say why.
func runPrefetchNeed(ctx context.Context, p stageInputPreparation, input schemas.HarnessStageInput, candidates []SourceFetch) (scores []prefetchNeedScore, ok bool) {
	a := p.Options.Advisor
	if a == nil || a.selectorOff || len(candidates) == 0 {
		return nil, false
	}
	digestInput := input.RequestIntent
	snap := schemas.SemanticSnapshot{
		RunID:              p.Options.SessionID,
		EpisodeID:          fmt.Sprintf("%s:%d:%d", input.StageName, p.Iteration, p.NowUnix),
		Stage:              input.StageName,
		StageInvocation:    invocationOrdinalFor(p),
		Iteration:          p.Iteration,
		EventID:            fmt.Sprintf("%s:%d", input.StageName, p.Iteration),
		SnapshotSchema:     schemas.SnapshotSchemaV1,
		QuestionPack:       QuestionPackPrefetchNeedV1,
		HostPolicy:         schemas.HostPolicyBoundedPrefetchV1,
		CanonicalProjectID: memoryProjectRoot(p.Options, p.WorkDir),
		DistilledIntent:    truncateClaim(input.RequestIntent, 600),
		UnresolvedRelation: "which remembered files this task must read",
	}
	for _, c := range candidates {
		head := fileHead(p.WorkDir, c.Path, prefetchNeedHeadChars)
		digestInput += "|" + c.Path + "|" + head
		snap.CandidateRefs = append(snap.CandidateRefs, schemas.CandidateRef{
			Ref:    c.Path,
			Origin: c.RecordRef,
			Claim:  c.Reason + "\nFile head:\n" + head,
		})
	}
	digest := sha256.Sum256([]byte(digestInput))
	snap.SourceDigest = hex.EncodeToString(digest[:8])
	record, _ := a.EvaluateGate1WithQuestions(ctx, snap, nil, prefetchNeedQuestions(len(candidates)))
	if record.Assessment == nil || len(record.Assessment.Answers) == 0 {
		return nil, false
	}
	byID := map[string]float64{}
	for _, ans := range record.Assessment.Answers {
		if pt, found := ans.Probabilities["true"]; found {
			byID[ans.QuestionID] = pt
		}
	}
	for i, c := range candidates {
		pt, found := byID[fmt.Sprintf("need_%d", i)]
		if !found {
			return nil, false
		}
		scores = append(scores, prefetchNeedScore{Path: c.Path, P: pt})
	}
	return scores, true
}

// selectByNeed keeps the candidates at or above the threshold, in
// probability order, capped at the prefetch bound. It may keep none.
func selectByNeed(candidates []SourceFetch, scores []prefetchNeedScore, threshold float64) []SourceFetch {
	p := map[string]float64{}
	for _, s := range scores {
		p[s.Path] = s.P
	}
	var kept []SourceFetch
	for _, c := range candidates {
		if p[c.Path] >= threshold {
			kept = append(kept, c)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return p[kept[i].Path] > p[kept[j].Path] })
	if len(kept) > maxMemoryPrefetchFiles {
		kept = kept[:maxMemoryPrefetchFiles]
	}
	return kept
}

// resolvePrefetchNeedThreshold reads the active threshold; ok is false when
// it is unset or invalid, and then no action is taken.
func resolvePrefetchNeedThreshold(env func(string) string) (float64, bool) {
	raw := strings.TrimSpace(env(PrefetchNeedThresholdEnvVar))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		return 0, false
	}
	return v, true
}

// resolvePrefetchNeedGate reads the gate; ok is false when it is unset or
// invalid, and then Jev is asked every time.
func resolvePrefetchNeedGate(env func(string) string) (float64, bool) {
	raw := strings.TrimSpace(env(PrefetchNeedGateEnvVar))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		return 0, false
	}
	return v, true
}

// formatNeedScores renders the scores for the raw stream.
func formatNeedScores(scores []prefetchNeedScore) string {
	parts := make([]string, 0, len(scores))
	for _, s := range scores {
		parts = append(parts, fmt.Sprintf("%s=%.3f", s.Path, s.P))
	}
	return strings.Join(parts, " ")
}
