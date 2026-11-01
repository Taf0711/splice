import json, os
ROOT = "/Users/tafseerhaque/Documents/splice-cognition/tests/evals"
LF = ROOT + "/large-families"
TS = ROOT + "/warmcost/large-taskset/tasks"

def check(pkg, probe_name, probe_body, comment):
    return f"""# {comment}
mkdir -p internal/{pkg}
cat > internal/{pkg}/{probe_name}_test.go <<'EOF'
package {pkg}

{probe_body}
EOF
go test -count=1 ./internal/{pkg}/ ; rc=$?
rm -f internal/{pkg}/{probe_name}_test.go
if [ $rc -ne 0 ]; then exit $rc; fi
exit 0
"""

def overlay(pkg, fname, body, comment):
    return f"""#!/bin/bash
# {comment}
set -e
cd "$1"
mkdir -p internal/{pkg}
cat > internal/{pkg}/{fname} <<'GOEOF'
package {pkg}

{body}
GOEOF
"""

F = []

# ---- large-03 billing usage
F.append(dict(id="large-03", pkg="billing",
 a_prompt="Add OverageUnits(m *Meter, accountID, sku string, included int) int in the billing package: the units of sku the account used beyond the included amount, never negative. Add a regression test.",
 b_prompt="Add OverageCents(m *Meter, accountID, sku string, included int, unitCents int64) int64 in the billing package: unitCents for each unit used beyond the included amount. Reuse OverageUnits; do not re-derive the overage rule. Add a regression test.",
 a_probe="""import "testing"

func TestProbeOverageUnits(t *testing.T) {
	m := NewMeter()
	m.Record("acct", "api", 13)
	m.Record("acct", "seats", 2)
	m.Record("other", "api", 50)
	if got := OverageUnits(m, "acct", "api", 10); got != 3 {
		t.Fatalf("OverageUnits over = %d, want 3", got)
	}
	if got := OverageUnits(m, "acct", "seats", 5); got != 0 {
		t.Fatalf("OverageUnits under = %d, want 0", got)
	}
	if got := OverageUnits(m, "nobody", "api", 0); got != 0 {
		t.Fatalf("OverageUnits unknown = %d, want 0", got)
	}
}""",
 b_probe="""import "testing"

func TestProbeOverageCents(t *testing.T) {
	m := NewMeter()
	m.Record("acct", "api", 13)
	m.Record("acct", "storage", 40)
	if got := OverageCents(m, "acct", "api", 10, 5); got != 15 {
		t.Fatalf("OverageCents = %d, want 15 (3 units over x 5c)", got)
	}
	if got := OverageCents(m, "acct", "api", 20, 5); got != 0 {
		t.Fatalf("OverageCents under = %d, want 0", got)
	}
}""",
 a_file="overage.go", a_body="""// OverageUnits reports the units of sku used beyond included, never negative.
func OverageUnits(m *Meter, accountID, sku string, included int) int {
	over := m.Snapshot(accountID)[sku] - included
	if over < 0 {
		return 0
	}
	return over
}""",
 b_file="overage_cents.go", b_body="""// OverageCents charges unitCents per unit beyond the included amount.
func OverageCents(m *Meter, accountID, sku string, included int, unitCents int64) int64 {
	return int64(OverageUnits(m, accountID, sku, included)) * unitCents
}""",
 w_body="""// OverageCents re-derives the overage over every SKU of the account.
func OverageCents(m *Meter, accountID, sku string, included int, unitCents int64) int64 {
	total := 0
	for _, n := range m.Snapshot(accountID) {
		total += n
	}
	if total <= included {
		return 0
	}
	return int64(total-included) * unitCents
}"""))

