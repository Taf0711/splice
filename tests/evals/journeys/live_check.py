#!/usr/bin/env python3
"""live_check.py: stream-json and screen-capture checks for live journeys J6 to J10.

Subcommands (exit 0 = check holds, 1 = check fails with a message on stderr):
  stream FILE                      every line is JSON; each event has a type
  topology FILE NODE...            each NODE has exactly one terminal stage event
  verdict FILE                     print JSON: splice's own verified claim and status
  failing FILE NODE                NODE reached a failed/errored/aborted status
  captures DIR                     status words change over time; no panic text
  errorbox FILE                    print the text of the first "Crystallization failed:"
                                   error card, joined across its wrapped lines
  receipt FILE...                  print JSON with the receipt kind: the last receipt card
                                   title (VERIFIED, DONE · NOT VERIFIED, FAILED, CANCELLED),
                                   cross-checked against the Ctrl+G pane's RECEIPT status
  receipt-seen FILE...             exit 0 when a receipt card OR the Ctrl+G pane's RECEIPT
                                   section is on screen (the run has ended)
"""
import glob
import json
import os
import re
import sys

# Stage statuses emitted by internal/splice/run.go (emitStageEvent call sites).
TERMINAL = {"completed", "failed", "skipped", "incomplete"}
FAILED = {"failed", "incomplete"}


def events(path):
    out = []
    with open(path, encoding="utf-8", errors="replace") as fh:
        for i, line in enumerate(fh, 1):
            line = line.strip()
            if not line:
                continue
            try:
                out.append(json.loads(line))
            except json.JSONDecodeError as exc:
                raise SystemExit(f"line {i} is not JSON: {exc}")
    return out


def fail(msg):
    print(msg, file=sys.stderr)
    sys.exit(1)


def text_of(evs):
    parts = []
    for e in evs:
        for key in ("delta", "text", "message", "reason", "output"):
            v = e.get(key)
            if isinstance(v, str):
                parts.append(v)
    return "\n".join(parts)


def error_card_text(screen):
    """Text of the first error card that starts with "Crystallization failed:".

    The card's left border column fixes where its text lives. Each wrapped line
    is read from that column up to the card's right border, so sidebar text on
    the same screen row is never mixed in.
    """
    lines = screen.splitlines()
    for start, line in enumerate(lines):
        pos = line.find("Crystallization failed:")
        if pos >= 0:
            break
    else:
        return ""
    left = line.rfind("│", 0, pos)
    if left < 0:
        return ""
    out = ""
    width = None
    for row in lines[start:]:
        if len(row) <= left or row[left] != "│":
            break
        seg = row[left + 1:]
        end = seg.find("│")
        if end >= 0:
            seg = seg[:end]
        if width is None:
            width = len(seg)
        # A full-width line whose last text cell is not a space was cut mid-token
        # (a long path). Word wrap pads short lines, so only a cut joins the next line.
        cut_mid_token = len(seg) == width and len(seg) > 1 and seg[-2] != " "
        out += seg.strip() + ("" if cut_mid_token else " ")
    return " ".join(out.split())


# A receipt card's top border has no title. The title is the first body line:
# "│ <gutter> VERIFIED  <elapsed>   │". The gutter pads to one or more cells.. The gutter token is required, so a sidebar
# counter such as "FAILED 1/1" never matches.
RECEIPT_CARD_RE = re.compile(r"│ \S+ +(DONE · NOT VERIFIED|VERIFIED|FAILED|CANCELLED)(?:  |\s*│)")
RECEIPT_KIND = {
    "VERIFIED": "verified",
    "DONE · NOT VERIFIED": "unverified",
    "FAILED": "failed",
    "CANCELLED": "cancelled",
}
# The Ctrl+G evidence pane prints a "RECEIPT" heading, then the completion status.
# "completed" covers both verified and unverified runs, so the card decides the kind.
# The pane heading sits in the drawer's box ("│ RECEIPT   │") and the status below it.
PANE_HEADING_RE = re.compile(r"(?:^|│)\s*RECEIPT\s*(?:│|$)")
PANE_STATUS_RE = re.compile(r"(?:^|│)\s*(completed|failed|cancelled)\b")
PANE_STATUS_KINDS = {
    "completed": {"verified", "unverified"},
    "failed": {"failed"},
    "cancelled": {"cancelled"},
}


