# Security in Splice

Splice can read a repository, change files, run commands, and call model or
extension endpoints. That makes its security boundary part of the product, not
just a deployment detail.

Splice gives you permission, sandbox, tool, hook, and provider controls. It adds
a pipeline that makes the analysis and execution stages explicit.

## The short version

- Reads stay local unless a configured tool or provider needs external data.
- Writes are limited to the workspace by default.
- Shell commands, network access, destructive actions, and extra write roots
  can require approval.
- Unsafe and autonomous modes are opt-in.
- Splice does not add telemetry or a hosted coordination service.
- Model providers, MCP servers, plugins, hooks, and browser helpers are still
  external trust boundaries. Review what you enable.

No coding agent can make an untrusted model or extension harmless. Run Splice
with the permissions and credentials appropriate for the repository in front of
it.

## What Splice protects

### Workspace boundaries

The normal policy allows the agent to inspect the workspace but limits writes to
it. Use an explicit grant for a second directory:

```bash
splice --add-dir ../shared
splice exec --add-dir ../shared "update the shared fixtures"
```

Inspect the effective policy before a sensitive run:

```bash
splice sandbox policy
splice sandbox grants list
```

### Side effects

Tool calls that can change the machine or reach outside the workspace are
visible to the permission layer. Review prompts before approving commands,
especially commands that install packages, modify git history, access secrets,
or send data to a network endpoint.

The `--worktree` mode is useful for reducing blast radius during experiments:

```bash
splice exec --worktree "try the migration"
```

A worktree is an isolation aid, not a security boundary. The process still uses
the host account and its available tools.

### Pipeline output

Pipeline stages exchange typed, validated structures. Deterministic checks such
as repository search, static analysis, tests, and diffs do not need to be
replaced by model claims. Invalid stage output stops the run rather than being
silently treated as success.

## Data and network boundaries

Splice stores sessions locally. The model provider receives the prompt and
context required by the provider you select. MCP servers, plugins, hooks,
`web_fetch`, browser helpers, and other configured integrations may send or
receive additional data.

Before using a cloud provider on a sensitive repository:

1. Check its data retention and training policy.
2. Avoid putting secrets in prompts or checked-in instruction files.
3. Review which tools and MCP servers are enabled.
4. Use a local model or disable network-capable tools when the repository
   requires it.

Splice may redact secrets from surfaces it controls, but redaction is not a
substitute for keeping secrets out of prompts, logs, plugins, and third-party
endpoints.

## Reporting a vulnerability

Please do not publish exploit details in a public issue.

Use GitHub's private vulnerability reporting from the repository's **Security**
tab and choose **Report a vulnerability**. Include:

- the affected version, branch, or commit;
- a minimal reproduction;
- the security impact and any required permissions;
- relevant logs or traces with credentials removed.

If private reporting is unavailable, open an issue containing only a request for
private contact. Do not include the vulnerability details.

We aim to acknowledge reports within a few business days and will coordinate a
fix and disclosure timeline with the reporter. Credit is included in release
notes unless the reporter prefers to remain anonymous.

## Scope notes

Reports are welcome even when you are unsure whether they are in scope. In
particular, please report possible sandbox escapes, unintended data disclosure,
credential exposure, unsafe permission bypasses, and malicious extension paths.

Issues that require a malicious local user who already controls the machine, or
that depend on the user explicitly disabling protections with
`--skip-permissions-unsafe`, are generally out of scope. We will still evaluate
the report and explain the decision.

## Responsible use

Treat repository instructions, plugins, MCP configurations, hooks, and model
output as inputs to review, not authority. A README or prompt can ask an agent
to run a command, but it cannot grant that command permission.
