#!/usr/bin/env python3
"""Chain families: a change whose hidden requirements show one at a time
(plans/SPEC_JEV_DECISION_LAYER_V2, trajectory monitor corpus).

Each family adds one item to a list in package P, which the prompt names.
Three other packages register a startup check for that kind of item
(registry.Register in init). The startup test runs the checks and stops at
the first failure, so a run sees one missing registration per test run: fix
it, and the next one shows. The messages name the symptom, not the file.
With two local repairs per pass, a run needs repairs and often a second
pass; that gives the trajectory monitor boundaries to judge.

Usage:
  gen_chain.py            write the fixture and the taskset
  gen_chain.py --validate write, then run the validation matrix
"""

import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

EVALS = pathlib.Path(__file__).resolve().parent.parent
# --trap: the last hop of each family looks its item up by registry.Key
# (lower case, letters and digits only), and its entries use that form. The
# message does not say so: adding the literal item fails with the same
# message. Continuing does not help; reading the check does (trajectory
# monitor: stagnation labels).
TRAP = "--trap" in sys.argv
NAME = "trap" if TRAP else "chain"
FIXTURE = EVALS / "chain-families" / ("fixture-trap" if TRAP else "fixture")
TASKS = EVALS / "warmcost" / f"{NAME}-taskset" / "tasks"
FIXTURE_REF = "../../chain-families/" + ("fixture-trap" if TRAP else "fixture")


def key(item):
    """registry.Key in Python."""
    return "".join(c for c in item.lower() if c.isalnum())


def trapped(f, i):
    return TRAP and i == len(f["hops"]) - 1

# Each family: the list the prompt names (pkg, file, var, existing items,
# how to add an item), and three hops (pkg, file, var, existing entries, the
# entry for item A and B, the check's error message). A hop is a map from
# item to a value; the check fails when the item has no entry.
FAMILIES = [
    dict(
        id="chn-01", family="event",
        head=("events", "kinds.go", "Kinds", ["user.created", "user.deleted"]),
        a="invoice.paid", b="invoice.refunded",
        a_prompt='In the events package, add the event kind "invoice.paid" to the kinds the service emits.',
        b_prompt='In the events package, add the event kind "invoice.refunded" to the kinds the service emits.',
        hops=[
            ("alerts", "routes.go", "Routes", "string", {"user.created": '"growth"', "user.deleted": '"trust"'}, '"billing"', "event %q has no on-call route"),
            ("audit", "labels.go", "Labels", "string", {"user.created": '"User created"', "user.deleted": '"User deleted"'}, None, "event %q: the audit trail cannot render it"),
            ("metrics", "events.go", "EventCounters", "string", {"user.created": '"users_created_total"', "user.deleted": '"users_deleted_total"'}, None, "event %q: no counter is exported for it"),
        ],
    ),
    dict(
        id="chn-02", family="flag",
        head=("flags", "defaults.go", "Names", ["new_checkout", "dark_mode"]),
        a="bulk_export", b="smart_retry",
        a_prompt='In the flags package, add the feature flag "bulk_export" to the known flags.',
        b_prompt='In the flags package, add the feature flag "smart_retry" to the known flags.',
        hops=[
            ("admin", "owners.go", "FlagOwners", "string", {"new_checkout": '"payments"', "dark_mode": '"web"'}, '"data"', "flag %q has no owning team"),
            ("config", "env.go", "FlagEnv", "string", {"new_checkout": '"FLAG_NEW_CHECKOUT"', "dark_mode": '"FLAG_DARK_MODE"'}, None, "flag %q cannot be overridden from the environment"),
            ("docs", "flags.go", "FlagDocs", "string", {"new_checkout": '"One-page checkout."', "dark_mode": '"Dark color scheme."'}, None, "flag %q is undocumented"),
        ],
    ),
    dict(
        id="chn-03", family="job",
        head=("jobs", "types.go", "Types", ["send-digest", "prune-sessions"]),
        a="rotate-keys", b="rebuild-index",
        a_prompt='In the jobs package, add the job type "rotate-keys" to the job types the scheduler accepts.',
        b_prompt='In the jobs package, add the job type "rebuild-index" to the job types the scheduler accepts.',
        hops=[
            ("alerts", "jobs.go", "JobPages", "string", {"send-digest": '"growth"', "prune-sessions": '"platform"'}, None, "job %q: its failures page nobody"),
            ("metrics", "jobs.go", "JobCounters", "string", {"send-digest": '"digest_runs_total"', "prune-sessions": '"session_prunes_total"'}, None, "job %q: no counter is exported for it"),
            ("worker", "timeouts.go", "Timeouts", "int", {"send-digest": "300", "prune-sessions": "60"}, None, "job %q would run without a deadline"),
        ],
    ),
    dict(
        id="chn-04", family="currency",
        head=("billing", "currencies.go", "Currencies", ["USD", "EUR"]),
        a="GBP", b="JPY",
        a_prompt='In the billing package, add the currency "GBP" to the currencies customers can pay in.',
        b_prompt='In the billing package, add the currency "JPY" to the currencies customers can pay in.',
        hops=[
            ("admin", "symbols.go", "CurrencySymbols", "string", {"USD": '"$"', "EUR": '"€"'}, None, "currency %q cannot be shown in the console"),
            ("config", "rounding.go", "MinorUnits", "int", {"USD": "2", "EUR": "2"}, None, "currency %q has no rounding rule"),
            ("docs", "currencies.go", "CurrencyNames", "string", {"USD": '"US dollar"', "EUR": '"Euro"'}, None, "currency %q is not in the price list"),
        ],
    ),
]

