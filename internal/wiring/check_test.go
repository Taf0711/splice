package wiring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContractsHold is the wiring guard. It fails when a registered producer is
// declared and never referenced in production code, or when the named consumer
// stops referencing it. A dropped reference is exactly the failure this system
// exists to catch.
func TestContractsHold(t *testing.T) {
	root, err := moduleRoot(".")
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	violations, err := Check(root, Contracts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, violation := range violations {
		t.Errorf("%s", violation.Error())
	}
}

// TestContractsNameExistingProofs fails when a contract names a proof test that
// does not exist. The proof test itself is run by the normal suite, so this
// check closes the gap between "the wiring is declared" and "the behavior is
// proven".
func TestContractsNameExistingProofs(t *testing.T) {
	root, err := moduleRoot(".")
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	_, tests, err := CheckWithTests(root, Contracts)
	if err != nil {
		t.Fatalf("CheckWithTests: %v", err)
	}
	for _, contract := range Contracts {
		if contract.Inert {
			continue
		}
		if contract.Proof == "" {
			t.Errorf("contract %s has no proof test", contract.ID)
			continue
		}
		if !tests[contract.Proof] {
			t.Errorf("contract %s names proof %s, which is not a test function", contract.ID, contract.Proof)
		}
	}
}

// TestContractsHaveNoDuplicateIDs keeps the registry readable and addressable.
func TestContractsHaveNoDuplicateIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, contract := range Contracts {
		if seen[contract.ID] {
			t.Errorf("duplicate contract id %q", contract.ID)
		}
		seen[contract.ID] = true
	}
}

// TestWiringCheckerDetectsDroppedReference proves the guard itself. A checker
// that only ever passes is worse than no checker, so this builds a fixture
// where the consumer drops the reference and asserts the violation fires. The
// AGENTS.md regression rule asks for guards to get the most adversarial tests.
func TestWiringCheckerDetectsDroppedReference(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "pkg/a.go", `package pkg

type Thing struct{ Field int }

func Read(t Thing) int { return t.Field }
`)
	writeFixture(t, dir, "pkg/b.go", `package pkg

func Drop(t Thing) int { return 0 }
`)
	writeFixture(t, dir, "pkg/c.go", `package pkg

type Unused struct{ Dead int }
`)

	t.Run("connected passes", func(t *testing.T) {
		violations, err := Check(dir, []Contract{{ID: "good", Producer: "pkg.Thing.Field", Consumer: "pkg.Read"}})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(violations) != 0 {
			t.Fatalf("connected contract reported %v", violations)
		}
	})

	t.Run("dropped reference fails", func(t *testing.T) {
		violations, err := Check(dir, []Contract{{ID: "dropped", Producer: "pkg.Thing.Field", Consumer: "pkg.Drop"}})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(violations) != 1 || !strings.Contains(violations[0].Message, "does not reference") {
			t.Fatalf("dropped contract violations = %v, want one does-not-reference", violations)
		}
	})

	t.Run("unreferenced producer fails", func(t *testing.T) {
		violations, err := Check(dir, []Contract{{ID: "unused", Producer: "pkg.Unused.Dead", Consumer: "pkg.Read"}})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		found := false
		for _, violation := range violations {
			if strings.Contains(violation.Message, "never referenced in production code") {
				found = true
			}
		}
		if !found {
			t.Fatalf("unreferenced producer violations = %v, want a never-referenced violation", violations)
		}
	})

	t.Run("missing producer fails", func(t *testing.T) {
		violations, err := Check(dir, []Contract{{ID: "ghost", Producer: "pkg.Thing.Ghost", Consumer: "pkg.Read"}})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(violations) == 0 || !strings.Contains(violations[0].Message, "not declared") {
			t.Fatalf("ghost producer violations = %v, want one not-declared violation", violations)
		}
	})

	t.Run("missing consumer fails", func(t *testing.T) {
		violations, err := Check(dir, []Contract{{ID: "ghost-consumer", Producer: "pkg.Thing.Field", Consumer: "pkg.Absent"}})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(violations) == 0 || !strings.Contains(violations[0].Message, "not a production function") {
			t.Fatalf("ghost consumer violations = %v, want one consumer violation", violations)
		}
	})
}

func writeFixture(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}
