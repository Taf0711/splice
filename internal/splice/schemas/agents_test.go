package schemas

import "testing"

func TestSelectedMemoryValidateAcceptsGraphIdentity(t *testing.T) {
	valid := []string{"observation:42", "graph:347"}
	for _, id := range valid {
		m := SelectedMemory{
			ID: id, Scope: MemoryScopeProject,
			Title: "t", Content: "c", MemoryType: "pattern",
		}
		if err := m.Validate(); err != nil {
			t.Errorf("identity %q rejected: %v", id, err)
		}
	}
	invalid := []string{"observation:0", "observation:042", "graph:0", "graph:", "42", "observation:-1", "graph:009"}
	for _, id := range invalid {
		m := SelectedMemory{
			ID: id, Scope: MemoryScopeProject,
			Title: "t", Content: "c", MemoryType: "pattern",
		}
		if err := m.Validate(); err == nil {
			t.Errorf("invalid identity %q accepted", id)
		}
	}
}
