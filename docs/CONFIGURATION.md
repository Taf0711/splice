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

The project file applies only in a trusted workspace. Splice does not read it
in a workspace that is not trusted. Trust the workspace with `/trust` in the
TUI, the `--trust` flag, or `defaultProjectTrust` in the user configuration.
After `/trust` in the TUI, the project file applies on the next launch. The
project file cannot set sandbox roots, `worktrees.directory`, or `plugins`.

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

## Feature flags

Feature flags turn some Splice behaviors on or off. Set them with the `flags`
key, `SPLICE_FLAGS`, or the root `--flags` option. See
[Feature flags](FEATURE_FLAGS.md) for the list of flags and the precedence
order.

## Verification command

Set `verify.testCommand` to run a repository-specific test command. The command
still uses the stage sandbox and the normal command allowlist.

Set `verify.testDir` to a relative directory below the workspace. The default is
the workspace root. Use `{tests}` or `{test_modules}` as a whole command item.
Splice replaces these items with the test files for the current pass.

```json
{"verify":{"testCommand":["python","tests/runtests.py","{test_modules}"],"testDir":"tests"}}
```

`{tests}` uses paths relative to `testDir`. `{test_modules}` drops `.py` and
changes path separators to dots.

Splice uses two kinds of test file:

- Each changed test file.
- Each existing test file that has the name of a changed source file.

Rules for the second kind, for a changed source file `dir/name.ext`:

- Python (`.py`): `dir/tests/test_name.py`, `dir/test_name.py`,
  `dir/name_test.py`, `dir/tests/name_test.py`, and `tests/test_name.py` at
  the workspace root (for `src` or `lib` layouts).
- Go (`.go`): the package directory, for example `./dir`. A changed Go source
  file or Go test file in `dir` gives `./dir` when `dir/name_test.go` exists.
  Splice passes the package, not one file, because `go test` cannot build a
  single test file alone.
- JavaScript and TypeScript (`.js`, `.jsx`, `.ts`, `.tsx`, `.mjs`, `.cjs`):
  `dir/name.test.ext`, `dir/name.spec.ext`, `dir/__tests__/name.test.ext`, and
  `dir/__tests__/name.ext`. The extension is the same as the source file.
  A file under a `__tests__` directory is a test file.

Each file must lie inside `testDir`. Splice lists the changed test files first,
then the related files. It passes at most 20 paths. The cap never drops a
changed test file for a related one.

When no test file applies, the command does not run. The run records that no
tests ran. The pass is not verified, and Splice does not start a repair.

## Pipeline wall-time limit

Set `pipeline.max_wall_seconds` in the user `config.json` to change the
wall-time limit of one pipeline run. The default is 600 seconds. The value
`0` uses the default.

```json
{"pipeline":{"max_wall_seconds":1800}}
```

`SPLICE_MAX_WALL` (a Go duration such as `30m`) replaces this value. The
`splice exec --max-wall` flag replaces both. A project file cannot set this key.

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

Pipeline stages that run repository code use a stricter stage sandbox. It
keeps your `denyRead` and `denyWrite` entries. It has no network access, and
it does not read credential files. Your grants and extra roots do not apply
to it. See [Stage sandbox](PIPELINE.md#stage-sandbox).

When Splice runs inside a sandbox, it cannot apply the stage sandbox. Stage
commands, command nodes, and agent nodes then refuse to run. To let them run
under the outer sandbox, set `sandbox.acceptNestedSandbox` in the user
`config.json`:

```json
{"sandbox":{"acceptNestedSandbox":true}}
```

Splice reads this key only from the user configuration. A project
`config.json`, a provider command, and an environment variable cannot set it.
The key does not let a command run when no sandbox backend is available or when
the policy is disabled. It does not change plugin tools or hooks. For one
`splice exec` run, use `--accept-nested-sandbox`.

## Plugins and hooks

Plugins are third-party code. Splice runs each plugin tool command and each
plugin hook inside the sandbox, in all modes.

Plugin tools and interactive plugin hooks use the agent sandbox policy. In a
pipeline run, hooks use the stage sandbox.

A sandboxed plugin can read its own install directory. It cannot read other
files in the Splice configuration directory, or other plugins.

When no sandbox enforces the policy, a plugin tool or hook does not run.
Splice shows an error that names the plugin and the configuration key. Splice
does not run the command on the host as a fallback.

To let one plugin run on the host, set `hostExecution` for its id in the user
`config.json`:

```json
{"plugins":{"acme.notes":{"hostExecution":true}}}
```

Splice reads this key only from the user configuration. A plugin manifest or a
project `config.json` cannot set it. The opt-in applies to plugins installed in the
user plugin directory only. The approval prompt of a plugin tool tells
you when the tool runs on the host.

Hooks that you write in the user `hooks.json` run on the host in the
interactive agent loop. Trusted project hooks follow the same rule. In a
pipeline run, these hooks run inside the stage sandbox: the workspace is
writable, and the network is not available.

To let one hook run on the host in a pipeline run, set `runOnHost` on it in
the user `hooks.json` (`$XDG_CONFIG_HOME/splice/hooks.json`):

```json
{"hooks":[{"id":"notify","event":"afterTool","command":"notify-send","args":["done"],"runOnHost":true}]}
```

Splice ignores `runOnHost` in a project `hooks.json` and shows a warning. When
the sandbox blocks a hook, or a hook cannot run in the sandbox, the hook fails.
The message names the hook and the key that allows host execution.

The sandbox cannot read the Splice configuration directory. Thus a hook script
that you keep in that directory needs `runOnHost` in a pipeline run.

Each hook run writes `execution` (`sandboxed`, `host`, or `refused`) to the hook
audit log. Each plugin tool result has a `plugin.execution` value and the
sandbox fields of the run.

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

A cold or busy sidecar can be slow to answer. Splice waits 10 seconds for one
request and 10 seconds for a new sidecar to start. Splice tries the health
check again one time before it turns memory off. Use a Go duration to change
these limits:

| Variable | Default | Effect |
|---|---|---|
| `SPLICE_MEMD_TIMEOUT` | `10s` | Time limit for one sidecar request. |
| `SPLICE_MEMD_SPAWN_TIMEOUT` | `10s` | Time limit for a new sidecar to answer. |

When memory is not available, the warning names these limits.

## Provider stream timeouts

Splice stops a provider stream that sends nothing. Two limits apply:

| Variable | Default | Effect |
|---|---|---|
| `SPLICE_STREAM_IDLE_TIMEOUT` | `5m` | Time limit with no data at all. `0` or `off` removes this limit and the content limit. |
| `SPLICE_STREAM_CONTENT_STALL_TIMEOUT` | 1.2 times the idle limit (`6m`) | Time limit with keep-alive messages but no output. |

A model that reasons for a long time without output can reach the content
limit. Set a larger `SPLICE_STREAM_CONTENT_STALL_TIMEOUT` for that model.
Each value is a Go duration, such as `10m`, or a number of seconds.

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
