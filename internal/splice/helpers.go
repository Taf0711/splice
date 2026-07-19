package splice

import "github.com/Taf0711/splice/internal/splice/schemas"

// Ptr returns a pointer to v. It removes the need for local string-only ptr
// helpers and keeps pointer-literal use consistent across the splice package.
func Ptr[T any](v T) *T {
	return &v
}

// DerefString safely dereferences a *string, returning "" for nil.
func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SummarizeStageOutput returns a one-line summary from a stage output.
func SummarizeStageOutput(stageName string, output schemas.HarnessStageOutput) string {
	if output.Summary != "" {
		return output.Summary
	}
	return stageName + " completed"
}
