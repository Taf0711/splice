package v2

import "fmt"

const memoryWriteDeniedRule = "memory_write_denied"

// MemoryWritePolicy controls observation and exemplar writes for a run. Trace
// writes and trace queries are outside this policy and remain available.
type MemoryWritePolicy struct {
	Mode RunMode
}

// CheckUpsert denies observation and exemplar writes in claim mode.
func (p MemoryWritePolicy) CheckUpsert(kind string) error {
	if !ValidRunMode(p.Mode) {
		return fmt.Errorf("memory write policy has unknown mode %q", p.Mode)
	}
	if p.Mode == RunModeClaim {
		return fmt.Errorf("memory write denied: kind=%q mode=%q rule_id=%s", kind, p.Mode, memoryWriteDeniedRule)
	}
	if kind != string(SnapshotKindObservation) && kind != string(SnapshotKindExemplar) {
		return fmt.Errorf("memory write kind %q is not an observation or exemplar", kind)
	}
	return nil
}

// CheckMarkReviewed applies the same claim-mode denial to review writes.
func (p MemoryWritePolicy) CheckMarkReviewed(kind string) error {
	return p.CheckUpsert(kind)
}

// CheckUpsertTrace is intentionally ungated. Trace writes are required in all
// modes and are paired with the runner's trace endpoint wiring.
func (p MemoryWritePolicy) CheckUpsertTrace() error { return nil }

// CheckQueryTraces is intentionally ungated. Trace queries are not memory
// corpus writes and remain available in claim mode.
func (p MemoryWritePolicy) CheckQueryTraces() error { return nil }

// MemoryStorePolicyAdapter is the dependency-free seam consumed by the future
// runner memory-store wrapper. It names the only two corpus writes gated here.
type MemoryStorePolicyAdapter struct {
	Policy MemoryWritePolicy
}

// CheckUpsert delegates the corpus upsert gate.
func (a MemoryStorePolicyAdapter) CheckUpsert(kind string) error {
	return a.Policy.CheckUpsert(kind)
}

// CheckMarkReviewed delegates the corpus review gate.
func (a MemoryStorePolicyAdapter) CheckMarkReviewed(kind string) error {
	return a.Policy.CheckMarkReviewed(kind)
}

// MemoryWriteDeniedRuleID returns the stable policy rule identifier.
func MemoryWriteDeniedRuleID() string { return memoryWriteDeniedRule }
