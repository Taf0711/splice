#!/usr/bin/env python3
"""Coupled families: tasks whose correct change also needs a file in another
package that the prompt does not name (plans/SPEC_JEV_DECISION_LAYER_V2,
phase P2).

Each family adds one item to a list in package P. A catalog in package Q must
list the same item, and a completeness test in Q (or in cmd/server) enforces
it. A cold run finds P from the prompt, misses Q, fails `go test ./...`, and
needs a repair iteration. Memory from the related write names the Q file, so a
warm read can change it in the first pass.

Usage:
  gen_coupled.py            write the fixture and the taskset
  gen_coupled.py --validate write, then run the validation matrix
"""

import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

EVALS = pathlib.Path(__file__).resolve().parent.parent
BASE_FIXTURE = EVALS / "large-families" / "fixture"
FIXTURE = EVALS / "coupled-families" / "fixture"
TASKS = EVALS / "warmcost" / "coupled-taskset" / "tasks"

# ---------------------------------------------------------------- fixture

FIXTURE_FILES = {
    "internal/config/catalog.go": '''package config

// The service catalog. Every kind that a subsystem can emit must be listed
// here: operators enable and route kinds from these lists, and the server
// refuses a kind that is not listed.

// NotificationKinds lists the notification template kinds the service may send.
var NotificationKinds = []string{
	"password_reset",
}

// WebhookEvents lists the webhook event types the service may deliver.
var WebhookEvents = []string{
	"invoice.paid",
}
''',
    "internal/notifications/catalog.go": '''package notifications

// Templates maps a notification kind to its template.
var Templates = map[string]Template{
	"password_reset": PasswordReset,
}

// RenderKind renders the template of one kind for one recipient.
func RenderKind(kind, to string) (string, bool) {
	t, ok := Templates[kind]
	if !ok {
		return "", false
	}
	return t.Render(to), true
}
''',
    "internal/webhooks/events.go": '''package webhooks

// Events maps each webhook event type to its human description.
var Events = map[string]string{
	"invoice.paid": "An invoice was paid",
}

// Describe returns the description of one event type.
func Describe(event string) (string, bool) {
	d, ok := Events[event]
	return d, ok
}
''',
    "cmd/server/jobs.go": '''package main

// jobOwners names the team that answers for each scheduled job. The on-call
// rota pages this team when the job fails.
var jobOwners = map[string]string{
	"expire-sessions": "identity",
	"close-invoices":  "billing",
	"prune-audit-log": "security",
}
''',
    "cmd/server/catalog_test.go": '''package main

import (
	"slices"
	"testing"

	"demo/internal/config"
	"demo/internal/notifications"
	"demo/internal/webhooks"
	"demo/internal/worker"
)

func TestEveryNotificationKindIsInTheCatalog(t *testing.T) {
	for kind := range notifications.Templates {
		if !slices.Contains(config.NotificationKinds, kind) {
			t.Errorf("notification kind %q has a template but the service catalog does not list it", kind)
		}
	}
}

func TestEveryWebhookEventIsInTheCatalog(t *testing.T) {
	for event := range webhooks.Events {
		if !slices.Contains(config.WebhookEvents, event) {
			t.Errorf("webhook event %q is described but the service catalog does not list it", event)
		}
	}
}

func TestEveryScheduledJobHasAnOwner(t *testing.T) {
	for _, s := range worker.DefaultSchedules {
		if jobOwners[s.Name] == "" {
			t.Errorf("scheduled job %q has no owning team", s.Name)
		}
	}
}
''',
    "internal/auth/roles.go": '''package auth

// Role names what a signed-in user may do.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
)

// Roles lists every role a user can hold.
var Roles = []Role{RoleViewer, RoleEditor}
''',
    "internal/admin/permissions.go": '''package admin

import "demo/internal/auth"

// RolePermissions grants each role its permissions in the admin console.
var RolePermissions = map[auth.Role][]string{
	auth.RoleViewer: {"read"},
	auth.RoleEditor: {"read", "write"},
}

// Can reports whether a role holds a permission.
func Can(role auth.Role, permission string) bool {
	for _, p := range RolePermissions[role] {
		if p == permission {
			return true
		}
	}
	return false
}
''',
    "internal/admin/pricing.go": '''package admin

// PricingOrder is the order in which the pricing page shows the plans.
var PricingOrder = []string{"free", "team", "scale"}
''',
    "internal/audit/actions.go": '''package audit

const (
	ActionPasswordReset = "password.reset"
	ActionLogin         = "auth.login"
)

// Actions lists every audit action the service records.
var Actions = []string{ActionPasswordReset, ActionLogin}
''',
    "internal/admin/labels.go": '''package admin

// ActionLabels are the console labels of the audit actions.
var ActionLabels = map[string]string{
	"password.reset": "Password reset",
	"auth.login":     "Sign-in",
}

// Label returns the console label of one audit action.
func Label(action string) string {
	return ActionLabels[action]
}
''',
    "internal/admin/catalog_test.go": '''package admin

import (
	"slices"
	"testing"

	"demo/internal/audit"
	"demo/internal/auth"
	"demo/internal/billing"
)

func TestEveryRoleHasPermissions(t *testing.T) {
	for _, role := range auth.Roles {
		if len(RolePermissions[role]) == 0 {
			t.Errorf("role %q grants no permissions in the admin console", role)
		}
	}
}

func TestPricingPageShowsEveryPlan(t *testing.T) {
	for _, plan := range billing.Catalog {
		if !slices.Contains(PricingOrder, plan.Name) {
			t.Errorf("plan %q is sold but the pricing page does not show it", plan.Name)
		}
	}
	for _, name := range PricingOrder {
		if _, ok := billing.Lookup(name); !ok {
			t.Errorf("the pricing page shows %q, which is not a plan", name)
		}
	}
}

func TestEveryAuditActionHasALabel(t *testing.T) {
	for _, action := range audit.Actions {
		if ActionLabels[action] == "" {
			t.Errorf("audit action %q has no console label", action)
		}
	}
}
''',
}

