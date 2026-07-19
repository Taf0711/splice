# Installing Splice

Splice is distributed as:

- an npm package, `@taf0711/splice`
- release archives on GitHub Releases
- source builds with Go 1.25+

The npm package and install scripts download a platform-specific release archive.
They require a published GitHub Release for the requested version.

> **Status:** Release infrastructure is now set up. The first release
> (v0.1.0) will be cut by merging the Release Please PR on `main`. Until
> that first release is published, install Splice **from source** (see
> [From Source](#from-source)) for a working setup. After the first release,
> the npm, Bun, install-script, and release-archive flows below become
> active.

## npm

Install via `npm install -g @taf0711/splice` (downloads the matching GitHub Release binary) or download archives directly from [GitHub Releases](https://github.com/Taf0711/splice/releases).

```bash
npm install -g @taf0711/splice
splice
```

The package supports Linux, macOS, and Windows on x64 and arm64. It installs the
`splice` command and downloads the matching release binary during `postinstall`.

Requirements:

- Node.js 18+
- network access to npm and GitHub Releases

## Bun

> **Planned / work in progress** (depends on the npm package above).

Bun is "default-secure" and does not run lifecycle scripts of installed
dependencies (only the installing project's own scripts), so the `postinstall`
that fetches the Splice binary is silently skipped. The first run then fails with
`No native binary found next to the npm wrapper`.

The simplest fix is to trust the package after installing, which runs the
blocked postinstall. This works for project and global installs:

```bash
# project install
bun add @taf0711/splice
bun pm trust @taf0711/splice

# global install
bun add -g @taf0711/splice
bun pm -g trust @taf0711/splice
```

`bun pm untrusted` (or `bun pm -g untrusted`) lists the blocked postinstalls if
you want to inspect before trusting.

Alternatively, allow the postinstall to run at install time by adding the
package to your project's `trustedDependencies` before installing:

```json
{
  "trustedDependencies": ["@taf0711/splice"]
}
```

```bash
bun add @taf0711/splice
```

On Bun versions that do not have `bun pm trust`, run the installer manually
after installing:

```bash
node node_modules/@taf0711/splice/scripts/postinstall.mjs
```

Reference: <https://bun.sh/docs/pm/lifecycle>

## Linux And macOS Script

> **Planned / work in progress.** The install script depends on published
> GitHub Releases, which do not exist yet.

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.sh | bash
```

From a checkout:

```bash
scripts/install.sh
```

Install a specific version:

```bash
ZERO_VERSION=0.1.0 scripts/install.sh
scripts/install.sh --version 0.1.0
```

Install somewhere else:

```bash
SPLICE_INSTALL_DIR="$HOME/bin" scripts/install.sh
scripts/install.sh --install-dir "$HOME/bin"
```

> **Note:** `ZERO_VERSION` retains the upstream `ZERO_` prefix; a rename to
> `SPLICE_VERSION` is planned. The repo and install-dir variables are already
> `SPLICE_`-prefixed.

Defaults:

- Repository: `Taf0711/splice`
- Version: latest GitHub release
- Install path: `~/.local/bin/splice`

Custom GitHub Enterprise endpoints can be configured via environment variables:

- `ZERO_GITHUB_API` — base URL for the GitHub API (e.g. `https://github.example.com/api/v3`).
- `ZERO_GITHUB_BASE_URL` — base URL for the GitHub instance (e.g. `https://github.example.com`).

> **Note:** Both variables retain the upstream `ZERO_` prefix; a rename to
> `SPLICE_` is planned.

Requirements: Bash, `curl` or `wget`, `tar`, and `shasum` or `sha256sum`.

## Windows PowerShell Script

> **Planned / work in progress.** The install script depends on published
> GitHub Releases, which do not exist yet.

Install the latest release:

```powershell
irm https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.ps1 | iex
```

From a checkout:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1
```

Install a specific version:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1 -Version 0.1.0
```

Install somewhere else:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install.ps1 -InstallDir "$env:USERPROFILE\bin"
```

Defaults:

- Repository: `Taf0711/splice`
- Version: latest GitHub release
- Install path: `%LOCALAPPDATA%\splice\bin\splice.exe`

## From Source

This is the currently working install path.

```bash
git clone https://github.com/Taf0711/splice.git
cd splice
go run ./cmd/splice
```

Build a local binary:

```bash
go build -o splice ./cmd/splice
```

Source builds require Go 1.25+.

## Memory sidecar (optional)

Splice includes an optional memory sidecar (`splice-memd`) that persists
observations across sessions for context injection. It auto-spawns when
the TUI or CLI starts a pipeline run, if the binary is discoverable.

### Install the sidecar

```bash
make install-memd
```

This runs `go install` in the `memd/` module, placing `splice-memd` on
your PATH. The main `splice` binary will find and auto-spawn it on the
first pipeline run.

### Manual binary path

If the sidecar is not on PATH (e.g. a dev build), set the
`SPLICE_MEMD_BIN` environment variable to the full path of the binary:

```bash
export SPLICE_MEMD_BIN=/path/to/splice/memd/splice-memd
```

The binary is also discovered automatically when it sits next to the
`splice` executable (the install directory), so `go install ./cmd/splice`
followed by `make install-memd` places both binaries in the same
directory.

### Socket and data directory

The sidecar listens on a Unix socket and stores observations in a SQLite
database. The default locations are platform-specific:

- macOS: `~/Library/Application Support/splice/` (mem.sock, mem.db)
- Linux: `~/.local/share/splice/` (or `$XDG_DATA_HOME/splice/`)

Override the socket path with `SPLICE_MEMD_SOCKET` and the database path
with `SPLICE_MEMD_DB`.

### Sandbox Helpers For Source Builds

Release archives include the platform sandbox helpers. If you build directly
from source, build the helpers you need:

Linux:

```bash
go build -o splice ./cmd/splice
go build -o splice-linux-sandbox ./cmd/splice-linux-sandbox
go build -o splice-seccomp ./cmd/splice-seccomp
```

Put `splice` and `splice-linux-sandbox` in the same directory on `PATH`, for example
`~/.local/bin`. `splice-seccomp` is kept as a compatibility wrapper; the sandbox
helper applies the Unix-socket filter itself when that sandbox option is enabled.
Linux native sandboxing also requires Bubblewrap to be installed.

macOS uses the system sandbox and does not need an extra helper binary.

### Termux (Android)

> **Note:** Android is currently **unsupported**. Splice no longer publishes an
> Android target in the npm package's `os` list, and `scripts/postinstall.mjs`
> fails on Android because there is no prebuilt binary. The source-build steps
> below are informational only; the supported install paths are documented in
> [From Source](#from-source) on Linux, macOS, and Windows. On mobile, run Splice
> from a source build on a supported platform.

Splice can run natively on Android via [Termux](https://termux.dev/). Build with
`GOOS=android` to avoid the `faccessat2` syscall that is blocked by Samsung's
seccomp filter on Android:

```bash
# Install Go in Termux
pkg install golang

# Build Splice for Android
git clone https://github.com/Taf0711/splice.git
cd splice
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags="-s -w" -o splice ./cmd/splice

# Move into PATH
mv splice ~/.local/bin/
```

> **Why `GOOS=android`?** Go 1.26+ detects `runtime.GOOS == "android"` and skips
> the `faccessat2` syscall inside `os/exec.findExecutable`, falling back to
> permission-bit checks. Without this flag, Android's seccomp sends SIGSYS and
> kills the process whenever Splice looks up a binary on `PATH` (git, sh, etc.).

**DNS.** Android does not expose `/etc/resolv.conf`. Go's pure-Go DNS resolver
needs one. Use `proot` to bind-mount Termux's resolver config:

```bash
pkg install proot
proot -b "$PREFIX/etc/resolv.conf:/etc/resolv.conf" splice
```

Create a wrapper at `~/.local/bin/splice` to avoid typing proot every time:

```bash
#!/data/data/com.termux/files/usr/bin/bash
exec proot -b "$PREFIX/etc/resolv.conf:/etc/resolv.conf" ~/.local/bin/splice.bin "$@"
```

**Scroll.** On native Termux (not under PRoot), mouse scrolling works out of the
box. The TUI uses Bubble Tea's `AllMotion` mouse mode by default. If you run Splice
inside PRoot (e.g. through proot-distro), the scroll fix activates `CellMotion`
to avoid PRoot's ptrace interference with the 1003 escape sequence.

**Providers.** Splice works with any OpenAI-compatible provider on Termux. For
example, to use OpenCode Zen's free tier:

```bash
splice providers add opencode \
  --name opencode \
  --model deepseek-v4-flash-free \
  --base-url https://opencode.ai/zen/v1 \
  --set-active
```

Windows source builds can use the main `splice.exe` as the command runner and setup
helper through Splice's built-in self-dispatch path. If you want a release-style
layout anyway, build the standalone helper executables next to `splice.exe`:

```powershell
go build -o splice.exe ./cmd/splice
go build -o splice-windows-command-runner.exe ./cmd/splice-windows-command-runner
go build -o splice-windows-sandbox-setup.exe ./cmd/splice-windows-sandbox-setup
```

## Release Archive Format

> **Planned / work in progress.** Release archives do not exist yet; this
> describes the intended format once Splice releases are published.

Release archives are named:

- `splice-v<version>-linux-<arch>.tar.gz`
- `splice-v<version>-macos-<arch>.tar.gz`
- `splice-v<version>-windows-<arch>.zip`

Supported targets:

- `linux-x64`
- `linux-arm64`
- `macos-x64`
- `macos-arm64`
- `windows-x64`
- `windows-arm64`

Each archive must have a matching `.sha256` file. The install scripts download
both files, verify the checksum, and then copy the binary into the install
directory.

## Updating

Check for a newer release:

```bash
splice update --check
```

Then reinstall with npm or rerun the install script for the version you want.
