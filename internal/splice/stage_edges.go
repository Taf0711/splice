package splice

import (
	"encoding/json"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// edgeOutputDataCapChars bounds the data JSON one `output` edge adds to the
// downstream summary. The cap keeps an output edge from turning a summary
// hand-off into an unbounded data channel. It is measured in characters so a
// multi-byte payload cannot smuggle more than the limit.
const edgeOutputDataCapChars = 2000

// scopedStageInputs returns the prior summaries and changed files that one
// stage may see, filtered to its incoming edges. The payload on each edge
// decides what crosses:
//
//   - summary carries the dependency's output summary;
//   - output carries the summary plus bounded data JSON;
//   - none carries nothing, so the edge orders execution only.
//
// A dependency with no recorded payload defaults to summary. The returned
// maps are new, so the caller's cumulative maps stay untouched.
func scopedStageInputs(
	stage schemas.ExecutionStage,
	summaries map[string]string,
	changed map[string][]string,
	outputs map[string]schemas.HarnessStageOutput,
) (map[string]string, map[string][]string) {
	scopedSummaries := make(map[string]string, len(stage.DependsOn))
	scopedChanged := make(map[string][]string, len(stage.DependsOn))
	for _, dependency := range stage.DependsOn {
		payload := schemas.EdgePayloadSummary
		if recorded, ok := stage.EdgePayloads[dependency]; ok {
			payload = recorded.Effective()
		}
		if payload == schemas.EdgePayloadNone {
			continue
		}
		summary := summaries[dependency]
		if payload == schemas.EdgePayloadOutput {
			if data := outputs[dependency].Data; len(data) > 0 {
				if encoded, err := json.Marshal(data); err == nil {
					summary += "\n[data] " + truncateForCap(string(encoded), edgeOutputDataCapChars)
				}
			}
		}
		if summary != "" {
			scopedSummaries[dependency] = summary
		}
		if files := changed[dependency]; len(files) > 0 {
			scopedChanged[dependency] = append([]string(nil), files...)
		}
	}
	return scopedSummaries, scopedChanged
}

// truncateForCap returns value truncated to at most limit characters. It
// counts runes so a truncated payload stays valid UTF-8.
func truncateForCap(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