# ---------------------------------------------------------------- families
# edits: list of (path, old, new) applied to the fixture text. A path that
# does not exist is created with new as its whole content (old must be "").

def probe(pkg, name, body):
    return (pkg, name, body)

FAMILIES = [
    dict(
        id="cpl-01",
        a_prompt='In the notifications package, add an invoice-due template: kind "invoice_due", greeting "Hello", action "your invoice is due in 3 days", footer "demo service billing team".',
        b_prompt='In the notifications package, add an account-locked template: kind "account_locked", greeting "Hello", action "your account was locked after repeated failed sign-ins", footer "demo service security team".',
        a_probe=probe("notifications", "probe_invoice_due", '''import (
	"strings"
	"testing"
)

func TestProbeInvoiceDue(t *testing.T) {
	got, ok := RenderKind("invoice_due", "ann")
	if !ok || !strings.Contains(got, "your invoice is due in 3 days") || !strings.Contains(got, "ann") {
		t.Fatalf("RenderKind(invoice_due) = %q, %v", got, ok)
	}
}'''),
        b_probe=probe("notifications", "probe_account_locked", '''import (
	"strings"
	"testing"
)

func TestProbeAccountLocked(t *testing.T) {
	got, ok := RenderKind("account_locked", "bo")
	if !ok || !strings.Contains(got, "your account was locked after repeated failed sign-ins") {
		t.Fatalf("RenderKind(account_locked) = %q, %v", got, ok)
	}
}'''),
        gold_a=[
            ("internal/notifications/catalog.go", '"password_reset": PasswordReset,', '"password_reset": PasswordReset,\n\t"invoice_due": {Greeting: "Hello", Action: "your invoice is due in 3 days", Footer: "demo service billing team"},'),
            ("internal/config/catalog.go", '"password_reset",', '"password_reset",\n\t"invoice_due",'),
        ],
        gold_b=[
            ("internal/notifications/catalog.go", '"password_reset": PasswordReset,', '"password_reset": PasswordReset,\n\t"account_locked": {Greeting: "Hello", Action: "your account was locked after repeated failed sign-ins", Footer: "demo service security team"},'),
            ("internal/config/catalog.go", '"password_reset",', '"password_reset",\n\t"account_locked",'),
        ],
        coupled="internal/config/catalog.go",
    ),
    dict(
        id="cpl-02",
        a_prompt='In the webhooks package, add the webhook event "account.created" with the description "An account was created".',
        b_prompt='In the webhooks package, add the webhook event "account.deleted" with the description "An account was deleted".',
        a_probe=probe("webhooks", "probe_account_created", '''import "testing"

func TestProbeAccountCreated(t *testing.T) {
	if d, ok := Describe("account.created"); !ok || d != "An account was created" {
		t.Fatalf("Describe(account.created) = %q, %v", d, ok)
	}
}'''),
        b_probe=probe("webhooks", "probe_account_deleted", '''import "testing"

func TestProbeAccountDeleted(t *testing.T) {
	if d, ok := Describe("account.deleted"); !ok || d != "An account was deleted" {
		t.Fatalf("Describe(account.deleted) = %q, %v", d, ok)
	}
}'''),
        gold_a=[
            ("internal/webhooks/events.go", '"invoice.paid": "An invoice was paid",', '"invoice.paid": "An invoice was paid",\n\t"account.created": "An account was created",'),
            ("internal/config/catalog.go", '"invoice.paid",', '"invoice.paid",\n\t"account.created",'),
        ],
        gold_b=[
            ("internal/webhooks/events.go", '"invoice.paid": "An invoice was paid",', '"invoice.paid": "An invoice was paid",\n\t"account.deleted": "An account was deleted",'),
            ("internal/config/catalog.go", '"invoice.paid",', '"invoice.paid",\n\t"account.deleted",'),
        ],
        coupled="internal/config/catalog.go",
    ),
    dict(
        id="cpl-03",
        a_prompt='In the worker package, schedule a "rotate-keys" job that runs every 24 hours. The security team owns it.',
        b_prompt='In the worker package, schedule a "purge-blobs" job that runs every 12 hours. The storage team owns it.',
        a_probe=probe("worker", "probe_rotate_keys", '''import (
	"testing"
	"time"
)

func TestProbeRotateKeys(t *testing.T) {
	for _, s := range DefaultSchedules {
		if s.Name == "rotate-keys" && s.Every == 24*time.Hour {
			return
		}
	}
	t.Fatal("no rotate-keys schedule every 24h")
}'''),
        b_probe=probe("worker", "probe_purge_blobs", '''import (
	"testing"
	"time"
)

func TestProbePurgeBlobs(t *testing.T) {
	for _, s := range DefaultSchedules {
		if s.Name == "purge-blobs" && s.Every == 12*time.Hour {
			return
		}
	}
	t.Fatal("no purge-blobs schedule every 12h")
}'''),
        gold_a=[
            ("internal/worker/cron.go", '{Name: "prune-audit-log", Every: 6 * time.Hour},', '{Name: "prune-audit-log", Every: 6 * time.Hour},\n\t{Name: "rotate-keys", Every: 24 * time.Hour},'),
            ("cmd/server/jobs.go", '"prune-audit-log": "security",', '"prune-audit-log": "security",\n\t"rotate-keys":     "security",'),
        ],
        gold_b=[
            ("internal/worker/cron.go", '{Name: "prune-audit-log", Every: 6 * time.Hour},', '{Name: "prune-audit-log", Every: 6 * time.Hour},\n\t{Name: "purge-blobs", Every: 12 * time.Hour},'),
            ("cmd/server/jobs.go", '"prune-audit-log": "security",', '"prune-audit-log": "security",\n\t"purge-blobs":     "storage",'),
        ],
        coupled="cmd/server/jobs.go",
    ),
    dict(
        id="cpl-04",
        a_prompt='In the auth package, add an "auditor" role (RoleAuditor) that users can hold. Auditors hold the permissions "read" and "export".',
        b_prompt='In the auth package, add a "billing_admin" role (RoleBillingAdmin) that users can hold. Billing admins hold the permissions "read" and "billing".',
        a_probe=probe("admin", "probe_role_auditor", '''import (
	"slices"
	"testing"

	"demo/internal/auth"
)

func TestProbeRoleAuditor(t *testing.T) {
	if !slices.Contains(auth.Roles, auth.RoleAuditor) || auth.RoleAuditor != "auditor" {
		t.Fatal("auth.Roles lacks the auditor role")
	}
	if !Can(auth.RoleAuditor, "read") || !Can(auth.RoleAuditor, "export") || Can(auth.RoleAuditor, "write") {
		t.Fatal("auditor permissions are not read and export")
	}
}'''),
        b_probe=probe("admin", "probe_role_billing_admin", '''import (
	"slices"
	"testing"

	"demo/internal/auth"
)

func TestProbeRoleBillingAdmin(t *testing.T) {
	if !slices.Contains(auth.Roles, auth.RoleBillingAdmin) || auth.RoleBillingAdmin != "billing_admin" {
		t.Fatal("auth.Roles lacks the billing_admin role")
	}
	if !Can(auth.RoleBillingAdmin, "read") || !Can(auth.RoleBillingAdmin, "billing") || Can(auth.RoleBillingAdmin, "write") {
		t.Fatal("billing_admin permissions are not read and billing")
	}
}'''),
        gold_a=[
            ("internal/auth/roles.go", 'RoleEditor Role = "editor"', 'RoleEditor Role = "editor"\n\tRoleAuditor Role = "auditor"'),
            ("internal/auth/roles.go", "var Roles = []Role{RoleViewer, RoleEditor}", "var Roles = []Role{RoleViewer, RoleEditor, RoleAuditor}"),
            ("internal/admin/permissions.go", 'auth.RoleEditor: {"read", "write"},', 'auth.RoleEditor: {"read", "write"},\n\tauth.RoleAuditor: {"read", "export"},'),
        ],
        gold_b=[
            ("internal/auth/roles.go", 'RoleEditor Role = "editor"', 'RoleEditor Role = "editor"\n\tRoleBillingAdmin Role = "billing_admin"'),
            ("internal/auth/roles.go", "RoleViewer, RoleEditor", "RoleViewer, RoleEditor, RoleBillingAdmin"),
            ("internal/admin/permissions.go", 'auth.RoleEditor: {"read", "write"},', 'auth.RoleEditor: {"read", "write"},\n\tauth.RoleBillingAdmin: {"read", "billing"},'),
        ],
        coupled="internal/admin/permissions.go",
    ),
    dict(
        id="cpl-05",
        a_prompt='In the billing package, add a "startup" subscription plan: 900 cents a month, 3 seats. Customers see it between free and team.',
        b_prompt='In the billing package, add an "enterprise" subscription plan: 49900 cents a month, 1000 seats. Customers see it after scale.',
        a_probe=probe("admin", "probe_plan_startup", '''import (
	"slices"
	"testing"

	"demo/internal/billing"
)

func TestProbePlanStartup(t *testing.T) {
	p, ok := billing.Lookup("startup")
	if !ok || p.MonthlyCents != 900 || p.Seats != 3 {
		t.Fatalf("billing.Lookup(startup) = %+v, %v", p, ok)
	}
	i, free, team := slices.Index(PricingOrder, "startup"), slices.Index(PricingOrder, "free"), slices.Index(PricingOrder, "team")
	if i < 0 || !(free < i && i < team) {
		t.Fatalf("pricing order %v: startup is not between free and team", PricingOrder)
	}
}'''),
        b_probe=probe("admin", "probe_plan_enterprise", '''import (
	"slices"
	"testing"

	"demo/internal/billing"
)

func TestProbePlanEnterprise(t *testing.T) {
	p, ok := billing.Lookup("enterprise")
	if !ok || p.MonthlyCents != 49900 || p.Seats != 1000 {
		t.Fatalf("billing.Lookup(enterprise) = %+v, %v", p, ok)
	}
	if i := slices.Index(PricingOrder, "enterprise"); i < 0 || i < slices.Index(PricingOrder, "scale") {
		t.Fatalf("pricing order %v: enterprise is not after scale", PricingOrder)
	}
}'''),
        gold_a=[
            ("internal/billing/plans.go", '{Name: "free", MonthlyCents: 0, Seats: 1},', '{Name: "free", MonthlyCents: 0, Seats: 1},\n\t{Name: "startup", MonthlyCents: 900, Seats: 3},'),
            ("internal/admin/pricing.go", '[]string{"free", "team", "scale"}', '[]string{"free", "startup", "team", "scale"}'),
        ],
        gold_b=[
            ("internal/billing/plans.go", '{Name: "scale", MonthlyCents: 9900, Seats: 100},', '{Name: "scale", MonthlyCents: 9900, Seats: 100},\n\t{Name: "enterprise", MonthlyCents: 49900, Seats: 1000},'),
            ("internal/admin/pricing.go", '"team", "scale"}', '"team", "scale", "enterprise"}'),
        ],
        coupled="internal/admin/pricing.go",
    ),
    dict(
        id="cpl-06",
        a_prompt='In the audit package, add the audit action "session.revoked" (ActionSessionRevoked) to the recorded actions. Its label is "Session revoked".',
        b_prompt='In the audit package, add the audit action "invoice.voided" (ActionInvoiceVoided) to the recorded actions. Its label is "Invoice voided".',
        a_probe=probe("admin", "probe_action_session_revoked", '''import (
	"slices"
	"testing"

	"demo/internal/audit"
)

func TestProbeActionSessionRevoked(t *testing.T) {
	if audit.ActionSessionRevoked != "session.revoked" || !slices.Contains(audit.Actions, audit.ActionSessionRevoked) {
		t.Fatal("audit.Actions lacks session.revoked")
	}
	if Label("session.revoked") != "Session revoked" {
		t.Fatalf("Label(session.revoked) = %q", Label("session.revoked"))
	}
}'''),
        b_probe=probe("admin", "probe_action_invoice_voided", '''import (
	"slices"
	"testing"

	"demo/internal/audit"
)

func TestProbeActionInvoiceVoided(t *testing.T) {
	if audit.ActionInvoiceVoided != "invoice.voided" || !slices.Contains(audit.Actions, audit.ActionInvoiceVoided) {
		t.Fatal("audit.Actions lacks invoice.voided")
	}
	if Label("invoice.voided") != "Invoice voided" {
		t.Fatalf("Label(invoice.voided) = %q", Label("invoice.voided"))
	}
}'''),
        gold_a=[
            ("internal/audit/actions.go", 'ActionLogin         = "auth.login"', 'ActionLogin         = "auth.login"\n\tActionSessionRevoked = "session.revoked"'),
            ("internal/audit/actions.go", "var Actions = []string{ActionPasswordReset, ActionLogin}", "var Actions = []string{ActionPasswordReset, ActionLogin, ActionSessionRevoked}"),
            ("internal/admin/labels.go", '"auth.login":     "Sign-in",', '"auth.login":     "Sign-in",\n\t"session.revoked": "Session revoked",'),
        ],
        gold_b=[
            ("internal/audit/actions.go", 'ActionLogin         = "auth.login"', 'ActionLogin         = "auth.login"\n\tActionInvoiceVoided = "invoice.voided"'),
            ("internal/audit/actions.go", "ActionPasswordReset, ActionLogin", "ActionPasswordReset, ActionLogin, ActionInvoiceVoided"),
            ("internal/admin/labels.go", '"auth.login":     "Sign-in",', '"auth.login":     "Sign-in",\n\t"invoice.voided": "Invoice voided",'),
        ],
        coupled="internal/admin/labels.go",
    ),
]


