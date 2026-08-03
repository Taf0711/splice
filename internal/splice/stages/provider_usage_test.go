package stages

import (
	"reflect"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

func TestUsageFromCollectedCarriesWebSearchUsage(t *testing.T) {
	// Regression guard: the conversion dropped web-search requests and engine.
	got := usageFromCollected(&zeroruntime.CollectedStream{Usage: zeroruntime.Usage{
		WebSearchRequests: 2,
		WebSearchEngine:   "parallel",
	}})
	want := &schemas.StageUsage{WebSearchRequests: 2, WebSearchEngine: "parallel"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageFromCollected = %#v, want %#v", got, want)
	}
}

func TestUsageFromCollectedWithoutSearchIsUnchanged(t *testing.T) {
	got := usageFromCollected(&zeroruntime.CollectedStream{Usage: zeroruntime.Usage{
		InputTokens:  10,
		OutputTokens: 5,
	}})
	want := &schemas.StageUsage{InputTokens: 10, OutputTokens: 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageFromCollected = %#v, want %#v", got, want)
	}
	if got := usageFromCollected(&zeroruntime.CollectedStream{}); got != nil {
		t.Fatalf("usageFromCollected(empty) = %#v, want nil", got)
	}
}
