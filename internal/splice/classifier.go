package splice

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

var (
	trivialKeywords     = []string{"typo", "rename", "format", "comment", "spelling"}
	substantialKeywords = []string{
		"architecture", "auth", "authentication", "authorization", "database",
		"migration", "oauth", "payment", "permissions", "service", "security",
	}
	architecturalKeywords = []string{
		"distributed", "multi-service", "multi-provider", "orchestrator",
		"platform", "storage layer", "system design",
	}
)

// ClassifyRequestTyped classifies a raw request with deterministic, auditable heuristics.
func ClassifyRequestTyped(request string) schemas.ComplexityClassifierOutput {
	return ClassifyComplexity(schemas.ComplexityClassifierInput{Request: request})
}

// ClassifyComplexity returns a typed complexity classification without requiring provider access.
func ClassifyComplexity(input schemas.ComplexityClassifierInput) schemas.ComplexityClassifierOutput {
	request := strings.Join(strings.Fields(input.Request), " ")
	normalized := strings.ToLower(request)
	domains := detectRiskDomains(normalized)

	if utf8.RuneCountInString(request) < 120 && containsAny(normalized, trivialKeywords) {
		return schemas.ComplexityClassifierOutput{
			Tier:                schemas.TierTrivial,
			Rationale:           "Small wording or mechanical change with low implementation risk.",
			Confidence:          0.86,
			DetectedRiskDomains: domainsOrDefault(domains, schemas.RiskDocumentation),
			DesignIntensity:     schemas.DesignNone,
		}
	}

	if containsAny(normalized, architecturalKeywords) {
		return schemas.ComplexityClassifierOutput{
			Tier:                schemas.TierArchitectural,
			Rationale:           "Request appears to affect system-level architecture or core orchestration.",
			Confidence:          0.78,
			DetectedRiskDomains: domainsOrDefault(domains, schemas.RiskUnknown),
			DesignIntensity:     schemas.DesignFull,
		}
	}

	if containsAny(normalized, substantialKeywords) || utf8.RuneCountInString(request) > 900 {
		return schemas.ComplexityClassifierOutput{
			Tier:                schemas.TierSubstantial,
			Rationale:           "Request touches higher-risk behavior or requires coordinated changes.",
			Confidence:          0.8,
			DetectedRiskDomains: domainsOrDefault(domains, schemas.RiskUnknown),
			DesignIntensity:     schemas.DesignLight,
		}
	}

	return schemas.ComplexityClassifierOutput{
		Tier:                schemas.TierLight,
		Rationale:           "Request appears scoped to a normal implementation pass.",
		Confidence:          0.74,
		DetectedRiskDomains: domains,
		DesignIntensity:     schemas.DesignNone,
	}
}

func detectRiskDomains(normalized string) []schemas.RiskDomain {
	var domains []schemas.RiskDomain
	addDomain(&domains, normalized, schemas.RiskAuth, "auth", "oauth", "login", "permission", "session")
	addDomain(&domains, normalized, schemas.RiskData, "database", "migration", "schema", "sqlite", "storage")
	addDomain(&domains, normalized, schemas.RiskDependencies, "dependency", "package", "install", "provider", "adapter")
	addDomain(&domains, normalized, schemas.RiskDocumentation, "readme", "docs", "documentation", "comment", "typo")
	addDomain(&domains, normalized, schemas.RiskInfrastructure, "ci", "workflow", "deploy", "docker", "service")
	addDomain(&domains, normalized, schemas.RiskSecurity, "security", "secret", "key", "token", "vulnerability")
	addDomain(&domains, normalized, schemas.RiskTests, "test", "pytest", "coverage", "fixture")
	addDomain(&domains, normalized, schemas.RiskUI, "ui", "tui", "terminal", "screen", "button")
	return domains
}

func addDomain(domains *[]schemas.RiskDomain, normalized string, domain schemas.RiskDomain, keywords ...string) {
	if !slices.Contains(*domains, domain) && containsAny(normalized, keywords) {
		*domains = append(*domains, domain)
	}
}

func containsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func domainsOrDefault(domains []schemas.RiskDomain, fallback schemas.RiskDomain) []schemas.RiskDomain {
	if len(domains) == 0 {
		return []schemas.RiskDomain{fallback}
	}
	return domains
}
