package tools

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// External output reducer.
//
// Splice ships no reducer and links no reducer code. When the operator names an
// executable in SPLICE_TOOL_OUTPUT_REDUCER, the tool layer pipes oversized tool
// output through it before the universal ceiling. The reducer reads the text on
// stdin and writes the reduced text on stdout.
//
// This exists because head-and-tail truncation is position-blind. Measured on
// this repository, a 51,705-byte `git diff` (17 files, 36 hunks) loses 6 files
// and 12 hunks to the 32 KiB bash budget, because the dropped middle is chosen
// by offset and not by meaning. A command-aware reducer keeps the signal.
//
// Placement rules:
//   - The reducer runs after secret scrubbing, so it never sees a value that
//     the transcript would have hidden.
//   - The reducer runs before the ceiling, so the ceiling stays the backstop
//     for a reducer that returns something too large.
//
// Failure policy is fail-open with one visible note per process. A reducer that
// is missing, slow, or broken must never break a tool call. Empty output counts
// as failure, because a silent empty reduction would look like a successful
// prune of all content.
const (
	outputReducerEnv        = "SPLICE_TOOL_OUTPUT_REDUCER"
	outputReducerMinEnv     = "SPLICE_TOOL_OUTPUT_REDUCER_MIN_BYTES"
	outputReducerTimeoutEnv = "SPLICE_TOOL_OUTPUT_REDUCER_TIMEOUT_MS"

	// reducedMetaKey records that a reducer already handled this result, so the
	// registry does not reduce the same text twice.
	reducedMetaKey = "output_reducer"

	defaultOutputReducerMinBytes = 8 * 1024
	defaultOutputReducerTimeout  = 5 * time.Second
)

// reducerWarningRaised keeps the failure note to one per process. A broken
// reducer on a chatty run would otherwise append the note to every tool result.
var reducerWarningRaised atomic.Bool

// resetReducerWarningForTest clears the one-note guard so each test observes the
// note it is asserting on.
func resetReducerWarningForTest() {
	reducerWarningRaised.Store(false)
}

// outputReducerCommand returns the configured reducer argv, or nil when unset.
// The value is split on whitespace and executed without a shell, so a reducer
// with arguments works and no shell metacharacter is interpreted.
func outputReducerCommand() []string {
	raw := strings.TrimSpace(os.Getenv(outputReducerEnv))
	if raw == "" {
		return nil
	}
	return strings.Fields(raw)
}

// outputReducerMinBytes is the size below which reduction is skipped. Small
// output has little to gain and a lot to lose.
func outputReducerMinBytes() int {
	raw := strings.TrimSpace(os.Getenv(outputReducerMinEnv))
	if raw == "" {
		return defaultOutputReducerMinBytes
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return defaultOutputReducerMinBytes
	}
	return parsed
}

// outputReducerTimeout bounds one reducer call. A reducer that hangs must not
// hang the tool call that produced the output.
func outputReducerTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(outputReducerTimeoutEnv))
	if raw == "" {
		return defaultOutputReducerTimeout
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return defaultOutputReducerTimeout
	}
	return time.Duration(parsed) * time.Millisecond
}

// reduceOutputText pipes text through the configured reducer. It returns the
// input unchanged when no reducer is configured, when the input is below the
// threshold, or on any reducer failure.
//
// The note appended on the first failure is bounded and derived from the error
// only. Reducer stderr is never copied into the output, because the note is
// added after secret scrubbing and must not carry unscrubbed content.
func reduceOutputText(toolName, text string, meta map[string]string) string {
	if len(text) == 0 {
		return text
	}
	// Native reducers run first. They are in process, need no dependency, and
	// cannot be defeated by a missing or broken binary.
	if reduced, ok := reduceNativeOutput(text); ok {
		recordReducerMeta(meta, "native", len(text), len(reduced), "")
		return reduced
	}
	cmdline := outputReducerCommand()
	if len(cmdline) == 0 {
		return text
	}
	if len(text) < outputReducerMinBytes() {
		return text
	}

	ctx, cancel := context.WithTimeout(context.Background(), outputReducerTimeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdline[0], cmdline[1:]...)
	cmd.Stdin = strings.NewReader(text)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Stderr stays nil: Go connects it to the null device, so reducer stderr
	// can neither block the pipe nor reach the transcript.

	if err := cmd.Run(); err != nil {
		return reducerFailed(text, meta, cmdline[0], boundedReason(err.Error()))
	}
	reduced := stdout.String()
	if strings.TrimSpace(reduced) == "" {
		return reducerFailed(text, meta, cmdline[0], "reducer returned empty output")
	}
	if len(reduced) >= len(text) {
		// No reduction. Keep the original bytes so the recorded sizes and the
		// emitted text cannot disagree.
		recordReducerMeta(meta, cmdline[0], len(text), len(text), "")
		return text
	}
	recordReducerMeta(meta, cmdline[0], len(text), len(reduced), "")
	return reduced
}

// reducerFailed keeps the original text and records why. The visible note is
// appended once per process so a permanently broken reducer stays observable
// without flooding every result.
func reducerFailed(text string, meta map[string]string, bin, reason string) string {
	recordReducerMeta(meta, bin, len(text), len(text), reason)
	if !reducerWarningRaised.CompareAndSwap(false, true) {
		return text
	}
	return text + "\n[splice] output reducer failed (" + reason + "); original output kept"
}

// recordReducerMeta writes the before/after sizes and any failure reason. A nil
// meta is allowed and simply records nothing.
func recordReducerMeta(meta map[string]string, bin string, raw, emitted int, failure string) {
	if meta == nil {
		return
	}
	meta[reducedMetaKey] = filepath.Base(bin)
	meta["output_reducer_raw_bytes"] = strconv.Itoa(raw)
	meta["output_reducer_emitted_bytes"] = strconv.Itoa(emitted)
	if failure != "" {
		meta["output_reducer_error"] = failure
	} else {
		delete(meta, "output_reducer_error")
	}
}

// reduceToolResultOutput applies the reducer to a finished tool result, unless
// the tool already reduced its own output.
func reduceToolResultOutput(toolName string, res Result) Result {
	if len(outputReducerCommand()) == 0 || len(res.Output) == 0 {
		return res
	}
	if res.Meta != nil {
		if _, done := res.Meta[reducedMetaKey]; done {
			return res
		}
	}
	if res.Meta == nil {
		res.Meta = map[string]string{}
	}
	res.Output = reduceOutputText(toolName, res.Output, res.Meta)
	return res
}

// boundedReason keeps a failure reason short and single-line so it cannot blow
// up a report or a status line.
func boundedReason(reason string) string {
	flat := strings.Join(strings.Fields(reason), " ")
	const max = 120
	if len(flat) > max {
		return flat[:max] + "..."
	}
	if flat == "" {
		return "unknown reducer error"
	}
	return flat
}