def check(p, comment):
    pkg, name, body = p
    return f"""# {comment}
mkdir -p internal/{pkg}
cat > internal/{pkg}/{name}_test.go <<'EOF'
package {pkg}

{body}
EOF
go test -count=1 ./... ; rc=$?
rm -f internal/{pkg}/{name}_test.go
exit $rc
"""


def write_fixture():
    # Files are overwritten in place, not deleted and rewritten: in a synced
    # folder (iCloud) a delete-and-rewrite leaves "name 2.ext" copies.
    shutil.copytree(BASE_FIXTURE, FIXTURE, dirs_exist_ok=True)
    for rel, body in FIXTURE_FILES.items():
        path = FIXTURE / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body)


def write_tasks():
    TASKS.mkdir(parents=True, exist_ok=True)
    for f in FAMILIES:
        write = dict(name=f"{f['id']}-write", phase="write", prompt=f["a_prompt"],
                     check=check(f["a_probe"], f"{f['id']} A"), fixture="../../coupled-families/fixture")
        read = dict(name=f"{f['id']}-read", phase="read", precursor=f"{f['id']}-write", prompt=f["b_prompt"],
                    check=check(f["b_probe"], f"{f['id']} B"), fixture="../../coupled-families/fixture")
        (TASKS / f"{f['id']}-write.json").write_text(json.dumps(write, indent=2) + "\n")
        (TASKS / f"{f['id']}-read.json").write_text(json.dumps(read, indent=2) + "\n")
    manifest = [dict(id=f["id"], coupled=f["coupled"]) for f in FAMILIES]
    (TASKS.parent / "coupled.json").write_text(json.dumps(manifest, indent=2) + "\n")


