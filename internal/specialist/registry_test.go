package specialist

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/tools"
)

func TestRegisterToolsEmptyRosterKeepsGenerateOnly(t *testing.T) {
	registry := tools.NewRegistry()
	runtime, err := RegisterTools(registry, Executor{Paths: Paths{UserDir: t.TempDir()}}, []Manifest{})
	if err != nil {
		t.Fatalf("RegisterTools returned error: %v", err)
	}
	defer runtime.Close()

	for _, name := range []string{TaskToolName, "TaskOutput", "TaskStop"} {
		if _, ok := registry.Get(name); ok {
			t.Fatalf("%s must not be registered for an empty specialist roster", name)
		}
	}
	if _, ok := registry.Get("GenerateSpecialist"); !ok {
		t.Fatal("GenerateSpecialist must remain registered for an empty roster")
	}
}

func TestRegisterToolsNamesLoadedSpecialistsInTaskSchema(t *testing.T) {
	registry := tools.NewRegistry()
	roster := []Manifest{
		{Metadata: Metadata{Name: "reviewer"}},
		{Metadata: Metadata{Name: "builder"}},
	}
	runtime, err := RegisterTools(registry, Executor{Paths: Paths{UserDir: t.TempDir()}}, roster)
	if err != nil {
		t.Fatalf("RegisterTools returned error: %v", err)
	}
	defer runtime.Close()

	for _, name := range []string{TaskToolName, "TaskOutput", "TaskStop", "GenerateSpecialist"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("%s must be registered for a non-empty specialist roster", name)
		}
	}
	task, _ := registry.Get(TaskToolName)
	description := task.Parameters().Properties["name"].Description
	for _, name := range []string{"builder", "reviewer"} {
		if !strings.Contains(description, name) {
			t.Fatalf("Task name description missing loaded specialist %q: %q", name, description)
		}
	}
	for _, fabricated := range []string{"worker", "explorer", "code-review"} {
		if strings.Contains(description, fabricated) {
			t.Fatalf("Task name description contains fabricated specialist %q: %q", fabricated, description)
		}
	}
}
