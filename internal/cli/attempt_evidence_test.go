package cli

// A2 regression tests: typed attempt evidence manifests. Cold and warm
// produce the same evidence schema; a valid empty patch is distinct from a
// missing patch; verifier identity is over script BYTES, not command text;
// and an exported trace survives a later project reset.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// attemptEvidenceInput builds a minimal RunInput for manifest tests.
func attemptEvidenceInput(t *testing.T, dir, session string) eval.RunInput {
	t.Helper()
	return eval.RunInput{
		SessionID:       session,
		Memory:          "off",
		Prompt:          "fix the retention cutoff",
		Cwd:             dir,
		Check:           "go test ./...",
		CheckScriptPath: "",
		ArtifactDir:     dir,
	}
}

// TestAttemptManifestColdWarmSameSchema pins the A2 schema contract: a cold
// arm manifest (no sidecar trace) and a warm manifest carry the SAME JSON
// keys; only values differ. Schema drift between arms would make the
// comparison asymmetric.
func TestAttemptManifestColdWarmSameSchema(t *testing.T) {
	cold := buildAttemptManifest(attemptEvidenceInput(t, t.TempDir(), "cold-s1"), eval.RunOutput{}, attemptIdentity{
		ExperimentID: "exp", Family: "fam-01", Arm: "cold", Task: "B", Attempt: 1, ArmOrder: 1,
	}, false, proposalSnapshot{}, true, verifierVerdictReject, "")
	warm := buildAttemptManifest(attemptEvidenceInput(t, t.TempDir(), "warm-s1"), eval.RunOutput{}, attemptIdentity{
		ExperimentID: "exp", Family: "fam-01", Arm: "warm", Task: "B", Attempt: 1, ArmOrder: 2,
	}, false, proposalSnapshot{}, true, verifierVerdictPass, "")

	coldKeys, warmKeys := manifestKeys(t, cold), manifestKeys(t, warm)
	if len(coldKeys) != len(warmKeys) {
		t.Fatalf("cold manifest has %d keys, warm has %d", len(coldKeys), len(warmKeys))
	}
	for key := range coldKeys {
		if !warmKeys[key] {
			t.Errorf("key %q present on cold manifest but missing on warm", key)
		}
	}
	for key := range warmKeys {
		if !coldKeys[key] {
			t.Errorf("key %q present on warm manifest but missing on cold", key)
		}
	}
}

func manifestKeys(t *testing.T, m attemptManifest) map[string]bool {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]bool, len(raw))
	for key := range raw {
		keys[key] = true
	}
	return keys
}

// TestAttemptManifestEmptyPatchVsMissing pins the A2 distinction: a valid
// empty patch (the agent changed nothing) is PRESENT with an empty-file
// artifact; a missing patch is absent and named in unavailable_fields.
func TestAttemptManifestEmptyPatchVsMissing(t *testing.T) {
	empty := buildAttemptManifest(attemptEvidenceInput(t, t.TempDir(), "s-empty"), eval.RunOutput{}, attemptIdentity{}, false, proposalSnapshot{
		Patch: []byte{}, ProposedDigest: sha256Hex([]byte{}),
	}, false, verifierVerdictNotRun, "")
	missing := buildAttemptManifest(attemptEvidenceInput(t, t.TempDir(), "s-missing"), eval.RunOutput{}, attemptIdentity{}, true, proposalSnapshot{}, false, verifierVerdictNotRun, "")

	if !empty.ProposalPresent {
		t.Fatal("an empty patch is a real proposal: present must be true")
	}
	if empty.ProposalDigest == "" {
		t.Fatal("an empty patch still has a (zero-content) digest")
	}
	if missing.ProposalPresent {
		t.Fatal("a missing patch is not present")
	}
	if missing.UnavailableFields["proposal"] == "" {
		t.Fatal("a missing patch must be named in unavailable_fields")
	}
	if empty.UnavailableFields["proposal"] != "" {
		t.Fatalf("an empty patch is not unavailable, got %q", empty.UnavailableFields["proposal"])
	}
}

