package memd

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// rankedResponse mirrors the sidecar /search_ranked response shape.
type rankedResponse struct {
	OK           bool `json:"ok"`
	Observations []struct {
		Observation schemas.MemoryObservation `json:"observation"`
		Rank        float64                   `json:"rank"`
	} `json:"observations"`
	Truncated bool   `json:"truncated"`
	Error     string `json:"error,omitempty"`
}

// The client parses ranks in observation order through the REAL /search_ranked
// route (server handler wired in-process), so producer and consumer cannot
// drift.
func TestSearchRankedRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search_ranked", func(w http.ResponseWriter, r *http.Request) {
		var req schemas.MemoryQuery
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp := rankedResponse{OK: true}
		first := validObservation()
		second := validObservation()
		second.ID = 7
		resp.Observations = append(resp.Observations,
			struct {
				Observation schemas.MemoryObservation `json:"observation"`
				Rank        float64                   `json:"rank"`
			}{first, -3.5},
			struct {
				Observation schemas.MemoryObservation `json:"observation"`
				Rank        float64                   `json:"rank"`
			}{second, -1.25},
		)
		_ = json.NewEncoder(w).Encode(resp)
	})
	c := newTestServer(t, mux)

	ranked, truncated, err := c.SearchRanked(context.Background(), schemas.MemoryQuery{
		RequestingAgent: "code_writer",
		Query:           "session store",
		Limit:           5,
	})
	if err != nil {
		t.Fatalf("search ranked: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(ranked) != 2 {
		t.Fatalf("got %d ranked observations, want 2", len(ranked))
	}
	if ranked[0].Rank != -3.5 || ranked[1].Rank != -1.25 {
		t.Fatalf("ranks = %v, %v; want -3.5, -1.25", ranked[0].Rank, ranked[1].Rank)
	}
	if ranked[0].Observation.ID == ranked[1].Observation.ID {
		t.Fatal("observations not distinguished")
	}
}

// An old sidecar (no /search_ranked route) surfaces the 404 as an error the
// caller can fall back from; it must never silently return an empty rank set
// as if it were truth.
func TestSearchRankedOldSidecarSurfacesError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not found"})
	})
	c := newTestServer(t, mux)

	_, _, err := c.SearchRanked(context.Background(), schemas.MemoryQuery{
		RequestingAgent: "code_writer",
		Query:           "anything",
	})
	if err == nil {
		t.Fatal("old sidecar must surface an error, not a silent empty rank set")
	}
}

// The legacy /search path keeps working unchanged (pairing guard for the
// store-level wrapper).
func TestSearchUnchangedOnRankedStore(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":           true,
			"observations": []schemas.MemoryObservation{validObservation()},
		})
	})
	c := newTestServer(t, mux)
	bundle, err := c.Search(context.Background(), schemas.MemoryQuery{
		RequestingAgent: "code_writer",
		Query:           "anything",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(bundle.Observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(bundle.Observations))
	}
}
