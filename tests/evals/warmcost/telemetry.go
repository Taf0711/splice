package warmcost

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file implements the Option B build dependency: the harness stays on the
// evidence-substitution branch and reads the cache telemetry from the raw final
// result JSON with harness-local structs, so the product schemas are not
// touched and no product commit is cherry-picked into the harness branch.
//
// The telemetry branch adds, per provider request, prompt_layout_hash,
// cache_hit and memory_position, and per run, prompt_layout_flips. The harness
// only needs those four values, and it already parses the same final event for
// the cost ledger, so it decodes them locally instead of depending on the
// product PipelineUsageRecord/PipelineResult fields.
//
// cache_hit is three-state: true, false, and absent. Absent means the provider
// reported nothing or the report did not normalize, which is NOT a miss, so the
// decoded value stays a *bool and a nil record is reported as unknown. A run
// that emits no telemetry reports Reported=false and a nil flip count; the
// harness never reads that as zero.

// requestTelemetry is the per-request telemetry decoded from the raw JSON.
type requestTelemetry struct {
	PromptLayoutHash string
	CacheHit         *bool
	MemoryPosition   string
}

// cacheTelemetry is the decoded telemetry for one attempt. Reported is true
// only when at least one request carried a telemetry value, which is how the
// harness tells a telemetry-emitting binary from an older one. Flips is nil
// when Reported is false.
type cacheTelemetry struct {
	Reported   bool
	Flips      *int
	BySequence map[int]requestTelemetry
}

// rawTelemetryResult mirrors only the telemetry fields of the final pipeline
// result. Unknown fields are ignored, so it decodes both telemetry-emitting and
// older binaries.
type rawTelemetryResult struct {
	PromptLayoutFlips *int                `json:"prompt_layout_flips"`
	UsageRecords      []rawTelemetryUsage `json:"usage_records"`
}

// rawTelemetryUsage mirrors only the telemetry fields of one usage record.
type rawTelemetryUsage struct {
	Sequence         int    `json:"sequence"`
	PromptLayoutHash string `json:"prompt_layout_hash"`
	CacheHit         *bool  `json:"cache_hit"`
	MemoryPosition   string `json:"memory_position"`
}

// parseCacheTelemetry decodes the cache telemetry from the same final
// stream-json event parsePipelineResult reads. A missing final event is an
// error; the caller decides whether to record partial coverage.
func parseCacheTelemetry(stdout []byte) (cacheTelemetry, error) {
	finalText, err := finalEventText(stdout)
	if err != nil {
		return cacheTelemetry{}, err
	}
	var raw rawTelemetryResult
	if err := json.Unmarshal([]byte(finalText), &raw); err != nil {
		return cacheTelemetry{}, fmt.Errorf("parse final cache telemetry: %w", err)
	}
	return decodeCacheTelemetry(raw), nil
}

// decodeCacheTelemetry normalizes the raw telemetry. It is separate from the
// JSON scan so a unit test can drive it directly.
func decodeCacheTelemetry(raw rawTelemetryResult) cacheTelemetry {
	tel := cacheTelemetry{BySequence: map[int]requestTelemetry{}}
	for _, r := range raw.UsageRecords {
		hasValue := r.PromptLayoutHash != "" || r.CacheHit != nil || r.MemoryPosition != ""
		if !hasValue {
			continue
		}
		tel.Reported = true
		tel.BySequence[r.Sequence] = requestTelemetry{
			PromptLayoutHash: r.PromptLayoutHash,
			CacheHit:         r.CacheHit,
			MemoryPosition:   r.MemoryPosition,
		}
	}
	if tel.Reported {
		// prompt_layout_flips is omitempty, so a reported zero is absent from
		// the JSON. Once any request proves the binary emits telemetry, an
		// absent flip field is the reported zero, not an unknown.
		flips := 0
		if raw.PromptLayoutFlips != nil {
			flips = *raw.PromptLayoutFlips
		}
		tel.Flips = &flips
	}
	return tel
}

