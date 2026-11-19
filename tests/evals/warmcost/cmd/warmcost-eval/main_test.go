package main

import (
	"flag"
	"testing"
)

func parseOptions(t *testing.T, args []string) options {
	t.Helper()
	var opts options
	fs := flag.NewFlagSet("warmcost-eval", flag.ContinueOnError)
	bindFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return opts
}

func TestTrustProjectFlagDefaultsOffAndParses(t *testing.T) {
	if parseOptions(t, nil).trustProject {
		t.Fatal("--trust-project defaulted on; every existing campaign must keep --no-trust")
	}
	if !parseOptions(t, []string{"--trust-project"}).trustProject {
		t.Fatal("--trust-project did not set trustProject")
	}
}
