package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestFormatDesignPlanRendersAutomatedAcceptanceCommandVerbatim(t *testing.T) {
	command := "go test ./internal/tui -run TestFormatDesignPlanRendersAutomatedAcceptanceCommandVerbatim -count=1"
	got := formatDesignPlan(schemas.DesignPlan{
		Epic: "Show acceptance facts",
		Tasks: []schemas.Task{{
			Title: "Render the plan",
			AcceptanceFacts: []schemas.AcceptanceFact{{
				Statement:             "The plan shows the command before execution.",
				AutomatedVerification: true,
				VerificationCommand:   &command,
			}},
		}},
	})
	if !strings.Contains(got, command) {
		t.Fatalf("formatted plan = %q, want command %q verbatim", got, command)
	}
}

func TestFormatDesignPlanBoundsAcceptanceFactsPerTask(t *testing.T) {
	facts := make([]schemas.AcceptanceFact, 0, 5)
	for i := 0; i < 5; i++ {
		facts = append(facts, schemas.AcceptanceFact{Statement: "Acceptance fact " + string(rune('1'+i))})
	}
	got := formatDesignPlan(schemas.DesignPlan{
		Epic:  "Bound acceptance facts",
		Tasks: []schemas.Task{{Title: "Render the plan", AcceptanceFacts: facts}},
	})
	if strings.Contains(got, "Acceptance fact 4") || strings.Contains(got, "Acceptance fact 5") {
		t.Fatalf("formatted plan = %q, want later facts elided", got)
	}
	if !strings.Contains(got, "... and 2 more acceptance facts") {
		t.Fatalf("formatted plan = %q, want elided count", got)
	}
}

func TestFormatDesignPlanRendersManualAcceptanceStatementWithoutCommand(t *testing.T) {
	got := formatDesignPlan(schemas.DesignPlan{
		Epic: "Show manual acceptance facts",
		Tasks: []schemas.Task{{
			Title: "Render the plan",
			AcceptanceFacts: []schemas.AcceptanceFact{{
				Statement: "A reviewer confirms the wording.",
			}},
		}},
	})
	if !strings.Contains(got, "A reviewer confirms the wording.") {
		t.Fatalf("formatted plan = %q, want manual statement", got)
	}
	if strings.Contains(got, "Automated verification command:") {
		t.Fatalf("formatted plan = %q, want no command for manual fact", got)
	}
}

// A cap that elides any criterion carrying a command hides shell the user is
// about to approve. Measured against real plans, a flat cap of three concealed
// 11 commands across 6 tasks. Only criteria that run nothing may be capped.
func TestFormatDesignPlanNeverHidesAVerificationCommand(t *testing.T) {
	facts := make([]schemas.AcceptanceFact, 0, 8)
	for i := 0; i < 8; i++ {
		command := fmt.Sprintf("run-check-%d --strict", i)
		facts = append(facts, schemas.AcceptanceFact{
			Statement:             fmt.Sprintf("criterion %d holds", i),
			AutomatedVerification: true,
			VerificationCommand:   &command,
		})
	}
	formatted := formatDesignPlan(schemas.DesignPlan{
		Epic:  "Many automated criteria",
		Tasks: []schemas.Task{{Title: "Verify everything", AcceptanceFacts: facts}},
	})
	for i := 0; i < 8; i++ {
		want := fmt.Sprintf("run-check-%d --strict", i)
		if !strings.Contains(formatted, want) {
			t.Fatalf("plan hid command %q:\n%s", want, formatted)
		}
	}
}
