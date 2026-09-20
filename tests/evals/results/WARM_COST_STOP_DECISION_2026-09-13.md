# Stop decision: the memory-reuse line for code-writing tasks

Date: 2026-09-13.
Branch: `wip/evidence-substitution-production`.
Status: DECISION. The warm-cost memory-reuse line for code-writing tasks is stopped. This is a structural negative, not a null measurement.

## 1. Decision

Stop the attempt to show that retained cognition reduces total cost for
code-writing tasks on this pipeline. Record the reason as structural. Do not
spend more provider runs on the mechanism. Keep the repaired contract and the
retention harness as correct infrastructure.

This follows the memo's own condition: if warm does not reduce provider requests
per verified completion on a corpus that triggers expansions, stop.

## 2. The four findings

Each finding is code-cited and was reproduced on a live model after the contract
repair.

### 2.1 The dominant need is not substitutable

`internal/splice/admission.go:163`: `NeedInspectEditTarget` is always
`AdmissionHintOnly`, with the reason "edit-target needs current body bytes;
location evidence is not a body view". A code-writing task must read the current
body of the file it edits. That read is the expensive operation, and it is
excluded by design.

`NeedOpenDiscovery` is also never substituted (`admission.go:161`).

### 2.2 Procedures are never freshness-provable

`internal/splice/discovery.go:443-444` anchors the verified procedure on the
`"test"` anchor kind only. `discovery.go:250` rejects any node with no file
anchor: "No file anchor to diff: the node cannot prove freshness."

The retention confirmation showed both sides of this rule: the write's
`clock_test.go` fact (file and symbol anchors) was admitted FRESH and delivered,
while the procedure node failed freshness with no file anchor. Procedural
knowledge therefore cannot enter the reusable set at all.

### 2.3 Delivered facts did not remove provider requests

Across the pilot rounds and the retention confirmation, the warm arm delivered
evidence but recorded **zero substitutions**, and the read task still issued its
own context requests and expansion rounds. The one measurable route used was
`request_context`, which is a cost, not a saving.

### 2.4 The elimination set is prompt-neutral

The earlier measured result (`EVIDENCE_SUBSTITUTION_ECONOMICS_2026-09-10.md`)
found that the only substitutable operations contribute no model-visible
context, so removing them cannot reduce prompt tokens or model tool calls. The
live retention run reproduces the same outcome on a correct contract, which
rules out the contract as the explanation.

## 3. What the contract repair did and did not do

It did remove real confounds, and that work stands:

- Schema, prompt, and decoder now agree on the `request_context` and
  `submit_changes` actions, guarded by `TestRequestContextSchemaMatchesValidators`.
- Typed-output retries are billed as `format_retry`, separately from generation
  and expansion.
- A declared-action conflict fails loud instead of being silently downgraded.
- The retention harness commits the verified write tree and reanchors the
  capture set, so HEAD-anchored evidence classifies fresh at the next task.

None of that changed the outcome, which is the point: the negative is now
measured on a repaired contract rather than on a broken one.

## 4. What would change the decision

1. Admission accepts current-body evidence for an edit target under an exact
   revision match, so the dominant read becomes substitutable. That is a design
   change with a correctness risk, not a tuning change.
2. Procedures gain a file anchor and a freshness rule that can validate them.
3. A task family where the expensive work is location or discovery rather than
   an edit target.

Without one of those, further runs can only re-measure the same wall.

## 5. Operating note

The measurement host is under disk pressure: `/System/Volumes/Data` at 93 percent
used, 29 GiB available, with macOS marking repo files compressed and dataless.
The Go toolchain read those files as zero bytes or `resource deadlock avoided`
and aborted one launch before any provider call.

Free space before any future run, and keep build artifacts out of the repo. This
is the same pressure class that preceded an earlier `.git` pack failure in this
work.

## 6. Non-claims

- No token or cost saving is claimed for warm cognition.
- No correctness improvement is claimed.
- No claim is made that the mechanism cannot work in another pipeline or for a
  different task class.
- The pilot and retention runs withheld the total-cost claim because coverage
  was partial in at least one attempt, so no aggregate cost figure is asserted
  here.