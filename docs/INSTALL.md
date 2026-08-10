# Install Splice

Choose npm for the shortest setup. Use a release archive for a managed install.
Build from source when you work on Splice itself.

| Method | Requirement | Result |
|---|---|---|
| npm | Node.js 24 or newer | The `splice` command and a platform binary |
| Install script | `curl` or PowerShell | The latest verified release in a selected directory |
| Release archive | Archive and checksum access | A fixed release without npm |
| Source | Go 1.25 or newer | A build from the current checkout |

## Install with npm

```bash
npm install -g @taf0711/splice
splice --version
splice
```

The package downloads the release archive that matches its package version. It
checks the SHA-256 digest before it installs the binary.

The npm package supports these native targets:

- Linux x64 and arm64
- macOS x64 and arm64
- Windows x64

The npm installer skips Windows arm64. Use the Windows install script for the
native arm64 archive, or use the x64 package through system emulation.

### Use Bun

Bun can block package lifecycle scripts until you trust the package.

```bash
bun add -g @taf0711/splice
bun pm -g trust @taf0711/splice
```

For a project install:

```bash
bun add @taf0711/splice
bun pm trust @taf0711/splice
```

Use `bun pm untrusted` to inspect blocked scripts. If your Bun version has no
trust command, run the package installer after the package install:

```bash
node node_modules/@taf0711/splice/scripts/postinstall.mjs
```

## Use the install scripts

On Linux or macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.sh | bash
```

You can use `wget` instead:

```bash
wget -qO- https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.sh | bash
```

On Windows:

```powershell
irm https://raw.githubusercontent.com/Taf0711/splice/main/scripts/install.ps1 | iex
```

The scripts install the latest release by default. Download the script first
when you need flags or a fixed version.

```bash
./scripts/install.sh --version vX.Y.Z --install-dir "$HOME/bin"
```

```powershell
./scripts/install.ps1 -Version vX.Y.Z -InstallDir "$HOME\bin"
```

The Bash script supports these variables:

| Variable | Purpose |
|---|---|
| `SPLICE_VERSION` | Select a release tag or version. |
| `SPLICE_INSTALL_DIR` | Select the destination directory. |
| `SPLICE_REPO` | Select another release repository. |
| `SPLICE_GITHUB_API` | Select another GitHub API base URL. |
| `SPLICE_GITHUB_BASE_URL` | Select another GitHub download base URL. |

The default destination is `~/.local/bin` on Linux and macOS. Add the selected
directory to `PATH` when the installer asks you to do so.

## Use a release archive

Download a release and its matching `.sha256` file from
[GitHub Releases](https://github.com/Taf0711/splice/releases).

Published targets:

- Linux x64 and arm64
- macOS x64 and arm64
- Windows x64 and arm64

Archive names use this format:

```text
splice-v<version>-linux-<arch>.tar.gz
splice-v<version>-macos-<arch>.tar.gz
splice-v<version>-windows-<arch>.zip
```

Each published archive contains the main binary and the `splice-memd` sidecar.
The current release archives do not include separate sandbox helper binaries.

Verify the checksum before you copy either binary to `PATH`.

## Build from source

```bash
git clone https://github.com/Taf0711/splice.git
cd splice
go build -o splice ./cmd/splice
./splice --version
./splice
```

You can also run the source tree without a persistent binary:

```bash
go run ./cmd/splice
```

### Build Linux helpers

A source build can place the native Linux helpers beside `splice`:

```bash
go build -o splice ./cmd/splice
go build -o splice-linux-sandbox ./cmd/splice-linux-sandbox
go build -o splice-seccomp ./cmd/splice-seccomp
```

Install [Bubblewrap](https://github.com/containers/bubblewrap) for the Linux
sandbox backend. `splice-seccomp` is an optional compatibility helper.

macOS uses the system sandbox and needs no separate helper.

### Build Windows helpers

```powershell
go build -o splice.exe ./cmd/splice
go build -o splice-windows-command-runner.exe ./cmd/splice-windows-command-runner
go build -o splice-windows-sandbox-setup.exe ./cmd/splice-windows-sandbox-setup
```

Keep the helper binaries beside `splice.exe` for a release-style source layout.

## Install the memory sidecar

Release archives include `splice-memd`. A source checkout can install it from
its separate Go module:

```bash
make install-memd
```

Splice finds the sidecar beside the main binary or on `PATH`. Select another
binary only when necessary:

```bash
export SPLICE_MEMD_BIN=/path/to/splice-memd
```

The sidecar stores a local SQLite database and uses a Unix socket. The default
data location follows the local Splice data directory.

```bash
export SPLICE_MEMD_SOCKET=/path/to/mem.sock
export SPLICE_MEMD_DB=/path/to/mem.db
```

The sidecar is optional. A run can continue when memory is unavailable.

## Complete setup

```bash
splice setup
splice providers list
splice models list
splice doctor
```

Use Ollama or LM Studio for a local model. Select a model with tool-call support
for typed pipeline stages.

Inspect safety policy before the first automated run:

```bash
splice sandbox policy
splice sandbox grants list
```

Then run:

```bash
splice exec "summarize this repository"
```

## Platform notes

Android is not a release target. A Termux source build can work, but the project
does not support it as a release platform.

The GitHub Action supports Linux and macOS runners. It rejects Windows runners.
The standalone CLI supports the Windows release targets above.

## Fix common install problems

### The npm wrapper cannot find a native binary

The package lifecycle script did not complete. Reinstall the package and confirm
that the process can reach GitHub Releases.

For Bun, trust the package as shown above. For Windows arm64, use the PowerShell
installer or the x64 package.

### A local model returns an invalid stage result

Select a model with tool-call support. Run `splice doctor`, update the model
profile, and retry.

### The Linux sandbox is unavailable

Install Bubblewrap. For a source layout, build the Linux helper and keep it
beside `splice`.

Inspect the selected backend with:

```bash
splice sandbox policy
```

## Update an installation

```bash
splice update --check
splice upgrade
```

Read [Update Splice](UPDATE.md) before you use a custom release endpoint.
