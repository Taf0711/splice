// Package report normalizes benchmark results into one per-task record
// schema, renders STANDARDIZED_EVAL_REPORT.md, and guards against numeric
// aggregation across different benchmarks.
//
// The schema is benchmark-agnostic: "official" holds the official evaluator's
// result object verbatim (opaque to this package), and splice_telemetry holds
// Splice-side cost and activity counters.
package report

// SchemaVersion is the current normalized record schema version.
const SchemaVersion = 1

// CostProvenance values for SpliceTelemetry.CostUSD.
const (
	CostReported  = "reported"  // provider reported the cost
	CostEstimated = "estimated" // computed from a price table; state the table in the run notes
	CostUnpriced  = "unpriced"  // no price data; cost_usd must be null
)

// SpliceTelemetry is Splice-side supplemental data. It is NEVER a benchmark
// metric and NEVER aggregated across benchmarks (see Guard).
type SpliceTelemetry struct {
	InputTokens     *int64   `json:"input_tokens"`
	OutputTokens    *int64   `json:"output_tokens"`
	CachedTokens    *int64   `json:"cached_tokens,omitempty"`
	ReasoningTokens *int64   `json:"reasoning_tokens,omitempty"`
	CostUSD         *float64 `json:"cost_usd"`
	CostProvenance  string   `json:"cost_provenance"` // reported | estimated | unpriced
	ModelCalls      *int64   `json:"model_calls"`
	ToolCalls       *int64   `json:"tool_calls"`
	FileReads       *int64   `json:"file_reads"`
	Searches        *int64   `json:"searches"`
	Repairs         *int64   `json:"repairs"`
	LatencyMS       *int64   `json:"latency_ms"`
}

// Artifacts paths point at the raw evidence backing each record.
type Artifacts struct {
	OfficialResult    string `json:"official_result,omitempty"`
	SpliceTrace       string `json:"splice_trace,omitempty"`
	WorkspaceArtifact string `json:"workspace_artifact,omitempty"`
}

// Record is the normalized per-task record.
type Record struct {
	SchemaVersion    int              `json:"schema_version"`
	Benchmark        string           `json:"benchmark"`         // e.g. "swebench_pro_os"
	BenchmarkVersion string           `json:"benchmark_version"` // pinned dataset revision
	InstanceID       string           `json:"instance_id"`
	DatasetSplit     string           `json:"dataset_split"`
	SpliceCommit     string           `json:"splice_commit"`
	Provider         string           `json:"provider"`
	Model            string           `json:"model"`
	Reasoning        string           `json:"reasoning"`        // reasoning effort/config label
	CognitionMode    string           `json:"cognition_mode"`   // e.g. "on" | "off"
	Official         map[string]any   `json:"official"`         // opaque official result object
	SpliceTelemetry  *SpliceTelemetry `json:"splice_telemetry"` // null when no trace was captured
	Artifacts        Artifacts        `json:"artifacts"`
}

// Validate enforces the record invariants. Zero-value SpliceTelemetry fields
// are pointers so "absent" and "zero" stay distinguishable in JSON.
func (r *Record) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return ErrInvalidRecord{Reason: "schema_version must be " + itoa(SchemaVersion)}
	}
	if r.Benchmark == "" {
		return ErrInvalidRecord{Reason: "benchmark is empty"}
	}
	if r.BenchmarkVersion == "" {
		return ErrInvalidRecord{Reason: "benchmark_version is empty"}
	}
	if r.InstanceID == "" {
		return ErrInvalidRecord{Reason: "instance_id is empty"}
	}
	if r.DatasetSplit == "" {
		return ErrInvalidRecord{Reason: "dataset_split is empty"}
	}
	if r.SpliceCommit == "" {
		return ErrInvalidRecord{Reason: "splice_commit is empty"}
	}
	if r.Official == nil {
		return ErrInvalidRecord{Reason: "official is null; the official evaluator result object is required, even when it reports a failure"}
	}
	switch r.SpliceTelemetry.CostProvenance {
	case "", CostReported, CostEstimated, CostUnpriced:
	default:
		return ErrInvalidRecord{Reason: "splice_telemetry.cost_provenance must be reported|estimated|unpriced, got " + r.SpliceTelemetry.CostProvenance}
	}
	if r.SpliceTelemetry.CostProvenance == CostUnpriced && r.SpliceTelemetry.CostUSD != nil {
		return ErrInvalidRecord{Reason: "splice_telemetry.cost_provenance is unpriced but cost_usd is set; unpriced runs must leave cost_usd null"}
	}
	return nil
}

// ErrInvalidRecord is returned by Validate and the loader.
type ErrInvalidRecord struct{ Reason string }

func (e ErrInvalidRecord) Error() string { return "report: invalid record: " + e.Reason }

// ErrCrossBenchmarkAggregation is returned by Guard when a caller tries to
// collapse numeric metrics from different benchmarks into one number.
type ErrCrossBenchmarkAggregation struct {
	Benchmarks []string
	Detail     string
}

func (e ErrCrossBenchmarkAggregation) Error() string {
	return "report: refusing cross-benchmark numeric aggregation over " + joinQuotes(e.Benchmarks) + ": " + e.Detail
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func joinQuotes(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += "\"" + s + "\""
	}
	return out
}
