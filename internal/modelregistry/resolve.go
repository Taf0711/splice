package modelregistry

import (
	"fmt"
	"strings"
)

// Resolve maps user input to a model: exact id/api-model/alias first, then a
// regex MatchPattern (e.g. "sonnet 4.5" -> the canonical id). It does NOT apply
// deprecation fallbacks — use ResolveWithFallback for that.
func (registry Registry) Resolve(input string) (ModelEntry, bool) {
	if model, ok := registry.Get(input); ok {
		return model, true
	}
	trimmed := strings.TrimSpace(input)
	for _, pattern := range registry.patterns {
		if pattern.re.MatchString(trimmed) {
			return registry.Get(pattern.modelID)
		}
	}
	return ModelEntry{}, false
}

// ResolveWithFallback resolves input (exact/alias/pattern) and, when the resolved
// model is deprecated and declares a fallback, redirects to the replacement. The
// returned notice is non-empty when a redirect happened or a soft-deprecation
// warning applies, so callers can surface it to the user.
func (registry Registry) ResolveWithFallback(input string) (ModelEntry, string, bool) {
	model, ok := registry.Resolve(input)
	if !ok {
		return ModelEntry{}, "", false
	}
	if model.Status == ModelStatusDeprecated && model.Deprecation != nil && strings.TrimSpace(model.Deprecation.FallbackID) != "" {
		if fallback, ok := registry.Get(model.Deprecation.FallbackID); ok {
			notice := strings.TrimSpace(model.Deprecation.WarningMsg)
			if notice == "" {
				notice = fmt.Sprintf("%s is deprecated; using %s instead", model.ID, fallback.ID)
			}
			return fallback, notice, true
		}
	}
	if model.Deprecation != nil && strings.TrimSpace(model.Deprecation.WarningMsg) != "" {
		return model, strings.TrimSpace(model.Deprecation.WarningMsg), true
	}
	return model, "", true
}

// effortLadder orders every tier from least to most thinking. clampEffort walks
// it outward from the request: up first (a request for more thinking prefers
// the next tier up), then down, so an unsupported tier never silently becomes
// the weakest one.
var effortLadder = []ReasoningEffort{
	ReasoningEffortNone, ReasoningEffortMinimal, ReasoningEffortLow,
	ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh, ReasoningEffortMax,
}

func clampEffort(efforts []ReasoningEffort, requested ReasoningEffort) ReasoningEffort {
	for _, effort := range efforts {
		if effort == requested {
			return effort
		}
	}
	requestedIndex := -1
	for index, effort := range effortLadder {
		if effort == requested {
			requestedIndex = index
			break
		}
	}
	if requestedIndex < 0 {
		return ""
	}
	for index := requestedIndex + 1; index < len(effortLadder); index++ {
		for _, effort := range efforts {
			if effort == effortLadder[index] {
				return effort
			}
		}
	}
	for index := requestedIndex - 1; index >= 0; index-- {
		for _, effort := range efforts {
			if effort == effortLadder[index] {
				return effort
			}
		}
	}
	return ""
}

// EffectiveReasoningEffort returns the effort to use for a model: the requested
// value if the model supports it, otherwise the nearest supported tier. It
// resolves the supported set through
// effectiveReasoningEfforts so it sees the same name-based fallback the /effort
// picker uses — the two must never disagree about which tiers a model supports.
func EffectiveReasoningEffort(model ModelEntry, requested ReasoningEffort) ReasoningEffort {
	efforts := effectiveReasoningEfforts(model)
	if len(efforts) == 0 {
		return ReasoningEffortNone
	}
	if requested != "" {
		if effort := clampEffort(efforts, requested); effort != "" {
			return effort
		}
	}
	if model.DefaultReasoningEffort != "" {
		if effort := clampEffort(efforts, model.DefaultReasoningEffort); effort != "" {
			return effort
		}
	}
	if effort := clampEffort(efforts, ReasoningEffortMedium); effort != "" {
		return effort
	}
	return efforts[0]
}

// effectiveReasoningEfforts returns a model's supported reasoning efforts, falling
// back to name-based inference (reasoningEffortsForModelName) when the catalog
// entry enumerates none. Both the /effort picker (Registry.ReasoningEfforts) and
// the run-time resolver (EffectiveReasoningEffort) read efforts through this
// single helper, so the picker can never advertise a tier the resolver drops.
func effectiveReasoningEfforts(model ModelEntry) []ReasoningEffort {
	if len(model.ReasoningEfforts) > 0 {
		return model.ReasoningEfforts
	}
	if efforts := reasoningEffortsForModelName(model.ID); len(efforts) > 0 {
		return efforts
	}
	return reasoningEffortsForModelName(model.APIModel)
}
