package report

import (
	"sort"
)

// This file is the CRITICAL GUARD against cross-benchmark numeric
// aggregation. Any code path that wants one number out of many records must
// go through Aggregate here, and Aggregate refuses to mix benchmarks.
// Nothing else in this package (or in benchmarks/) may compute an overall
// score, a mean, or a total across records from different benchmarks.

// AggKind labels what kind of aggregation is being requested.
type AggKind string

const (
	// AggOfficialMetric aggregates the OFFICIAL benchmark metric within ONE
	// benchmark (e.g. resolved/total for SWE-bench Pro). Allowed.
	AggOfficialMetric AggKind = "official_metric"
	// AggSpendTotal sums experiment spend (e.g. cost_usd) across benchmarks
	// for budgeting. Allowed only when the result is labeled as spend, never
	// presented as a benchmark score. The caller carries the label; this
	// package refuses to emit the number without an explicit label.
	AggSpendTotal AggKind = "experiment_spend_total"
	// AggTelemetry aggregates Splice telemetry within ONE benchmark.
	// Allowed (supplemental, single-benchmark).
	AggTelemetry AggKind = "splice_telemetry"
)

// LabeledNumber is the ONLY shape this package emits for an aggregated
// number. Scope is explicit so a consumer cannot silently mislabel it.
type LabeledNumber struct {
	// Kind is the AggKind used to produce this number.
	Kind AggKind
	// Benchmark is the single benchmark scope, or "" for spend totals.
	Benchmark string
	// Label is a human-readable label. For spend totals it must contain
	// "experiment spend" (case-insensitive), enforced by Aggregate.
	Label string
	// Value is the aggregated number.
	Value float64
	// Count is how many records were aggregated.
	Count int
}

// AggregationError wraps guard failures.
type AggregationError struct{ Msg string }

func (e AggregationError) Error() string { return "report: " + e.Msg }

// Aggregate is the guarded aggregation entry point.
//
// Rules:
//   - AggOfficialMetric and AggTelemetry require every record to come from
//     ONE benchmark; a mix returns ErrCrossBenchmarkAggregation.
//   - AggSpendTotal may span benchmarks, but its Label must identify the
//     result as experiment spend ("experiment spend"), and the returned
//     LabeledNumber carries Benchmark="" so it cannot masquerade as a
//     per-benchmark score.
func Aggregate(kind AggKind, label string, records []*Record, value func(*Record) (float64, bool)) (LabeledNumber, error) {
	if len(records) == 0 {
		return LabeledNumber{}, AggregationError{Msg: "no records to aggregate"}
	}
	if value == nil {
		return LabeledNumber{}, AggregationError{Msg: "value function is nil"}
	}
	benchmarks := benchmarkSet(records)
	switch kind {
	case AggOfficialMetric, AggTelemetry:
		if len(benchmarks) != 1 {
			return LabeledNumber{}, ErrCrossBenchmarkAggregation{
				Benchmarks: sortedKeys(benchmarks),
				Detail:     string(kind) + " requires a single benchmark scope",
			}
		}
		sum, count := 0.0, 0
		for _, r := range records {
			if v, ok := value(r); ok {
				sum += v
				count++
			}
		}
		return LabeledNumber{Kind: kind, Benchmark: records[0].Benchmark, Label: label, Value: sum, Count: count}, nil
	case AggSpendTotal:
		if !containsSpendWord(label) {
			return LabeledNumber{}, AggregationError{
				Msg: "cross-benchmark numeric totals must be labeled as experiment spend; label was " + quote(label),
			}
		}
		sum, count := 0.0, 0
		for _, r := range records {
			if v, ok := value(r); ok {
				sum += v
				count++
			}
		}
		return LabeledNumber{Kind: kind, Benchmark: "", Label: label, Value: sum, Count: count}, nil
	default:
		return LabeledNumber{}, AggregationError{Msg: "unknown aggregation kind " + quote(string(kind))}
	}
}

// benchmarkSet returns the set of distinct benchmark identifiers in records.
func benchmarkSet(records []*Record) map[string]struct{} {
	set := make(map[string]struct{}, 1)
	for _, r := range records {
		set[r.Benchmark] = struct{}{}
	}
	return set
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func containsSpendWord(label string) bool {
	// case-insensitive substring check for the spend label
	l := toLower(label)
	for _, marker := range []string{"experiment spend", "spend total", "total spend"} {
		if indexOf(l, marker) >= 0 {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 || m > n {
		if m == 0 {
			return 0
		}
		return -1
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

func quote(s string) string { return "\"" + s + "\"" }
