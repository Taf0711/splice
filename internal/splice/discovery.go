package splice

import (
	"context"
	"fmt"
	"strings"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Track C integration: cognition-driven discovery planning. The planner
// resolves task questions from the cognition graph (exact anchor retrieval
// first) so the stage executes only the UNRESOLVED part of its context
// needs. It never runs a model call; everything here is deterministic host
// work over the sidecar graph.

// DiscoveryPlan records which parts of a stage's context need are already
// answered and which require discovery. It is the unit the telemetry
// counters measure: resolved_by_cognition items are work the run skipped.
type DiscoveryPlan struct {
	// ResolvedByTask lists questions the task prompt itself answers.
	ResolvedByTask []string `json:"resolved_by_task,omitempty"`
	// ResolvedByCognition lists questions a cognition node answers, paired
	// with the node id that answered them for explainability.
	ResolvedByCognition []ResolvedQuestion `json:"resolved_by_cognition,omitempty"`
	// Unresolved lists questions requiring discovery (reads/searches).
	Unresolved []string `json:"unresolved,omitempty"`
}

// ResolvedQuestion pairs a resolved question with its cognition source.
type ResolvedQuestion struct {
	Question string `json:"question"`
	NodeID   int64  `json:"node_id"`
	NodeKind string `json:"node_kind"`
	Claim    string `json:"claim"`
}

// discoveryQuestion describes one thing a stage would otherwise discover by
// reading or searching the repository.
type discoveryQuestion struct {
	question    string
	anchorKind  string
	anchorValue string
}

// planDiscovery builds a DiscoveryPlan for a stage: it checks the task's
// structural hints against the cognition graph via exact anchor retrieval,
// and classifies each question as resolved-by-cognition or unresolved.
// client may be nil (cognition off): everything stays unresolved and the
// plan is still returned so callers emit consistent telemetry.
func planDiscovery(ctx context.Context, client *memd.Client, projectPath, intent string, questions []discoveryQuestion) DiscoveryPlan {
	plan := DiscoveryPlan{}
	if len(questions) == 0 {
		return plan
	}
	hints := structuralHintsFromIntent(intent)
	hintSet := make(map[string]struct{}, len(hints))
	for _, h := range hints {
		hintSet[strings.ToLower(h)] = struct{}{}
	}

	for _, q := range questions {
		if _, known := hintSet[strings.ToLower(q.anchorValue)]; known {
			plan.ResolvedByTask = append(plan.ResolvedByTask, q.question)
			continue
		}
		if client == nil {
			plan.Unresolved = append(plan.Unresolved, q.question)
			continue
		}
		nodes, err := client.GetExactNodes(ctx, map[string][]string{q.anchorKind: {q.anchorValue}}, projectPath, 4)
		if err != nil || len(nodes) == 0 {
			plan.Unresolved = append(plan.Unresolved, q.question)
			continue
		}
		node := nodes[0]
		plan.ResolvedByCognition = append(plan.ResolvedByCognition, ResolvedQuestion{
			Question: q.question,
			NodeID:   node.ID,
			NodeKind: node.Kind,
			Claim:    node.Claim,
		})
	}
	plan.ResolvedByTask = hints
	return plan
}

// structuralHintsFromIntent extracts explicit structural hints the task
// prompt already names: file paths (word.foo.go tokens) and CamelCase
// identifiers. Deterministic token scan, no model involvement.
func structuralHintsFromIntent(intent string) []string {
	var hints []string
	seen := map[string]struct{}{}
	fields := strings.FieldsFunc(intent, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '/')
	})
	for _, f := range fields {
		f = strings.Trim(f, "./_")
		if len(f) < 4 {
			continue
		}
		isPath := strings.Contains(f, ".go") || strings.Contains(f, "/")
		isIdent := strings.ContainsFunc(f, func(r rune) bool { return r >= 'A' && r <= 'Z' })
		if !isPath && !isIdent {
			continue
		}
		key := strings.ToLower(f)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		hints = append(hints, f)
		if len(hints) >= 8 {
			break
		}
	}
	return hints
}

