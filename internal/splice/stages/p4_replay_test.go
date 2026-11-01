package stages

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestReplayRecordedP4HedgedPayloads replays the actual submit_code payloads
// recorded from the blocked P4 re-run smoke: six hedged modify clock_test.go
// entries (content + base_ref + edits) that the old normalizer passed straight
// to Validate and rejected as "mixed representation". The ad71955 fix must
// collapse every one to exactly one representation that Validate accepts. This
// is the required pre-spend replay against the recorded bytes.
func TestReplayRecordedP4HedgedPayloads(t *testing.T) {
	data, err := os.ReadFile("testdata/p4_smoke2_hedged.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ViewText string            `json:"view_text"`
		RawText  string            `json:"raw_text"`
		Payloads []json.RawMessage `json:"payloads"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Payloads) == 0 {
		t.Fatal("no recorded payloads in the fixture")
	}
	if fixture.ViewText == "" {
		t.Fatal("fixture carries no delivered view text")
	}

	reg := NewProposalBaseRegistry()
	reg.RecordFromBundle(&schemas.ContextBundle{Items: []schemas.ContextItem{{
		Summary: "replay of the delivered clock_test.go view",
		Payload: map[string]interface{}{
			"path":    "clock_test.go",
			"version": "replay-view",
			"text":    fixture.ViewText,
			"raw":     fixture.RawText,
		},
	}}})
	reset := SetProposalBases(reg)
	defer reset()

	for i, raw := range fixture.Payloads {
		var args struct {
			Files []ProposedFileChange `json:"files"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatalf("payload %d: %v", i, err)
		}
		if len(args.Files) == 0 {
			t.Fatalf("payload %d: no files", i)
		}
		for _, f := range args.Files {
			norm := normalizeProposal(f, currentProposalSnapshot)
			if norm.Content != nil {
				t.Fatalf("payload %d: hedge did not collapse to one representation (content still set)", i)
			}
			if err := norm.Validate(); err != nil {
				t.Fatalf("payload %d: normalized hedged modify still fails Validate: %v", i, err)
			}
			// Materialization is the guard: the recorded edits are
			// raw-anchored and the snapshot carries the raw bytes, so every
			// payload must materialize through the raw fallback. This pins
			// the former residual (a content-derived diff over the numbered
			// display view) as a CI failure, not an assumption.
			change, err := MaterializeProposal(f, currentProposalSnapshot)
			if err != nil {
				t.Fatalf("payload %d: normalized but not materializable: %v", i, err)
			}
			if change.ChangeType != "modify" {
				t.Fatalf("payload %d: change type = %q, want modify", i, change.ChangeType)
			}
			for _, e := range norm.Edits {
				if !strings.Contains(change.Content, e.New) {
					t.Fatalf("payload %d: materialized content lost the replacement %q", i, e.New)
				}
			}
			if strings.Contains(change.Content, " | ") {
				t.Fatalf("payload %d: materialized content still carries display prefixes", i)
			}
		}
	}
}
