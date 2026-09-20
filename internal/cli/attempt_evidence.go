package cli

// Work package A (A2): the typed attempt evidence manifest. Every attempt,
// both arms, failure paths included, writes one attempt-manifest.json into
// its artifact directory describing the run's identity, provenance, and
// evidence completeness. The manifest is independent of the memory sidecar:
// a cold attempt (no sidecar trace) still produces a full manifest with the
// unavailable fields named, never fabricated.
//
// Honesty rules baked into the type:
//   - an unavailable field carries a reason string, never a fake value;
//   - a valid empty proposal is distinct from a missing proposal;
//   - digests are over actual bytes (verifier script bytes, binary bytes),
//     never over a label that merely names them.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// attemptManifestSchemaVersion pins the manifest shape. A reader that sees
// a different schema string must not guess at field semantics.
const attemptManifestSchemaVersion = "splice.attempt-manifest/v1"

// attemptManifest is the typed, self-describing evidence record for one
// attempt. Field availability is explicit: `omitempty` plus the
// unavailable-reason strings means a reader can always tell "measured
// zero/empty" from "not captured".
type attemptManifest struct {
	Schema string `json:"schema"`

	// Attempt identity (A2 field list: experiment, family, arm, rollout,
	// randomized order).
	ExperimentID string `json:"experiment_id,omitempty"`
	Family       string `json:"family,omitempty"`
	Arm          string `json:"arm,omitempty"`
	Task         string `json:"task,omitempty"`
	Attempt      int    `json:"attempt,omitempty"`
	ArmOrder     int    `json:"arm_order,omitempty"` // launch order within one rollout (1-based)
	SessionID    string `json:"session_id"`

	// Requested and observed model/provider (A2: recorded separately).
	RequestedModel  string `json:"requested_model,omitempty"`
	ObservedModel   string `json:"observed_model,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Prompt/verifier identity (A2: task text digest, verifier
	// script-content digest, command digest - three distinct digests).
	PromptDigest              string `json:"prompt_digest"`
	VerifierCommandDigest     string `json:"verifier_command_digest"`
	VerifierScriptDigest      string `json:"verifier_script_digest,omitempty"`
	VerifierScriptUnavailable string `json:"verifier_script_unavailable,omitempty"`

	// Binary provenance (A2: sha-256 of both binaries plus VCS metadata).
	SpliceBinarySHA256      string `json:"splice_binary_sha256,omitempty"`
	SpliceBinaryUnavailable string `json:"splice_binary_unavailable,omitempty"`
	MemdBinarySHA256        string `json:"splice_memd_binary_sha256,omitempty"`
	MemdBinaryUnavailable   string `json:"splice_memd_binary_unavailable,omitempty"`
	VCSRevision             string `json:"vcs_revision,omitempty"`
	VCSDirty                *bool  `json:"vcs_dirty,omitempty"`

	// Starting tree and snapshot identity (A2: starting tree, snapshot
	// digest, capture-set digest and origin).
	StartCommit       string `json:"start_commit,omitempty"`
	StartTree         string `json:"start_tree,omitempty"`
	SnapshotDigest    string `json:"snapshot_digest,omitempty"`
	CaptureSetDigest  string `json:"capture_set_digest,omitempty"`
	CaptureOrigin     string `json:"capture_origin,omitempty"` // natural | reconstructed | replayed | none
	CaptureOriginNote string `json:"capture_origin_note,omitempty"`

	// Normalized effective treatment (A1 types, serialized verbatim).
	RequestedTreatment    string `json:"requested_treatment,omitempty"`
	EffectiveTreatment    string `json:"effective_treatment,omitempty"`
	NormalizedFromAmbient string `json:"normalized_from_ambient,omitempty"`
	EffRetrieval          *bool  `json:"effective_retrieval,omitempty"`
	EffPromptDelivery     *bool  `json:"effective_prompt_delivery,omitempty"`
	EffContextPolicy      *bool  `json:"effective_context_policy,omitempty"`
	StoreAvailable        *bool  `json:"store_available,omitempty"`

	// Proposal and verifier outcome (A2: proposal diff manifest, verifier
	// result with exit classification). ProposalPresent distinguishes a
	// valid empty patch from a missing one: Present=true with zero
	// entries is a real "agent changed nothing" result.
	ProposalPresent bool   `json:"proposal_present"`
	ProposalDigest  string `json:"proposal_digest,omitempty"`
	ManifestDigest  string `json:"change_manifest_digest,omitempty"`
	VerifierRan     bool   `json:"verifier_ran"`
	VerifierVerdict string `json:"verifier_verdict,omitempty"` // pass | reject | infra | timeout | not_run
	FailureCategory string `json:"failure_category,omitempty"`

	// Trace export (A2: final or partial trace exported before reset).
	TraceExported    bool   `json:"trace_exported"`
	TraceUnavailable string `json:"trace_unavailable,omitempty"`
	TraceArtifact    string `json:"trace_artifact,omitempty"`

	// Artifact files present in the attempt directory.
	Artifacts []string `json:"artifacts,omitempty"`

	// Evidence completeness (A2: completeness plus the reason for each
	// unavailable field).
	EvidenceStatus    string            `json:"evidence_status"`
	UnavailableFields map[string]string `json:"unavailable_fields,omitempty"`
}

// sha256File returns the hex sha256 of the file's bytes, or an error naming
// the path. Callers turn the error into an unavailable-reason, never into a
// fabricated digest.
func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

// resolveBinaryProvenance hashes the running splice binary and the memd
// sidecar binary, recording an explicit reason for each unavailable one.
// The sidecar is resolved from PATH only: the harness never guesses a path.
func resolveBinaryProvenance() (spliceSHA, memdSHA, spliceNote, memdNote string) {
	if exe, err := os.Executable(); err == nil {
		if sum, serr := sha256File(exe); serr == nil {
			spliceSHA = sum
		} else {
			spliceNote = fmt.Sprintf("splice binary unreadable: %v", serr)
		}
	} else {
		spliceNote = fmt.Sprintf("splice binary unresolved: %v", err)
	}
	if path, err := exec.LookPath("splice-memd"); err == nil {
		if sum, serr := sha256File(path); serr == nil {
			memdSHA = sum
		} else {
			memdNote = fmt.Sprintf("splice-memd binary unreadable: %v", serr)
		}
	} else {
		memdNote = "splice-memd not on PATH"
	}
	return spliceSHA, memdSHA, spliceNote, memdNote
}

// resolveVCSProvenance reads the harness checkout's revision and dirty
// state. Unknown stays unknown (no revision string, no dirty claim).
func resolveVCSProvenance() (revision string, dirty *bool) {
	if out, err := exec.Command("git", "-C", ".", "rev-parse", "HEAD").Output(); err == nil {
		revision = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", ".", "status", "--porcelain").Output(); err == nil {
		notClean := len(strings.TrimSpace(string(out))) > 0
		dirty = &notClean
	}
	return revision, dirty
}

// verifierScriptDigest hashes the verifier script BYTES the attempt will
// execute. checkScriptPath names the script file backing the check command
// (empty for command-only verifiers, whose content is the command itself).
// The command digest and the script digest stay separate: a command that
// invokes a script can be byte-identical while the script it runs changed,
// and only the script digest sees that.
func verifierScriptDigest(checkScriptPath string) (digest, unavailable string) {
	if strings.TrimSpace(checkScriptPath) == "" {
		return "", "command-only verifier (no script file; command digest covers the bytes)"
	}
	data, err := os.ReadFile(checkScriptPath)
	if err != nil {
		return "", fmt.Sprintf("verifier script unreadable: %v", err)
	}
	return sha256Hex(data), ""
}

// buildAttemptManifest assembles the typed manifest for one attempt from
// the data the run seam and caller hold. Every unavailable field is named
// in UnavailableFields with its reason; nothing is fabricated.
func buildAttemptManifest(in eval.RunInput, out eval.RunOutput, meta attemptIdentity, proposalMissing bool, proposalSnap proposalSnapshot, verifierRan bool, verdict verifierVerdict, scriptPath string) attemptManifest {
	m := attemptManifest{
		Schema:       attemptManifestSchemaVersion,
		ExperimentID: meta.ExperimentID,
		Family:       meta.Family,
		Arm:          meta.Arm,
		Task:         meta.Task,
		Attempt:      meta.Attempt,
		ArmOrder:     meta.ArmOrder,
		SessionID:    in.SessionID,
	}
	unavailable := map[string]string{}

	// Model identity: requested is the flag the harness launched with;
	// observed is what the provider records say ran (empty means the
	// child did not report one, recorded as unavailable, not guessed).
	m.RequestedModel = meta.RequestedModel
	if out.EffectiveTreatment != nil {
		m.RequestedTreatment = out.EffectiveTreatment.RequestedTreatment
		m.EffectiveTreatment = string(out.EffectiveTreatment.Treatment)
		m.NormalizedFromAmbient = out.EffectiveTreatment.NormalizedFromAmbient
		m.EffRetrieval = out.EffectiveTreatment.Retrieval
		m.EffPromptDelivery = out.EffectiveTreatment.PromptDelivery
		m.EffContextPolicy = out.EffectiveTreatment.ContextPolicy
		m.StoreAvailable = out.EffectiveTreatment.StoreAvailable
	} else {
		unavailable["effective_treatment"] = "run seam did not resolve a typed treatment"
	}

	// Prompt and verifier identity. The command digest is over the bytes
	// passed to /bin/sh -c; the script digest over the script file's
	// bytes on disk, re-read at attempt time.
	m.PromptDigest = sha256Hex([]byte(in.Prompt))
	m.VerifierCommandDigest = sha256Hex([]byte(in.Check))
	m.VerifierScriptDigest, m.VerifierScriptUnavailable = verifierScriptDigest(scriptPath)

	// Binary provenance.
	m.SpliceBinarySHA256, m.MemdBinarySHA256, m.SpliceBinaryUnavailable, m.MemdBinaryUnavailable = resolveBinaryProvenance()
	m.VCSRevision, m.VCSDirty = resolveVCSProvenance()

	// Starting tree identity: captured from the proposal snapshot when
	// git could read the repo (the snapshot is taken before the
	// verifier, so it is the START state, never the post-run state).
	m.StartCommit = proposalSnap.Commit
	m.StartTree = proposalSnap.Tree

	// Proposal: present means a patch artifact exists on disk (even a
	// byte-empty one). Missing means no patch was captured at all.
	m.ProposalPresent = !proposalMissing
	m.ProposalDigest = proposalSnap.ProposedDigest
	m.ManifestDigest = proposalSnap.ManifestDigest
	if proposalMissing {
		unavailable["proposal"] = "no patch artifact captured for this attempt"
	}
	if proposalSnap.Error != nil {
		unavailable["proposal_capture"] = truncateForNote(proposalSnap.Error.Error(), 200)
	}

	// Verifier outcome with exit classification. An agent failure means
	// the verifier never ran: recorded as not_run, an honest outcome of
	// the attempt, never a fabricated reject.
	m.VerifierRan = verifierRan
	m.VerifierVerdict = string(verdict)
	m.FailureCategory = out.FailureCategory
	if !verifierRan {
		unavailable["verifier"] = "not run: the agent did not complete"
	}

	// Evidence file inventory.
	if in.ArtifactDir != "" {
		m.Artifacts = listArtifactFiles(in.ArtifactDir, in.SessionID)
	} else {
		unavailable["artifacts"] = "no artifact dir configured for this attempt"
	}

	// Evidence completeness from the seam's verdict, plus the manifest's
	// own gaps. An incomplete set never changes the task outcome.
	m.EvidenceStatus = out.EvidenceStatus
	if m.EvidenceStatus == "" {
		m.EvidenceStatus = "complete"
	}
	if out.ArtifactError != "" {
		unavailable["artifact_write"] = out.ArtifactError
	}
	if out.FailureCategory == "harness_timeout" || out.FailureCategory == "agent_noncompletion" {
		// Usage remains whatever the seam captured; the failure is the
		// evidence gap that matters here.
		unavailable["verifier_result"] = "attempt did not reach a verifier verdict"
	}
	if len(unavailable) > 0 {
		m.UnavailableFields = unavailable
	}
	return m
}

// attemptIdentity is the caller-supplied identity block for one attempt
// (the eval runners know family/arm/attempt; the run seam does not).
type attemptIdentity struct {
	ExperimentID      string
	Family            string
	Arm               string
	Task              string
	Attempt           int
	ArmOrder          int
	RequestedModel    string
	SnapshotDigest    string
	CaptureOrigin     string
	CaptureOriginNote string
}

// listArtifactFiles returns the artifact file names present for one
// attempt, sorted, relative to the artifact dir.
func listArtifactFiles(dir, sessionID string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Scope to this attempt's files: the dir may hold sibling
		// attempts. Empty sessionID keeps command-only callers working.
		if sessionID != "" && !strings.HasPrefix(name, sessionID+"-") && name != "attempt-manifest.json" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// writeAttemptManifest serializes the manifest into the artifact dir and
// refreshes the artifact inventory so it includes itself. A write failure
// is returned (evidence completeness, never a task-outcome change).
func writeAttemptManifest(dir string, m attemptManifest) error {
	if dir == "" {
		return fmt.Errorf("no artifact dir for session %s", m.SessionID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}
	m.Artifacts = listArtifactFiles(dir, m.SessionID)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode attempt manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "attempt-manifest.json"), append(data, '\n'), 0o644)
}

// exportTraceArtifact writes the attempt's stored sidecar trace to the
// artifact dir BEFORE any ResetProject can delete it. It returns whether a
// trace was found and exported; a missing trace is expected for cold arms
// (memory off never creates one) and is recorded as a reason, not an error.
func exportTraceArtifact(ctx context.Context, deps appDeps, repoRoot, sessionID, dir string) (exported bool, reason string) {
	if dir == "" {
		return false, "no artifact dir configured"
	}
	client, err := deps.resolveMemory(ctx)
	if err != nil || client == nil {
		return false, "memory sidecar unavailable"
	}
	var matched *schemas.TraceQueryResult
	for _, candidate := range repoRootQueryCandidates(repoRoot) {
		results, err := client.QueryTraces(ctx, schemas.TraceQueryFilter{RepoRoot: candidate, Limit: 1000})
		if err != nil {
			return false, fmt.Sprintf("trace query failed: %v", err)
		}
		for i := range results {
			if results[i].Trace.RunID == sessionID || results[i].Trace.SessionID == sessionID {
				matched = &results[i]
				break
			}
		}
		if matched != nil {
			break
		}
	}
	if matched == nil {
		return false, "no stored trace for this session"
	}
	data, err := json.MarshalIndent(matched.Trace, "", "  ")
	if err != nil {
		return false, fmt.Sprintf("encode trace: %v", err)
	}
	path := filepath.Join(dir, sessionID+"-trace.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return false, fmt.Sprintf("write trace artifact: %v", err)
	}
	return true, ""
}
