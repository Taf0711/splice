# Configuration

Use `splice setup` for the first provider profile. Use `splice config` to inspect
the resolved result without secret values.

```bash
splice setup
splice config
splice config --json
splice doctor
```

## Configuration files

Splice reads these files when they exist:

| Scope | Path |
|---|---|
| User | `$XDG_CONFIG_HOME/splice/config.json` |
| User fallback | `~/.config/splice/config.json` |
| Project | `<workspace>/.splice/config.json` |

The macOS user path also uses `~/.config/splice` unless
`XDG_CONFIG_HOME` is set.

Splice applies sources in this order. A later source can replace an earlier
value.

1. Built-in defaults
2. User configuration
3. Project configuration
4. Environment variables
5. Provider command output
6. CLI flags

A project file cannot grant access outside the workspace. It also cannot enable
network access when the user policy denies it. Use user configuration or an
explicit CLI grant for those changes.

## Provider profiles

Use the provider commands instead of direct file edits when possible.

```bash
splice providers list
splice providers catalog
splice providers add
splice providers use <name>
splice providers check <name>
splice models list
```

A profile selects a transport, endpoint, model, and credential source. Custom
OpenAI-compatible and Anthropic-compatible endpoints are supported.

Use environment variables for short-lived provider keys:

```bash
export OPENAI_API_KEY=...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
export OPENROUTER_API_KEY=...
```

Do not commit provider keys to `.splice/config.json`. Splice can migrate stored
provider keys to its local credential store.

Set `SPLICE_PROVIDER=<profile-name>` to select an existing profile for one
process.

## Local models

Use `splice providers detect` to check local Ollama and LM Studio services.
Select the detected profile through setup or the provider commands.

Typed pipeline stages need tool-call support. A text-only model can fail a typed
stage even when ordinary chat works.

## Sandbox configuration

Inspect the effective policy before you change it:

```bash
splice sandbox policy
splice sandbox grants list
splice sandbox check bash --side-effect shell
```

Project configuration can make a policy stricter. It cannot add external read
or write roots, restore protected credential paths, or enable network access.

Use `--add-dir <path>` for a specific extra write root. Use the user
configuration for persistent personal grants.

## Worktrees

TUI pipeline runs use an isolated Git worktree by default. Set this in user or
project `config.json`:

```json
{"worktrees":{"enabled":true,"directory":"/path/to/worktrees"}}
```

`enabled` defaults to on. `directory` selects the base directory for created
worktrees. When worktrees are off, or create fails, the TUI runs in the live
checkout and reports that rollback is unavailable. Design and spec-draft runs
stay in the live checkout.

## Sessions and local data

Splice stores session events on the local machine. Use these commands to inspect
or remove old session data:

```bash
splice sessions list
splice sessions prune-plan --older-than 30
splice sessions prune --older-than 30
```

The optional memory sidecar uses a local SQLite database. Set these variables
only when the defaults do not fit:

```bash
export SPLICE_MEMD_BIN=/path/to/splice-memd
export SPLICE_MEMD_SOCKET=/path/to/mem.sock
export SPLICE_MEMD_DB=/path/to/mem.db
```

Memory access is fail-soft. A sidecar error does not replace the pipeline result.

## Data directories

Splice stores per-user data under the XDG base directories. The variables are
read at startup. Create the directories if you pre-seed them.

| Data | Location |
|---|---|
| Sessions, cron jobs, skills, hooks, memory socket | `$XDG_DATA_HOME/splice/...`, or `~/.local/share/splice/...` |
| Worktree state | `$XDG_STATE_HOME/splice/...`, or `~/.local/state/splice/...` |
| Config, trust decisions | `$XDG_CONFIG_HOME/splice/...`, or `~/.config/splice/...` |

Session history lives under `XDG_DATA_HOME`. Set `XDG_DATA_HOME` (not
`XDG_STATE_HOME`) when you test with an empty session store. Set both
variables for a full sandbox.

A tool override does not relocate data. `SPLICE_MEMD_DB` moves the memory
database only.

On macOS the API keys in the system keyring follow the login keychain, which
resolves under the real home directory. A test process that overrides `HOME`
cannot read stored keys. Override `XDG_*` variables instead, or run
`splice auth login` inside the sandbox.

## Display and notifications

Common display controls:

| Variable | Effect |
|---|---|
| `NO_COLOR` | Disable color output. |
| `SPLICE_THEME` | Select the initial TUI theme. |
| `SPLICE_NO_FADE=1` | Disable the stream fade effect. |
| `SPLICE_NO_RESUME_PROMPT=1` | Skip the session resume prompt. |

Use `--notify` for one run. Use `--no-notify` for CI or another noninteractive
surface.

## Update controls

| Variable | Effect |
|---|---|
| `SPLICE_DISABLE_UPDATE_NOTICE=1` | Hide the background update notice. |
| `SPLICE_DISABLE_UPDATES=1` | Disable update checks and install commands. |
| `SPLICE_UPDATE_RELEASE_URL=<url>` | Select another release metadata endpoint. |

Read [Update Splice](UPDATE.md) before you use a custom endpoint.

## Authentication storage

Use `splice auth status` to inspect the active backend. The command never prints
a token.

macOS uses the system keyring by default. Other platforms use an encrypted local
file. Select a backend during a successful login:

```bash
splice auth login <provider> --storage encrypted-file
```

This command saves `auth.storage` in the user `config.json` file. A project file
cannot set this value. Valid values are `keyring`, `encrypted-file`, and `file`.
The `file` value stores plaintext credentials with `0600` permissions.

When `auth.storage` is not set, `SPLICE_CRED_STORAGE` selects API key storage.
`SPLICE_OAUTH_STORAGE` selects OAuth storage. Automatic selection is last.
Splice does not read another backend when the selected backend fails.

Read [OAuth and provider login](oauth-subscriptions.md) for the supported flows.
