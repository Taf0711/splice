package tools

import (
	"strings"
	"testing"
)

// bigText returns n non-empty lines joined by newlines, so a reducer that keeps
// or drops lines is observable in the byte count.
func bigText(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line of tool output for the reducer test"
	}
	return strings.Join(lines, "\n")
}

// TestReduceOutputTextIdentityKeepsOriginal pins that a reducer which returns an
// equal or larger body does not replace the text, and that the recorded sizes
// agree with the emitted text.
func TestReduceOutputTextIdentityKeepsOriginal(t *testing.T) {
	t.Setenv(outputReducerEnv, "cat")
	t.Setenv(outputReducerMinEnv, "1")
	resetReducerWarningForTest()

	text := bigText(20)
	meta := map[string]string{}
	got := reduceOutputText("bash", text, meta)
	if got != text {
		t.Fatalf("identity reducer changed output: got %d bytes, want %d", len(got), len(text))
	}
	if meta[reducedMetaKey] != "cat" {
		t.Fatalf("meta reducer = %q, want cat", meta[reducedMetaKey])
	}
	if meta["output_reducer_raw_bytes"] != meta["output_reducer_emitted_bytes"] {
		t.Fatalf("sizes disagree: raw=%s emitted=%s", meta["output_reducer_raw_bytes"], meta["output_reducer_emitted_bytes"])
	}
	if meta["output_reducer_error"] != "" {
		t.Fatalf("unexpected error meta: %q", meta["output_reducer_error"])
	}
}

// TestReduceOutputTextTruncatingReducerApplies pins the happy path: a reducer
// that shrinks the text replaces it.
func TestReduceOutputTextTruncatingReducerApplies(t *testing.T) {
	t.Setenv(outputReducerEnv, "head -c 200")
	t.Setenv(outputReducerMinEnv, "1")
	resetReducerWarningForTest()

	text := bigText(100)
	if len(text) <= 200 {
		t.Fatalf("fixture too small: %d bytes", len(text))
	}
	meta := map[string]string{}
	got := reduceOutputText("bash", text, meta)
	if len(got) != 200 {
		t.Fatalf("reduced size = %d, want 200", len(got))
	}
	if meta["output_reducer_emitted_bytes"] != "200" {
		t.Fatalf("emitted meta = %q, want 200", meta["output_reducer_emitted_bytes"])
	}
	if meta["output_reducer_raw_bytes"] == meta["output_reducer_emitted_bytes"] {
		t.Fatalf("raw and emitted should differ: %s", meta["output_reducer_raw_bytes"])
	}
}

// TestReduceOutputTextFailsOpen is the guard that matters. A broken reducer must
// never destroy tool output. Each case keeps the original text as a prefix and
// records the reason.
func TestReduceOutputTextFailsOpen(t *testing.T) {
	cases := []struct {
		name    string
		command string
		timeout string
		reason  string
	}{
		{name: "nonzero exit", command: "false", reason: "exit status 1"},
		{name: "empty output", command: "true", reason: "empty output"},
		{name: "missing binary", command: "definitely-not-a-real-binary-xyz", reason: "not found"},
		{name: "timeout", command: "sleep 5", timeout: "50", reason: "killed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(outputReducerEnv, tc.command)
			t.Setenv(outputReducerMinEnv, "1")
			if tc.timeout == "" {
				t.Setenv(outputReducerTimeoutEnv, "")
			} else {
				t.Setenv(outputReducerTimeoutEnv, tc.timeout)
			}
			resetReducerWarningForTest()

			text := bigText(20)
			meta := map[string]string{}
			got := reduceOutputText("bash", text, meta)

			if !strings.HasPrefix(got, text) {
				t.Fatalf("original text was not preserved as a prefix: %q", got)
			}
			reason := meta["output_reducer_error"]
			if !strings.Contains(reason, tc.reason) {
				t.Fatalf("error meta = %q, want it to contain %q", reason, tc.reason)
			}
			if !strings.Contains(got, "output reducer failed") {
				t.Fatalf("first failure should append a visible note: %q", got)
			}
		})
	}
}

