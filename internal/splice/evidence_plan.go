package splice

// Production evidence substitution: one narrow, exact reuse case over the
// existing E1-E4 typed plan machinery. This file does not add a second
// memory subsystem; it connects the records that discovery already admitted
// to a concrete context request so the live executor can omit one redundant
// operation. The offline slice remains test infrastructure.

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// EvidencePlan is the per-invocation production decision: the improved cold
// plan, the warm plan after admitted substitutions, the concrete operation
// difference, and the accepted/rejected typed admission results.
type EvidencePlan struct {
	Cold     ColdPlan
	Warm     WarmPlan
	Diff     planDifferences
	Admitted []AdmittedResolution
	Rejected []AdmittedResolution
	Needs    []ContextNeed
}

// SubstitutionCount reports how many operations an admitted record replaced.
func (p *EvidencePlan) SubstitutionCount() int {
	if p == nil {
		return 0
	}
	return len(p.Warm.Substitutions)
}

// EvidenceRequestFromPlan builds the concrete host ContextRequest for a warm
// operation plan. Only operations remaining after substitution are issued;
// eliminated operations never appear in the request. The request keeps the
// list operation only when the plan retained it, so the global listing is
// dropped only with an explicit structural decision.
func EvidenceRequestFromOperations(ops []ColdOperation, reason string) schemas.ContextRequest {
	queries := make([]schemas.ContextQuery, 0, len(ops))
	for _, op := range ops {
		switch op.Kind {
		case "list":
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextListFiles,
				MaxResults: 100,
				MaxChars:   10000,
			})
		case "read":
			if op.Subject == "" {
				continue
			}
			path := op.Subject
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextReadFile,
				Path:       &path,
				MaxResults: 10,
				MaxChars:   5000,
			})
		case "symbol":
			if op.Subject == "" {
				continue
			}
			symbol := op.Subject
			var path *string
			if idx := strings.Index(symbol, "#"); idx > 0 {
				p := symbol[:idx]
				path = &p
			}
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextGetSymbol,
				Path:       path,
				Symbol:     &symbol,
				MaxResults: 10,
				MaxChars:   4000,
			})
		case "search":
			if op.Subject == "" {
				continue
			}
			pattern := op.Subject
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextSearch,
				Pattern:    &pattern,
				MaxResults: 20,
				MaxChars:   4000,
			})
		}
	}
	return schemas.ContextRequest{Reason: reason, Queries: queries}
}

// graphClientFromMemory returns the sidecar graph client when the store
// exposes one. A plain store returns nil, and the caller keeps the ordinary
// discovery path byte-for-byte.
func graphClientFromMemory(mem MemoryStore) *memd.Client {
	type graphProvider interface{ GraphClient() *memd.Client }
	provider, ok := mem.(graphProvider)
	if !ok || provider == nil {
		return nil
	}
	return provider.GraphClient()
}

// buildEvidencePlan maps the already-admitted discovery nodes to typed reuse
// records, runs the existing E3 admission checks for the derived needs, and
// applies the existing E4 transformation. It never reads or mutates the
// workspace directly beyond the admission digest checks.
func buildEvidencePlan(intent, workspace string, priorFiles []string, nodes []memd.GraphNode) *EvidencePlan {
	if strings.TrimSpace(workspace) == "" {
		return nil
	}
	needs := deriveContextNeeds(intent, workspace, priorFiles, nil)
	cold := buildColdPlan(intent, workspace, priorFiles, 8)
	candidates := recordsFromDiscoveryNodes(nodes, needs)
	admitted, rejected := admitCandidates(candidates, needs, admissionContext{Workspace: workspace})
	warm := buildWarmPlan(cold, admitted, needs)
	return &EvidencePlan{
		Cold:     cold,
		Warm:     warm,
		Diff:     diffPlans(cold, warm),
		Admitted: admitted,
		Rejected: rejected,
		Needs:    needs,
	}
}

// recordsFromDiscoveryNodes pairs each fresh graph node that carries a typed
// reuse record with the first derived need it addresses by subject. Nodes
// without a valid record stay hints and never authorize substitution.
func recordsFromDiscoveryNodes(nodes []memd.GraphNode, needs []ContextNeed) []nodeWithRecord {
	out := make([]nodeWithRecord, 0, len(nodes))
	for _, node := range nodes {
		if node.MetadataJSON == nil || strings.TrimSpace(*node.MetadataJSON) == "" {
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(*node.MetadataJSON), &meta); err != nil {
			continue
		}
		rec := parseReuseRecord(meta)
		if rec == nil {
			continue
		}
		for _, need := range needs {
			if need.Kind == NeedOpenDiscovery {
				continue
			}
			if !recordSpeaksOfSubject(rec, need.Subject) {
				continue
			}
			out = append(out, nodeWithRecord{NodeID: node.ID, NeedID: need.ID, Record: rec})
		}
	}
	return out
}

// priorChangedFilesForEvidence flattens the structured prior changed-file
// record into the deterministic ordered list E1 derivation expects.
func priorChangedFilesForEvidence(files map[string][]string) []string {
	if len(files) == 0 {
		return nil
	}
	stages := make([]string, 0, len(files))
	for stage := range files {
		stages = append(stages, stage)
	}
	sort.Strings(stages)
	seen := map[string]bool{}
	var out []string
	for _, stage := range stages {
		for _, path := range files[stage] {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}
