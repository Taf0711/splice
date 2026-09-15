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

// PrefetchQueries returns the bounded source queries the substituted
// subjects authorize: the current source of each located dependency,
// fetched through the guarded context tools so the model receives the
// declaration body instead of a location claim. The handoff names this
// delivery honestly: source acquired earlier because of retained evidence,
// not an eliminated discovery operation. Queries deduplicate against the
// remaining plan operations (a subject the plan still fetches needs no
// second query) and against each other.
func (p *EvidencePlan) PrefetchQueries() []schemas.ContextQuery {
	if p == nil {
		return nil
	}
	needByID := make(map[string]ContextNeed, len(p.Needs))
	for _, n := range p.Needs {
		needByID[n.ID] = n
	}
	// The remaining ops the request already carries: a subject fetched
	// there needs no prefetch copy.
	already := map[string]bool{}
	for _, op := range p.Warm.Operations {
		already[op.Subject] = true
	}
	seen := map[string]bool{}
	queries := make([]schemas.ContextQuery, 0, len(p.Warm.Substitutions))
	for _, sub := range p.Warm.Substitutions {
		need, ok := needByID[sub.NeedID]
		if !ok || already[need.Subject] || seen[need.Subject] {
			continue
		}
		file, symbol := splitSubject(need.Subject)
		seen[need.Subject] = true
		if symbol != "" {
			sym := need.Subject
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextGetSymbol,
				Path:       &file,
				Symbol:     &sym,
				MaxResults: 10,
				MaxChars:   4000,
			})
			continue
		}
		if file != "" {
			queries = append(queries, schemas.ContextQuery{
				QueryType:  schemas.ContextReadFile,
				Path:       &file,
				MaxResults: 10,
				MaxChars:   5000,
			})
		}
	}
	return queries
}

// SourceFetch is one memory-assisted dependency prefetch decision: the
// host will fetch this file's current bounded view into the initial
// context handshake because a retained verified record names it. This is
// NOT a substitution: no operation is removed, the source is acquired
// earlier, and the handoff's tracking separates the two.
type SourceFetch struct {
	// Path is the repo-relative file the record vouches for.
	Path string `json:"path"`
	// Reason is the record's own claim line, bounded, so the delivery is
	// explainable after the fact.
	Reason string `json:"reason"`
	// RecordRef and ContentVersion identify the retained record that
	// authorized the fetch (producer, kind, content version).
	RecordRef      string `json:"record_ref"`
	ContentVersion string `json:"content_version"`
}

// maxMemoryPrefetchFiles bounds the prefetch: one dependency group per
// invocation, per the measurement design's conservative default.
const maxMemoryPrefetchFiles = 2

// MemoryPrefetchFromNodes derives the prefetch decisions from the delivered
// fresh records' file anchors. Only records the retrieval stage already
// selected and freshness-validated are candidates, the same records the
// ordinary delivery path would hand to the model as prose; the prefetch
// adds the CURRENT source those anchors name. Deduplicated by path and
// capped, because the selection rule is uncalibrated.
func MemoryPrefetchFromNodes(nodes []memd.GraphNode) []SourceFetch {
	seen := map[string]bool{}
	var out []SourceFetch
	for _, n := range nodes {
		if len(out) >= maxMemoryPrefetchFiles {
			break
		}
		for _, a := range n.Anchors {
			if len(out) >= maxMemoryPrefetchFiles {
				break
			}
			if a.Kind != "file" || strings.TrimSpace(a.Value) == "" || seen[a.Value] {
				continue
			}
			seen[a.Value] = true
			reason := n.Claim
			if len(reason) > 200 {
				reason = reason[:200]
			}
			ref := n.Kind
			if n.SourceRunID != nil {
				ref = n.Kind + ":" + *n.SourceRunID
			}
			version := ""
			if n.VerifiedRevision != nil {
				version = *n.VerifiedRevision
			}
			out = append(out, SourceFetch{
				Path:           a.Value,
				Reason:         reason,
				RecordRef:      ref,
				ContentVersion: version,
			})
		}
	}
	return out
}

// PrefetchQueriesFor converts prefetch decisions into the bounded context
// queries the handshake fulfills. Bounded read_file views: the declaration
// body plus its neighbors, which is what the caller needs to USE the
// dependency (a function name alone is not enough when the caller must
// construct its argument types).
func PrefetchQueriesFor(fetches []SourceFetch) []schemas.ContextQuery {
	queries := make([]schemas.ContextQuery, 0, len(fetches))
	for _, f := range fetches {
		path := f.Path
		queries = append(queries, schemas.ContextQuery{
			QueryType:  schemas.ContextReadFile,
			Path:       &path,
			MaxResults: 10,
			MaxChars:   5000,
		})
	}
	return queries
}

// EvidenceRequestFromOperations builds the concrete host ContextRequest for
// a warm operation plan. Only operations remaining after substitution are
// issued; eliminated operations never appear in the request. The request
// keeps the list operation only when the plan retained it, so the global
// listing is dropped only with an explicit structural decision.
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

// vouchedFilesFromNodes collects the file anchors of the delivered discovery
// nodes. A file anchor means the record's freshness was proven against that
// file at the current verified revision, so the file is evidence the
// pipeline itself validated. Only such files may confirm task-text
// identifiers the production index cannot, which keeps the trap-test guard
// intact: a planted decoy test file is never vouched.
func vouchedFilesFromNodes(nodes []memd.GraphNode) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range nodes {
		for _, a := range n.Anchors {
			if a.Kind != "file" || strings.TrimSpace(a.Value) == "" || seen[a.Value] {
				continue
			}
			seen[a.Value] = true
			out = append(out, a.Value)
		}
	}
	sort.Strings(out)
	return out
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
