# Decision backends

The trajectory classifier uses the decision advisor's evaluator.
The host controls every action. A model cannot change a hard limit or a permission.

The default backend is `openrouter_decisions`.
The classifier uses question pack `traj_action_v2` unless the section sets another pack.

Mode `off` keeps the rules path and makes no model call.
These runs produce no classifier records or classifier progress lines.

## The default classifier

The trajectory classifier is on by default. The flag is `pipeline.trajectory_classifier`.
It applies when you have not set a `decision_advisor` section, or when your section is inactive
(`pipeline.decision_advisor` is off and the section's `mode` is not `off`).
In that case the default replaces the section, and the section's backend, model, and mode are not used.
It uses the first configured ChatGPT sign-in profile, which is a profile whose catalog id is `chatgpt`.
The settings are the ones validated in campaign A1 (2026-10-09). Every setting is fixed:

- mode `active_experimental`, backend `provider`, model `gpt-6.1-sol` (fixed; the ChatGPT profile supplies only its endpoint and credentials)
- question pack `traj_action_v2`, no reasoning effort is sent (as in A1)
- default thresholds: step_back 0.6, rollback 0.7, escalate 0.7, stop 0.8
- timeout 120 s, request and response 64 KiB, advisor cost cap $1

The 120 s timeout is the timeout A1 ran with, from its campaign config. No p99 measurement backs it.

The default runs the classifier only. The dependency selector and the prefetch decisions stay off.
They run only when you set `pipeline.decision_advisor` and a `decision_advisor` section.

When no ChatGPT sign-in profile exists, the rules decide. The run prints one line that says so.
`splice doctor` prints the state of the classifier: rules only, on with a profile and model, or off.

To turn the default off, run with `--flags -pipeline.trajectory_classifier`, or set the flag to `false` in user config.

To use another backend, write a `decision_advisor` section. An explicit section wins over the default.
Such a section applies only when `pipeline.decision_advisor` is `true`.
A section whose `mode` is `off` is an opt-out: the default classifier stays off, even when `pipeline.decision_advisor` is off.
Any other section that is inactive hands the classifier to the default.

Set the user flag `pipeline.decision_advisor` to `true` to enable the advisor.
Select `shadow` to record classifier decisions while the rules control the run.
Use a shadow campaign to calibrate each backend's thresholds before active use.

```json
{
  "flags": { "pipeline.decision_advisor": true },
  "decision_advisor": {
    "mode": "shadow",
    "backend": "http",
    "profile": "laya-traj-1",
    "question_pack": "traj_action_v1",
    "endpoint": "https://example.com/decide",
    "api_key_env": "LAYA_API_KEY",
    "limits": {
      "max_request_bytes": 16384,
      "max_response_bytes": 65536,
      "timeout_ms": 1500,
      "reservation_usd_per_million_input": 0.042
    }
  },
  "trajectory": {
    "thresholds": {
      "step_back": 0.6,
      "rollback": 0.7,
      "escalate": 0.7,
      "stop": 0.8
    }
  }
}
```

The configuration accepts `decision_advisor` and the existing `decisionAdvisor` name.
`SPLICE_DECISION_ADVISOR_MODE` and `SPLICE_DECISION_ADVISOR_MODEL` retain the existing experiment controls.
`SPLICE_TRAJECTORY_PACK` can select the question pack for an experiment.

| Backend | Configuration | Authentication |
| --- | --- | --- |
| `openrouter_decisions` | Set `profile` to the decision model. | Use `OPENROUTER_API_KEY` or the stored OpenRouter key. |
| `http` | Set `endpoint` and `profile` to the service model. | Set `api_key_env` when the service requires a bearer token. |
| `provider` | Set `provider_profile` to a configured provider profile. | Use that profile's API key or OAuth login. |

The provider backend uses the profile's model and one forced `answer` tool.
It permits one automatic-tool-choice retry if the provider rejects the forced tool.
These probabilities are model statements, not logprobs. Calibrate the thresholds for each backend.

The host first checks the iteration limit and the output token budget.
It then selects the label with the highest probability.
A probability below that label's threshold gives `continue`.

A valid advisor answer can request `step_back` once per run.
After that action, two consecutive `stop` or `step_back` decisions can stop the run.
A rollback requires an isolated worktree and an earlier snapshot with a higher score.
Without an isolated worktree, a rollback decision becomes a step back.

An escalation can occur once per run.

At a repair boundary, an active classifier decision of `step_back` or `stop` skips the remaining repairs.
Shadow decisions and rules fallbacks preserve the repair loop.
The pass boundary executes the requested action.

A headless `stop` ends the run with the `aborted` status.

The `traj_v1` pack retains two noul questions: `same_cause` and `new_information`.
Its `step_back` probability is `same_cause * (1 - new_information)`.
Its `continue` probability is the remainder. The other three probabilities are zero.

A refusal, absent evaluator, transport error, or invalid answer returns the rules decision.
The disposition record states the cause. The rules fallback keeps the existing actions.
A boundary state error records its cause and keeps the original repair or stage failure path.

Missing cost remains unknown. The advisor retains a conservative reservation until it receives a reported charge.

## Splice HTTP decision contract v1

Send one POST request to the configured endpoint.
Set `Content-Type: application/json`.
When `api_key_env` is set, use its value in the `Authorization: Bearer <token>` header.

```json
{
  "contract": "splice.decision.v1",
  "model": "laya-traj-1",
  "session_id": "run-123",
  "state": "{\"boundary\":{},\"scores\":[],\"hash_repeats\":{}}",
  "questions": [
    {
      "id": "trajectory_action",
      "kind": "choice",
      "instructions": "Select the next trajectory action from the boundary evidence.",
      "criteria": [
        {"label": "continue", "description": "Continue the current approach."},
        {"label": "step_back", "description": "Review the cause and choose a new approach."},
        {"label": "rollback", "description": "Restore an earlier state with a higher score."},
        {"label": "escalate", "description": "Use the escalation model."},
        {"label": "stop", "description": "Ask the user for a decision."}
      ]
    }
  ]
}
```

The `state` field is a compact JSON string.
It contains pass scores, test counts, acceptance counts, changed file paths, hash repeats, and the rules decision.
Change summaries and failure text have fixed bounds. The state contains no file contents or credentials.

Return a 2xx status and this response shape:

```json
{
  "request_id": "abc",
  "resolved_model": "laya-traj-1-2026-10-01",
  "provider": "laya",
  "answers": [
    {
      "question_id": "trajectory_action",
      "kind": "choice",
      "probabilities": {
        "continue": 0.7,
        "step_back": 0.2,
        "rollback": 0.0,
        "escalate": 0.0,
        "stop": 0.1
      },
      "confidence": 0.8
    }
  ],
  "usage": {"input_tokens": 0, "output_tokens": 0, "cost_usd": null}
}
```

Return one answer for each question. Use the exact question ID and kind.
For a noul answer, return `true_probability` instead of `probabilities`.
The noul criteria must have the labels `false` and `true`, in that order.

All probabilities and optional confidence values must be between zero and one.
Omitted choice labels become zero. Unknown labels are errors.
The choice probability sum must differ from one by no more than 0.02.

The resolved model must equal the requested model or start with that model plus a hyphen.
A different model is an error.
`cost_usd: null` means unknown cost. A reported zero means zero cost.

A non-2xx response is an error. Its bounded body text appears in the error.
The client bounds both bodies, honors the timeout and cancellation, and makes no HTTP retry.
A malformed answer retains any valid reported usage for the ledger.

## Save and replay decision records

Set `SPLICE_DECISION_RECORDS_DIR` to a local directory before a shadow campaign.
At each run exit, Splice writes all advisor records to `<directory>/<session-id>.jsonl`.
Each line contains one record with its semantic snapshot.
The directory uses mode `0700`. The file uses mode `0600`.
A write error produces one warning. It does not fail the run.

Use the replay command to compare decision backends on the same boundaries:

```sh
go run ./tests/evals/decisionreplay/cmd/decision-replay \
  --records ./decision-records \
  --backend-config ./replay-backend.json \
  --out ./replay.jsonl \
  --concurrency 2
```

The backend file contains the `decision_advisor` section's JSON object, without the outer section name.
The command loads the normal Splice configuration and respects `XDG_CONFIG_HOME`.
Provider profiles and stored keys use the normal resolver.
Repeat `--records` for more files or directories.

The default pack comes from each snapshot.
Use `--pack traj_action_v1` or `--pack traj_v1` to override it.
A pack change requires complete boundary evidence.
Use `--thresholds '{"step_back":0.65,"stop":0.85}'` to override the thresholds.

Replay bypasses the advisor's budget gates. Request limits, response limits, and timeouts still apply.
The command evaluates boundaries concurrently and applies the active host policy in order within each run.
Each backend uses its own step-back and escalation state.
Duplicate records for one boundary produce one output row.
Older candidate-only snapshots have conservative defaults for absent rollback evidence.

### Compare recorded and swapped evidence

Each saved boundary carries two sets of workspace evidence.
The recorded values are the HEAD values.
The evidence shadow holds the pass-builder values.
Use the `--evidence` flag to choose which values the replay reads:

- `recorded` replays the boundaries as saved. This is the default.
- `swap` replays each boundary with the shadow values.
- `compare` replays both and prints a drift summary.

Use records from a run with `pipeline.pass_evidence` off.
Then the recorded values are HEAD values and the shadow holds the pass values.

```sh
go run ./tests/evals/decisionreplay/cmd/decision-replay \
  --records ./decision-records \
  --backend-config ./replay-backend.json \
  --evidence compare
```

`compare` writes the JSON summary to standard output.
It writes a table to standard error.
It needs no `--out` file.

The replay compares two results for each boundary: the rules action and the
classifier top label. It does not compare reason text.

The replay rebuilds the rules for each boundary from the History it replays.
Both evidence modes use the same rules.
A live decision also includes the billed cost signal.
A saved record does not carry that signal, so replay rules leave it out.

`compare` also replays the recorded evidence a second time. It reports the
self-agreement S. S is the share of boundaries where both replays give the same
classifier top label. S measures the backend's own noise. Read the drift
against S.

`compare` reports the number of distinct runs it compared.
It also reports the number of degraded shadows.
A shadow whose pass builder degraded is skipped, with its reason.
A git campaign should report zero degraded shadows.

The records hold only the passes at a classifier boundary.
To count the degraded passes of every pass, give the run traces with `--traces`.
A trace is a `RunOutcome` JSON object. A file can hold one object or one object per line.
`--traces` accepts a file or a directory, and you can repeat it.
The table then shows the degraded trace passes, with each run, iteration, and reason.
Without `--traces`, the table shows "not read" for the trace passes.

Each disagreement has one class.
The class looks only at the History entries where the two sources differ on
workspace progress.

- `preexisting_dirt`: a file was already changed before pass 1. No pass builder
  lists it.
- `stale_progress`: the HEAD evidence lists a file that the pass builder did
  not list in that pass.
- `unexplained`: no class fits. A recorded progress flag that disagrees with
  the pass shadow also gives this class. Review these boundaries by hand.

The replay skips these kinds of record. It does not evaluate them.

- A projection failure. It has no snapshot.
- A record that is not a trajectory boundary, for example a selector record
  of the `dependency_relation_v1` pack. The records file holds these too.
- A later record of a boundary. The replay uses the first record.
- A record with no usable evidence shadow, in `swap` or `compare` mode.

`compare` also skips a boundary when one of its replays fails.
Each record is compared or skipped. The table shows `records`, and `records`
is equal to `boundaries` plus `skipped`.

The summary lists each skipped record with its run and iteration.
A projection failure has no run ID. The summary lists it as `(unknown)`, with
its reason.

The output contains probabilities, the selected label, the policy action, usage, and reported cost.
Unknown cost is `null`. A failed call produces an error row, and replay continues.
The command prints boundary, answer, error, and action counts to standard output.
