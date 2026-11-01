package presentation

import (
	"fmt"
	"math"
	"strings"
)

// EvidenceStatus is the closed set of evidence group statuses.
type EvidenceStatus string

const (
	EvidencePending    EvidenceStatus = "pending"
	EvidencePassed     EvidenceStatus = "passed"
	EvidenceFailed     EvidenceStatus = "failed"
	EvidenceIncomplete EvidenceStatus = "incomplete"
)

// Validate reports an error for any value outside the closed set.
func (s EvidenceStatus) Validate() error {
	switch s {
	case EvidencePending, EvidencePassed, EvidenceFailed, EvidenceIncomplete:
		return nil
	}
	return fmt.Errorf("unknown evidence status %q", s)
}

// EvidenceGroup bundles the QA evidence collected for one check surface.
type EvidenceGroup struct {
	Label      string         `json:"label"`
	Status     EvidenceStatus `json:"status"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Incomplete int            `json:"incomplete"`
	Findings   []string       `json:"findings"`
	Duration   float64        `json:"duration"`
}

// Validate checks the label, the closed status, non-negative counts,
// non-empty findings, and a finite non-negative duration.
func (g EvidenceGroup) Validate() error {
	if strings.TrimSpace(g.Label) == "" {
		return fmt.Errorf("evidence group label is required")
	}
	if err := g.Status.Validate(); err != nil {
		return fmt.Errorf("evidence group %s: %w", g.Label, err)
	}
	if g.Passed < 0 {
		return fmt.Errorf("evidence group %s: passed must be non-negative, got %d", g.Label, g.Passed)
	}
	if g.Failed < 0 {
		return fmt.Errorf("evidence group %s: failed must be non-negative, got %d", g.Label, g.Failed)
	}
	if g.Incomplete < 0 {
		return fmt.Errorf("evidence group %s: incomplete must be non-negative, got %d", g.Label, g.Incomplete)
	}
	if math.IsNaN(g.Duration) || math.IsInf(g.Duration, 0) || g.Duration < 0 {
		return fmt.Errorf("evidence group %s: duration must be finite and non-negative, got %v", g.Label, g.Duration)
	}
	for i, finding := range g.Findings {
		if strings.TrimSpace(finding) == "" {
			return fmt.Errorf("evidence group %s: findings[%d] must not be empty", g.Label, i)
		}
	}
	return nil
}
