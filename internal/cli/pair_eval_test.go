package cli

import (
	"testing"
)

func TestParsePairEvalArgs(t *testing.T) {
	options, help, err := parsePairEvalArgs([]string{"--taskset", "/tmp/ts", "--out", "/tmp/out", "--model", "m1"})
	if err != nil {
		t.Fatalf("parsePairEvalArgs: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	if options.TasksetDir != "/tmp/ts" || options.OutDir != "/tmp/out" || options.Model != "m1" {
		t.Fatalf("options = %#v", options)
	}

	if _, _, err := parsePairEvalArgs([]string{"--taskset="}); err == nil {
		t.Fatal("empty --taskset must error")
	}
	if _, _, err := parsePairEvalArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag must error")
	}
}