def apply(repo, edits):
    for rel, old, new in edits:
        path = repo / rel
        text = path.read_text()
        if text.count(old) != 1:
            raise RuntimeError(f"{rel}: anchor {old!r} found {text.count(old)} times")
        path.write_text(text.replace(old, new))


def run(cmd, repo):
    proc = subprocess.run(["/bin/bash", "-c", cmd], cwd=repo, capture_output=True, text=True)
    return proc.returncode, (proc.stdout + proc.stderr)[-400:]


def validate():
    failures = []

    def fresh():
        repo = pathlib.Path(tempfile.mkdtemp(prefix="coupled-validate-"))
        shutil.copytree(FIXTURE, repo, dirs_exist_ok=True)
        return repo

    repo = fresh()
    rc, out = run("go vet ./... && go test -count=1 ./...", repo)
    if rc != 0:
        failures.append(f"base fixture does not build and pass: {out}")
    shutil.rmtree(repo)

    for f in FAMILIES:
        a, b = check(f["a_probe"], "a"), check(f["b_probe"], "b")
        # Only the coupled half of each gold: the feature without the
        # catalog entry. It must fail, so the coupling is enforced.
        def feature_only(edits):
            return [e for e in edits if e[0] != f["coupled"]]
        cases = [
            ("A fails on base", [], a, False),
            ("A passes on gold A", f["gold_a"], a, True),
            ("A fails without the coupling", feature_only(f["gold_a"]), a, False),
            ("B fails on gold A", f["gold_a"], b, False),
            ("B passes on gold A + gold B", f["gold_a"] + f["gold_b"], b, True),
            ("B fails without the coupling", f["gold_a"] + feature_only(f["gold_b"]), b, False),
        ]
        for name, edits, script, want_pass in cases:
            repo = fresh()
            try:
                apply(repo, edits)
                rc, out = run(script, repo)
            except RuntimeError as err:
                rc, out = 99, str(err)
            if (rc == 0) != want_pass:
                failures.append(f"{f['id']}: {name}: rc={rc} {out}")
            shutil.rmtree(repo)
        print(f"{f['id']}: checked {len(cases)} cases")
    if failures:
        print("FAILURES:")
        for x in failures:
            print(" ", x)
        sys.exit(1)
    print("all families valid")


if __name__ == "__main__":
    write_fixture()
    write_tasks()
    if "--validate" in sys.argv:
        validate()
