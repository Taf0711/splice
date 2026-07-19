# Splice GitHub Action

Run Splice headlessly inside a GitHub workflow. The action is a thin wrapper around
`splice exec`: it installs a pinned Splice release, runs your prompt in the checked-out
repository, surfaces Splice's exit code as the step status, captures the output as a
file, and can optionally post a summary to the triggering pull request or to Slack.

Splice is model- and provider-agnostic, and so is this action: **you** choose the
provider and supply the API key. Nothing is hardcoded to any single provider.

> **Note:** Releases are published from v0.1.0. Pin to a tag or commit SHA
> for reproducible workflows.

## Quick start

```yaml
# .github/workflows/splice.yml
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
      - uses: Taf0711/splice@v0.1.1
        with:
          prompt: ${{ inputs.task }}
          provider: openai
          api-key-env: OPENAI_API_KEY
          api-key: ${{ secrets.OPENAI_API_KEY }}
          model: gpt-4.1
```

> The `provider`, `api-key-env`, and `api-key` trio is how you stay
> provider-neutral: `api-key-env` is the environment variable name the chosen
> provider reads its key from (for example `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`,
> `OPENROUTER_API_KEY`), and `api-key` is the secret value. The action exports
> that variable only for the Splice step and never prints it. If your repository
> already commits a `.splice/config.json` with an active provider, you can omit
> `provider`.

## Inputs

| Input | Required | Default | Description |
| --- | --- | --- | --- |
| `prompt` | one of `prompt`/`prompt-file` | `""` | The instruction for Splice to execute. |
| `prompt-file` | one of `prompt`/`prompt-file` | `""` | Path (relative to `working-directory`) to a file whose contents are the prompt. |
| `provider` | no | `""` | Provider id to activate (e.g. `openai`, `anthropic`, `gemini`, `ollama`, or any compatible endpoint). |
| `api-key` | no | `""` | The provider API key. Pass from a secret; exported only for the Splice step and never logged. |
| `api-key-env` | no | `""` | Env var name the provider reads its key from (e.g. `OPENAI_API_KEY`). Exported only for the Splice step when used with `api-key`. |
| `model` | no | `""` | Model id. Defaults to the resolved provider's default. |
| `mode` | no | `""` | Run mode (`splice exec --mode`), e.g. `smart`, `deep`, `fast`. |
| `auto` | no | `low` | Autonomy ceiling (`splice exec --auto`): `low`, `medium`, or `high`. Conservative by default. |
| `self-correct` | no | `false` | Allow mid-run model escalation (`splice exec --allow-escalation`). |
| `add-dir` | no | `""` | Newline-/comma-separated extra write roots (`splice exec --add-dir`). |
| `worktree` | no | `false` | Run in an isolated git worktree (`splice exec --worktree`). |
| `output-format` | no | `stream-json` | `text`, `json`, or `stream-json`. Captured to a file. |
| `post-to` | no | `none` | `pr-comment`, `slack`, or `none`. Where to post a summary after the run. |
| `slack-webhook-url` | no | `""` | Slack incoming-webhook (or generic webhook) URL for `post-to: slack`. Pass from a secret. |
| `github-token` | no | `${{ github.token }}` | Token used to post a PR comment. Requires `pull-requests: write`. |
| `working-directory` | no | `${{ github.workspace }}` | Directory to run Splice in. |
| `splice-version` | no | (action ref → `latest`) | Splice release version/tag to install, e.g. `v1.2.3` or `latest`. |
| `splice-repo` | no | `Taf0711/splice` | Repository to install the Splice release from. |

## Outputs

| Output | Description |
| --- | --- |
| `exit-code` | Splice's exit code (`0` success, `2` usage, `3` provider, non-zero otherwise). |
| `output-file` | Path to the captured Splice stdout (the raw `output-format` stream). |
| `summary` | A short, single-line summary parsed from the run, when available. |

The step **fails when Splice returns a non-zero exit code**, so a failed run fails
the job by default. Use `continue-on-error: true` on the step (and read
`exit-code`) if you want to handle failures yourself.

## Examples