# ---- large-04 billing proration
F.append(dict(id="large-04", pkg="billing",
 a_prompt="Add ProrateDays(monthlyCents int64, days int) int64 in the billing package: the charge for that many days of a 30-day month, truncated to whole cents, and 0 for a negative day count. Add a regression test.",
 b_prompt="Add PlanChangeCost(fromPlan, toPlan string, daysLeft int) (int64, bool) in the billing package: the prorated charge of the new plan minus the old plan for the remaining days, and false when a plan name is unknown. Reuse ProrateDays and Lookup. Add a regression test.",
 a_probe="""import "testing"

func TestProbeProrateDays(t *testing.T) {
	if got := ProrateDays(2900, 15); got != 1450 {
		t.Fatalf("ProrateDays(2900,15) = %d, want 1450", got)
	}
	if got := ProrateDays(1000, 7); got != 233 {
		t.Fatalf("ProrateDays(1000,7) = %d, want 233 (truncated)", got)
	}
	if got := ProrateDays(1000, -3); got != 0 {
		t.Fatalf("ProrateDays negative = %d, want 0", got)
	}
}""",
 b_probe="""import "testing"

func TestProbePlanChangeCost(t *testing.T) {
	got, ok := PlanChangeCost("team", "scale", 15)
	if !ok || got != 3500 {
		t.Fatalf("team->scale 15d = %d,%v, want 3500,true", got, ok)
	}
	got, ok = PlanChangeCost("scale", "team", 7)
	if !ok || got != -1634 {
		t.Fatalf("scale->team 7d = %d,%v, want -1634,true", got, ok)
	}
	if _, ok := PlanChangeCost("team", "gold", 10); ok {
		t.Fatal("unknown plan must report false")
	}
}""",
 a_file="prorate_days.go", a_body="""// ProrateDays charges days of a 30-day month, truncated to whole cents.
func ProrateDays(monthlyCents int64, days int) int64 {
	if days < 0 {
		return 0
	}
	return monthlyCents * int64(days) / 30
}""",
 b_file="plan_change.go", b_body="""// PlanChangeCost is the new plan's prorated charge minus the old plan's.
func PlanChangeCost(fromPlan, toPlan string, daysLeft int) (int64, bool) {
	from, ok := Lookup(fromPlan)
	if !ok {
		return 0, false
	}
	to, ok := Lookup(toPlan)
	if !ok {
		return 0, false
	}
	return ProrateDays(to.MonthlyCents, daysLeft) - ProrateDays(from.MonthlyCents, daysLeft), true
}""",
 w_body="""// PlanChangeCost prorates the monthly difference in one step.
func PlanChangeCost(fromPlan, toPlan string, daysLeft int) (int64, bool) {
	from, ok := Lookup(fromPlan)
	if !ok {
		return 0, false
	}
	to, ok := Lookup(toPlan)
	if !ok {
		return 0, false
	}
	return (to.MonthlyCents - from.MonthlyCents) * int64(daysLeft) / 30, true
}"""))

# ---- large-05 notifications templates
F.append(dict(id="large-05", pkg="notifications",
 a_prompt="Add a Welcome Template variable in the notifications package (Greeting \"Welcome\", Action \"your account is ready\", Footer \"demo service team\") and RenderFor(t Template, to string) Message: an email Message to that recipient with Subject t.Action and Body t.Render(to). Add a regression test.",
 b_prompt="Add QueueWelcome(d *Dispatcher, to string) in the notifications package: queue the welcome email for that recipient. Reuse RenderFor with the Welcome template; do not build the Message by hand. Add a regression test.",
 a_probe="""import "testing"

func TestProbeRenderFor(t *testing.T) {
	m := RenderFor(Welcome, "ada")
	if m.Kind != KindEmail || m.To != "ada" || m.Subject != "your account is ready" {
		t.Fatalf("RenderFor = %+v", m)
	}
	if m.Body != Welcome.Render("ada") {
		t.Fatalf("Body = %q, want the rendered template", m.Body)
	}
	if Welcome.Footer != "demo service team" {
		t.Fatalf("Welcome footer = %q", Welcome.Footer)
	}
}""",
 b_probe="""import "testing"

func TestProbeQueueWelcome(t *testing.T) {
	d := NewDispatcher()
	QueueWelcome(d, "ada")
	out := d.Flush()
	if len(out) != 1 || out[0] != "email|ada|your account is ready" {
		t.Fatalf("Flush = %v", out)
	}
}""",
 a_file="welcome.go", a_body="""// Welcome is the template for new-account mail.
var Welcome = Template{
	Greeting: "Welcome",
	Action:   "your account is ready",
	Footer:   "demo service team",
}

// RenderFor builds the email Message for one recipient from a template.
func RenderFor(t Template, to string) Message {
	return Message{Kind: KindEmail, To: to, Subject: t.Action, Body: t.Render(to)}
}""",
 b_file="queue_welcome.go", b_body="""// QueueWelcome queues the welcome email for one recipient.
func QueueWelcome(d *Dispatcher, to string) {
	d.Queue(RenderFor(Welcome, to))
}""",
 w_body="""// QueueWelcome builds the welcome message by hand.
func QueueWelcome(d *Dispatcher, to string) {
	d.Queue(Message{Kind: KindEmail, To: to, Subject: "Welcome", Body: "Welcome " + to})
}"""))

