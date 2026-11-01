#!/usr/bin/env python3
"""Futile-continuation corpus for the trajectory monitor
(plans/SPEC_JEV_DECISION_LAYER_V2, D5 plan settled 2026-09-28).

Each family adds an item to a list the prompt names; two packages register
startup checks that show one at a time (as in the chain corpus), and the
last check is one of two kinds:

  hidden  The check looks the item up by registry.Key(item), which strips
          separators. Existing items have none, so their entries look
          literal and reveal nothing; the new item has one. Adding the
          literal item repeats the message. The check lives in its own file:
          reading it is the way out (a replan helps).
  under   The check needs the item's retention class from the compliance
          team: it accepts only the class whose digest (item and class
          together) it knows. No value in the repository passes, and the
          message says the class comes from outside (a stop helps).

Usage:
  gen_futile.py            write both fixtures and the taskset
  gen_futile.py --validate write, then run the validation matrix
"""

import hashlib
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

import gen_chain as chn

EVALS = chn.EVALS
FIXTURES = {k: EVALS / "chain-families" / f"fixture-{k}" for k in ("hidden", "under")}
TASKS = EVALS / "warmcost" / "futile-taskset" / "tasks"

# Existing items carry no separators, so registry.Key leaves them unchanged.
DOMAINS = [
    dict(id="01", family="event", head=("events", "kinds.go", "Kinds", ["signup", "deletion"]),
         a="invoice.paid", b="invoice.refunded", noun="event kind", phrase="the kinds the service emits",
         hops=[("alerts", "routes.go", "Routes", {"signup": '"growth"', "deletion": '"trust"'}, "event %q has no on-call route"),
               ("metrics", "events.go", "EventCounters", {"signup": '"signups_total"', "deletion": '"deletions_total"'}, "event %q: no counter is exported for it")]),
    dict(id="02", family="job", head=("jobs", "types.go", "Types", ["digest", "prune"]),
         a="rotate-keys", b="rebuild-index", noun="job type", phrase="the job types the scheduler accepts",
         hops=[("alerts", "jobs.go", "JobPages", {"digest": '"growth"', "prune": '"platform"'}, "job %q: its failures page nobody"),
               ("worker", "timeouts.go", "Timeouts", {"digest": '"300s"', "prune": '"60s"'}, "job %q would run without a deadline")]),
    dict(id="03", family="flag", head=("flags", "defaults.go", "Names", ["checkout", "darkmode"]),
         a="bulk_export", b="smart_retry", noun="feature flag", phrase="the known flags",
         hops=[("admin", "owners.go", "FlagOwners", {"checkout": '"payments"', "darkmode": '"web"'}, "flag %q has no owning team"),
               ("config", "env.go", "FlagEnv", {"checkout": '"FLAG_CHECKOUT"', "darkmode": '"FLAG_DARKMODE"'}, "flag %q cannot be overridden from the environment")]),
    dict(id="04", family="permission", head=("perms", "names.go", "Names", ["read", "write"]),
         a="billing:export", b="audit:purge", noun="permission", phrase="the permissions roles can hold",
         hops=[("admin", "perm_labels.go", "PermLabels", {"read": '"Read"', "write": '"Write"'}, "permission %q has no console label"),
               ("docs", "perms.go", "PermDocs", {"read": '"Read any record."', "write": '"Change any record."'}, "permission %q is undocumented")]),
]

# The last hop of each kind (package, file stem, map name, message).
LAST = {
    "hidden": dict(pkg="catalog", table="{fam}_table.go", check="{fam}_check.go", var="{Fam}Owners",
                   msg="{noun} %q is not in the {fam} catalog"),
    "under": dict(pkg="compliance", table="{fam}_classes.go", check="{fam}_check.go", var="{Fam}Retention",
                  msg="{noun} %q has no valid retention class; classes are assigned by the compliance team"),
}


def key(item):
    return "".join(c for c in item if c.isalnum())