### Run Splice on every issue labeled `splice`

```yaml
name: Splice issue triage
on:
  issues:
    types: [labeled]

permissions:
  contents: write
  issues: write
  pull-requests: write

jobs:
  triage:
    if: github.event.label.name == 'splice'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: Taf0711/splice@v0.1.1
        with:
          prompt: |
            Investigate this issue and propose a fix.

            Title: ${{ github.event.issue.title }}

            ${{ github.event.issue.body }}
          provider: anthropic
          api-key-env: ANTHROPIC_API_KEY
          api-key: ${{ secrets.ANTHROPIC_API_KEY }}
          auto: low
          post-to: slack
          slack-webhook-url: ${{ secrets.ZERO_SLACK_WEBHOOK_URL }}
```

### Nightly dependency-upgrade PR

```yaml
name: Splice nightly deps
on:
  schedule:
    - cron: "0 6 * * 1" # Mondays 06:00 UTC

permissions:
  contents: write
  pull-requests: write

jobs:
  deps:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: Taf0711/splice@v0.1.1
        id: splice
        with:
          prompt-file: .github/splice/upgrade-deps.md
          provider: openai
          api-key-env: OPENAI_API_KEY
          api-key: ${{ secrets.OPENAI_API_KEY }}
          worktree: true
          auto: medium
      - name: Open pull request
        run: |
          git checkout -b splice/deps-$(date +%Y%m%d)
          git commit -am "chore(deps): nightly upgrade via Splice" || exit 0
          git push -u origin HEAD
          gh pr create --fill --label dependencies
        env:
          GH_TOKEN: ${{ github.token }}
      - name: Upload Splice output
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: splice-output
          path: ${{ steps.splice.outputs.output-file }}
          if-no-files-found: warn
```

## Security notes

- **Secrets are passed as Action secrets and never logged.** The action exports
  `api-key` (under `api-key-env`) only for the Splice step. `slack-webhook-url`
  is passed only to the Slack post step. They are never echoed or persisted to
  later workflow steps, and Splice redacts secret-shaped strings from its own
  output.
- **The sandbox is always active.** This action never passes
  `--skip-permissions-unsafe`, so writes stay inside the checked-out repository
  (plus any roots you grant with `add-dir`). Unsafe mode is never enabled
  implicitly.
- **Autonomy defaults to `low`.** Raise it deliberately (`auto: medium`/`high`)
  only for tasks you trust to run unattended.
- **Least-privilege tokens.** Grant only the permissions the workflow needs
  (`contents: write` to edit files, `pull-requests: write` to comment). The
  default `GITHUB_TOKEN` is scoped to the repository.
- **Pin the action.** Reference a tag (`Taf0711/splice@v0.1.1`) or a commit SHA so a
  workflow run uses a known Splice version. `splice-version` lets you pin the
  installed binary independently of the action ref.
- **Linux and macOS runners** are supported; Windows runners are rejected with a
  clear error.

## Slack / webhook notifier

The action's `post-to: slack` step sends a one-line summary to a Slack incoming
webhook after the run. Splice also has a built-in webhook notifier sink
(`internal/notify`) that an unattended run can use to report
"finished / needs input / verify failed after N retries" to Slack or any generic
webhook:

- Configure the destination with the `ZERO_SLACK_WEBHOOK_URL` environment variable
  (or settings). A blank URL disables the sink. These environment variables
  retain the upstream `ZERO_` prefix; a rename to `SPLICE_` is planned.
- The sink POSTs a JSON body `{ "text", "type", "message", "summary?", "links?" }`.
  The `text` field is what Slack renders; the structured fields carry the
  machine-readable detail.
- **Fail-soft:** a non-2xx response or a transport error is logged (redacted) and
  swallowed — a webhook problem never crashes the run.
- **Redaction:** the message, summary, links, and the webhook URL itself are run
  through Splice's redaction before being sent or logged, so tokens never leak.
- **Egress/proxy:** the notifier uses the default HTTP transport, which honors
  `HTTP_PROXY`/`HTTPS_PROXY` when a proxy is configured.