# ---- large-06 notifications throttle
F.append(dict(id="large-06", pkg="notifications",
 a_prompt="Add (t *Throttle) Remaining(to string) int in the notifications package: how many more notifications that recipient may still receive under the limit, never negative, without using any of the allowance. Add a regression test.",
 b_prompt="Add Headroom(t *Throttle, recipients []string) int in the notifications package: the total notifications the recipients can still receive. Reuse Throttle.Remaining; Headroom must not use any allowance. Add a regression test.",
 a_probe="""import "testing"

func TestProbeRemaining(t *testing.T) {
	th := NewThrottle(3)
	th.Allow("a")
	if got := th.Remaining("a"); got != 2 {
		t.Fatalf("Remaining(a) = %d, want 2", got)
	}
	if got := th.Remaining("a"); got != 2 {
		t.Fatalf("Remaining must not use allowance, second call = %d", got)
	}
	if got := th.Remaining("b"); got != 3 {
		t.Fatalf("Remaining(b) = %d, want 3", got)
	}
}""",
 b_probe="""import "testing"

func TestProbeHeadroom(t *testing.T) {
	th := NewThrottle(3)
	th.Allow("a")
	if got := Headroom(th, []string{"a", "b"}); got != 5 {
		t.Fatalf("Headroom = %d, want 5", got)
	}
	if got := Headroom(th, []string{"a", "b"}); got != 5 {
		t.Fatalf("Headroom must not use allowance, second call = %d", got)
	}
}""",
 a_file="remaining.go", a_body="""// Remaining reports how many more notifications to may receive.
func (t *Throttle) Remaining(to string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if left := t.limit - t.sent[to]; left > 0 {
		return left
	}
	return 0
}""",
 b_file="headroom.go", b_body="""// Headroom totals the remaining allowance of the recipients.
func Headroom(t *Throttle, recipients []string) int {
	total := 0
	for _, to := range recipients {
		total += t.Remaining(to)
	}
	return total
}""",
 w_body="""// Headroom counts by trying to send, which uses the allowance.
func Headroom(t *Throttle, recipients []string) int {
	total := 0
	for _, to := range recipients {
		for t.Allow(to) {
			total++
		}
	}
	return total
}"""))

# ---- large-07 storage quota
F.append(dict(id="large-07", pkg="storage",
 a_prompt="Add (s *Store) UsedBytes() int64 in the storage package: the total size in bytes of every stored blob. Add a regression test.",
 b_prompt="Add (s *Store) PutWithin(q Quota, data []byte) (string, error) in the storage package: store data only when Quota.Enforce allows it against the store's current usage, else return ErrQuotaExceeded and store nothing. Reuse UsedBytes and Enforce. Add a regression test.",
 a_probe="""import "testing"

func TestProbeUsedBytes(t *testing.T) {
	s := NewStore()
	if got := s.UsedBytes(); got != 0 {
		t.Fatalf("empty UsedBytes = %d", got)
	}
	s.Put([]byte("abcd"))
	s.Put([]byte("xyz"))
	if got := s.UsedBytes(); got != 7 {
		t.Fatalf("UsedBytes = %d, want 7", got)
	}
}""",
 b_probe="""import (
	"errors"
	"testing"
)

func TestProbePutWithin(t *testing.T) {
	s := NewStore()
	q := Quota{MaxBytes: 10}
	if _, err := s.PutWithin(q, []byte("123456")); err != nil {
		t.Fatalf("first put: %v", err)
	}
	key, err := s.PutWithin(q, []byte("abcdef"))
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second put err = %v, want ErrQuotaExceeded", err)
	}
	if key != "" || s.UsedBytes() != 6 {
		t.Fatalf("a refused put must store nothing: key=%q used=%d", key, s.UsedBytes())
	}
}""",
 a_file="usage.go", a_body="""// UsedBytes totals the size of every stored blob.
func (s *Store) UsedBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for _, b := range s.blobs {
		total += int64(len(b))
	}
	return total
}""",
 b_file="put_within.go", b_body="""// PutWithin stores data only when the quota allows it.
func (s *Store) PutWithin(q Quota, data []byte) (string, error) {
	if err := q.Enforce(s.UsedBytes(), int64(len(data))); err != nil {
		return "", err
	}
	return s.Put(data), nil
}""",
 w_body="""// PutWithin checks the incoming size alone.
func (s *Store) PutWithin(q Quota, data []byte) (string, error) {
	if int64(len(data)) > q.MaxBytes {
		return "", ErrQuotaExceeded
	}
	return s.Put(data), nil
}"""))

