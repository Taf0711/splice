package main

// Cognition graph handlers (/graph/*). Each handler follows the existing
// sidecar conventions: method check, MaxBytesReader, JSON decode, Validate,
// store call, writeJSON.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Taf0711/splice/memd/store"
)

// newSemanticIndex builds the semantic index over a store. It exists because
// newServer names its store parameter "store", which shadows the package.
func newSemanticIndex(st *store.Store) *store.SemanticIndex {
	return store.NewSemanticIndex(st)
}

// toGraphNode converts a store node into its wire form. Anchors ride along so
// a client sees the exact retrieval keys the node carries.
func toGraphNode(n store.Node) graphNode {
	out := graphNode{
		ID:        n.ID,
		Kind:      n.Kind,
		Claim:     n.Claim,
		Scope:     n.Scope,
		Status:    n.Status,
		CreatedAt: n.CreatedAt,
		ClaimHash: n.ClaimHash,
	}
	if n.ProjectPath.Valid {
		v := n.ProjectPath.String
		out.ProjectPath = &v
	}
	if n.Confidence.Valid {
		v := n.Confidence.Float64
		out.Confidence = &v
	}
	if n.SourceRunID.Valid {
		v := n.SourceRunID.String
		out.SourceRunID = &v
	}
	if n.CreatedRevision.Valid {
		v := n.CreatedRevision.String
		out.CreatedRevision = &v
	}
	if n.VerifiedRevision.Valid {
		v := n.VerifiedRevision.String
		out.VerifiedRevision = &v
	}
	if n.VerifiedAt.Valid {
		v := n.VerifiedAt.Int64
		out.VerifiedAt = &v
	}
	if n.MetadataJSON.Valid {
		v := n.MetadataJSON.String
		out.MetadataJSON = &v
	}
	return out
}

// handleGraphUpsert creates or updates one node with its anchors and edges in
// a single call, then indexes the node into the semantic index.
func (s *server) handleGraphUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}

	in := store.NodeInput{
		Kind:             req.Kind,
		Claim:            req.Claim,
		Scope:            req.Scope,
		ProjectPath:      req.ProjectPath,
		Status:           req.Status,
		SourceRunID:      req.SourceRunID,
		CreatedRevision:  req.CreatedRevision,
		VerifiedRevision: req.VerifiedRevision,
		Metadata:         req.Metadata,
	}
	if req.Confidence != nil {
		in.Confidence = *req.Confidence
		in.ConfidenceValid = true
	}
	for _, a := range req.Anchors {
		in.Anchors = append(in.Anchors, store.AnchorInput{Kind: a.Kind, Value: a.Value})
	}
	for _, e := range req.Edges {
		in.Edges = append(in.Edges, store.EdgeInput{DstID: e.DstID, Kind: e.Kind})
	}
	for _, ev := range req.Evidence {
		in.Evidence = append(in.Evidence, store.EvidenceInput{Kind: ev.Kind, Ref: ev.Ref, Detail: ev.Detail})
	}

	node, err := s.store.UpsertNode(r.Context(), in)
	if err != nil {
		writeGraphError(w, err)
		return
	}

	// Index the node text into the semantic index. The claim plus anchor
	// values form the indexed text. A failed index write is an internal
	// error: the node is stored, but search would silently miss it, which
	// the fail-loud rule forbids.
	text := node.Claim
	for _, a := range req.Anchors {
		text += " " + a.Value
	}
	if err := s.semantic.IndexNode(r.Context(), node.ID, text); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := graphUpsertResponse{OK: true, Node: toGraphNode(node)}
	writeJSON(w, http.StatusOK, resp)
}

// handleGraphExact returns active nodes carrying all requested anchors.
func (s *server) handleGraphExact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphExactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	nodes, err := s.store.GetExact(r.Context(), req.Anchors, store.GetExactOptions{
		ProjectPath: req.ProjectPath,
		Status:      req.Status,
		Limit:       req.Limit,
	})
	if err != nil {
		writeGraphError(w, err)
		return
	}
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	anchors, aerr := s.store.AnchorsFor(r.Context(), ids)
	if aerr != nil {
		writeGraphError(w, aerr)
		return
	}
	out := make([]graphNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, withAnchors(toGraphNode(n), anchors[n.ID]))
	}
	writeJSON(w, http.StatusOK, graphExactResponse{OK: true, Nodes: out})
}