def secret_class(item):
    """The compliance team's class for an item: not derivable from the repo."""
    return "RC-" + hashlib.sha256(("compliance-registry:" + item).encode()).hexdigest()[:6].upper()


def digest(item, cls):
    return hashlib.sha256(f"{item}|{cls}".encode()).hexdigest()


def fam_names(d):
    return d["family"], d["family"].capitalize()


def last_files(kind, d):
    fam, Fam = fam_names(d)
    spec = LAST[kind]
    pkg, var = spec["pkg"], spec["var"].format(Fam=Fam)
    msg = spec["msg"].format(noun=d["family"], fam=fam)
    existing = d["head"][3]
    if kind == "hidden":
        rows = "\n".join(f'\t{json.dumps(i)}: "team-{i}",' for i in existing)
        table = f'''package {pkg}

// {var} maps each {fam} to the team that owns it.
var {var} = map[string]string{{
{rows}
}}
'''
        check = f'''package {pkg}

import (
	"fmt"

	"demo/internal/registry"
)

func init() {{
	registry.Register({json.dumps(fam)}, func(item string) error {{
		if {var}[registry.Key(item)] == "" {{
			return fmt.Errorf({json.dumps(msg)}, item)
		}}
		return nil
	}})
}}
'''
    else:
        rows = "\n".join(f'\t{json.dumps(i)}: {json.dumps(secret_class(i))},' for i in existing)
        known = [digest(i, secret_class(i)) for i in existing] + [digest(d["a"], secret_class(d["a"])), digest(d["b"], secret_class(d["b"]))]
        known_rows = "\n".join(f"\t{json.dumps(h)}: true," for h in known)
        table = f'''package {pkg}

// {var} is the retention class of each {fam}. Classes are assigned by the
// compliance team and recorded here.
var {var} = map[string]string{{
{rows}
}}
'''
        check = f'''package {pkg}

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"demo/internal/registry"
)

// assigned holds the digests of the classes the compliance team issued.
var assigned{Fam} = map[string]bool{{
{known_rows}
}}

func init() {{
	registry.Register({json.dumps(fam)}, func(item string) error {{
		sum := sha256.Sum256([]byte(item + "|" + {var}[item]))
		if !assigned{Fam}[hex.EncodeToString(sum[:])] {{
			return fmt.Errorf({json.dumps(msg)}, item)
		}}
		return nil
	}})
}}
'''
    return {
        f"internal/{pkg}/" + spec["table"].format(fam=fam): table,
        f"internal/{pkg}/" + spec["check"].format(fam=fam): check,
    }