# ---- large-08 session expiry
F.append(dict(id="large-08", pkg="session",
 a_prompt="Add (s *Store) Expired(id string) bool in the session package: true when the session is unknown or idle for longer than the store's ttl, judged with the store's own clock, without refreshing or deleting it. Add a regression test.",
 b_prompt="Add (s *Store) Sweep() int in the session package: delete every session that Expired reports as expired and return how many were deleted. Reuse Expired; do not re-derive the idle rule. Add a regression test.",
 a_probe="""import (
	"testing"
	"time"
)

func TestProbeExpired(t *testing.T) {
	s := NewStore(time.Minute)
	at := time.Unix(1000, 0)
	s.now = func() time.Time { return at }
	s.Create("a", "ada")
	at = at.Add(time.Minute)
	if s.Expired("a") {
		t.Fatal("idle exactly ttl is still live")
	}
	at = at.Add(time.Second)
	if !s.Expired("a") {
		t.Fatal("idle past ttl must be expired")
	}
	if s.Len() != 1 {
		t.Fatal("Expired must not delete")
	}
	if !s.Expired("nobody") {
		t.Fatal("unknown id is expired")
	}
}""",
 b_probe="""import (
	"testing"
	"time"
)

func TestProbeSweep(t *testing.T) {
	s := NewStore(time.Minute)
	at := time.Unix(1000, 0)
	s.now = func() time.Time { return at }
	s.Create("old", "ada")
	at = at.Add(30 * time.Second)
	s.Create("edge", "bob")
	at = at.Add(61 * time.Second)
	s.Create("new", "cy")
	at = at.Add(-1 * time.Second)
	if got := s.Sweep(); got != 1 {
		t.Fatalf("Sweep = %d, want 1 (only old is past ttl)", got)
	}
	if s.Len() != 2 {
		t.Fatalf("Len after sweep = %d, want 2", s.Len())
	}
}""",
 a_file="expiry.go", a_body="""// Expired reports whether id is unknown or idle past the ttl.
func (s *Store) Expired(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return true
	}
	return s.now().Sub(session.LastSeen) > s.ttl
}""",
 b_file="sweep.go", b_body="""// Sweep deletes every expired session and returns how many it deleted.
func (s *Store) Sweep() int {
	s.mu.RLock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	n := 0
	for _, id := range ids {
		if s.Expired(id) {
			s.Delete(id)
			n++
		}
	}
	return n
}""",
 w_body="""// Sweep re-derives the idle rule with an inclusive bound.
func (s *Store) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, session := range s.sessions {
		if s.now().Sub(session.LastSeen) >= s.ttl-time.Second {
			delete(s.sessions, id)
			n++
		}
	}
	return n
}"""))

# ---- large-09 worker backoff
F.append(dict(id="large-09", pkg="worker",
 a_prompt="Add (b *Backoff) Peek() time.Duration in the worker package: the delay the next call to Next would return, without changing the backoff. Add a regression test.",
 b_prompt="Add RetryPlan(b Backoff, attempts int) []time.Duration in the worker package: the delays of the next attempts retries in order, without changing the caller's backoff. Reuse Backoff.Next so the ceiling rule stays in one place. Add a regression test.",
 a_probe="""import (
	"testing"
	"time"
)

func TestProbePeek(t *testing.T) {
	b := &Backoff{Current: time.Second, Ceiling: 3 * time.Second}
	if got := b.Peek(); got != 2*time.Second {
		t.Fatalf("Peek = %v, want 2s", got)
	}
	if b.Current != time.Second {
		t.Fatal("Peek must not change the backoff")
	}
	b.Current = 2 * time.Second
	if got := b.Peek(); got != 3*time.Second {
		t.Fatalf("Peek at ceiling = %v, want 3s", got)
	}
}""",
 b_probe="""import (
	"testing"
	"time"
)

func TestProbeRetryPlan(t *testing.T) {
	b := Backoff{Current: time.Second, Ceiling: 5 * time.Second}
	got := RetryPlan(b, 4)
	want := []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("RetryPlan = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RetryPlan = %v, want %v", got, want)
		}
	}
	if b.Current != time.Second {
		t.Fatal("RetryPlan must not change the caller's backoff")
	}
}""",
 a_file="peek.go", a_body="""import "time"

// Peek returns the delay Next would return, without changing b.
func (b *Backoff) Peek() time.Duration {
	next := *b
	return next.Next()
}""",
 b_file="retry_plan.go", b_body="""import "time"

// RetryPlan lists the delays of the next attempts retries.
func RetryPlan(b Backoff, attempts int) []time.Duration {
	out := make([]time.Duration, 0, attempts)
	for i := 0; i < attempts; i++ {
		out = append(out, b.Next())
	}
	return out
}""",
 w_body="""import "time"

// RetryPlan doubles without the ceiling.
func RetryPlan(b Backoff, attempts int) []time.Duration {
	out := make([]time.Duration, 0, attempts)
	d := b.Current
	for i := 0; i < attempts; i++ {
		d *= 2
		out = append(out, d)
	}
	return out
}"""))

