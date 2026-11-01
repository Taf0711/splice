# demo fixture

This module is a small demo web service. The eval runner copies it and
gives the copy to an agent. It uses only the standard library.

## Layout

- go.mod: module demo, go 1.26.
- cmd/server/main.go: builds the session store and wires the HTTP
  handlers. The session TTL is hard-coded here.
- internal/session/store.go: in-memory session store with an idle TTL.
- internal/session/store_test.go: basic store tests.
- internal/auth/password.go: the password reset flow. It does not touch
  sessions today.
- internal/admin/handler.go: admin HTTP handler skeleton. Force sign-out
  is a placeholder.
- pkg/errs/errors.go: small error helpers. It is not part of the session
  flow.