// handleGraphNeighbors walks a bounded BFS from one node.
func (s *server) handleGraphNeighbors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphNeighborsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	kinds := make([]store.EdgeKindFilter, 0, len(req.Kinds))
	for _, k := range req.Kinds {
		kinds = append(kinds, store.EdgeKindFilter(k))
	}
	nodes, edges, err := s.store.Neighbors(r.Context(), req.NodeID, kinds, req.Depth, req.Limit)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	outNodes := make([]graphNode, 0, len(nodes))
	for _, n := range nodes {
		outNodes = append(outNodes, toGraphNode(n))
	}
	outEdges := make([]graphEdge, 0, len(edges))
	for _, e := range edges {
		outEdges = append(outEdges, graphEdge{DstID: e.DstID, Kind: e.Kind})
	}
	writeJSON(w, http.StatusOK, graphNeighborsResponse{OK: true, Nodes: outNodes, Edges: outEdges})
}

// handleGraphStatus moves one node to a new status.
func (s *server) handleGraphStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	if !store.ValidNodeStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "validation: unknown node status "+req.Status)
		return
	}
	if err := s.store.SetStatus(r.Context(), req.NodeID, req.Status); err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, genericResponse{OK: true})
}

// handleGraphContradict marks one node contradicted by another node.
func (s *server) handleGraphContradict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphContradictRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	in := store.EvidenceInput{Kind: req.Kind, Ref: req.Ref, Detail: req.Detail}
	if err := s.store.Contradict(r.Context(), req.NodeID, req.ByNodeID, in); err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, genericResponse{OK: true})
}

// handleGraphSearchSemantic ranks active nodes by cosine similarity to text.
func (s *server) handleGraphSearchSemantic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	hits, err := s.semantic.Search(r.Context(), req.Text, req.K, req.ProjectPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids := make([]int64, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.NodeID)
	}
	byID := map[int64]store.Node{}
	if len(ids) > 0 {
		nodes, gerr := s.store.GetByIDs(r.Context(), ids, "")
		if gerr != nil {
			writeError(w, http.StatusInternalServerError, gerr.Error())
			return
		}
		for _, n := range nodes {
			byID[n.ID] = n
		}
		anchors, aerr := s.store.AnchorsFor(r.Context(), ids)
		if aerr != nil {
			writeError(w, http.StatusInternalServerError, aerr.Error())
			return
		}
		out := make([]graphSearchHit, 0, len(hits))
		for _, h := range hits {
			hit := graphSearchHit{NodeID: h.NodeID, Score: h.Score}
			if n, ok := byID[h.NodeID]; ok {
				node := withAnchors(toGraphNode(n), anchors[n.ID])
				hit.Node = &node
			}
			out = append(out, hit)
		}
		writeJSON(w, http.StatusOK, graphSearchResponse{OK: true, Hits: out})
		return
	}
	out := make([]graphSearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, graphSearchHit{NodeID: h.NodeID, Score: h.Score})
	}
	writeJSON(w, http.StatusOK, graphSearchResponse{OK: true, Hits: out})
}

// withAnchors attaches the store's anchor rows to a wire node.
func withAnchors(n graphNode, anchors []store.Anchor) graphNode {
	if len(anchors) == 0 {
		return n
	}
	n.Anchors = make([]graphAnchor, 0, len(anchors))
	for _, a := range anchors {
		n.Anchors = append(n.Anchors, graphAnchor{Kind: a.Kind, Value: a.Value})
	}
	return n
}

// handleGraphReanchor advances the verified revision of a project's active
// nodes (POST /graph/reanchor). See store.Reanchor for the contract.
func (s *server) handleGraphReanchor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphReanchorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	n, err := s.store.Reanchor(r.Context(), req.ProjectPath, req.FromRevision, req.ToRevision)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, graphReanchorResponse{OK: true, Nodes: n})
}

// handleGraphReanchorIDs advances exactly the given capture set
// (POST /graph/reanchor_ids). See store.ReanchorByIDs.
func (s *server) handleGraphReanchorIDs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphReanchorIDsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	n, err := s.store.ReanchorByIDs(r.Context(), req.ProjectPath, req.NodeIDs, req.FromRevision, req.ToRevision)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, graphReanchorResponse{OK: true, Nodes: n})
}

// handleGraphCaptureSet lists a project's active node ids anchored at a
// revision (POST /graph/capture_set) - the capture set of one verified run.
// An optional source_run_id scopes the set to the producer run that
// persisted the nodes; omitting it preserves the historical
// project+revision behavior.
func (s *server) handleGraphCaptureSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphCaptureSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	sourceRunID := ""
	if req.SourceRunID != nil {
		sourceRunID = *req.SourceRunID
	}
	ids, err := s.store.CaptureSetIDs(r.Context(), req.ProjectPath, req.Revision, sourceRunID)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, graphCaptureSetResponse{OK: true, IDs: ids})
}

