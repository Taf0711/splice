# Update Splice

Check the latest release without a file change:

```bash
splice update --check
```

Install the latest release:

```bash
splice update --apply
```

`splice upgrade` is the short form for the install operation:

```bash
splice upgrade
```

The install path downloads the matching archive, verifies its SHA-256 digest,
and replaces the installed release files.

## Check options

```bash
splice update --check --json
splice update --check --repo Taf0711/splice
splice update --check --target windows-x64
```

| Flag | Purpose |
|---|---|
| `--json` | Print a machine-readable result. |
| `--repo <owner/repo>` | Select another release repository. |
| `--endpoint <url|owner/repo>` | Select a release API URL or repository. |
| `--timeout <duration>` | Set the release request timeout. |
| `--target <platform-arch>` | Check assets for another release target. |

`--target` works only with `--check`. It does not install a foreign-platform
binary.

Supported target names are:

```text
linux-x64
linux-arm64
macos-x64
macos-arm64
windows-x64
windows-arm64
```

A successful check returns exit code `0`, even when a newer release exists. A
check error returns a nonzero code.

## Release endpoint order

Splice selects release metadata in this order:

1. `--endpoint`
2. `SPLICE_UPDATE_RELEASE_URL`
3. `--repo`
4. The official Splice GitHub release endpoint

Use a custom endpoint only when you trust its archive and checksum source.

## Disable update checks

Set `SPLICE_DISABLE_UPDATE_NOTICE=1` to hide the automatic notice. Manual update
commands remain available.

Set `SPLICE_DISABLE_UPDATES=1` to disable update checks and install commands for
the process.

## Package manager installs

Splice detects an npm-managed installation. In that case, `splice upgrade` runs
the global npm update for `@taf0711/splice@latest`.

For an install-script or archive layout, Splice downloads, verifies, and replaces
the release files directly.

You can also update an npm package yourself:

```bash
npm install -g @taf0711/splice@latest
```

For a source build, update the checkout and rebuild:

```bash
git pull
go build -o splice ./cmd/splice
```