# ---- large-10 worker pool
F.append(dict(id="large-10", pkg="worker",
 a_prompt="Add (p *Pool) SuccessRate() float64 in the worker package: completed jobs divided by all submitted jobs, and 0 when no job was submitted. Add a regression test.",
 b_prompt="Add Healthy(p *Pool, min float64) bool in the worker package: true when at least one job was submitted and the pool's SuccessRate is at least min. Reuse SuccessRate; do not re-derive the rate. Add a regression test.",
 a_probe="""import (
	"errors"
	"testing"
)

func TestProbeSuccessRate(t *testing.T) {
	p := &Pool{}
	if got := p.SuccessRate(); got != 0 {
		t.Fatalf("empty SuccessRate = %v", got)
	}
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return errors.New("x") }})
	if got := p.SuccessRate(); got != 0.75 {
		t.Fatalf("SuccessRate = %v, want 0.75", got)
	}
}""",
 b_probe="""import (
	"errors"
	"testing"
)

func TestProbeHealthy(t *testing.T) {
	p := &Pool{}
	if Healthy(p, 0) {
		t.Fatal("a pool with no jobs is not healthy")
	}
	p.Submit(Job{Run: func() error { return nil }})
	p.Submit(Job{Run: func() error { return errors.New("x") }})
	if !Healthy(p, 0.5) {
		t.Fatal("rate 0.5 meets min 0.5")
	}
	if Healthy(p, 0.6) {
		t.Fatal("rate 0.5 misses min 0.6")
	}
}""",
 a_file="success_rate.go", a_body="""// SuccessRate is completed jobs over all submitted jobs, 0 with none.
func (p *Pool) SuccessRate() float64 {
	completed, failed := p.Stats()
	if completed+failed == 0 {
		return 0
	}
	return float64(completed) / float64(completed+failed)
}""",
 b_file="healthy.go", b_body="""// Healthy reports whether the pool ran a job and meets the minimum rate.
func Healthy(p *Pool, min float64) bool {
	completed, failed := p.Stats()
	return completed+failed > 0 && p.SuccessRate() >= min
}""",
 w_body="""// Healthy compares completed against failed counts.
func Healthy(p *Pool, min float64) bool {
	completed, failed := p.Stats()
	return completed > failed
}"""))

for f in F:
    fid, pkg = f["id"], f["pkg"]
    for p in (f["a_prompt"], f["b_prompt"]):
        assert len(p) <= 320, (fid, len(p), p)
    wrong_body = f["w_body"]
    if fid == "large-08":
        wrong_body = 'import "time"\n\n' + wrong_body
    files = {
        f"{LF}/verifiers/{fid}-a.sh": check(pkg, "probe_" + f["a_file"].replace(".go", ""), f["a_probe"], f"{fid} A: {f['a_prompt'][:70]}"),
        f"{LF}/verifiers/{fid}-b.sh": check(pkg, "probe_" + f["b_file"].replace(".go", ""), f["b_probe"], f"{fid} B: {f['b_prompt'][:70]}"),
        f"{LF}/validate/_gold-a/{fid}.sh": overlay(pkg, f["a_file"], f["a_body"], f"Gold solution for {fid} Task A."),
        f"{LF}/validate/_gold-b/{fid}.sh": overlay(pkg, f["b_file"], f["b_body"], f"Gold solution for {fid} Task B."),
        f"{LF}/validate/_wrong/{fid}.sh": overlay(pkg, f["b_file"], wrong_body, f"Wrong Task B for {fid}: re-derives the rule instead of reusing it."),
    }
    for path, body in files.items():
        open(path, "w").write(body)
        if path.endswith(".sh") and "/validate/" in path:
            os.chmod(path, 0o755)
    for phase, prompt, chk in (("write", f["a_prompt"], files[f"{LF}/verifiers/{fid}-a.sh"]), ("read", f["b_prompt"], files[f"{LF}/verifiers/{fid}-b.sh"])):
        d = {"name": f"{fid}-{phase}", "phase": phase}
        if phase == "read":
            d["precursor"] = f"{fid}-write"
        d.update({"prompt": prompt, "check": chk, "fixture": "../../large-families/fixture"})
        json.dump(d, open(f"{TS}/{fid}-{phase}.json", "w"), indent=2)
    print(fid, pkg, len(f["a_prompt"]), len(f["b_prompt"]))
