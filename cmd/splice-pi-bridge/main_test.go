package main

import (
	"testing"

	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/tools"
)

// The bridge registry must carry the raw source reader, or a pipeline
// context request fails with "tool not found in registry: raw_file_read".
func TestBridgeRegistryCarriesRawSourceReader(t *testing.T) {
	registry := bridgeRegistry(t.TempDir())
	if _, ok := registry.Get(tools.RawFileReadToolName); !ok {
		t.Fatalf("bridge registry lacks %s", tools.RawFileReadToolName)
	}
}

// The bridge runs without config resolution, so its run must not report the
// trajectory classifier as rules only. The classifier flag is off in its options.
func TestBridgeOptionsTurnTrajectoryClassifierOff(t *testing.T) {
	options := bridgeOptions("/tmp/work")
	if options.Flags.Enabled(flags.TrajectoryClassifierEnabled) {
		t.Fatal("bridge run must not leave the trajectory classifier on by default")
	}
	if options.Cwd != "/tmp/work" || options.MaxTurns != 1 {
		t.Fatalf("bridge options = %+v, want cwd and one turn", options)
	}
}
