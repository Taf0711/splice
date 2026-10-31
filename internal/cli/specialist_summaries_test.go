package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/specialist"
	"github.com/Taf0711/splice/internal/tools"
)

// TestSpecialistSummariesIncludesBuiltins confirms the orchestrator delegation
// prompt is fed the built-in specialists (the wiring behind auto-delegation).
func TestSpecialistSummariesIncludesBuiltins(t *testing.T) {
	paths, err := specialist.DefaultPaths(t.TempDir())
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	descByName := map[string]string{}
	for _, info := range specialistSummaries(paths) {
		descByName[info.Name] = info.WhenToUse
	}
	for _, name := range []string{"worker", "explorer", "code-review"} {
		if descByName[name] == "" {
			t.Fatalf("specialistSummaries missing built-in %q (or empty description); got %v", name, descByName)
		}
	}
}

// TestRegisterSpecialistToolsLoadsProjectSpecialistsOnlyWhenTrusted pins the
// trust gate: an untrusted workspace's .splice/specialists neither adds a
// specialist nor replaces a built-in, and the Task tool cannot load one.
func TestRegisterSpecialistToolsLoadsProjectSpecialistsOnlyWhenTrusted(t *testing.T) {
	cwd := hostileProjectWorkspace(t, "", `{}`)
	dir := filepath.Join(cwd, ".splice", "specialists")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"explorer.md": "---\nname: explorer\ndescription: Project explorer override\ntools: [read-only]\n---\nProject prompt.",
		"evil.md":     "---\nname: evil\ndescription: Project specialist\ntools: [read-only]\n---\nProject prompt.",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%v", trusted), func(t *testing.T) {
			registry := tools.NewRegistry()
			runtime, err := registerSpecialistTools(registry, cwd, 0, trusted)
			if err != nil {
				t.Fatalf("registerSpecialistTools() error = %v", err)
			}
			defer closeSpecialistRuntime(io.Discard, runtime)
			descriptions := map[string]string{}
			for _, info := range runtime.specialists {
				descriptions[info.Name] = info.WhenToUse
			}
			_, hasEvil := descriptions["evil"]
			overridden := descriptions["explorer"] == "Project explorer override"
			if hasEvil != trusted || overridden != trusted {
				t.Fatalf("trusted=%v: project specialist loaded=%v, explorer overridden=%v (%v)", trusted, hasEvil, overridden, descriptions)
			}
			task, ok := registry.Get(specialist.TaskToolName)
			if !ok {
				t.Fatal("task tool not registered")
			}
			permissioner, ok := task.(tools.ArgsPermissioner)
			if !ok {
				t.Fatal("task tool does not implement ArgsPermissioner")
			}
			want := tools.PermissionPrompt
			if trusted {
				// A trusted read-only project specialist is harmless to spawn.
				want = tools.PermissionAllow
			}
			if got := permissioner.PermissionForArgs(map[string]any{"name": "evil", "prompt": "x"}); got != want {
				t.Fatalf("trusted=%v: PermissionForArgs(evil) = %q, want %q", trusted, got, want)
			}
		})
	}
}
