# Specialists

A specialist is a named agent profile for focused interactive work. The profile
selects a prompt, model override, reasoning effort, and tool set.

Specialists belong to the interactive agent loop. The deterministic execution
pipeline does not delegate its fixed stages to specialists.

## Profile scopes

| Scope | Location | Priority |
|---|---|---:|
| Built-in | Compiled into Splice | 1 |
| User | `~/.config/splice/specialists/*.md` | 2 |
| Project | `.splice/specialists/*.md` | 3 |

A higher-priority profile replaces a profile with the same name. Splice includes
`worker`, `explorer`, and `code-review` profiles.

Review a project specialist before you trust the repository. Its prompt and tool
selection are repository input.

## Manage profiles

```bash
splice specialist list
splice specialist show worker
splice specialist path

splice specialist create api-review \
  --project \
  --description "Review API changes" \
  --tools read-only,plan \
  --prompt "Report compatibility breaks and missing tests."

splice specialist edit api-review --project
splice specialist delete api-review --project
```

Use `--json` with commands that support scripted output. `create --force` can
replace a regular profile file. It does not replace a symbolic link.

`edit` also rejects symbolic-link profiles before it opens `$VISUAL` or
`$EDITOR`.

## Manifest format

```markdown
---
name: api-review
description: Reviews API changes for compatibility and missing tests.
tools:
  - read-only
  - plan
---

Review API changes. Report behavior regressions, compatibility breaks, and
missing tests with file paths.
```

Supported frontmatter:

| Key | Purpose |
|---|---|
| `name` | Lowercase ID with letters, numbers, and dashes |
| `description` | Short list and task description |
| `extends` | Optional parent profile |
| `model` | Optional model override |
| `reasoningEffort` | Optional reasoning effort override |
| `tools` | Tool categories or tool IDs |

When a body is empty, Splice can use the description as the prompt and report a
warning.

## Tool categories

| Category | Tools |
|---|---|
| `read-only` | File read, directory list, search, and glob tools |
| `edit` | Read-only tools plus file edit tools |
| `execute` | Read-only tools plus the shell tool |
| `plan` | Plan update tool |

A child specialist cannot start another specialist or create a new specialist
profile. This rule bounds delegation depth at the tool boundary.

## Interactive task tools

The top-level interactive agent can:

- start a specialist;
- read or wait for background output;
- stop a background task; and
- create a project specialist from a description.

A background task returns a task ID. The same ID identifies its child session.
Use it to inspect, stop, or resume that task.

## Background state

Splice stores background task state under the local Splice data directory. Each
task has an event stream and a metadata file.

The metadata contains status, process identity, parent session, and timestamps.
A new process can therefore read a completed task after the original TUI exits.

If Splice starts and finds a stale task marked as active, it changes that task to
an error state. It also clears the old process ID.

## Cancel and timeout behavior

On Linux and macOS, each specialist process has its own process group. A cancel
or timeout stops the group.

On Windows, Splice stops only the direct child process. A process started by that
child can remain active. A two-second output wait prevents the Splice process
from waiting forever on an inherited pipe.

This Windows limit does not apply to Linux or macOS. Check the child processes
manually after a Windows timeout when the specialist started external tools.
