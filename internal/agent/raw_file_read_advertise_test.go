package agent

// B1 review fix regression (advertisement side): raw_file_read must never
// appear on the model's tool surface in any permission mode. The
// registry-side execution gate lives in the tools package's
// raw_file_read_test.go. This file pins assertion (1) via the same
// ToolAdvertised gate the loop's tool partition uses.

import (
	"testing"

	"github.com/Taf0711/splice/internal/tools"
)

// TestRawFileReadNotModelFacingInAnyMode pins: the tool is never
// advertised in auto, member-auto, spec-draft, ask, or unsafe. The probe
// defect was auto/member-auto leaking it because readOnlySafety set
// PermissionAllow; the fix keys advertisement off PermissionDeny, which
// ToolAdvertised rejects before any mode-specific logic runs.
func TestRawFileReadNotModelFacingInAnyMode(t *testing.T) {
	tool := tools.NewScopedRawFileReadTool(t.TempDir(), nil)
	if tool.Safety().Permission != tools.PermissionDeny {
		t.Fatalf("raw_file_read permission = %q, want deny (ToolAdvertised keys on Deny)", tool.Safety().Permission)
	}
	if hs, ok := tool.(tools.HostSeamTool); !ok || !hs.HostSeamOnly() {
		t.Fatal("raw_file_read must declare itself host-seam-only")
	}
	modes := []PermissionMode{
		PermissionModeAuto,
		PermissionModeMemberAuto,
		PermissionModeSpecDraft,
		PermissionModeAsk,
		PermissionModeUnsafe,
	}
	for _, mode := range modes {
		if ToolAdvertised(tool, mode) {
			t.Errorf("raw_file_read advertised in %s mode; the model must never see it", mode)
		}
		// ToolVisible is ToolAllowedByFilters && ToolAdvertised; with no
		// operator filters the filter gate passes everything, so Visible
		// must be false exactly when Advertised is false.
		if ToolVisible(tool, mode, nil, nil) {
			t.Errorf("raw_file_read visible via ToolVisible in %s mode", mode)
		}
	}
}
