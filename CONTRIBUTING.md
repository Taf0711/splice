# Contribution guide

Splice uses an issue-first contribution policy. Maintainers complete
most implementation work while the project stabilizes.

Bug reports, feature requests, reproduction details, and focused discussion are
welcome.

## Start with an issue

Do not open an unsolicited pull request.

1. Search the current issues.
2. Open a bug report or feature request when no issue matches.
3. Describe the user problem and expected result.
4. Wait for maintainer review.
5. Start a pull request only after the issue has the `issue-approved` label.

Approval applies only to that issue and its agreed scope. It does not approve
other changes from the same contributor.

Maintainers can open pull requests for current project work. That activity does
not make the same area open for an unsolicited change.

## Useful contributions

You can help before an issue receives implementation approval:

- provide exact reproduction steps;
- identify the affected version and platform;
- attach redacted logs or screenshots;
- reduce a failure to a small repository;
- confirm whether a proposed fix resolves the problem; or
- describe a user need and current workaround.

Do not put credentials, private source, or vulnerability details in a public
issue. Read [SECURITY.md](SECURITY.md) for private reports.

## Pull request scope

An approved pull request must:

- link the approved issue;
- address only the approved behavior;
- explain the change and its reason;
- include tests or verification evidence;
- include a TUI capture when the visual result changes; and
- avoid unrelated format changes, refactors, dependencies, or features.

Use `Fixes #123` or another clear issue link in the description.

Ask on the issue before you expand scope. Maintainers can close a pull request
that starts before approval or contains unrelated work.

## Technical direction

Splice is a Go project. New Splice pipeline behavior belongs under
`internal/splice/` or a small CLI seam.

Keep inherited Zero changes small. A broad substrate rewrite creates a permanent
upstream merge cost.

The `memd/` directory is a separate Go module. The root test command does not
test it.

Prefer the standard library and existing project abstractions. Add a dependency
only when it solves an approved need that the current code cannot solve safely.

Read [AGENTS.md](AGENTS.md) for architecture, style, test, and documentation
rules.

## Validate a change

Run the smallest relevant test during development. Run the complete gates before
you request review.

```bash
gofmt -l .
go vet ./...
go test ./...
cd memd && go test ./...
```

Useful Make targets:

```bash
make check
make test-cli-tui
make test-memd
```

`gofmt -l .` must print no file names.

Document any test that you could not run. State the exact reason and the
residual risk.

## Write a bug report

Include:

- the Splice version, branch, or commit;
- the operating system and architecture;
- the provider type, without credentials;
- exact reproduction steps;
- the expected result;
- the actual result;
- redacted logs or screenshots; and
- a small reproduction when possible.

Use `splice --version` and `splice doctor` to collect basic setup information.
Review the output before you post it.

## Write a feature request

Describe:

- the user problem;
- the requested behavior;
- why it belongs in Splice;
- current alternatives; and
- acceptance conditions that can be checked.

Do not start implementation before the issue receives approval.

## Review behavior

Maintainers can request a smaller scope, more evidence, or another test. They can
also close a request that does not follow this policy.

A closed request is not a judgment about the contributor. It means the proposed
work does not fit the current project phase or approved scope.

## Ask for help

Open an issue when you are unsure whether a change fits. Keep the question
specific and include the affected command or file area.
