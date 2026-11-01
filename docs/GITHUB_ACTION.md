# GitHub Action

The Splice action installs a release and runs `splice exec` in the checked-out
repository. It records standard output, returns the Splice exit code, and can
post a short result.

The action supports Linux and macOS runners. It rejects Windows runners.

## Quick start

Pin the action to a release tag or commit SHA.

```yaml
name: Splice
on:
  workflow_dispatch:
    inputs:
      task:
        description: What should Splice do?
        required: true

permissions:
  contents: write

jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: Taf0711/splice@v0.2.0
        with:
          prompt: ${{ inputs.task }}
          provider: openai
          api-key-env: OPENAI_API_KEY
          api-key: ${{ secrets.OPENAI_API_KEY }}
          model: MODEL_ID
```

Set `api-key-env` to the environment variable that your provider reads. Pass the
value through a GitHub secret.

You can omit `provider` when the repository contains an approved Splice provider
profile.

## Inputs

| Input | Required | Default | Purpose |
|---|---|---|---|
| `prompt` | One of the prompt inputs | Empty | Instruction for Splice |
| `prompt-file` | One of the prompt inputs | Empty | Prompt file relative to the work directory |
| `provider` | No | Empty | Provider profile or catalog ID |
| `api-key` | No | Empty | Provider key from a secret |
| `api-key-env` | No | Empty | Environment variable name for the key |
| `model` | No | Provider default | Model ID |
| `mode` | No | Empty | `smart`, `deep`, `fast`, `large`, or `precise` |
| `auto` | No | `low` | Autonomy limit for `splice exec` |
| `self-correct` | No | `false` | Compatibility input that forwards `--allow-escalation` |
| `add-dir` | No | Empty | Extra write roots, split on commas or new lines |
| `worktree` | No | `false` | Start the run in an isolated worktree |
| `output-format` | No | `stream-json` | `text`, `json`, or `stream-json` |
| `post-to` | No | `none` | `none`, `pr-comment`, or `slack` |
| `slack-webhook-url` | No | Empty | Webhook secret for a Slack result |
| `github-token` | No | Workflow token | Token for a pull request comment |
| `working-directory` | No | Workspace root | Directory for the Splice process |
| `splice-version` | No | Action ref or latest | Release tag to install |
| `splice-repo` | No | `Taf0711/splice` | Release repository |

`prompt` and `prompt-file` are mutually exclusive.

The current deterministic pipeline does not use `--allow-escalation` directly.
It uses stage-model escalation configuration instead. Therefore, the
`self-correct` action input does not change a normal pipeline run.

The `worktree` input isolates changes from the checked-out worktree. A later
workflow step in the original checkout will not see those files automatically.
The action does not expose the `--merge-back` flag.

## Outputs

| Output | Purpose |
|---|---|
| `exit-code` | Exit code from Splice |
| `output-file` | File that contains the selected output format |
| `summary` | Best-effort one-line result |

The action step fails when Splice returns a nonzero code. Use
`continue-on-error: true` only when a later step handles the failure.

## Pull request comment

Grant pull request write access and select `pr-comment`.

```yaml
permissions:
  contents: read
  pull-requests: write

steps:
  - uses: actions/checkout@v4
  - uses: Taf0711/splice@v0.2.0
    with:
      prompt: Review this pull request. Report only verified blockers.
      provider: anthropic
      api-key-env: ANTHROPIC_API_KEY
      api-key: ${{ secrets.ANTHROPIC_API_KEY }}
      model: MODEL_ID
      auto: low
      post-to: pr-comment
```

The comment uses `github-token`. The workflow must run in a pull request event
that provides a pull request number.

## Slack result

```yaml
steps:
  - uses: actions/checkout@v4
  - uses: Taf0711/splice@v0.2.0
    with:
      prompt-file: .github/splice/check.md
      provider: openrouter
      api-key-env: OPENROUTER_API_KEY
      api-key: ${{ secrets.OPENROUTER_API_KEY }}
      post-to: slack
      slack-webhook-url: ${{ secrets.SPLICE_SLACK_WEBHOOK_URL }}
```

The Slack step sends a short result after the run. A webhook failure produces a
workflow warning and does not replace the Splice exit code.

## Save the full output

```yaml
steps:
  - uses: actions/checkout@v4
  - uses: Taf0711/splice@v0.2.0
    id: splice
    continue-on-error: true
    with:
      prompt: Run the repository checks and report failures.
      provider: openai
      api-key-env: OPENAI_API_KEY
      api-key: ${{ secrets.OPENAI_API_KEY }}
      output-format: stream-json

  - uses: actions/upload-artifact@v4
    if: always()
    with:
      name: splice-output
      path: ${{ steps.splice.outputs.output-file }}
      if-no-files-found: error
```

Read `exit-code` before you treat the artifact as a successful result.

## Security guidance

- Pin the action to a tag or commit SHA.
- Pin `splice-version` when the action and binary versions must differ.
- Grant only the repository permissions that the task needs.
- Keep `auto: low` unless the task requires more autonomy.
- Pass provider keys and webhooks through GitHub secrets.
- Do not put secrets in the prompt or checked-in configuration.
- Review `add-dir` because it expands the write boundary.
- Treat project plugins, hooks, MCP configuration, and instructions as untrusted
  repository input.

The action does not pass `--skip-permissions-unsafe`. It also disables the local
notification sink because CI has no interactive terminal.

Splice redacts known secret values from its own output. Redaction cannot protect
a secret that an external tool sends to another service.

## Version selection

The action selects the installed binary in this order:

1. `splice-version`
2. The action tag, when the action ref is a release tag
3. The latest release

A commit-SHA action pin does not identify a release asset. Set `splice-version`
when you pin the action by commit and also need a fixed binary version.