// handleGraphExportCaptureSet returns the FULL payloads of a capture set
// (POST /graph/export_capture_set): complete nodes with anchors and
// evidence, ready to persist elsewhere. The existing /graph/capture_set
// returns only ids, which is not enough to export a capture set's content;
// this endpoint is the export side of the A4 natural-capture contract.
// Producer identity (source_run_id, revisions, claim text, evidence) is
// preserved verbatim; only the caller's later import remaps project
// identity.
func (s *server) handleGraphExportCaptureSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphCaptureSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	sourceRunID := ""
	if req.SourceRunID != nil {
		sourceRunID = *req.SourceRunID
	}
	ids, err := s.store.CaptureSetIDs(r.Context(), req.ProjectPath, req.Revision, sourceRunID)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	nodes, err := s.store.GetByIDs(r.Context(), ids, req.ProjectPath)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	anchorMap, err := s.store.AnchorsFor(r.Context(), ids)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	evidenceMap, err := s.store.EvidenceFor(r.Context(), ids)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	out := make([]exportedCaptureNode, 0, len(nodes))
	for _, n := range nodes {
		exported := exportedCaptureNode{
			Node:      toGraphNode(n),
			Anchors:   make([]graphAnchor, 0),
			Evidence:  make([]graphEvidence, 0),
			SourceID:  n.ID,
			ClaimHash: n.ClaimHash,
		}
		for _, a := range anchorMap[n.ID] {
			exported.Anchors = append(exported.Anchors, graphAnchor{Kind: a.Kind, Value: a.Value})
		}
		for _, e := range evidenceMap[n.ID] {
			exported.Evidence = append(exported.Evidence, graphEvidence{Kind: e.Kind, Ref: nullStringValue(e.Ref), Detail: nullStringValue(e.Detail)})
		}
		out = append(out, exported)
	}
	writeJSON(w, http.StatusOK, graphExportCaptureSetResponse{OK: true, Nodes: out})
}

// handleGraphImportCaptureSet persists a previously exported capture set
// (POST /graph/import_capture_set). Project identity is REMAPPED to the
// request's project_path; producer identity (source_run_id, revisions,
// claims, anchors, evidence) is preserved verbatim. Sidecar numeric ids
// differ between source and import, so canonical identity is the
// (kind, claim_hash) pair every node carries. Each node is upserted
// individually; a failure mid-set is reported with the count persisted so
// far (the upsert dedupe makes a retry idempotent).
func (s *server) handleGraphImportCaptureSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var req graphImportCaptureSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	imported := int64(0)
	for _, n := range req.Nodes {
		in := store.NodeInput{
			Kind:             n.Node.Kind,
			Claim:            n.Node.Claim,
			Scope:            n.Node.Scope,
			ProjectPath:      req.ProjectPath,
			Status:           n.Node.Status,
			SourceRunID:      deref(n.Node.SourceRunID),
			CreatedRevision:  deref(n.Node.CreatedRevision),
			VerifiedRevision: deref(n.Node.VerifiedRevision),
		}
		// Exported provenance metadata round-trips verbatim: it is the
		// node's identity evidence, not new claims.
		if n.Node.MetadataJSON != nil && *n.Node.MetadataJSON != "" {
			var meta map[string]any
			if err := json.Unmarshal([]byte(*n.Node.MetadataJSON), &meta); err == nil && len(meta) > 0 {
				in.Metadata = meta
			}
		}
		if n.Node.Confidence != nil {
			in.Confidence = *n.Node.Confidence
			in.ConfidenceValid = true
		}
		for _, a := range n.Anchors {
			in.Anchors = append(in.Anchors, store.AnchorInput{Kind: a.Kind, Value: a.Value})
		}
		for _, e := range n.Evidence {
			in.Evidence = append(in.Evidence, store.EvidenceInput{Kind: e.Kind, Ref: e.Ref, Detail: e.Detail})
		}
		node, err := s.store.UpsertNode(r.Context(), in)
		if err != nil {
			writeGraphError(w, err)
			return
		}
		if err := s.semantic.IndexNode(r.Context(), node.ID, node.Claim); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		imported++
	}
	writeJSON(w, http.StatusOK, graphImportCaptureSetResponse{OK: true, Imported: imported})
}

// deref is a small helper for optional string fields on import.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nullStringValue unwraps a nullable store string for the wire form.
func nullStringValue(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

// handleGraphCompact merges duplicate nodes and reports what it did.
func (s *server) handleGraphCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	report, err := s.store.Compact(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, graphCompactResponse{OK: true, Report: report})
}

// handleGraphCollect hard-deletes stale unreferenced ephemeral nodes.
func (s *server) handleGraphCollect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req graphCollectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	n, err := s.store.Collect(r.Context(), time.Duration(req.OlderThan)*time.Second)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, graphCollectResponse{OK: true, Collected: n})
}

// writeGraphError maps store sentinel errors to HTTP statuses so a missing
// node is a 404 and a bad enum value is a 400, never a generic 500.
func writeGraphError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
