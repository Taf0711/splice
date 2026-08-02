package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/plugins"
	"github.com/Taf0711/splice/internal/tools"
)

func writeTestSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pluginWithSkill(t *testing.T, name string) plugins.LoadedPlugin {
	t.Helper()
	pluginDir := t.TempDir()
	skillPath := filepath.Join(pluginDir, "skills", name, "SKILL.md")
	writeTestSkill(t, filepath.Dir(filepath.Dir(skillPath)), name, "plugin skill body")
	return plugins.LoadedPlugin{
		ID:        "splice." + name,
		Name:      name,
		Enabled:   true,
		Source:    plugins.SourceProject,
		PluginDir: pluginDir,
		Skills:    []plugins.PathExtension{{Name: name, Path: skillPath}},
	}
}

func TestExecSkillRegistrationKeepsTheMergedSkillSurface(t *testing.T) {
	cases := []struct {
		name         string
		defaultSkill bool
		pluginSkill  bool
		wantSkills   []string
		wantAbsent   bool
	}{
		{
			name:         "default and plugin",
			defaultSkill: true,
			pluginSkill:  true,
			wantSkills:   []string{"default-only", "plugin-only"},
		},
		{
			name:         "default only",
			defaultSkill: true,
			wantSkills:   []string{"default-only"},
		},
		{
			name:        "plugin only",
			pluginSkill: true,
			wantSkills:  []string{"plugin-only"},
		},
		{
			name:       "neither",
			wantAbsent: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			defaultDir := t.TempDir()
			if testCase.defaultSkill {
				writeTestSkill(t, defaultDir, "default-only", "default skill body")
			}
			t.Setenv("SPLICE_SKILLS_DIR", defaultDir)

			loaded := []plugins.LoadedPlugin{}
			if testCase.pluginSkill {
				loaded = append(loaded, pluginWithSkill(t, "plugin-only"))
			}
			deps := appDeps{
				loadPlugins: func(plugins.LoadOptions) (plugins.LoadResult, error) {
					return plugins.LoadResult{Plugins: loaded}, nil
				},
				skillsDir: func() string { return defaultDir },
			}
			workspaceRoot := t.TempDir()
			registry := newCoreRegistry(workspaceRoot)
			var stderr bytes.Buffer
			activatePlugins(workspaceRoot, registry, deps, true, &stderr)
			registerExecCoreTools(registry, workspaceRoot, nil)

			skill, exists := registry.Get(tools.SkillToolName)
			if testCase.wantAbsent {
				if exists {
					t.Fatal("skill tool must not be registered when neither skill source exists")
				}
				return
			}
			if !exists {
				t.Fatal("skill tool is missing")
			}
			for _, wantSkill := range testCase.wantSkills {
				result := skill.Run(context.Background(), map[string]any{"name": wantSkill})
				if result.Status != tools.StatusOK {
					if testCase.pluginSkill && testCase.defaultSkill && wantSkill == "plugin-only" {
						// The model was told a plugin skill existed and then got "unknown skill".
						t.Fatalf("plugin skill %q unresolved: %s", wantSkill, result.Output)
					}
					t.Fatalf("skill %q unresolved: %s", wantSkill, result.Output)
				}
				if !strings.Contains(result.Output, wantSkill[:len(wantSkill)-len("-only")]) {
					t.Fatalf("skill %q returned unexpected body: %q", wantSkill, result.Output)
				}
			}
		})
	}
}