// TestReduceOutputTextWarningIsOncePerProcess pins the anti-flood guard: only the
// first failure appends a note.
func TestReduceOutputTextWarningIsOncePerProcess(t *testing.T) {
	t.Setenv(outputReducerEnv, "false")
	t.Setenv(outputReducerMinEnv, "1")
	resetReducerWarningForTest()

	text := bigText(20)
	first := reduceOutputText("bash", text, map[string]string{})
	second := reduceOutputText("bash", text, map[string]string{})

	if !strings.Contains(first, "output reducer failed") {
		t.Fatalf("first failure should carry the note: %q", first)
	}
	if strings.Contains(second, "output reducer failed") {
		t.Fatalf("second failure should not repeat the note: %q", second)
	}
}

// TestReduceOutputTextBelowThresholdSkips pins that small output is untouched.
func TestReduceOutputTextBelowThresholdSkips(t *testing.T) {
	t.Setenv(outputReducerEnv, "head -c 5")
	t.Setenv(outputReducerMinEnv, "10000")
	resetReducerWarningForTest()

	text := bigText(3)
	meta := map[string]string{}
	got := reduceOutputText("bash", text, meta)
	if got != text {
		t.Fatalf("below-threshold text changed: %q", got)
	}
	if meta[reducedMetaKey] != "" {
		t.Fatalf("below-threshold text should record no reducer meta, got %q", meta[reducedMetaKey])
	}
}

// TestReduceOutputTextUnsetIsNoop pins the default: Splice ships no reducer, so
// with nothing configured the tool layer is byte-identical to before.
func TestReduceOutputTextUnsetIsNoop(t *testing.T) {
	t.Setenv(outputReducerEnv, "")
	text := bigText(50)
	meta := map[string]string{}
	if got := reduceOutputText("bash", text, meta); got != text {
		t.Fatalf("unset reducer changed output")
	}
	if len(meta) != 0 {
		t.Fatalf("unset reducer wrote meta: %v", meta)
	}
}

// TestReduceToolResultOutputSkipsAlreadyReduced pins the double-reduction guard:
// a tool that reduced its own output in place is not reduced again at the
// registry boundary.
func TestReduceToolResultOutputSkipsAlreadyReduced(t *testing.T) {
	t.Setenv(outputReducerEnv, "head -c 10")
	t.Setenv(outputReducerMinEnv, "1")

	original := bigText(50)
	res := Result{Output: original, Meta: map[string]string{reducedMetaKey: "bash"}}
	got := reduceToolResultOutput("bash", res)
	if got.Output != original {
		t.Fatalf("already-reduced output was reduced again: %d bytes, want %d", len(got.Output), len(original))
	}
}

// TestReduceToolResultOutputAppliesAndAllocatesMeta pins that the registry path
// reduces output and creates meta when the result has none.
func TestReduceToolResultOutputAppliesAndAllocatesMeta(t *testing.T) {
	t.Setenv(outputReducerEnv, "head -c 200")
	t.Setenv(outputReducerMinEnv, "1")
	resetReducerWarningForTest()

	original := bigText(50)
	res := Result{Output: original}
	got := reduceToolResultOutput("web_fetch", res)
	if len(got.Output) != 200 {
		t.Fatalf("reduced size = %d, want 200", len(got.Output))
	}
	if got.Meta == nil || got.Meta[reducedMetaKey] != "head" {
		t.Fatalf("meta not recorded: %v", got.Meta)
	}
}

// TestReduceToolResultOutputNoopWhenUnset pins that an unconfigured reducer does
// not allocate meta on every tool result.
func TestReduceToolResultOutputNoopWhenUnset(t *testing.T) {
	t.Setenv(outputReducerEnv, "")
	res := Result{Output: bigText(50)}
	got := reduceToolResultOutput("bash", res)
	if got.Output != res.Output || got.Meta != nil {
		t.Fatalf("unset reducer changed the result: meta=%v", got.Meta)
	}
}

// TestBoundedReasonIsSingleLineAndBounded pins the note formatter.
func TestBoundedReasonIsSingleLineAndBounded(t *testing.T) {
	long := strings.Repeat("a", 400) + "\n\nsecond line"
	got := boundedReason(long)
	if strings.Contains(got, "\n") {
		t.Fatalf("reason should be single-line: %q", got)
	}
	if len(got) > 123 {
		t.Fatalf("reason too long: %d", len(got))
	}
	if got := boundedReason("   "); got != "unknown reducer error" {
		t.Fatalf("empty reason = %q", got)
	}
}
