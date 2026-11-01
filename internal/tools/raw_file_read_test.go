package tools

// B1 review fix regression (registry side): raw_file_read must be
// unexecutable from the model surface in every permission mode, while the
// host seam still acquires bytes through it. The advertisement-side check
// (ToolAdvertised across modes) lives in the agent package's
// raw_file_read_advertise_test.go; this file pins the execution gate.
// The enforcement is joint: Safety().Permission = Deny keeps the tool out
// of every advertised tool list, and the registry's host-seam gate
// rejects model-initiated calls even if one slips through.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRawFileReadDeclarations pins the two declarations the boundary
// needs: static Deny (the advertisement gate keys on Deny in every mode)
// and the HostSeamTool interface (the execution gate keys on it).
func TestRawFileReadDeclarations(t *testing.T) {
	tool := NewScopedRawFileReadTool(t.TempDir(), nil)
	if tool.Safety().Permission != PermissionDeny {
		t.Fatalf("raw_file_read permission = %q, want deny", tool.Safety().Permission)
	}
	if hs, ok := tool.(HostSeamTool); !ok || !hs.HostSeamOnly() {
		t.Fatal("raw_file_read must declare itself host-seam-only")
	}
}

// TestRawFileReadUnexecutableFromModelSurface pins the execution gate: a
// model-initiated call (no HostSeam marker) is rejected by the registry in
// every mode, with and without PermissionGranted. The rejection must not
// leak the file bytes.
func TestRawFileReadUnexecutableFromModelSurface(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("secret bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.Register(NewScopedRawFileReadTool(dir, nil))
	modes := []string{"auto", "member-auto", "spec-draft", "ask", "unsafe", ""}
	for _, mode := range modes {
		res := registry.RunWithOptions(context.Background(), RawFileReadToolName, map[string]any{"path": "a.txt"}, RunOptions{PermissionMode: mode})
		if res.Status == StatusOK {
			t.Fatalf("model-initiated raw_file_read executed in %q mode: %q", mode, res.Output)
		}
		if res.Output == "secret bytes" {
			t.Fatalf("file bytes leaked to the model surface in %q mode", mode)
		}
	}
	// Even a granted prompt-flow flag cannot lift the deny: that flag
	// authorizes prompt tools only.
	res := registry.RunWithOptions(context.Background(), RawFileReadToolName, map[string]any{"path": "a.txt"}, RunOptions{PermissionGranted: true, PermissionMode: "auto"})
	if res.Status == StatusOK || res.Output == "secret bytes" {
		t.Fatalf("PermissionGranted lifted a deny: %q", res.Output)
	}
}

// TestRawFileReadHostSeamStillExecutes pins assertion (2): the host seam
// acquires bytes through the SAME registry with the tool's guard set intact
// (scoped paths still deny outside roots), so the seam is unaffected.
func TestRawFileReadHostSeamStillExecutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("host bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.Register(NewScopedRawFileReadTool(dir, nil))
	res := registry.RunWithOptions(context.Background(), RawFileReadToolName, map[string]any{"path": "a.txt"}, RunOptions{HostSeam: true})
	if res.Status != StatusOK {
		t.Fatalf("host-seam read failed: %q", res.Output)
	}
	if res.Output != "host bytes" {
		t.Fatalf("host-seam read = %q, want exact bytes", res.Output)
	}
	// The guard set survives: an outside-root path is still scoped away.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "escape.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	denied := registry.RunWithOptions(context.Background(), RawFileReadToolName, map[string]any{"path": filepath.Join(outside, "escape.txt")}, RunOptions{HostSeam: true})
	if denied.Status == StatusOK {
		t.Fatalf("host-seam marker bypassed path scoping: %q", denied.Output)
	}
	// The marker cannot widen the surface: a host-seam-flagged call to a
	// Deny tool WITHOUT the interface is hard-denied even flagged.
	registry.Register(denyOnlyTool{})
	plain := registry.RunWithOptions(context.Background(), "deny_only", map[string]any{}, RunOptions{HostSeam: true})
	if plain.Status == StatusOK {
		t.Fatal("host-seam flag must not authorize a deny tool lacking the interface")
	}
}

// denyOnlyTool is a minimal PermissionDeny tool WITHOUT the HostSeamTool
// interface, pinning that the flag alone executes nothing.
type denyOnlyTool struct{ baseTool }

func (denyOnlyTool) Name() string        { return "deny_only" }
func (denyOnlyTool) Description() string { return "always denied" }
func (denyOnlyTool) Parameters() Schema  { return Schema{Type: "object"} }
func (denyOnlyTool) Safety() Safety {
	return Safety{Permission: PermissionDeny, Reason: "always denied"}
}
func (denyOnlyTool) Run(context.Context, map[string]any) Result { return okResult("should never run") }