def scan_receipts(screens):
    """Last receipt card title and last Ctrl+G pane status across the screens."""
    card = None
    pane = None
    for screen in screens:
        for m in RECEIPT_CARD_RE.finditer(screen):
            card = m.group(1)
        lines = screen.splitlines()
        for i, line in enumerate(lines[:-1]):
            if PANE_HEADING_RE.search(line):
                st = PANE_STATUS_RE.search(lines[i + 1])
                if st:
                    pane = st.group(1)
    return card, pane


def receipt_kind(screens):
    """Kind of the last receipt on screen, cross-checked against the pane status.

    Returns (kind, card_title, pane_status). Fails loudly when no card is found,
    or when the pane status contradicts the card.
    """
    card, pane = scan_receipts(screens)
    if card is None and pane is not None:
        fail(f"Ctrl+G pane shows RECEIPT {pane} but no titled receipt card is on screen "
             "(VERIFIED, DONE · NOT VERIFIED, FAILED, CANCELLED); the run's kind is unproven")
    if card is None:
        fail("no receipt card title on screen (VERIFIED, DONE · NOT VERIFIED, FAILED, CANCELLED)")
    kind = RECEIPT_KIND[card]
    if pane is not None and kind not in PANE_STATUS_KINDS[pane]:
        fail(f"receipt card says {card} ({kind}) but the Ctrl+G pane says RECEIPT {pane}")
    return kind, card, pane


def main(argv):
    if len(argv) < 3:
        fail(__doc__)
    cmd, path = argv[1], argv[2]
    if cmd == "captures":
        frames = sorted(glob.glob(os.path.join(path, "*.txt")))
        if len(frames) < 3:
            fail(f"only {len(frames)} screen captures; need at least 3")
        bodies = [open(f, encoding="utf-8", errors="replace").read() for f in frames]
        if len(set(bodies)) < 3:
            fail("screen did not change across captures (statuses never moved)")
        words = {"running", "passed", "done", "failed", "not run", "skipped"}
        seen = {w for b in bodies for w in words if w in b.lower()}
        if len(seen) < 2:
            fail(f"fewer than two status words seen across captures: {sorted(seen)}")
        for b in bodies:
            if re.search(r"panic:|goroutine \d+ \[", b):
                fail("panic text in a capture")
        print(json.dumps({"frames": len(frames), "status_words": sorted(seen)}))
        return
    if cmd == "receipt":
        screens = [open(f, encoding="utf-8", errors="replace").read() for f in argv[2:]]
        kind, card, pane = receipt_kind(screens)
        print(json.dumps({"kind": kind, "card": card, "pane": pane}))
        return
    if cmd == "receipt-seen":
        screens = [open(f, encoding="utf-8", errors="replace").read() for f in argv[2:]]
        card, pane = scan_receipts(screens)
        if card is None and pane is None:
            fail("no receipt card and no Ctrl+G pane RECEIPT section on screen")
        print(json.dumps({"card": card, "pane": pane}))
        return
    if cmd == "errorbox":
        print(error_card_text(open(path, encoding="utf-8", errors="replace").read()))
        return
    evs = events(path)
    if cmd == "stream":
        if not all("type" in e for e in evs):
            fail("an event has no type field")
        print(json.dumps({"events": len(evs)}))
    elif cmd == "topology":
        nodes = argv[3:]
        stages = [e for e in evs if e.get("type") == "stage"]
        report = {}
        for node in nodes:
            terminal = [e for e in stages if e.get("name") == node and e.get("status") in TERMINAL]
            report[node] = {"terminal": len(terminal), "statuses": [e.get("status") for e in terminal]}
            if len(terminal) != 1:
                fail(f"node {node}: {len(terminal)} terminal stage events, want exactly 1")
        print(json.dumps(report))
    elif cmd == "verdict":
        ends = [e for e in evs if e.get("type") == "run_end"]
        status = ends[-1].get("status") if ends else None
        body = text_of(evs)
        errors = [e for e in evs if e.get("type") == "error"]
        claim = status == "success" and "Not verified" not in body and not errors
        print(json.dumps({"run_end": status, "splice_verified": claim, "errors": len(errors)}))
    elif cmd == "failing":
        node = argv[3]
        stages = [e for e in evs if e.get("type") == "stage" and e.get("name") == node]
        if not any(e.get("status") in FAILED for e in stages):
            fail(f"node {node} has no failed/errored/aborted stage event")
        named = [e for e in evs if e.get("type") != "run_start" and node in json.dumps(e)]
        if not named:
            fail(f"no event names the failing node {node}")
        print(json.dumps({"node": node, "named_events": len(named)}))
    else:
        fail(__doc__)


if __name__ == "__main__":
    main(sys.argv)
