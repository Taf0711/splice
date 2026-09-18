package cli

// ADDENDUM 4 pins a 10m per-attempt bound for the fam-05 mechanism run so a
// reasoning loop cannot burn the spend ceiling. This pins the flag, the
// default, and the helper the matched runner uses.

import (
	"testing"
	"time"
)

func TestAttemptTimeoutFlagAndDefault(t *testing.T) {
	opts, _, err := parseMvpEvalArgs([]string{"--manifest", "m.json", "--taskset", "t", "--attempt-timeout", "10m"})
	if err != nil {
		t.Fatalf("parse --attempt-timeout: %v", err)
	}
	if opts.AttemptTimeout != 10*time.Minute || opts.attemptTimeout() != 10*time.Minute {
		t.Fatalf("attempt timeout = %v / helper %v, want 10m", opts.AttemptTimeout, opts.attemptTimeout())
	}

	def, _, err := parseMvpEvalArgs([]string{"--manifest", "m.json", "--taskset", "t"})
	if err != nil {
		t.Fatalf("parse default: %v", err)
	}
	if def.AttemptTimeout != 0 || def.attemptTimeout() != familiesRunTimeout {
		t.Fatalf("default attempt timeout = %v / helper %v, want 0 / %v", def.AttemptTimeout, def.attemptTimeout(), familiesRunTimeout)
	}

	if _, _, err := parseMvpEvalArgs([]string{"--manifest", "m.json", "--taskset", "t", "--attempt-timeout", "banana"}); err == nil {
		t.Fatal("invalid duration must be rejected")
	}
	if _, _, err := parseMvpEvalArgs([]string{"--manifest", "m.json", "--taskset", "t", "--attempt-timeout=-1m"}); err == nil {
		t.Fatal("non-positive duration must be rejected")
	}
}
