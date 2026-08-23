package cli

import (
	"strings"
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

func TestSumStreamJSONTokens(t *testing.T) {
	transcript := strings.Join([]string{
		`{"totalTokens":5739,"type":"usage","stage":"code_writer"}`,
		`{"totalTokens":1200,"type":"usage","stage":"code_writer"}`,
		`{"type":"final","text":"{}"}`,
		"not json at all",
		`{"totalTokens":9999}`,
	}, "\n")
	if got := sumStreamJSONTokens([]byte(transcript)); got != 6939 {
		t.Fatalf("sum = %d, want only the usage records summed (6939)", got)
	}
	if got := sumStreamJSONTokens(nil); got != 0 {
		t.Fatalf("empty transcript sum = %d, want 0", got)
	}
}
