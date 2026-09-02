# EVAL-V0 Task Authoring — Progress (checkpoint)

## Where
- Taskset: ~/Documents/splice-c1c-work/eval-taskset-v0/
  - fixture/ = 5-file session-service demo (copied from splice-eval-taskset; baseline build+test green)
  - tasks/*.json = {name, prompt, check, gold{path:content}, wrong{...}, hack{...}}
  - validate/validate.py = the section-16 validator (base-fails / gold-passes / wrong-fails / hack-fails)
  - registry.json = last full validation run

## Validator
  cd ~/Documents/splice-c1c-work/eval-taskset-v0 && python3 validate/validate.py [--only NAME]
Exit 0 = all validated. '-' marks = audit metadata absent for that check.

## Validated (10/30) - all [YYYY]
1. create-conflict-error        bug fix/API: Create returns (Session, error), ErrConflict, 409
2. sessions-active-since        feature: SessionsActiveSince, sorted asc, strictly-after cutoff
3. get-no-refresh               bug fix: Get read-only + Touch + POST /session/touch (204/404)
4. len-skips-expired            bug fix: Len counts only live; exactly-ttl boundary is live
5. expire-sweep-endpoint        feature: Sweep() int + POST /admin/sweep {"swept":N} + 405
6. typed-lookup-error           API: LookupError{ID} with errors.Is(err, ErrNotFound) compat
7. listen-addr-from-env         build/config: listenAddr(getenv) validating ADDR, default :8080
8. unix-timestamps-wire         data/state: Created/LastSeen as unix seconds via MarshalJSON
9. structured-json-logs         CLI/tooling: logLine(level,msg,fields) one-JSON-line logger
10. expiry-predicate-single-source  refactor: extract expired(session) + structural greps

## Existing 12 (not duplicated) live in ~/Documents/splice-eval-taskset/tasks
audit-ring-buffer, capacity-eviction, conflict-on-recreate, count-by-user,
delete-session-endpoint, healthz-uptime, list-sessions-sorted, parse-ttl-from-env,
per-session-ttl, session-json-tags, snapshot-persistence, token-auth-middleware

## Authoring gotchas (do not re-trip)
- Signature changes break the FIXTURE's own session_test.go -> gold must carry
  the updated test file, or the change must be additive-only.
- Fixture Get REFRESHES LastSeen: any probe Get shifts expiry. Compute "all
  expired" timestamps AFTER accounting for refreshes.
- Map iteration order is random: unsorted-output wrong-patches can pass by luck.
  Assert exact order over ~20 repeated queries.
- Handlers registered on the DEFAULT mux in main.go; probes call sessionHandler
  etc. directly. POST "/session/touch" via sessionHandler hits the POST
  /session branch (400). go 1.26 mux: "/x" and "/x/touch" both exact; most
  specific wins.
- Refactor tasks: behavior probes cannot see structure. The check must append
  structural greps AFTER the go-test block but BEFORE `exit $rc` (an `exit $rc`
  early-return silently skips everything after it - burned one loop).
- A pure-refactor hack must break behavior subtly (e.g. > becomes >=) to fail
  the probe; a no-op patch passes everything.
- Race-detection tasks need their own fixture variant with a real race; the
  shared fixture is race-clean. Logged, not faked.

## Remaining (20 tasks to 30) - distribution gaps vs section 14
refactors (2), tests/debugging (1-2), CLI/tooling (1-2), security/safety (2-3),
API/backend (3), docs-needing-code (1), data/state (2), feature (3-4), bug (2-3).
Ideas: per-user rate limit, content-type enforcement on POST, graceful shutdown,
read-replica snapshot, id-validation rule, write-through persistence hook,
pagination for list, request-size cap, Healthz detail gating, marker interface
for stores, table-driven fixture test task, changelog-with-code task.

## After 30
- Commit taskset into repo under tests/evals/taskset-v0 (owner preference on
  location); registry.json is the section 34 task-quality registry (status:
  validated -> adversarially-reviewed once human-reviewed).
