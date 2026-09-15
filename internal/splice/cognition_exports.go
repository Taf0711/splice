package splice

// Exported seams for the Section-11 campaign runner (internal/cli). The
// matched-snapshot campaign needs three deterministic pieces of the E1-E4
// work outside the splice package: need derivation over the target task
// text, the E4 subject-matching predicate, and reuse-record decoding from
// stored node metadata. Each wrapper delegates to the single internal
// implementation, so the campaign and the pipeline cannot drift.

import "encoding/json"

// DeriveContextNeeds is the exported E1 entry for harnesses: the context
// needs a task must satisfy, derived from the task text, confirmed against
// the current-source index of workspace, the prior changed files (the
// integration surface), and recorded failure evidence.
func DeriveContextNeeds(intent string, workspace string, priorFiles []string, failureEvidence []string) []ContextNeed {
	return deriveContextNeeds(intent, workspace, priorFiles, failureEvidence)
}

// RecordSpeaksOfSubject is the exported E4 subject-matching predicate: does
// a saved record speak of the need's subject ("path", "path#Symbol", or an
// ambiguous declaration subject, which never matches). The manual-selection
// arm of the campaign selects records with exactly this predicate.
func RecordSpeaksOfSubject(rec *ReuseRecord, subject string) bool {
	return recordSpeaksOfSubject(rec, subject)
}

// ParseReuseRecordJSON decodes a stored node metadata_json payload into a
// typed reuse record. Anything that is not a current-schema record payload
// returns nil (the node stays a hint), exactly like parseReuseRecord.
func ParseReuseRecordJSON(metadataJSON string) *ReuseRecord {
	if metadataJSON == "" {
		return nil
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &meta); err != nil {
		return nil
	}
	return parseReuseRecord(meta)
}
