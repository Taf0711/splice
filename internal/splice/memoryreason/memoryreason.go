// Package memoryreason implements the deterministic reasoning-memory
// contract shared by the orchestrator and the reasoning-stage adapters:
// admission policy over retrieved memory, stable audit IDs, bounded
// selection for stage input, and non-blocking reconciliation of model
// disposition claims. It imports only schemas plus the standard library.
package memoryreason

import (
	"path/filepath"
	"strconv"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// Admission caps mirror the retrieval limits: five observations per query
// and three exemplars from the retriever.
const (
	MaxObservations = 5
	MaxExemplars    = 3

	// MaxObservationRunes is the existing content bound, applied after
	// admission so rejected items never pay the truncation cost.
	MaxObservationRunes = 500
)

// AdmissionCounts aggregates why items were excluded. Counts are metadata:
// excluded content and IDs never enter a stage prompt or a trace review.
type AdmissionCounts struct {
	Invalid      int
	ReviewDue    int
	WrongProject int
	Duplicate    int
	OverLimit    int
}

// AdmissionResult contains only prompt-eligible evidence in incoming order.
type AdmissionResult struct {
	Bundle   *schemas.MemoryBundle
	Rejected AdmissionCounts
}

// StableID returns the auditable identity for one observation.
func StableID(obs schemas.MemoryObservation) string {
	if obs.ID <= 0 {
		return ""
	}
	return "observation:" + strconv.FormatInt(obs.ID, 10)
}

// Admit applies deterministic metadata policy to a retrieved bundle and
// returns the admitted subset plus exclusion counts. Given the same bundle,
// project root, and nowUnix, the output is identical. It never consults
// model output and never sees raw traces.
func Admit(bundle *schemas.MemoryBundle, projectRoot string, nowUnix int64) AdmissionResult {
	result := AdmissionResult{}
	if bundle == nil {
		return result
	}
	admitted := &schemas.MemoryBundle{
		RequestingAgent: bundle.RequestingAgent,
	}

	seenObs := make(map[string]bool, len(bundle.Observations))
	for _, obs := range bundle.Observations {
		switch {
		case obs.Validate() != nil || obs.ID <= 0 || obs.DeletedAt != nil:
			result.Rejected.Invalid++
			continue
		case obs.ReviewAfter != nil && *obs.ReviewAfter <= nowUnix:
			result.Rejected.ReviewDue++
			continue
		case obs.Scope == schemas.MemoryScopeProject:
			if obs.ProjectPath == nil || filepath.Clean(*obs.ProjectPath) != filepath.Clean(projectRoot) {
				result.Rejected.WrongProject++
				continue
			}
		}
		id := StableID(obs)
		if seenObs[id] {
			result.Rejected.Duplicate++
			continue
		}
		if len(admitted.Observations) >= MaxObservations {
			result.Rejected.OverLimit++
			continue
		}
		seenObs[id] = true
		obs.Content = truncateRunes(obs.Content, MaxObservationRunes)
		admitted.Observations = append(admitted.Observations, obs)
	}

	seenEx := make(map[string]bool, len(bundle.Exemplars))
	for _, ex := range bundle.Exemplars {
		if ex.Validate() != nil || ex.RunID == "" {
			result.Rejected.Invalid++
			continue
		}
		id := "exemplar:" + ex.RunID
		if seenEx[id] {
			result.Rejected.Duplicate++
			continue
		}
		if len(admitted.Exemplars) >= MaxExemplars {
			result.Rejected.OverLimit++
			continue
		}
		seenEx[id] = true
		admitted.Exemplars = append(admitted.Exemplars, ex)
	}

	if len(admitted.Observations) == 0 && len(admitted.Exemplars) == 0 {
		return result
	}
	result.Bundle = admitted
	return result
}

// Select converts an admitted bundle into the exact bounded items delivered
// to a reasoning stage. Every item carries its stable audit ID so the model
// can dispose each one by ID and the trace can prove what was delivered.
// Nil or empty bundles select nothing.
func Select(bundle *schemas.MemoryBundle) []schemas.SelectedMemory {
	if bundle == nil || (len(bundle.Observations) == 0 && len(bundle.Exemplars) == 0) {
		return nil
	}
	items := make([]schemas.SelectedMemory, 0, len(bundle.Observations)+len(bundle.Exemplars))
	for _, obs := range bundle.Observations {
		item := schemas.SelectedMemory{
			ID:         StableID(obs),
			Title:      obs.Title,
			Content:    obs.Content,
			MemoryType: obs.MemoryType,
			Scope:      obs.Scope,
		}
		if obs.SourceStage != nil {
			item.SourceStage = *obs.SourceStage
		}
		if obs.SourceCommit != nil {
			item.SourceCommit = *obs.SourceCommit
		}
		items = append(items, item)
	}
	for _, ex := range bundle.Exemplars {
		items = append(items, schemas.SelectedMemory{
			ID:         "exemplar:" + ex.RunID,
			Title:      "exemplar run " + ex.RunID,
			Content:    ex.Content,
			MemoryType: schemas.MemoryScopeExemplar,
			Scope:      schemas.MemoryScopeExemplar,
			RunID:      ex.RunID,
		})
	}
	return items
}

// Reconcile converts model claims into one complete, ordered review of the
// delivered items. It never fails: malformed claims, unknown IDs, and
// duplicates increment InvalidClaims (alongside parseIssues) and missing
// delivered IDs become unreported/missing synthesized entries. Delivered
// order is preserved; unknown claimed IDs are never persisted. An empty
// delivered set yields no review at all.
func Reconcile(delivered []schemas.SelectedMemory, claims []schemas.MemoryDisposition, parseIssues int) *schemas.MemoryReview {
	if len(delivered) == 0 {
		return nil
	}
	review := &schemas.MemoryReview{InvalidClaims: parseIssues}
	firstValid := make(map[string]schemas.MemoryDisposition, len(delivered))
	for _, claim := range claims {
		id := claim.MemoryID
		known := false
		for _, item := range delivered {
			if item.ID == id {
				known = true
				break
			}
		}
		if !known {
			review.InvalidClaims++
			continue
		}
		if _, dup := firstValid[id]; dup {
			review.InvalidClaims++
			continue
		}
		if claim.Validate() != nil {
			review.InvalidClaims++
			continue
		}
		firstValid[id] = claim
	}
	for _, item := range delivered {
		if claim, ok := firstValid[item.ID]; ok {
			review.Items = append(review.Items, claim)
			continue
		}
		review.Items = append(review.Items, schemas.MemoryDisposition{
			MemoryID: item.ID,
			Action:   schemas.MemoryActionUnreported,
			Reason:   schemas.MemoryReasonMissing,
		})
	}
	return review
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
