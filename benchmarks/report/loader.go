package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// OfficialResultFile is one official evaluator output file, keyed by the
// file path it came from. Content stays opaque: this package never
// interprets official scoring semantics.
type OfficialResultFile struct {
	Path    string
	Content map[string]any
}

// LoadOfficialResult reads one official evaluator output file (JSON object).
func LoadOfficialResult(path string) (OfficialResultFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OfficialResultFile{}, fmt.Errorf("report.LoadOfficialResult: read %s: %w", path, err)
	}
	var content map[string]any
	if err := json.Unmarshal(data, &content); err != nil {
		return OfficialResultFile{}, fmt.Errorf("report.LoadOfficialResult: parse %s: %w", path, err)
	}
	return OfficialResultFile{Path: path, Content: content}, nil
}

// TraceEvent is the subset of Splice stream-json events the loader consumes.
// Unknown fields are ignored; unknown event types are skipped. Usage fields
// are flat on usage events (docs/STREAM_JSON_PROTOCOL.md).
type TraceEvent struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	Stage         string `json:"stage"`
	TimestampMS   int64  `json:"timestampMs"`

	// Flat usage-event fields.
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Iteration        int    `json:"iteration"`
	UsageSequence    int    `json:"usageSequence"`
	UsageReported    bool   `json:"usageReported"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	TotalTokens      int64  `json:"totalTokens"`
}

// RunMeta identifies the run that produced a trace. It is supplied by the
// operator (or the runner) because the trace itself does not carry the
// benchmark identity.
type RunMeta struct {
	Benchmark         string
	BenchmarkVersion  string
	DatasetSplit      string
	SpliceCommit      string
	Provider          string
	Model             string
	Reasoning         string
	CognitionMode     string
	WorkspaceArtifact string // optional
}

// LoadTrace parses a Splice stream-json JSONL trace into telemetry, following
// docs/STREAM_JSON_PROTOCOL.md. Token counters are summed WITHIN the trace
// (one run of one instance on one benchmark). cost_usd is always unpriced
// here: stream-json carries token counts, not prices, so provenance stays
// honest unless an operator supplies a provider-reported cost separately.
func LoadTrace(path string) (*SpliceTelemetry, []TraceEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("report.LoadTrace: open %s: %w", path, err)
	}
	defer f.Close()

	tel := &SpliceTelemetry{CostProvenance: CostUnpriced}
	var (
		modelCalls int64
		toolCalls  int64
		fileReads  int64
		searches   int64
		repairs    int64
		firstTS    int64 = -1
		lastTS     int64
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev TraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, nil, fmt.Errorf("report.LoadTrace: %s: non-JSON line: %w", path, err)
		}
		if firstTS < 0 && ev.TimestampMS > 0 {
			firstTS = ev.TimestampMS
		}
		if ev.TimestampMS > 0 {
			lastTS = ev.TimestampMS
		}
		switch ev.Type {
		case "usage":
			modelCalls++
			tel.InputTokens = add(tel.InputTokens, ev.PromptTokens)
			tel.OutputTokens = add(tel.OutputTokens, ev.CompletionTokens)
		case "tool_call":
			toolCalls++
			switch ev.Stage {
			case "read_file", "read":
				fileReads++
			case "search", "grep", "glob":
				searches++
			}
		case "repair", "recovery":
			repairs++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("report.LoadTrace: %s: scan: %w", path, err)
	}
	tel.ModelCalls = &modelCalls
	tel.ToolCalls = &toolCalls
	tel.FileReads = &fileReads
	tel.Searches = &searches
	tel.Repairs = &repairs
	if firstTS > 0 && lastTS >= firstTS {
		latency := lastTS - firstTS
		tel.LatencyMS = &latency
	}
	return tel, nil, nil
}

func add(p *int64, v int64) *int64 {
	if p == nil {
		out := v
		return &out
	}
	*p += v
	return p
}

// BuildRecord joins one official result file and one Splice trace into a
// normalized Record. It does not interpret the official object.
func BuildRecord(of OfficialResultFile, meta RunMeta, tracePath string, instanceID string) (*Record, error) {
	rec := &Record{
		SchemaVersion:    SchemaVersion,
		Benchmark:        meta.Benchmark,
		BenchmarkVersion: meta.BenchmarkVersion,
		InstanceID:       instanceID,
		DatasetSplit:     meta.DatasetSplit,
		SpliceCommit:     meta.SpliceCommit,
		Provider:         meta.Provider,
		Model:            meta.Model,
		Reasoning:        meta.Reasoning,
		CognitionMode:    meta.CognitionMode,
		Official:         of.Content,
		Artifacts: Artifacts{
			OfficialResult:    of.Path,
			SpliceTrace:       tracePath,
			WorkspaceArtifact: meta.WorkspaceArtifact,
		},
	}
	if tracePath != "" {
		tel, _, err := LoadTrace(tracePath)
		if err != nil {
			return nil, err
		}
		rec.SpliceTelemetry = tel
	}
	if err := rec.Validate(); err != nil {
		return nil, err
	}
	return rec, nil
}

// LoadRecords loads every official result JSON in dir (sorted by filename)
// and joins it with a trace from tracesDir named <instance>.jsonl. Missing
// traces leave SpliceTelemetry null; missing official files are an error.
func LoadRecords(officialDir, tracesDir string, meta RunMeta) ([]*Record, error) {
	entries, err := os.ReadDir(officialDir)
	if err != nil {
		return nil, fmt.Errorf("report.LoadRecords: read %s: %w", officialDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	records := make([]*Record, 0, len(names))
	for _, name := range names {
		path := officialDir + "/" + name
		of, err := LoadOfficialResult(path)
		if err != nil {
			return nil, err
		}
		instanceID := strings.TrimSuffix(name, ".json")
		tracePath := tracesDir + "/" + instanceID + ".jsonl"
		if _, err := os.Stat(tracePath); err != nil {
			tracePath = ""
		}
		rec, err := BuildRecord(of, meta, tracePath, instanceID)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}