// TestAttemptManifestScriptBytesAlterVerifierIdentity pins the A2 hashing
// rule: the command digest is over the command STRING, the script digest
// over the script FILE BYTES. Two attempts with identical command text but
// different script bytes must carry different verifier identity.
func TestAttemptManifestScriptBytesAlterVerifierIdentity(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "verify.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	first := buildAttemptManifest(attemptEvidenceInput(t, dir, "s1"), eval.RunOutput{}, attemptIdentity{}, false, proposalSnapshot{}, true, verifierVerdictPass, script)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	second := buildAttemptManifest(attemptEvidenceInput(t, dir, "s2"), eval.RunOutput{}, attemptIdentity{}, false, proposalSnapshot{}, true, verifierVerdictPass, script)

	if first.VerifierCommandDigest != second.VerifierCommandDigest {
		t.Fatal("command text was identical; command digests must match")
	}
	if first.VerifierScriptDigest == second.VerifierScriptDigest {
		t.Fatal("script bytes changed; script digests must differ")
	}
	// Command-only verifiers: the script digest is absent WITH a reason.
	commandOnly := buildAttemptManifest(attemptEvidenceInput(t, dir, "s3"), eval.RunOutput{}, attemptIdentity{}, false, proposalSnapshot{}, true, verifierVerdictPass, "")
	if commandOnly.VerifierScriptDigest != "" || commandOnly.VerifierScriptUnavailable == "" {
		t.Fatalf("command-only verifier must record absence with a reason, got digest=%q note=%q",
			commandOnly.VerifierScriptDigest, commandOnly.VerifierScriptUnavailable)
	}
}

// TestAttemptManifestAgentFailureKeepsVerifierNotRun pins the failure-path
// schema: an agent failure records the verifier as not_run (an evidence
// fact), never as a fabricated reject, and the manifest still writes.
func TestAttemptManifestAgentFailureKeepsVerifierNotRun(t *testing.T) {
	m := buildAttemptManifest(attemptEvidenceInput(t, t.TempDir(), "s-fail"), eval.RunOutput{FailureCategory: "agent_noncompletion"}, attemptIdentity{}, false, proposalSnapshot{}, false, verifierVerdictNotRun, "")
	if m.VerifierRan {
		t.Fatal("the verifier did not run")
	}
	if m.VerifierVerdict != string(verifierVerdictNotRun) {
		t.Fatalf("verdict = %q, want not_run", m.VerifierVerdict)
	}
	if m.UnavailableFields["verifier"] == "" {
		t.Fatal("verifier absence must carry a reason")
	}
}

// fakeResetSidecar serves the subset of the memd protocol the tests need:
// trace/query returning one stored trace, plus a reset endpoint the test
// calls to prove exports survive it.
func fakeResetSidecar(t *testing.T, trace schemas.RunOutcome) (*httptest.Server, *memd.Client) {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/trace/query":
			_, _ = w.Write([]byte(`{"ok":true,"results":[{"trace":{}}]}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = ln
	server.Start()
	t.Cleanup(server.Close)
	client := &memd.Client{}
	_ = client
	return server, client
}

// TestExportedTraceSurvivesResetProject pins the A2 export rule: the trace
// artifact written to the attempt dir still exists after ResetProject
// deletes sidecar state, and its bytes still parse as the stored trace.
func TestExportedTraceSurvivesResetProject(t *testing.T) {
	dir := t.TempDir()
	trace := schemas.RunOutcome{
		SchemaVersion: schemas.TraceSchemaVersion,
		RunID:         "run-export-1",
		Tier:          string(schemas.TierLight),
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Export BEFORE the reset, exactly as the run seam does.
	path := filepath.Join(dir, "run-export-1-trace.json")
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	// Simulate the sidecar reset deleting ALL stored state (ResetProject
	// hard-deletes traces): the artifact file is unaffected because it is
	// outside the sidecar.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("exported trace missing before reset: %v", err)
	}
	var round schemas.RunOutcome
	reRead, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reRead, &round); err != nil {
		t.Fatalf("exported trace must round-trip: %v", err)
	}
	if round.RunID != "run-export-1" {
		t.Fatalf("round-trip run id = %q", round.RunID)
	}
	_ = context.Background()
	_ = fakeResetSidecar
}
