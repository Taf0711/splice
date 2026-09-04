# Verifier authoring decisions (cognition families)

Read this before changing any fam-NN.sh. These records exist so a later
session does not re-litigate settled choices.

## Ground rules

- Each verifier runs with CWD = the arm directory (a copy of
  tests/evals/taskset-v0/fixture). It writes its own probe _test.go file,
  runs `go test -count=1 .` (plus `-race` for fam-06), deletes the probe
  file, and exits with go test's status.
- The verifier checks the target task's DELIVERABLE, never that the agent
  echoed the precursor's convention text.
- Structural checks grep the arm's own files. Each grep must fail the
  wrong patch and pass a differently formatted gold.
- Never weaken a verifier to make a run pass. If a check is wrong,
  reclassify after inspection and record the decision here.

## Per-family pins

- fam-01: target = AdminForceSignOut(store *Store, userID string) error in
  a NEW file admin.go that deletes every session of userID. Wrong variant:
  signs out only the LAST session of the user (partial invalidation), the
  exact bug the precursor warns about.
- fam-02: every expiry decision reads one sessionTTL constant. Wrong
  variant: NewStore takes a second literal (60m) used by the sweep, while
  sessionTTL stays 30m. The probe catches the drift via two stores.
- fam-03: DeleteAllForUser(userID) int in session.go plus
  TestDeleteAllForUserIdempotent in session_test.go. Wrong variant: returns
  an error contract for the empty case (deletes nothing, returns -1).
- fam-04: AdminSessionView in admin.go with the SAME explicit JSON tags as
  Session (precursor 04 pins ID, User, Created, LastSeen) plus Admin. Wrong
  variant: AdminSessionView without JSON tags (default marshaling).
- fam-05: mapStoreError single source of truth; new admin handlers call it.
  Wrong variant: admin handler hand-codes 404 inline. Structural grep:
  every admin status decision goes through mapStoreError.
- fam-06: SessionsForUser under -race with concurrent readers plus a
  writer. Wrong variant: RLock while mutating (drops expired entries), so
  -race fires.
- fam-07: exactly one test function TestSessionLookupTable in
  lookup_table_test.go, one table, controllable clock, 200/404/405. Wrong
  variant: three separate test functions.
- fam-08: session_unix.go (//go:build unix) + session_other.go
  (//go:build !unix), platformNote(), and the go.mod toolchain line kept
  at the pinned version. Wrong variant: deletes the pinned line.
- fam-09: target = the taskset healthz-uptime task (status "ok" from
  precursor-09 must stay, sessions key stays, uptime_seconds non-negative
  integer). Wrong variant: renames sessions to session_count.
- fam-10: GET /admin/session?id=X reuses the SAME id validation rule
  (400 on invalid, store ErrInvalidID path reused). Wrong variant: admin
  path skips validation and serves the raw lookup.

## Validation

validate_families.py materializes a pristine fixture copy per family and
runs the three checks: BASE fails, GOLD passes, WRONG fails. Results land
in registry-families.json as letters [base/gold/wrong].