# A value for any hop entry of a new item (the checks test presence only).
def hop_value(typ, item):
    return "1" if typ == "int" else json.dumps(item.replace(".", " ").replace("-", " ").replace("_", " "))

REGISTRY_KEY = '''
// Key is the lookup form of an item: lower case, letters and digits only.
func Key(item string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(item) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
'''

REGISTRY = '''package registry

// Check reports why an item cannot be served, or nil.
type Check func(item string) error

var checks = map[string][]Check{}

// Register adds a startup check for one kind of item (event, flag, job,
// currency). Packages register their checks in init.
func Register(kind string, c Check) {
	checks[kind] = append(checks[kind], c)
}

// Run checks every item of one kind and returns the first failure.
func Run(kind string, items []string) error {
	for _, item := range items {
		for _, c := range checks[kind] {
			if err := c(item); err != nil {
				return err
			}
		}
	}
	return nil
}
'''


def hop_message(hop, item):
    """The check's error text as Go prints it (%q quotes the item)."""
    return hop[6].replace("%q", json.dumps(item))


def go_list(items):
    return "\n".join(f"\t{json.dumps(i)}," for i in items)


def head_file(f):
    pkg, _, var, items = f["head"]
    return f'''package {pkg}

// {var} lists every {f["family"]} the service knows.
var {var} = []string{{
{go_list(items)}
}}
'''


def hop_files():
    """One file per (pkg, file); a file may hold hops of several families."""
    files = {}
    for f in FAMILIES:
        for i, (pkg, fname, var, typ, entries, _, msg) in enumerate(f["hops"]):
            lookup = "registry.Key(item)" if trapped(f, i) else "item"
            body = "\n".join(f"\t{json.dumps(key(k) if trapped(f, i) else k)}: {v}," for k, v in entries.items())
            zero = '""' if typ == "string" else "0"
            files.setdefault((pkg, fname), []).append(f'''
// {var} maps each {f["family"]} to its value for this package.
var {var} = map[string]{typ}{{
{body}
}}

func init() {{
	registry.Register({json.dumps(f["family"])}, func(item string) error {{
		if {var}[{lookup}] == {zero} {{
			return fmt.Errorf({json.dumps(msg)}, item)
		}}
		return nil
	}})
}}
''')
    out = {}
    for (pkg, fname), parts in files.items():
        out[f"internal/{pkg}/{fname}"] = f'''package {pkg}

import (
	"fmt"

	"demo/internal/registry"
)
''' + "".join(parts)
    return out


