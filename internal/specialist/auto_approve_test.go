package specialist

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/tools"
)

func executorWithReadOnlyAndWriteSpecialists() Executor {
	return Executor{
		Load: func(LoadOptions) (LoadResult, error) {
			return LoadResult{Specialists: []Manifest{
				{
					Metadata:      Metadata{Name: "explorer", Description: "read-only", Tools: []string{"read-only"}},
					ResolvedTools: []string{"read_file", "list_directory", "grep", "glob"},
				},
				{
					Metadata:      Metadata{Name: "worker", Description: "writes + runs", Tools: []string{"read-only", "edit", "execute", "plan"}},
					ResolvedTools: []string{"read_file", "grep", "write_file", "edit_file", "bash", "update_plan"},
				},
			}}, nil
		},
	}
}

func TestIsReadOnlySpecialist(t *testing.T) {
	ex := executorWithReadOnlyAndWriteSpecialists()
	if !ex.IsReadOnlySpecialist("explorer") {
		t.Fatal("explorer (reads only) should be read-only")
	}
	if ex.IsReadOnlySpecialist("worker") {
		t.Fatal("worker (write_file/edit_file/bash) must NOT be read-only")
	}
	if ex.IsReadOnlySpecialist("does-not-exist") {
		t.Fatal("an unknown specialist must not be treated as read-only")
	}
}

func TestTaskToolPermissionForArgs(t *testing.T) {
	tool := NewTaskTool(executorWithReadOnlyAndWriteSpecialists())
	cases := []struct {
		name string
		args map[string]any
		want tools.Permission
	}{
		{"read-only specialist auto-approves", map[string]any{"name": "explorer", "prompt": "look around"}, tools.PermissionAllow},
		{"write-capable specialist still prompts", map[string]any{"name": "worker", "prompt": "edit a file"}, tools.PermissionPrompt},
		{"unknown specialist prompts", map[string]any{"name": "ghost", "prompt": "x"}, tools.PermissionPrompt},
		{"resume always prompts", map[string]any{"resume": "sess_123", "name": "explorer", "prompt": "x"}, tools.PermissionPrompt},
		{"malformed args prompt", map[string]any{}, tools.PermissionPrompt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tool.PermissionForArgs(tc.args); got != tc.want {
				t.Fatalf("PermissionForArgs(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestProjectOverrideOfBuiltinNameDoesNotInheritAutoApproval pins that the
// auto-approval follows the tools of the manifest that wins, not the name. A
// trusted project "explorer" that can write or run commands still prompts.
func TestProjectOverrideOfBuiltinNameDoesNotInheritAutoApproval(t *testing.T) {
	for name, body := range map[string]string{
		"write tools":      "---\nname: explorer\ndescription: Project explorer\ntools: [read-only, edit, execute]\n---\nProject prompt.",
		"extends worker":   "---\nname: explorer\ndescription: Project explorer\nextends: worker\n---\nProject prompt.",
		"execute only":     "---\nname: explorer\ndescription: Project explorer\ntools: [execute]\n---\nProject prompt.",
		"code-review edit": "---\nname: code-review\ndescription: Project review\ntools: [edit]\n---\nProject prompt.",
	} {
		t.Run(name, func(t *testing.T) {
			projectDir := filepath.Join(t.TempDir(), "specialists")
			writeManifest(t, filepath.Join(projectDir, "override.md"), body)
			executor := Executor{Paths: Paths{UserDir: filepath.Join(t.TempDir(), "user"), ProjectDir: projectDir}}
			tool := NewTaskTool(executor)
			specialistName := "explorer"
			if strings.Contains(body, "name: code-review") {
				specialistName = "code-review"
			}
			if got := tool.PermissionForArgs(map[string]any{"name": specialistName, "prompt": "x"}); got != tools.PermissionPrompt {
				t.Fatalf("PermissionForArgs(%s) = %q, want prompt for a write-capable override", specialistName, got)
			}
		})
	}
}
