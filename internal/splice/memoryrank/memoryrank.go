// Package memoryrank implements the deterministic miss-path reranker from
// the freshness research report (sections 27 and 28): identifier-aware
// tokenization plus a fixed-weight feature sum over FTS candidates. It is
// stdlib-only, uses no LLM and no embeddings, and given identical inputs it
// returns an identical order. Scores are ranking signals for admission and
// traces; they never enter a stage prompt.
package memoryrank

import (
	"sort"
	"strings"
	"unicode"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Weights are the report section 28 feature weights: initial values to be
// benchmarked, not truth. One table so retuning is a one-place change.
const (
	wExactTopic        = 0.30
	wExactPath         = 0.15
	wIdentifierOverlap = 0.20
	wSameStage         = 0.10
	wProvenance        = 0.05
	wConfidence        = 0.10
	wRecency           = 0.10

	recencyHalfLifeDays = 30.0
)

// Candidates carries the FTS candidates plus their optional BM25 ranks from
// the sidecar (more negative = more relevant). A nil Ranks slice means the
// sidecar did not provide ranks: all candidates then sit in one tie group and
// the original order is preserved among equal feature scores.
type Candidates struct {
	Observations []schemas.MemoryObservation
	Ranks        []float64
}

// Context is the query-side evidence the features compare against.
type Context struct {
	// TopicKeys are the derived cognition keys for this stage invocation
	// (cognition.DeriveKeys output; may be empty on the miss path).
	TopicKeys []string
	// Intent is the user request text for this stage.
	Intent string
	// ProjectPath is the requesting project root.
	ProjectPath string
	// StageName is the requesting stage.
	StageName string
	// NowUnix anchors the recency feature.
	NowUnix int64
}

// Scored pairs one observation with its feature sum and the features
// themselves, for trace honesty about WHY an item ranked where it did.
type Scored struct {
	Observation schemas.MemoryObservation
	Score       float64
	Features    Features
}

// Features are the bounded per-item signals (all in [0,1]).
type Features struct {
	ExactTopic        float64
	ExactPath         float64
	IdentifierOverlap float64
	SameStage         float64
	Provenance        float64
	Confidence        float64
	Recency           float64
}

type scoredInternal struct {
	Scored
	// hasRank and rank support the BM25 tie-break.
	hasRank bool
	rank    float64
}

// Rank orders candidates most-useful-first. Deterministic and independent
// of candidate input order: equal scores break by stronger BM25 rank (more
// negative) when ranks exist, then by observation ID. Nil input yields nil.
func Rank(cands Candidates, ctx Context) []Scored {
	if len(cands.Observations) == 0 {
		return nil
	}
	intentTokens := tokenSet(Tokenize(ctx.Intent))
	keyTokens := make(map[string]bool)
	for _, k := range ctx.TopicKeys {
		for _, tok := range Tokenize(k) {
			keyTokens[tok] = true
		}
	}

	internal := make([]scoredInternal, len(cands.Observations))
	for i, obs := range cands.Observations {
		f := features(obs, ctx, intentTokens, keyTokens)
		internal[i] = scoredInternal{
			Scored: Scored{
				Observation: obs,
				Score: f.ExactTopic*wExactTopic +
					f.ExactPath*wExactPath +
					f.IdentifierOverlap*wIdentifierOverlap +
					f.SameStage*wSameStage +
					f.Provenance*wProvenance +
					f.Confidence*wConfidence +
					f.Recency*wRecency,
				Features: f,
			},
			hasRank: i < len(cands.Ranks),
		}
		if internal[i].hasRank {
			internal[i].rank = cands.Ranks[i]
		}
	}

	sort.SliceStable(internal, func(a, b int) bool {
		if internal[a].Score != internal[b].Score {
			return internal[a].Score > internal[b].Score
		}
		// Tie: stronger BM25 first (more negative = more relevant). Missing
		// ranks sort as if neutral, which keeps original order among them.
		if internal[a].hasRank && internal[b].hasRank && internal[a].rank != internal[b].rank {
			return internal[a].rank < internal[b].rank
		}
		// Input-order-independent final tie-break: stable identity. Ties on
		// every feature mean the evidence is genuinely indistinguishable, so
		// the ID orders them without depending on retrieval order.
		return internal[a].Observation.ID < internal[b].Observation.ID
	})

	out := make([]Scored, len(internal))
	for i, s := range internal {
		out[i] = s.Scored
	}
	return out
}

// features computes the per-item bounded signals.
func features(obs schemas.MemoryObservation, ctx Context, intentTokens, keyTokens map[string]bool) Features {
	obsTokens := tokenSet(Tokenize(obs.Title + " " + obs.Content))

	f := Features{}
	// Exact topic: the observation was persisted under a key this stage
	// derivation also produced.
	if obs.TopicKey != nil && *obs.TopicKey != "" {
		for _, k := range ctx.TopicKeys {
			if *obs.TopicKey == k {
				f.ExactTopic = 1
				break
			}
		}
	}
	// Exact path: a path-shaped token from the intent or a file: key appears
	// verbatim in the observation text.
	haystack := obs.Title + " " + obs.Content
	for _, tok := range pathTokens(ctx.Intent, ctx.TopicKeys) {
		if strings.Contains(haystack, tok) {
			f.ExactPath = 1
			break
		}
	}
	// Identifier overlap: Jaccard over component tokens against the intent
	// plus derived-key vocabulary.
	f.IdentifierOverlap = jaccard(obsTokens, union(intentTokens, keyTokens))
	// Same stage provenance.
	if obs.SourceStage != nil && *obs.SourceStage == ctx.StageName {
		f.SameStage = 1
	}
	// Provenance strength: pinned > review-cleared > plain.
	switch {
	case obs.Pinned:
		f.Provenance = 1
	case obs.ReviewAfter == nil:
		f.Provenance = 0.6
	default:
		f.Provenance = 0.2
	}
	// Confidence: bounded to [0,1]; absent confidence is neutral 0.5.
	if obs.Confidence != nil {
		c := *obs.Confidence
		if c < 0 {
			c = 0
		}
		if c > 1 {
			c = 1
		}
		f.Confidence = c
	} else {
		f.Confidence = 0.5
	}
	// Recency: exponential decay with a 30-day half-life, bounded [0,1].
	f.Recency = recency(obs.UpdatedAt, ctx.NowUnix)
	return f
}

// recency maps age to [0,1]: 1.0 for updated now, halving every 30 days,
// floored at 0 (a zero/absent timestamp decays to the floor over time and
// never exceeds fresher items).
func recency(updatedAt, nowUnix int64) float64 {
	if nowUnix <= 0 {
		return 0
	}
	ageDays := float64(nowUnix-updatedAt) / 86400.0
	if ageDays < 0 {
		ageDays = 0
	}
	halfLives := ageDays / recencyHalfLifeDays
	if halfLives > 40 {
		return 0
	}
	result := 1.0
	for i := 0; i < int(halfLives); i++ {
		result /= 2
	}
	// Fractional remainder: linear interpolation between powers, monotonic
	// and bounded so ordering is stable.
	if frac := halfLives - float64(int(halfLives)); frac > 0 {
		result /= 1 + frac
	}
	return result
}

// jaccard is |A cap B| / |A cup B| over token sets.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for tok := range a {
		if b[tok] {
			inter++
		}
	}
	unionSize := len(a) + len(b) - inter
	if unionSize == 0 {
		return 0
	}
	return float64(inter) / float64(unionSize)
}

