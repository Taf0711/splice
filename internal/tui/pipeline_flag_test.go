package tui

import (
	"testing"

	"github.com/Taf0711/splice/internal/flags"
)

func TestUsesPipelineHonorsFlagAndRunKind(t *testing.T) {
	set, err := flags.Resolve(flags.Sources{User: map[string]bool{
		string(flags.TUIPipelineEnabled): false,
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	tests := []struct {
		name    string
		runKind tuiRunKind
		set     flags.Set
		want    bool
	}{
		{name: "default runs the pipeline", runKind: tuiRunPipeline, set: nil, want: true},
		{name: "flag off falls back to agent loop", runKind: tuiRunPipeline, set: set, want: false},
		{name: "spec draft always skips the pipeline", runKind: tuiRunSpecDraft, set: nil, want: false},
		{name: "spec draft skips even with flag on", runKind: tuiRunSpecDraft, set: flags.Set{flags.TUIPipelineEnabled: true}, want: false},
		{name: "design conversation always skips the pipeline", runKind: tuiRunDesignConversation, set: nil, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := usesPipeline(tc.runKind, tc.set); got != tc.want {
				t.Fatalf("usesPipeline(%v) = %v, want %v", tc.runKind, got, tc.want)
			}
		})
	}
}
