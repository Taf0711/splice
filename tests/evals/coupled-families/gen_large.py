#!/usr/bin/env python3
"""Competing records with source files of realistic size
(plans/SPEC_JEV_DECISION_LAYER_V2, phase P5b).

The coupled fixture's files are small (median 536 bytes), so a prefetch of
one file more or less changes no measurable token cost (P4). This fixture is
the same code with each non-test file grown to 2 to 5k characters by
unexported helpers named after the file. The helpers compile, no test calls
them, and they contain no gold-edit anchor, so every family and check is
unchanged.

Usage:
  gen_large.py            write the fixture and the taskset
  gen_large.py --validate write, then replay the gold edits in order
"""

import hashlib
import shutil
import sys

import gen_competing as cmp
import gen_coupled as cpl

FIXTURE = cpl.EVALS / "coupled-families" / "fixture-large"
FIXTURE_REF = "../../coupled-families/fixture-large"
TASKS = cpl.EVALS / "warmcost" / "competing-large-taskset" / "tasks"

TEMPLATES = [
    '''
// {name}Normalize{n} drops empty entries and keeps the first occurrence of
// each value, in input order.
func {name}Normalize{n}(values []string) []string {{
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {{
		if v == "" || seen[v] {{
			continue
		}}
		seen[v] = true
		out = append(out, v)
	}}
	return out
}}
''',
    '''
// {name}Window{n} counts uses inside one period and refuses uses past the
// limit until the period resets.
type {name}Window{n} struct {{
	limit int
	used  int
}}

// take reserves n uses and reports whether they fit in the window.
func (w *{name}Window{n}) take(n int) bool {{
	if n <= 0 || w.used+n > w.limit {{
		return false
	}}
	w.used += n
	return true
}}

// reset starts a new period.
func (w *{name}Window{n}) reset() {{
	w.used = 0
}}
''',
    '''
// {name}Clamp{n} bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func {name}Clamp{n}(v, lo, hi int) int {{
	if hi < lo {{
		return lo
	}}
	if v < lo {{
		return lo
	}}
	if v > hi {{
		return hi
	}}
	return v
}}
''',
    '''
// {name}Describe{n} renders a status code for operator logs.
func {name}Describe{n}(code int) string {{
	switch {{
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}}
}}
''',
    '''
// {name}Page{n} returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func {name}Page{n}(items []string, offset, limit int) []string {{
	if offset < 0 || limit <= 0 || offset >= len(items) {{
		return nil
	}}
	end := offset + limit
	if end > len(items) {{
		end = len(items)
	}}
	return items[offset:end]
}}
''',
]


def ident(rel):
    """A lower camel identifier from the file name: rate_limit.go -> rateLimit."""
    parts = rel.rsplit("/", 1)[-1].removesuffix(".go").split("_")
    return parts[0] + "".join(p.title() for p in parts[1:])


def pad(rel, text):
    digest = int(hashlib.sha256(rel.encode()).hexdigest(), 16)
    target = 2000 + digest % 3001
    name, n = ident(rel), 0
    while len(text) < target:
        text += TEMPLATES[(digest + n) % len(TEMPLATES)].format(name=name, n=n)
        n += 1
    return text


def write_fixture():
    # Overwrite in place (a synced folder turns delete-and-rewrite into
    # "name 2" copies).
    shutil.copytree(cpl.FIXTURE, FIXTURE, dirs_exist_ok=True)
    for path in sorted(FIXTURE.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        rel = path.relative_to(FIXTURE).as_posix()
        path.write_text(pad(rel, (cpl.FIXTURE / rel).read_text()))


if __name__ == "__main__":
    write_fixture()
    cmp.write_tasks(TASKS, FIXTURE_REF)
    if "--validate" in sys.argv:
        rc, out = cpl.run('test -z "$(gofmt -l .)" && go vet ./... && go test -count=1 ./...', FIXTURE)
        if rc != 0:
            print("fixture-large does not format, vet, and pass:", out)
            sys.exit(1)
        cmp.validate(FIXTURE)
