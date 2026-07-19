# Update Flow

`splice update --check` checks the latest GitHub release and compares it with the
local CLI version.

```bash
splice update --check
splice update --check --json
splice update --check --repo Taf0711/splice
splice update --check --target windows-x64
```

The command is intentionally check-only:

- It does not replace the running binary.
- It exits with code `0` when the check succeeds, even when an update is
  available.
- It exits with code `1` when the release check cannot be completed.
- `--json` prints the same result in a machine-readable format for scripts and
  CI.

Useful flags:

| Flag | Purpose |
|---|---|
| `--repo <owner/repo>` | Check another GitHub repository. |
| `--endpoint <url|owner/repo>` | Check a specific release API URL or repository slug. |
| `--timeout <duration>` | Override the default release check timeout. |
| `--target <platform-arch>` | Validate release metadata for another supported target. |

Supported targets are `linux-x64`, `linux-arm64`, `macos-x64`, `macos-arm64`,
`windows-x64`, and `windows-arm64`. Without `--target`, Splice checks the current
platform.

Endpoint resolution order:

1. `--endpoint`
2. `ZERO_UPDATE_RELEASE_URL`
3. `--repo`
4. `https://api.github.com/repos/Taf0711/splice/releases/latest`

These environment variables retain the upstream ZERO_ prefix; a rename to SPLICE_ is planned.

Installer scripts download the matching release asset for the local platform and
verify its `.sha256` file. If Splice is already installed, run `splice update --check`
before reinstalling.
