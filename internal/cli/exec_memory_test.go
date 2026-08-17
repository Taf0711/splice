package cli

import (
	"strings"
	"testing"
)

func TestParseExecMemoryFlag(t *testing.T) {
	// Default is "on".
	options, _, err := parseExecArgs([]string{"hello"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if options.memoryMode != "on" {
		t.Fatalf("default memoryMode = %q, want on", options.memoryMode)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--memory", "off", "hello"}, want: "off"},
		{args: []string{"--memory=on", "hello"}, want: "on"},
	} {
		options, _, err := parseExecArgs(tc.args)
		if err != nil {
			t.Fatalf("parse %v: %v", tc.args, err)
		}
		if options.memoryMode != tc.want {
			t.Fatalf("memoryMode = %q, want %q", options.memoryMode, tc.want)
		}
	}

	_, _, err = parseExecArgs([]string{"--memory", "banana", "hello"})
	if err == nil || !strings.Contains(err.Error(), "--memory must be on or off") {
		t.Fatalf("invalid --memory error = %v", err)
	}
}
