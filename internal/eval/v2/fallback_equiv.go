package v2

import (
	"fmt"
	"sort"
)

// FallbackPair pairs trace-store usage with validated stream-fallback usage
// for EV2-6 sensitivity inputs.
type FallbackPair struct {
	Key        TrialKey   `json:"key"`
	FromTrace  TokenUsage `json:"from_trace"`
	FromStream TokenUsage `json:"from_stream"`
	Match      bool       `json:"match"`
}

// FallbackEquivalenceSummary aggregates the tolerance-free fallback check.
type FallbackEquivalenceSummary struct {
	Pairs      int            `json:"pairs"`
	Matched    int            `json:"matched"`
	Mismatches []FallbackPair `json:"mismatches"`
}

// CompareFallback compares every uint64 token field exactly. Nil fields are
// mismatches, not implicit zeroes. Diff names use canonical protocol order.
func CompareFallback(a, b TokenUsage) (bool, []string) {
	fields := []struct {
		name        string
		left, right *uint64
	}{
		{"total_tokens", a.TotalTokens, b.TotalTokens},
		{"input_tokens", a.InputTokens, b.InputTokens},
		{"cached_input_tokens", a.CachedInputTokens, b.CachedInputTokens},
		{"cache_write_tokens", a.CacheWriteTokens, b.CacheWriteTokens},
		{"output_tokens", a.OutputTokens, b.OutputTokens},
		{"reasoning_tokens", a.ReasoningTokens, b.ReasoningTokens},
	}
	diffs := make([]string, 0)
	for _, field := range fields {
		if field.left == nil || field.right == nil {
			if field.left != nil || field.right != nil {
				diffs = append(diffs, field.name)
			}
			continue
		}
		if *field.left != *field.right {
			diffs = append(diffs, field.name)
		}
	}
	return len(diffs) == 0, diffs
}

// Validate checks summary arithmetic and requires every mismatch to be
// explicit rather than silently accepting an incomplete comparison.
func (s FallbackEquivalenceSummary) Validate() error {
	if s.Pairs < 0 || s.Matched < 0 || s.Matched > s.Pairs {
		return fmt.Errorf("fallback summary counts are invalid: pairs=%d matched=%d", s.Pairs, s.Matched)
	}
	if s.Matched == s.Pairs && len(s.Mismatches) != 0 {
		return fmt.Errorf("fallback summary has mismatches despite all pairs matched")
	}
	if s.Matched != s.Pairs && len(s.Mismatches) == 0 {
		return fmt.Errorf("fallback summary has unmatched pairs but no mismatches")
	}
	if s.Matched+len(s.Mismatches) != s.Pairs {
		return fmt.Errorf("fallback summary pairs=%d does not equal matched=%d plus mismatches=%d", s.Pairs, s.Matched, len(s.Mismatches))
	}
	for i, pair := range s.Mismatches {
		if err := pair.Key.Validate(); err != nil {
			return fmt.Errorf("mismatches[%d] key: %w", i, err)
		}
		match, diffs := CompareFallback(pair.FromTrace, pair.FromStream)
		if match || pair.Match {
			return fmt.Errorf("mismatches[%d] is marked mismatched but has equal usage", i)
		}
		if len(diffs) == 0 {
			return fmt.Errorf("mismatches[%d] has no differing token fields", i)
		}
	}
	return nil
}

// CanonicalizeFallbackDiff sorts a diff list in protocol field order.
func CanonicalizeFallbackDiff(fields []string) []string {
	order := map[string]int{"total_tokens": 0, "input_tokens": 1, "cached_input_tokens": 2, "cache_write_tokens": 3, "output_tokens": 4, "reasoning_tokens": 5}
	result := append([]string(nil), fields...)
	sort.SliceStable(result, func(i, j int) bool { return order[result[i]] < order[result[j]] })
	return result
}