def startup_files():
    heads = sorted({f["head"][0] for f in FAMILIES})
    hops = sorted({h[0] for f in FAMILIES for h in f["hops"]})
    imports = "\n".join(f'\t"demo/internal/{p}"' for p in heads)
    blank = "\n".join(f'\t_ "demo/internal/{p}"' for p in hops if p not in heads)
    runs = "\n".join(
        f'\tif err := registry.Run({json.dumps(f["family"])}, {f["head"][0]}.{f["head"][2]}); err != nil {{\n\t\treturn fmt.Errorf("startup: %w", err)\n\t}}'
        for f in FAMILIES)
    return {
        "cmd/server/startup.go": f'''package main

import (
	"fmt"

{imports}
	"demo/internal/registry"
{blank}
)

// validate runs the startup checks every package registered.
func validate() error {{
{runs}
	return nil
}}
''',
        "cmd/server/main.go": '''package main

import "log"

func main() {
	if err := validate(); err != nil {
		log.Fatal(err)
	}
	log.Print("server ready")
}
''',
        "cmd/server/startup_test.go": '''package main

import "testing"

func TestStartupChecksPass(t *testing.T) {
	if err := validate(); err != nil {
		t.Fatal(err)
	}
}
''',
    }


def fixture_files():
    registry = REGISTRY
    if TRAP:
        registry = registry.replace("package registry\n", "package registry\n\nimport (\n\t\"strings\"\n\t\"unicode\"\n)\n", 1) + REGISTRY_KEY
    files = {"go.mod": "module demo\n\ngo 1.26\n", "internal/registry/registry.go": registry}
    for f in FAMILIES:
        pkg, fname, _, _ = f["head"]
        files[f"internal/{pkg}/{fname}"] = head_file(f)
    files.update(hop_files())
    files.update(startup_files())
    return files


def probe(f, item):
    pkg, _, var, _ = f["head"]
    name = "probe_" + "".join(c if c.isalnum() else "_" for c in item.lower())
    body = f'''package {pkg}

import (
	"slices"
	"testing"
)

func TestProbe(t *testing.T) {{
	if !slices.Contains({var}, {json.dumps(item)}) {{
		t.Fatal("{var} lacks {item}")
	}}
}}
'''
    return pkg, name, body


def check(f, item, comment):
    pkg, name, body = probe(f, item)
    return f"""# {comment}
cat > internal/{pkg}/{name}_test.go <<'EOF'
{body}EOF
go test -count=1 ./... ; rc=$?
rm -f internal/{pkg}/{name}_test.go
exit $rc
"""


def gold(f, item):
    """Edits that add item to the head list and every hop."""
    pkg, fname, var, items = f["head"]
    edits = [(f"internal/{pkg}/{fname}", f"\t{json.dumps(items[-1])},\n", f"\t{json.dumps(items[-1])},\n\t{json.dumps(item)},\n")]
    for i, (hpkg, hfname, hvar, typ, entries, _, _) in enumerate(f["hops"]):
        # The entry goes before the last key: gofmt aligns map values, so
        # the key is the only stable anchor.
        norm = (lambda k: key(k)) if trapped(f, i) else (lambda k: k)
        anchor = f"\t{json.dumps(norm(list(entries)[-1]))}:"
        edits.append((f"internal/{hpkg}/{hfname}", anchor, f"\t{json.dumps(norm(item))}: {hop_value(typ, item)},\n" + anchor))
    return edits


def literal_trap_edit(f, item):
    """The obvious wrong fix for the trapped hop: the literal item as key."""
    hpkg, hfname, _, typ, entries, _, _ = f["hops"][-1]
    anchor = f"\t{json.dumps(key(list(entries)[-1]))}:"
    return (f"internal/{hpkg}/{hfname}", anchor, f"\t{json.dumps(item)}: {hop_value(typ, item)},\n" + anchor)