func union(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for tok := range a {
		out[tok] = true
	}
	for tok := range b {
		out[tok] = true
	}
	return out
}

func tokenSet(tokens []string) map[string]bool {
	out := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		out[t] = true
	}
	return out
}

// Tokenize splits text into identifier-aware component tokens (report
// section 27): CamelCase, snake_case, dotted names, and path segments split
// into parts; the exact original words are always retained too. Output is
// lowercase and deduplicated, and single characters and bare numbers are
// dropped so a pathological input cannot blow up the token set.
func Tokenize(text string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(tok string) {
		tok = strings.ToLower(tok)
		if len(tok) < 2 || len(tok) > 64 || seen[tok] {
			return
		}
		if isAllDigits(tok) {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}

	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != '/'
	})
	for _, field := range fields {
		// Retain the exact original field (case-folded).
		add(field)
		// Path segments: internal/auth/session -> internal, auth, session.
		for _, seg := range strings.Split(field, "/") {
			add(seg)
			// snake_case and dotted components.
			for _, part := range strings.Split(seg, "_") {
				add(part)
				for _, d := range strings.Split(part, ".") {
					add(d)
				}
			}
		}
		// CamelCase components inside the whole field: InvalidateSession ->
		// invalidate, session.
		for _, camel := range splitCamel(field) {
			add(camel)
		}
	}
	return out
}

// pathTokens extracts path-shaped tokens (containing / or .) from the intent
// and file: keys, for the exact-path feature.
func pathTokens(intent string, keys []string) []string {
	var out []string
	add := func(s string) {
		if strings.ContainsAny(s, "/.") && len(s) > 3 {
			out = append(out, s)
		}
	}
	for _, field := range strings.FieldsFunc(intent, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';' || r == '"' || r == '\''
	}) {
		add(field)
	}
	for _, k := range keys {
		if rest, ok := strings.CutPrefix(k, "file:"); ok {
			add(rest)
		}
	}
	return out
}

// splitCamel splits CamelCase and lowerCamel words into components,
// preserving consecutive-uppercase runs (HTTPServer -> http, server).
func splitCamel(s string) []string {
	if !hasUpper(s) {
		return nil
	}
	var parts []string
	var cur strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			if !unicode.IsUpper(prev) || (i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		}
		cur.WriteRune(r)
	}
	parts = append(parts, cur.String())
	return parts
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
