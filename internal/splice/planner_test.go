package splice

import (
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestPlanCarriesTheFullRequestBesideTheIntentKey pins audit item E03: the
// plan carries the full request for the model stages. RequestIntent keeps the
// distilled value that memory, cognition, and receipt keys use.
func TestPlanCarriesTheFullRequestBesideTheIntentKey(t *testing.T) {
	request := "Bug report:\n```python\n" + strings.Repeat("value = compute(item)  # step\n", 70) + "```\nFix the crash."
	plan, err := BuildExecutionPlan(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Request != schemas.BoundRequest(request) {
		t.Fatalf("plan.Request does not carry the full request (%d bytes)", len(plan.Request))
	}
	if plan.RequestIntent != DistillRequestIntent(request) {
		t.Fatal("plan.RequestIntent must stay the distilled key value")
	}
}
