package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWarnObsoleteEnvVars(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "none set",
			env:  map[string]string{},
			want: "",
		},
		{
			name: "one set",
			env:  map[string]string{"ZERO_THEME": "dark"},
			want: "warning: SPLICE ignores the obsolete ZERO_* environment variables. Rename them:\nwarning:   ZERO_THEME -> SPLICE_THEME\n",
		},
		{
			name: "several set sorted",
			env: map[string]string{
				"ZERO_THEME":                "dark",
				"ZERO_CHECKPOINTS":          "true",
				"ZERO_OAUTH_STORAGE":        "/private/secret",
				"ZERO_CHECKPOINT_MAX_BYTES": "1024",
			},
			want: "warning: SPLICE ignores the obsolete ZERO_* environment variables. Rename them:\n" +
				"warning:   ZERO_CHECKPOINTS -> SPLICE_CHECKPOINTS\n" +
				"warning:   ZERO_CHECKPOINT_MAX_BYTES -> SPLICE_CHECKPOINT_MAX_BYTES\n" +
				"warning:   ZERO_OAUTH_STORAGE -> SPLICE_OAUTH_STORAGE\n" +
				"warning:   ZERO_THEME -> SPLICE_THEME\n",
		},
		{
			name: "unknown variable ignored",
			env:  map[string]string{"ZERO_SOMETHING_ELSE": "ignored"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			warnObsoleteEnvVars(func(name string) string { return tt.env[name] }, &stderr)
			if got := stderr.String(); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
			for _, value := range tt.env {
				if value != "" && strings.Contains(stderr.String(), value) {
					t.Fatalf("output contains an obsolete variable value %q", value)
				}
			}
		})
	}
}

func TestRunWarnsObsoleteEnvVarsAtEntry(t *testing.T) {
	t.Run("obsolete variable", func(t *testing.T) {
		t.Setenv("ZERO_THEME", "dark")
		var stdout, stderr bytes.Buffer

		if exitCode := Run([]string{"--help"}, &stdout, &stderr); exitCode != 0 {
			t.Fatalf("exit code = %d, want 0", exitCode)
		}
		if !strings.Contains(stderr.String(), "ZERO_THEME -> SPLICE_THEME") {
			t.Fatalf("stderr = %q, want obsolete variable warning", stderr.String())
		}
	})

	t.Run("no obsolete variables", func(t *testing.T) {
		for name := range obsoleteEnvVarRenames {
			t.Setenv(name, "")
		}
		var stdout, stderr bytes.Buffer

		if exitCode := Run([]string{"--help"}, &stdout, &stderr); exitCode != 0 {
			t.Fatalf("exit code = %d, want 0", exitCode)
		}
		if strings.Contains(stderr.String(), "obsolete") {
			t.Fatalf("stderr = %q, must not contain obsolete warning", stderr.String())
		}
	})
}
