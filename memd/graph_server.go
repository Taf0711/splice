package main

// Cognition graph handlers (/graph/*). Each handler follows the existing
// sidecar conventions: method check, MaxBytesReader, JSON decode, Validate,
// store call, writeJSON.

import (
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