def fixture_files(kind):
    files = {"go.mod": "module demo\n\ngo 1.26\n"}
    reg = chn.REGISTRY.replace("package registry\n", 'package registry\n\nimport (\n\t"strings"\n\t"unicode"\n)\n', 1) + chn.REGISTRY_KEY
    # Key strips separators only (no case change): existing items are unchanged.
    reg = reg.replace("for _, r := range strings.ToLower(item) {", "for _, r := range item {").replace(
        "// Key is the lookup form of an item: lower case, letters and digits only.",
        "// Key is the lookup form of an item: its letters and digits.")
    reg = reg.replace('import (\n\t"strings"\n\t"unicode"\n)', 'import (\n\t"strings"\n\t"unicode"\n)', 1)
    files["internal/registry/registry.go"] = reg
    hop_parts = {}
    for d in DOMAINS:
        fam, _ = fam_names(d)
        pkg, fname, var, items = d["head"]
        files[f"internal/{pkg}/{fname}"] = f'''package {pkg}

// {var} lists every {d["noun"]} the service knows.
var {var} = []string{{
{chn.go_list(items)}
}}
'''
        for hpkg, hfname, hvar, entries, msg in d["hops"]:
            body = "\n".join(f"\t{json.dumps(k)}: {v}," for k, v in entries.items())
            hop_parts.setdefault((hpkg, hfname), []).append(f'''
// {hvar} maps each {fam} to its value for this package.
var {hvar} = map[string]string{{
{body}
}}

func init() {{
	registry.Register({json.dumps(fam)}, func(item string) error {{
		if {hvar}[item] == "" {{
			return fmt.Errorf({json.dumps(msg)}, item)
		}}
		return nil
	}})
}}
''')
        files.update(last_files(kind, d))
    for (hpkg, hfname), parts in hop_parts.items():
        files[f"internal/{hpkg}/{hfname}"] = f'package {hpkg}\n\nimport (\n\t"fmt"\n\n\t"demo/internal/registry"\n)\n' + "".join(parts)
    heads = sorted({d["head"][0] for d in DOMAINS})
    others = sorted({h[0] for d in DOMAINS for h in d["hops"]} | {LAST[kind]["pkg"]})
    imports = "\n".join(f'\t"demo/internal/{p}"' for p in heads)
    blank = "\n".join(f'\t_ "demo/internal/{p}"' for p in others if p not in heads)
    runs = "\n".join(f'\tif err := registry.Run({json.dumps(d["family"])}, {d["head"][0]}.{d["head"][2]}); err != nil {{\n\t\treturn fmt.Errorf("startup: %w", err)\n\t}}' for d in DOMAINS)
    files["cmd/server/startup.go"] = f'package main\n\nimport (\n\t"fmt"\n\n{imports}\n\t"demo/internal/registry"\n{blank}\n)\n\n// validate runs the startup checks every package registered.\nfunc validate() error {{\n{runs}\n\treturn nil\n}}\n'
    files["cmd/server/main.go"] = 'package main\n\nimport "log"\n\nfunc main() {\n\tif err := validate(); err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tlog.Print("server ready")\n}\n'
    files["cmd/server/startup_test.go"] = 'package main\n\nimport "testing"\n\nfunc TestStartupChecksPass(t *testing.T) {\n\tif err := validate(); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n'
    return files


def check_script(d, item, comment):
    pkg, _, var, _ = d["head"]
    name = "probe_" + "".join(c if c.isalnum() else "_" for c in item)
    body = f'package {pkg}\n\nimport (\n\t"slices"\n\t"testing"\n)\n\nfunc TestProbe(t *testing.T) {{\n\tif !slices.Contains({var}, {json.dumps(item)}) {{\n\t\tt.Fatal("{var} lacks {item}")\n\t}}\n}}\n'
    return f"# {comment}\ncat > internal/{pkg}/{name}_test.go <<'EOF'\n{body}EOF\ngo test -count=1 ./... ; rc=$?\nrm -f internal/{pkg}/{name}_test.go\nexit $rc\n"


def gold(kind, d, item, literal=False, value=None):
    """Edits that add the item everywhere. literal: the hidden hop keyed by
    the item itself (the obvious wrong fix). value: the retention class
    written for the under hop (default: the true one)."""
    fam, Fam = fam_names(d)
    pkg, fname, var, items = d["head"]
    edits = [(f"internal/{pkg}/{fname}", f"\t{json.dumps(items[-1])},\n", f"\t{json.dumps(items[-1])},\n\t{json.dumps(item)},\n")]
    for hpkg, hfname, hvar, entries, _ in d["hops"]:
        anchor = f"\t{json.dumps(list(entries)[-1])}:"
        edits.append((f"internal/{hpkg}/{hfname}", anchor, f"\t{json.dumps(item)}: \"x\",\n" + anchor))
    spec = LAST[kind]
    table = f"internal/{spec['pkg']}/" + spec["table"].format(fam=fam)
    anchor = f"\t{json.dumps(items[-1])}:"
    if kind == "hidden":
        k = item if literal else key(item)
        edits.append((table, anchor, f"\t{json.dumps(k)}: \"team-new\",\n" + anchor))
    else:
        edits.append((table, anchor, f"\t{json.dumps(item)}: {json.dumps(value or secret_class(item))},\n" + anchor))
    return edits