// cognitionBundleFromNodes converts graph nodes into memory observations
// delivered to a stage through the existing MemoryBundle channel. Only
// ACTIVE nodes are converted; freshness validation happens upstream. Facts
// stay one line each: typed title plus the node claim.
func cognitionBundleFromNodes(nodes []memd.GraphNode) []schemas.MemoryObservation {
	out := make([]schemas.MemoryObservation, 0, len(nodes))
	for _, n := range nodes {
		if n.Status != "active" {
			continue
		}
		project := ""
		if n.ProjectPath != nil {
			project = *n.ProjectPath
		}
		id := n.ID
		obs := schemas.MemoryObservation{
			ID:          id,
			ProjectPath: &project,
			Scope:       schemas.MemoryScopeProject,
			OwnerAgent:  "cognition_graph",
			Visibility:  "shareable",
			MemoryType:  nodeKindToMemoryType(n.Kind),
			Title:       nodeTitle(n),
			Content:     n.Claim,
		}
		if n.VerifiedRevision != nil && *n.VerifiedRevision != "" {
			rev := *n.VerifiedRevision
			obs.SourceCommit = &rev
		}
		if n.VerifiedAt != nil {
			obs.UpdatedAt = *n.VerifiedAt
		}
		out = append(out, obs)
	}
	return out
}

func nodeKindToMemoryType(kind string) string {
	switch kind {
	case "failure":
		return "failure"
	case "procedure":
		return "procedure"
	case "decision":
		return "decision"
	case "evidence":
		return "evidence"
	default:
		return "pattern"
	}
}

func nodeTitle(n memd.GraphNode) string {
	kind := strings.ToUpper(strings.TrimSpace(n.Kind))
	if kind == "" {
		kind = "COGNITION"
	}
	return fmt.Sprintf("[%s] %s", kind, firstLine(n.Claim))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return strings.TrimSpace(s)
}

// GraphCapture is one verified cognition node derived from a completed run,
// ready to persist through the sidecar. Capture is evidence-gated: the run
// must have completed verification before a FACT node is proposed.
type GraphCapture struct {
	Kind     string
	Claim    string
	Project  string
	RunID    string
	Revision string
	Anchors  []memd.GraphAnchor
	Evidence []memd.GraphEvidence
}

// GraphEvidence is one deterministic support record for a capture. It is an
// alias of the memd client's wire type so capture callers need no imports
// beyond internal/memd.

// captureFromVerifiedRun derives candidate cognition from a completed
// pipeline run's deterministic artifacts: a verified procedure (the test
// command that passed) and per-file modification facts. It never captures
// model prose as FACT, and produces nothing when verification did not
// complete.
func captureFromVerifiedRun(projectPath, outcomeStatus string, changedFiles []string, testCommand, revision, runID string) []GraphCapture {
	if outcomeStatus != "completed" {
		return nil
	}
	var captures []GraphCapture
	if testCommand != "" {
		captures = append(captures, GraphCapture{
			Kind:     "procedure",
			Claim:    fmt.Sprintf("Verification passes with: %s", testCommand),
			Project:  projectPath,
			RunID:    runID,
			Revision: revision,
			Anchors:  []memd.GraphAnchor{{Kind: "command", Value: testCommand}},
			Evidence: []memd.GraphEvidence{{Kind: "test_run", Ref: runID, Detail: "test command exited 0"}},
		})
	}
	for _, file := range changedFiles {
		if file == "" {
			continue
		}
		captures = append(captures, GraphCapture{
			Kind:     "fact",
			Claim:    fmt.Sprintf("%s was modified by a verified run at revision %s", file, shortRev(revision)),
			Project:  projectPath,
			RunID:    runID,
			Revision: revision,
			Anchors:  []memd.GraphAnchor{{Kind: "file", Value: file}},
			Evidence: []memd.GraphEvidence{{Kind: "git", Ref: revision, Detail: "verified run changed this file"}},
		})
	}
	return captures
}

// persistGraphCapture upserts one capture with its anchors and evidence via
// the sidecar client. Best-effort at the call site: a sidecar failure must
// degrade the run to cold, never fail it.
func persistGraphCapture(ctx context.Context, client *memd.Client, c GraphCapture) (int64, error) {
	if client == nil {
		return 0, nil
	}
	node, err := client.UpsertGraphNode(ctx, memd.GraphUpsertInput{
		Kind:             c.Kind,
		Claim:            c.Claim,
		Scope:            "project",
		ProjectPath:      c.Project,
		Status:           "active",
		SourceRunID:      c.RunID,
		VerifiedRevision: c.Revision,
		Anchors:          c.Anchors,
		Evidence:         c.Evidence,
	})
	if err != nil {
		return 0, err
	}
	return node.ID, nil
}

func shortRev(rev string) string {
	if len(rev) > 10 {
		return rev[:10]
	}
	return rev
}
