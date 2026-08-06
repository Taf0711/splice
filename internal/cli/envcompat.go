package cli

import (
	"fmt"
	"io"
	"sort"
)

var obsoleteEnvVarRenames = map[string]string{
	"ZERO_CHECKPOINTS":              "SPLICE_CHECKPOINTS",
	"ZERO_CHECKPOINT_MAX_BYTES":     "SPLICE_CHECKPOINT_MAX_BYTES",
	"ZERO_DISABLE_MODELS_FETCH":     "SPLICE_DISABLE_MODELS_FETCH",
	"ZERO_DISABLE_PROMPT_CACHE_KEY": "SPLICE_DISABLE_PROMPT_CACHE_KEY",
	"ZERO_FORMAT_ON_WRITE":          "SPLICE_FORMAT_ON_WRITE",
	"ZERO_MAX_TURNS":                "SPLICE_MAX_TURNS",
	"ZERO_MODELS_CACHE_PATH":        "SPLICE_MODELS_CACHE_PATH",
	"ZERO_MODELS_URL":               "SPLICE_MODELS_URL",
	"ZERO_OAUTH_DEVICE":             "SPLICE_OAUTH_DEVICE",
	"ZERO_OAUTH_STORAGE":            "SPLICE_OAUTH_STORAGE",
	"ZERO_OPENROUTER_BASE_URL":      "SPLICE_OPENROUTER_BASE_URL",
	"ZERO_PROVIDER_COMMAND":         "SPLICE_PROVIDER_COMMAND",
	"ZERO_THEME":                    "SPLICE_THEME",
	"ZERO_UPDATE_RELEASE_URL":       "SPLICE_UPDATE_RELEASE_URL",
}

func warnObsoleteEnvVars(env func(string) string, stderr io.Writer) {
	if env == nil || stderr == nil {
		return
	}

	var obsolete []string
	for oldName := range obsoleteEnvVarRenames {
		if env(oldName) != "" {
			obsolete = append(obsolete, oldName)
		}
	}
	if len(obsolete) == 0 {
		return
	}

	sort.Strings(obsolete)
	message := "warning: SPLICE ignores the obsolete ZERO_* environment variables. Rename them:\n"
	for _, oldName := range obsolete {
		message += fmt.Sprintf("warning:   %s -> %s\n", oldName, obsoleteEnvVarRenames[oldName])
	}
	_, _ = fmt.Fprintf(stderr, "%s", message)
}
