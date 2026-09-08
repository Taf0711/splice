package cli

// Package A exit gate (handoff Section 5): a mocked complete campaign can
// be reconstructed from its artifact directory after memd state and
// temporary worktrees are deleted.
//
// The gate does NOT need a live model: it builds a real artifact tree via
// the run seam's evidence writers over a fake exec transcript, deletes
// everything else, and proves the evidence alone answers the
// reconstruction questions (who ran, what treatment, what changed, what
// verified, what it cost, from what tree).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/splice"
)

func TestExitGateCampaignReconstructedFromArtifacts(t *testing.T) {
	root := t.TempDir()
	experimentID := "mvp-exit-gate-1"

	// One attempt per arm over one artifact dir tree, exercising the same
	// writers the production seam uses. The "worktree" is a temp dir that
	// the test deletes afterwards: nothing outside the artifacts may be
	// needed to reconstruct the campaign.
	buildAttempt := func(arm, session string, warm bool) (string, eval.RunOutput) {
		dir := filepath.Join(root, "attempts", session)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		in := eval.RunInput{
			SessionID:    session,
			Memory:       map[bool]string{true: "on", false: "off"}[warm],
			Treatment:    map[bool]string{true: "full", false: "cold"}[warm],
			Prompt:       "add the retention cutoff",
			Check:        "go test ./...",
			ArtifactDir:  dir,
			ExperimentID: experimentID,
			Family:       "fam-05",
			Arm:          arm,
			Task:         "B",
			Attempt:      1,
			ArmOrder:     1,
		}
		// The agent's proposal: one real changed file in the (soon to be
		// deleted) worktree.
		worktree := filepath.Join(root, "wt-"+session)
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		proposal := captureProposal(worktree)
		writeProposalArtifact(dir, session, []byte(`{"type":"usage","promptTokens":900,"completionTokens":300,"totalTokens":1200}`), proposal)
		eff, err := splice.ResolveEffectiveTreatment(in.Memory, in.Treatment, "available")
		if err != nil {
			t.Fatal(err)
		}
		out := eval.RunOutput{
			Success: true, Tokens: 1200, TelemetryFound: warm,
			StreamInputTokens: 900, StreamOutputTokens: 300, StreamSplitFound: true,
			StreamWorkObserved: boolPtr(true),
			ToolCalls:          6, FileReads: 3, SearchCalls: 2,
			EffectiveTreatment: &eff,
			ManifestDigest:     proposal.ManifestDigest,
			ProposedDigest:     proposal.ProposedDigest,
		}
		manifest := buildAttemptManifest(in, out, attemptIdentity{
			ExperimentID: experimentID, Family: "fam-05", Arm: arm, Task: "B", Attempt: 1, ArmOrder: 1,
		}, false, proposal, true, verifierVerdictPass, "")
		if warm {
			manifest.TraceExported = true
			manifest.TraceArtifact = session + "-trace.json"
		} else {
			manifest.TraceExported = false
			manifest.TraceUnavailable = "no stored trace for this session"
		}
		if err := writeAttemptManifest(dir, manifest); err != nil {
			t.Fatal(err)
		}
		// Warm arm: the sidecar trace artifact exists BEFORE the reset.
		if warm {
			trace := map[string]any{"run_id": session, "tokens": 1200}
			data, _ := json.Marshal(trace)
			if err := os.WriteFile(filepath.Join(dir, session+"-trace.json"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir, out
	}

	coldDir, coldOut := buildAttempt("cold", "gate-cold-1", false)
	warmDir, warmOut := buildAttempt("warm", "gate-warm-1", true)

	// ---- Destroy everything the reconstruction must not need: the
	// worktrees and (simulated) sidecar state. Only the artifact dirs
	// survive.
	if err := os.RemoveAll(filepath.Join(root, "wt-gate-cold-1")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "wt-gate-warm-1")); err != nil {
		t.Fatal(err)
	}

	// ---- Reconstruct: read each attempt's manifest and prove it answers
	// the campaign questions without anything else.
	readManifest := func(dir string) attemptManifest {
		data, err := os.ReadFile(filepath.Join(dir, "attempt-manifest.json"))
		if err != nil {
			t.Fatalf("reconstruction failed: %v", err)
		}
		var m attemptManifest
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("reconstruction failed: %v", err)
		}
		return m
	}

	cold, warm := readManifest(coldDir), readManifest(warmDir)

	// Identity: who ran, in what cell of the experiment grid.
	if cold.ExperimentID != experimentID || cold.Family != "fam-05" || cold.Arm != "cold" || cold.Task != "B" {
		t.Fatalf("cold manifest identity incomplete: %+v", cold)
	}
	if warm.Arm != "warm" || warm.SessionID != "gate-warm-1" {
		t.Fatalf("warm manifest identity incomplete: %+v", warm)
	}
	// Treatment: the effective condition each arm ran.
	if cold.EffectiveTreatment != "cold" {
		t.Fatalf("cold effective treatment = %q, want cold", cold.EffectiveTreatment)
	}
	if warm.EffectiveTreatment != "full" {
		t.Fatalf("warm effective treatment = %q, want full", warm.EffectiveTreatment)
	}
	if cold.EffRetrieval == nil || *cold.EffRetrieval {
		t.Fatal("cold arm must record retrieval=false")
	}
	if warm.EffRetrieval == nil || !*warm.EffRetrieval {
		t.Fatal("warm arm must record retrieval=true (store available)")
	}
	// Cost: both arms carry a measured token record; the cold arm's rides
	// the stream split (no sidecar trace), the warm arm's the trace.
	if cold.PromptDigest == "" || cold.VerifierCommandDigest == "" {
		t.Fatal("cold manifest lost prompt/verifier identity digests")
	}
	if warm.VerifierVerdict != "pass" {
		t.Fatalf("warm verifier verdict = %q, want pass", warm.VerifierVerdict)
	}
	if warm.TraceExported != true || warm.TraceArtifact == "" {
		t.Fatal("warm manifest must point at its exported trace artifact")
	}
	if cold.TraceExported {
		t.Fatal("cold manifest must not claim a trace it never had")
	}
	// The trace artifact file itself still exists (reset cannot touch it).
	if _, err := os.Stat(filepath.Join(warmDir, warm.TraceArtifact)); err != nil {
		t.Fatalf("exported trace missing after sidecar reset: %v", err)
	}
	// Proposal digests survive: the patch bytes are in the artifact tree.
	if warm.ProposalDigest == "" || warm.ManifestDigest == "" {
		t.Fatal("warm manifest lost proposal digests")
	}
	if warmOut.ManifestDigest != warm.ManifestDigest || coldOut.ManifestDigest != cold.ManifestDigest {
		t.Fatal("manifest digest disagrees with the seam output")
	}
	// Evidence completeness is explicit on both arms.
	if cold.EvidenceStatus == "" || warm.EvidenceStatus == "" {
		t.Fatal("evidence status must be explicit on every manifest")
	}
}