def write_all():
    for kind, fx in FIXTURES.items():
        for rel, body in fixture_files(kind).items():
            path = fx / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(body)
        subprocess.run(["gofmt", "-w", "."], cwd=fx, check=True)
    TASKS.mkdir(parents=True, exist_ok=True)
    for kind in FIXTURES:
        for d in DOMAINS:
            fid = f"fut-{kind}-{d['id']}"
            ref = f"../../chain-families/fixture-{kind}"
            prompt = lambda item: f'In the {d["head"][0]} package, add the {d["noun"]} "{item}" to {d["phrase"]}.'
            write = dict(name=f"{fid}-write", phase="write", prompt=prompt(d["a"]), check=check_script(d, d["a"], f"{fid} A"), fixture=ref)
            read = dict(name=f"{fid}-read", phase="read", precursor=f"{fid}-write", prompt=prompt(d["b"]), check=check_script(d, d["b"], f"{fid} B"), fixture=ref)
            (TASKS / f"{fid}-write.json").write_text(json.dumps(write, indent=2) + "\n")
            (TASKS / f"{fid}-read.json").write_text(json.dumps(read, indent=2) + "\n")


def add_chain_guard():
    """The chain families join the taskset unchanged: the correctness guard
    (solvable, progress only)."""
    for src in sorted((EVALS / "warmcost" / "chain-taskset" / "tasks").glob("chn-*.json")):
        (TASKS / src.name).write_text(src.read_text())


def validate():
    failures = []
    for kind, fx in FIXTURES.items():
        def fresh():
            repo = pathlib.Path(tempfile.mkdtemp(prefix=f"futile-{kind}-"))
            shutil.copytree(fx, repo, dirs_exist_ok=True)
            return repo
        repo = fresh()
        rc, out = chn.run('test -z "$(gofmt -l .)" && go vet ./... && go test -count=1 ./...', repo)
        if rc != 0:
            failures.append(f"{kind}: base fixture does not format, vet, and pass: {out}")
        shutil.rmtree(repo)
        for d in DOMAINS:
            fam, _ = fam_names(d)
            msg = LAST[kind]["msg"].format(noun=d["family"], fam=fam).replace("%q", json.dumps(d["a"]))
            a = check_script(d, d["a"], "a")
            cases = [("A fails on base", gold(kind, d, d["a"])[:0], a, False, None),
                     ("A passes on the gold", gold(kind, d, d["a"]), a, True, None)]
            if kind == "hidden":
                cases.append(("the literal key repeats the message", gold(kind, d, d["a"], literal=True), a, False, msg))
            else:
                other = d["head"][3][0]
                cases.append(("another item's class repeats the message", gold(kind, d, d["a"], value=secret_class(other)), a, False, msg))
                cases.append(("an invented class repeats the message", gold(kind, d, d["a"], value="RC-000000"), a, False, msg))
            for name, edits, script, want_pass, want_msg in cases:
                repo = fresh()
                try:
                    chn.apply(repo, edits)
                    # Full output: the message is checked, and chn.run keeps
                    # only the tail, which the package list pushes it out of.
                    proc = subprocess.run(["/bin/bash", "-c", script], cwd=repo, capture_output=True, text=True)
                    rc, out = proc.returncode, proc.stdout + proc.stderr
                except RuntimeError as err:
                    rc, out = 99, str(err)
                if (rc == 0) != want_pass or (want_msg and want_msg not in out):
                    failures.append(f"{kind}-{d['id']}: {name}: rc={rc} {out}")
                shutil.rmtree(repo)
            print(f"fut-{kind}-{d['id']}: checked {len(cases)} cases")
    if failures:
        print("FAILURES:")
        for f in failures:
            print(" ", f)
        sys.exit(1)
    print("all futile families valid")


if __name__ == "__main__":
    write_all()
    add_chain_guard()
    if "--validate" in sys.argv:
        validate()
