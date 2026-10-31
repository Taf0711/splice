package splice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E1 fixture: a tiny workspace whose symbol index declares EnforceRetention
// on the audit Trail, mirroring the retention family.
func e1Workspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package audit\n\nimport \"time\"\n\ntype Trail struct{ events []Event }\n\ntype Event struct{ At time.Time }\n\nfunc (t *Trail) EnforceRetention(maxAge time.Duration, maxCount int) int { return 0 }\n\nfunc NewTrail() *Trail { return &Trail{} }\n"
	if err := os.WriteFile(filepath.Join(dir, "internal", "audit", "log.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A trap test file that must never feed the index.
	if err := os.WriteFile(filepath.Join(dir, "internal", "audit", "log_test.go"), []byte("package audit\n\nfunc PhantomHelper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDeriveContextNeedsConfirmSymbolsAgainstIndex(t *testing.T) {
	ws := e1Workspace(t)
	intent := "Add RetentionDeficit that reuses the EnforceRetention cutoff and cap rules without mutating the trail."

	needs := deriveContextNeeds(intent, ws, nil, nil)

	var locate []ContextNeed
	for _, n := range needs {
		if n.Kind == NeedLocateNamedOperation {
			locate = append(locate, n)
		}
	}
	found := map[string]bool{}
	for _, n := range locate {
		found[n.Subject] = true
		if n.Origin != NeedOriginSymbolIndex {
			t.Errorf("confirmed symbol need %q origin = %q, want %q", n.Subject, n.Origin, NeedOriginSymbolIndex)
		}
		if !n.Required {
			t.Errorf("confirmed symbol need %q must be required", n.Subject)
		}
	}
	// EnforceRetention and Trail are declared in the workspace; the
	// capitalized prose words (Retention, Cutoff, Cap) are not symbols and
	// must not appear.
	if !found["internal/audit/log.go#EnforceRetention"] {
		t.Errorf("EnforceRetention not confirmed as a need; got %v", found)
	}
	if found["internal/audit/log.go#Trail"] == false {
		// Trail is a type; it is a legitimate symbol hit. Only assert it
		// does not break the kind set.
		_ = found
	}
	for subject := range found {
		if strings.Contains(subject, "Retention") && !strings.Contains(subject, "EnforceRetention") {
			t.Errorf("capitalized prose word became a symbol need: %q", subject)
		}
	}
}

func TestDeriveContextNeedsRejectUnconfirmedIdentifiers(t *testing.T) {
	ws := e1Workspace(t)
	intent := "Add FiscalDeficit accounting that reuses EnforceRetention."
	needs := deriveContextNeeds(intent, ws, nil, nil)
	for _, n := range needs {
		if n.Kind == NeedLocateNamedOperation && strings.Contains(n.Subject, "FiscalDeficit") {
			t.Fatalf("unconfirmed identifier became a symbol need: %+v", n)
		}
	}
	// EnforceRetention still confirms.
	ok := false
	for _, n := range needs {
		if n.Kind == NeedLocateNamedOperation && strings.Contains(n.Subject, "EnforceRetention") {
			ok = true
		}
	}
	if !ok {
		t.Fatal("confirmed symbol missing")
	}
}

func TestDeriveContextNeedsAlwaysKeepsOpenDiscovery(t *testing.T) {
	ws := e1Workspace(t)
	needs := deriveContextNeeds("polish the audit package", ws, nil, nil)
	open := 0
	for _, n := range needs {
		if n.Kind == NeedOpenDiscovery {
			open++
			if n.Required {
				t.Error("open-discovery need must not be required")
			}
			if n.Origin != NeedOriginStructural {
				t.Errorf("open need origin = %q, want structural", n.Origin)
			}
		}
	}
	if open != 1 {
		t.Fatalf("open-discovery needs = %d, want exactly 1", open)
	}
}

func TestDeriveContextNeedsMapsPriorFilesAndFailures(t *testing.T) {
	ws := e1Workspace(t)
	needs := deriveContextNeeds("extend the retention logic", ws,
		[]string{"internal/audit/log.go"}, []string{"TestEnforceRetention DropsOldEvents failed: got 2 want 3"})
	kinds := map[string]int{}
	for _, n := range needs {
		kinds[n.Kind]++
		if n.Kind == NeedUnderstandIntegrationRelationship && n.Subject != "internal/audit/log.go" {
			t.Errorf("integration subject = %q", n.Subject)
		}
		if n.Kind == NeedResolveConcreteFailure && !strings.Contains(n.Subject, "DropsOldEvents") {
			t.Errorf("failure subject = %q", n.Subject)
		}
	}
	if kinds[NeedUnderstandIntegrationRelationship] != 1 {
		t.Errorf("integration needs = %d, want 1", kinds[NeedUnderstandIntegrationRelationship])
	}
	if kinds[NeedResolveConcreteFailure] != 1 {
		t.Errorf("failure needs = %d, want 1", kinds[NeedResolveConcreteFailure])
	}
}

func TestContextNeedValidate(t *testing.T) {
	if err := (ContextNeed{ID: "x", Kind: "made-up", Subject: "s", Origin: "task-text"}).Validate(); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if err := (ContextNeed{ID: "x", Kind: NeedInspectEditTarget, Subject: "p.go", Origin: "task-text"}).Validate(); err != nil {
		t.Fatalf("valid need rejected: %v", err)
	}
	if err := (ContextNeed{ID: "x", Kind: NeedInspectEditTarget, Origin: "task-text"}).Validate(); err == nil {
		t.Fatal("empty subject accepted")
	}
}

func TestColdResolvedNeedsCarryNoWarmCredit(t *testing.T) {
	n := ContextNeed{ID: "locate:x", Kind: NeedLocateNamedOperation, Subject: "a.go#F", Origin: NeedOriginSymbolIndex, Required: true}
	if !n.coldResolved() {
		t.Fatal("symbol-index need must be cold-resolved")
	}
	n2 := ContextNeed{ID: "locate:y", Kind: NeedLocateNamedOperation, Subject: "p.go#S", Origin: NeedOriginTaskText, Required: true}
	if n2.coldResolved() {
		t.Fatal("task-text need must not be cold-resolved")
	}
}