def write_fixture():
    for rel, body in fixture_files().items():
        path = FIXTURE / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body)
    subprocess.run(["gofmt", "-w", "."], cwd=FIXTURE, check=True)


def write_tasks():
    TASKS.mkdir(parents=True, exist_ok=True)
    for f in FAMILIES:
        write = dict(name=f"{f['id']}-write", phase="write", prompt=f["a_prompt"], check=check(f, f["a"], f"{f['id']} A"), fixture=FIXTURE_REF)
        read = dict(name=f"{f['id']}-read", phase="read", precursor=f"{f['id']}-write", prompt=f["b_prompt"], check=check(f, f["b"], f"{f['id']} B"), fixture=FIXTURE_REF)
        (TASKS / f"{f['id']}-write.json").write_text(json.dumps(write, indent=2) + "\n")
        (TASKS / f"{f['id']}-read.json").write_text(json.dumps(read, indent=2) + "\n")


def apply(repo, edits):
    for rel, old, new in edits:
        path = repo / rel
        text = path.read_text()
        if text.count(old) != 1:
            raise RuntimeError(f"{rel}: anchor {old!r} found {text.count(old)} times")
        path.write_text(text.replace(old, new))


def run(cmd, repo):
    proc = subprocess.run(["/bin/bash", "-c", cmd], cwd=repo, capture_output=True, text=True)
    return proc.returncode, (proc.stdout + proc.stderr)[-600:]


def validate():
    failures = []

    def fresh():
        repo = pathlib.Path(tempfile.mkdtemp(prefix="chain-validate-"))
        shutil.copytree(FIXTURE, repo, dirs_exist_ok=True)
        return repo

    repo = fresh()
    rc, out = run('test -z "$(gofmt -l .)" && go vet ./... && go test -count=1 ./...', repo)
    if rc != 0:
        failures.append(f"base fixture does not format, vet, and pass: {out}")
    shutil.rmtree(repo)

    for f in FAMILIES:
        ga, gb = gold(f, f["a"]), gold(f, f["b"])
        a, b = check(f, f["a"], "a"), check(f, f["b"], "b")
        cases = [("A fails on base", [], a, False), ("A passes on gold A", ga, a, True),
                 ("B passes on gold A + gold B", ga + gb, b, True)]
        # Each hop hidden in turn: the chain must show that hop's message.
        for i, hop in enumerate(f["hops"]):
            cases.append((f"A fails without hop {i} ({hop[0]})", [e for j, e in enumerate(ga) if j != i + 1], a, False))
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
        # The chain shows one hop at a time: each run names exactly one
        # missing registration, and adding it shows the next.
        repo = fresh()
        apply(repo, ga[:1])
        shown = []
        for step in range(len(f["hops"])):
            rc, out = run(a, repo)
            hit = [i for i, h in enumerate(f["hops"]) if hop_message(h, f["a"]) in out]
            if rc == 0 or len(hit) != 1:
                failures.append(f"{f['id']}: chain step {step}: want one hop message, got {hit}: {out}")
                break
            shown.append(f["hops"][hit[0]][0])
            apply(repo, [ga[hit[0] + 1]])
        rc, out = run(a, repo)
        if rc != 0:
            failures.append(f"{f['id']}: the chain does not end in a pass: {out}")
        shutil.rmtree(repo)
        if TRAP:
            repo = fresh()
            apply(repo, ga[:-1] + [literal_trap_edit(f, f["a"])])
            rc, out = run(a, repo)
            if rc == 0 or hop_message(f["hops"][-1], f["a"]) not in out:
                failures.append(f"{f['id']}: the literal key must keep the trapped hop's message: rc={rc} {out}")
            shutil.rmtree(repo)
        print(f"{f['id']}: checked {len(cases)} cases; chain order {shown}")
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
