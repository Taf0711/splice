# Security in Splice

Splice can read source, change files, run commands, and call external services.
Use permissions that match the repository and task.

No code agent can make an untrusted model, extension, or repository harmless.
Review every trust boundary that you enable.

## Default posture

- Workspace reads are available by default.
- Writes stay inside approved roots.
- Shell, network, destructive, and elevated actions pass through policy checks.
- Unsafe and high-autonomy modes require explicit flags.
- Project configuration cannot grant access outside the workspace.
- Sessions and optional memory stay on the local machine.
- Splice adds no required hosted coordinator and no product telemetry.

Inspect the effective policy before a sensitive run:

```bash
splice sandbox policy
splice sandbox grants list
```

## Workspace boundaries

Use an explicit grant for another write root:

```bash
splice exec --add-dir ../shared "update the shared fixtures"
```

A project `.splice/config.json` cannot add an external read or write root. It
also cannot enable network access when user policy denies it.

The sandbox denies common credential directories by default. A user-level
policy can restore a required nested path. Project configuration cannot restore
it.

## Side effects

The permission layer classifies tool calls by side effect. Review prompts for:

- package installation;
- network access;
- Git history changes;
- credential access;
- commands outside the workspace; and
- privileged or destructive operations.

Do not use `--skip-permissions-unsafe` on an untrusted repository. Treat
`--auto high` as an explicit expansion of agent authority.

## Worktrees

An isolated worktree reduces the effect of a failed edit:

```bash
splice exec --worktree "try the migration"
```

A worktree is not a security boundary. The process still uses the host account,
provider credentials, tools, and network policy.

Use `--merge-back` only when the source worktree is clean and you intend to
apply the result.

## Model and extension boundaries

A cloud provider receives the prompt and the context sent to that provider. MCP
servers, plugins, hooks, skills, browser helpers, and compatible endpoints can
create additional network or process boundaries.

Before you use them on sensitive source:

1. Review provider data-use and retention terms.
2. Remove secrets from prompts and repository instructions.
3. Inspect project extension configuration.
4. Disable tools and network access that the task does not need.
5. Prefer a local model when policy requires local inference.

Repository instructions are data, not permission. A file can request a command,
but it cannot approve that command.

## Local storage

Splice stores session events locally. Session data can contain prompts, file
paths, tool arguments, and tool output.

The optional memory sidecar stores bounded observations in a local SQLite
database. The sidecar can retain test commands and project configuration
summaries across sessions.

Protect the local data and configuration directories with host file permissions.
Prune old sessions when their retention is no longer necessary.

```bash
splice sessions prune-plan --older-than 30
splice sessions prune --older-than 30
```

OAuth tokens and provider keys use the configured credential store. Use
`splice auth status` or `splice config`; these commands do not print token
values.

## Output redaction

Splice redacts known secrets on output surfaces that it controls. Redaction is a
last defense, not a safe transport for secrets.

A plugin, hook, MCP server, provider, shell command, or proxy can send data before
Splice sees its output. Keep secrets out of prompts and untrusted tools.

## Typed pipeline boundary

Pipeline stages exchange validated data. Local tools supply repository search,
static checks, tests, diffs, and exit codes.

Typed validation prevents malformed model output from producing silent success.
It does not prove that a valid model decision is correct.

An incomplete verification report remains visible in the pipeline result. Review
it before you accept a change.

## Report a vulnerability

Do not publish exploit details in a public issue.

Use GitHub private vulnerability reports:

1. Open the repository Security tab.
2. Select **Report a vulnerability**.
3. Provide the affected version or commit.
4. Add a small reproduction and impact statement.
5. Remove credentials and private source from all attachments.

If private reports are unavailable, open a public issue that asks for private
contact. Do not include vulnerability details.

Maintainers will confirm receipt and coordinate the fix and disclosure with the
reporter. Release credit is optional.

## Report scope

Report possible sandbox escapes, data disclosure, credential exposure,
permission bypasses, and malicious extension paths.

A report can be out of scope when it requires a malicious local user who already
controls the host. It can also be out of scope when it requires the user to
disable the relevant protection explicitly.

Maintainers will still review the report and explain the scope decision.