// mergeCacheTelemetry copies the decoded telemetry onto the typed request
// records, matching on the ledger sequence. A record with no telemetry entry
// keeps a nil CacheHit, which the report states as unknown rather than a miss.
func mergeCacheTelemetry(records []RequestRecord, tel cacheTelemetry) []RequestRecord {
	if len(records) == 0 || len(tel.BySequence) == 0 {
		return records
	}
	out := make([]RequestRecord, len(records))
	copy(out, records)
	for i := range out {
		t, ok := tel.BySequence[out[i].Sequence]
		if !ok {
			continue
		}
		out[i].PromptLayoutHash = t.PromptLayoutHash
		out[i].CacheHit = t.CacheHit
		out[i].MemoryPosition = t.MemoryPosition
	}
	return out
}

// --- Per-round and per-stage reporting (SPEC section 8 item 3) -------------

// RoundCache is the per-(stage, iteration) cache view for one attempt. A round
// with no hit is a full-priced round. CacheHitRate is cached/input tokens for
// the round, the same definition as the arm-level CacheShare.
type RoundCache struct {
	Stage        string  `json:"stage"`
	Iteration    int     `json:"iteration"`
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"input_tokens"`
	CachedTokens int     `json:"cached_input_tokens"`
	CacheHitRate float64 `json:"cache_hit_rate"`
	// SpendSource is the set of ledger spend sources in the round, sorted and
	// joined with "+" when a round mixes sources. A single value when the
	// round is one source.
	SpendSource string `json:"spend_source"`
}

// StageLayout is the distinct prompt layout hashes seen for one stage in one
// attempt. More than one distinct hash for a stage means the cacheable prefix
// flipped, which forfeits the provider prefix cache.
type StageLayout struct {
	Stage              string   `json:"stage"`
	PromptLayoutHashes []string `json:"prompt_layout_hashes"`
	Requests           int      `json:"requests"`
	RequestsWithHash   int      `json:"requests_with_hash"`
	// Hashed is false when no request in the stage carried a layout hash, so
	// stability is unknown rather than claimed.
	Hashed bool `json:"hashed"`
	// Stable is true only when at least one request carried a hash and every
	// hashed request shares one value.
	Stable bool `json:"stable"`
}

// computeRounds groups the request records of one attempt by (stage, iteration)
// and sums the tokens the provider reported. Nothing is recomputed from stage
// rows; the input is the authoritative per-request ledger projection.
func computeRounds(records []RequestRecord) []RoundCache {
	type key struct {
		stage     string
		iteration int
	}
	order := []key{}
	byKey := map[key]*RoundCache{}
	sources := map[key]map[string]bool{}
	for _, r := range records {
		k := key{stage: r.Stage, iteration: r.Iteration}
		rc, ok := byKey[k]
		if !ok {
			rc = &RoundCache{Stage: r.Stage, Iteration: r.Iteration}
			byKey[k] = rc
			order = append(order, k)
			sources[k] = map[string]bool{}
		}
		rc.Requests++
		rc.InputTokens += r.InputTokens
		rc.CachedTokens += r.CachedTokens
		sources[k][spendSourceKey(r.SpendSource)] = true
	}
	out := make([]RoundCache, 0, len(order))
	for _, k := range order {
		rc := byKey[k]
		if rc.InputTokens > 0 {
			rc.CacheHitRate = float64(rc.CachedTokens) / float64(rc.InputTokens)
		}
		names := make([]string, 0, len(sources[k]))
		for name := range sources[k] {
			names = append(names, name)
		}
		sort.Strings(names)
		rc.SpendSource = strings.Join(names, "+")
		out = append(out, *rc)
	}
	return out
}

// computeLayoutStability groups the request records of one attempt by stage and
// collects the distinct prompt layout hashes. A stage with more than one
// distinct hash is a layout flip.
func computeLayoutStability(records []RequestRecord) []StageLayout {
	order := []string{}
	byStage := map[string]*StageLayout{}
	hashes := map[string]map[string]bool{}
	for _, r := range records {
		sl, ok := byStage[r.Stage]
		if !ok {
			sl = &StageLayout{Stage: r.Stage}
			byStage[r.Stage] = sl
			order = append(order, r.Stage)
			hashes[r.Stage] = map[string]bool{}
		}
		sl.Requests++
		if r.PromptLayoutHash != "" {
			sl.RequestsWithHash++
			hashes[r.Stage][r.PromptLayoutHash] = true
		}
	}
	out := make([]StageLayout, 0, len(order))
	for _, stage := range order {
		sl := byStage[stage]
		distinct := make([]string, 0, len(hashes[stage]))
		for h := range hashes[stage] {
			distinct = append(distinct, h)
		}
		sort.Strings(distinct)
		sl.PromptLayoutHashes = distinct
		sl.Hashed = len(distinct) > 0
		sl.Stable = len(distinct) == 1
		out = append(out, *sl)
	}
	return out
}

// --- Aggregate-level cache layout summary ----------------------------------

// CacheLayoutReport summarizes prefix layout stability across the attempts of a
// run. A non-zero TotalPromptLayoutFlips is a finding: report it, do not hide
// it. AttemptsWithoutTelemetry is stated so the layout view is never read as
// complete when some binaries emitted no telemetry.
type CacheLayoutReport struct {
	Attempts                 int                 `json:"attempts"`
	AttemptsWithTelemetry    int                 `json:"attempts_with_telemetry"`
	AttemptsWithoutTelemetry int                 `json:"attempts_without_telemetry"`
	TotalPromptLayoutFlips   int                 `json:"total_prompt_layout_flips"`
	AttemptsWithFlips        []string            `json:"attempts_with_flips,omitempty"`
	DistinctHashesByStage    map[string][]string `json:"distinct_hashes_by_stage,omitempty"`
	Note                     string              `json:"note,omitempty"`
}

// computeCacheLayout aggregates the per-attempt telemetry. It names every
// attempt whose flip count is non-zero and lists the distinct hashes seen per
// stage across the run.
func computeCacheLayout(attempts []Attempt) CacheLayoutReport {
	rep := CacheLayoutReport{Attempts: len(attempts), DistinctHashesByStage: map[string][]string{}}
	stageHashes := map[string]map[string]bool{}
	for _, a := range attempts {
		if !a.CacheTelemetryReported {
			rep.AttemptsWithoutTelemetry++
			continue
		}
		rep.AttemptsWithTelemetry++
		if a.PromptLayoutFlips != nil {
			rep.TotalPromptLayoutFlips += *a.PromptLayoutFlips
			if *a.PromptLayoutFlips != 0 {
				rep.AttemptsWithFlips = append(rep.AttemptsWithFlips, fmt.Sprintf("%s/%s/r%d", a.TaskID, a.Arm, a.Repeat))
			}
		}
		for _, sl := range a.LayoutStability {
			if stageHashes[sl.Stage] == nil {
				stageHashes[sl.Stage] = map[string]bool{}
			}
			for _, h := range sl.PromptLayoutHashes {
				stageHashes[sl.Stage][h] = true
			}
		}
	}
	sort.Strings(rep.AttemptsWithFlips)
	for stage, set := range stageHashes {
		list := make([]string, 0, len(set))
		for h := range set {
			list = append(list, h)
		}
		sort.Strings(list)
		rep.DistinctHashesByStage[stage] = list
	}
	if rep.AttemptsWithoutTelemetry > 0 {
		rep.Note = fmt.Sprintf("%d of %d attempt(s) emitted no cache telemetry, so the layout view is partial and the flip count is unknown for those attempts, not zero", rep.AttemptsWithoutTelemetry, rep.Attempts)
	}
	return rep
}
