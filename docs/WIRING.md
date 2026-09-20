# Wiring checks

A feature can pass a build and a test suite while its wiring is missing. A
field can be produced and never read. A config value can resolve and never
reach its consumer. A callback can be defined and never invoked. The wiring
check makes these failures loud.

This defect class is not visible to a green build or green CI. It is the
dominant defect class in this project.

## How it works

`internal/wiring` holds a contract list. Each contract names three items:

- a producer declaration, for example
  `internal/splice/schemas.ExecutionStage.DependsOn`;
- the production function or method that must reference the producer;
- the test that proves the behavior end to end.

`TestContractsHold` parses every non-test Go file and fails when a producer is
never referenced in production code, or when the named consumer stops
referencing it. `TestContractsNameExistingProofs` fails when a named proof test
does not exist. The proof test runs in the normal suite.

The checker uses `go/parser` only. It adds no dependency.

## Add a contract

1. Add a `Contract` entry to `internal/wiring/contracts.go`.
2. Name the direct reader as the consumer. If a refactor moves the read, update
   the consumer. Move wiring only on purpose.
3. Name the test that proves the behavior. Add that test when it is missing.
4. Run `go test ./internal/wiring/`.

## Inert producers

Some producers have no consumer yet. Record them with `Inert: true`, an
`Owner`, and a `Reason`. The check fails when the symbol disappears, so the
entry cannot rot. Change the entry when the consumer lands.

## Limits

The check is static and name-based. It proves that a reference exists. It does
not prove that the reference runs, and it does not build a full call graph. The
named proof test covers the behavior.