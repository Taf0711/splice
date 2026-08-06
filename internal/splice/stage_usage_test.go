package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestApplyStageUsageCarriesWebSearchUsage(t *testing.T) {
	record := schemas.StageRecord{}
	applyStageUsage(&record, &schemas.StageUsage{
		WebSearchRequests: 2,
		WebSearchEngine:   "parallel",
	})
	if record.WebSearchRequests != 2 || record.WebSearchEngine != "parallel" {
		t.Fatalf("stage record web search usage = (%d, %q), want (2, %q)", record.WebSearchRequests, record.WebSearchEngine, "parallel")
	}
}
