package secrets

import (
	"strings"
)

// credentialEnvExact contains literal environment variable names whose values
// should never be forwarded to child processes.
var credentialEnvExact = map[string]struct{}{
	"AWS_ACCESS_KEY_ID":              {},
	"AWS_SECRET_ACCESS_KEY":          {},
	"AWS_SESSION_TOKEN":              {},
	"AZURE_CLIENT_SECRET":            {},
	"GOOGLE_APPLICATION_CREDENTIALS": {},
	"GITHUB_TOKEN":                   {},
	"GH_TOKEN":                       {},
	"ZERO_API_KEY":                   {},
	"SPLICE_API_KEY":                 {},
	"ANTHROPIC_API_KEY":              {},
	"OPENAI_API_KEY":                 {},
	"API_KEY":                        {},
	"AUTH_TOKEN":                     {},
	"ACCESS_TOKEN":                   {},
	"REFRESH_TOKEN":                  {},
}

// credentialEnvSuffixes matches any variable name ending with one of these
// suffixes (case-insensitive).
var credentialEnvSuffixes = []string{
	"_API_KEY",
	"_TOKEN",
	"_SECRET",
	"_SECRET_KEY",
	"_ACCESS_KEY",
	"_PASSWORD",
}

// childEnvAllowlistVar is the environment variable that holds a comma-separated
// list of variable names that must be kept in the child environment even if
// they otherwise look like credentials.
const childEnvAllowlistVar = "SPLICE_CHILD_ENV_ALLOWLIST"

// isCredentialEnv reports whether name looks like a credential-bearing
// environment variable. The check is case-insensitive.
func isCredentialEnv(name string) bool {
	upper := strings.ToUpper(name)
	if _, ok := credentialEnvExact[upper]; ok {
		return true
	}
	for _, suffix := range credentialEnvSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

// parseAllowlist extracts the set of variable names from the allowlist env entry.
// Names are stored with uppercase/lowercase/normalized variants so the common
// Unix uppercase convention and any input casing all match.
func parseAllowlist(allowlistEntry string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, name := range strings.Split(allowlistEntry, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		allowed[name] = struct{}{}
		allowed[strings.ToUpper(name)] = struct{}{}
		allowed[strings.ToLower(name)] = struct{}{}
	}
	return allowed
}

// ScrubChildEnv returns a copy of env suitable for passing to a child process.
// It removes entries whose names look credential-bearing (exact list or
// suffixes such as _API_KEY and _TOKEN). Entries named in
// SPLICE_CHILD_ENV_ALLOWLIST (read from env itself) are retained.
// Malformed entries are skipped silently.
func ScrubChildEnv(env []string) []string {
	var allowlist map[string]struct{}
	// Extract the allowlist value from env itself so a parent can explicitly
	// pass keys through to selected children.
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if strings.EqualFold(kv[:eq], childEnvAllowlistVar) {
			allowlist = parseAllowlist(kv[eq+1:])
			break
		}
	}

	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			// Malformed entry: do not propagate.
			continue
		}
		name := kv[:eq]
		if allowlist != nil {
			if _, ok := allowlist[name]; ok {
				out = append(out, kv)
				continue
			}
		}
		if isCredentialEnv(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
