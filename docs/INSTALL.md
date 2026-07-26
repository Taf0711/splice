# Install Splice

Splice runs locally and is available in three forms. Pick the path that matches
how you want to use it:

| Path | Best for | What you get |
|---|---|---|
| npm | Most users | A platform binary and the `splice` command |
| GitHub Releases | Managed or offline installs | A versioned archive |
| Source | Contributors and local development | A build from this checkout |

Splice is built on [Zero's Engine](https://github.com/gitlawb/zero), but the
commands and binaries below are Splice's (`splice`, not `zero`).

## Fast path: npm

Requirements:

- Node.js 18 or newer
- network access to npm and GitHub Releases
- Linux, macOS, or Windows on x64 or arm64

```bash
npm install -g @taf0711/splice
splice
```

The npm package installs a small wrapper and downloads the matching Splice
release binary during `postinstall`. The first launch opens the provider setup
wizard. To check the installation without starting a session:

```bash
splice --version
splice doctor
```

### Bun

Bun does not run dependency lifecycle scripts unless a package is trusted. If
the wrapper is installed but no native binary is found, trust the package and
rerun the installer:

```bash
bun add -g @taf0711/splice
bun pm -g trust @taf0711/splice
```

For a project install:

```bash
bun add @taf0711/splice
bun pm trust @taf0711/splice
```

You can inspect blocked scripts with `bun pm untrusted`. On Bun versions without
`bun pm trust`, run the wrapper installer directly after installing:

```bash
node node_modules/@taf0711/splice/scripts/postinstall.mjs
```

## Versioned install: GitHub Releases

Release archives and checksums are published on
[GitHub Releases](https://github.com/Taf0711/splice/releases). Download the
archive for your operating system and architecture, unpack it, and put the
binaries on `PATH`.

Supported release targets:

- Linux: x64, arm64
- macOS: x64, arm64
- Windows: x64, arm64

Verify the matching `.sha256` file before installing a binary. Archive names
follow this pattern:

```text
splice-v<version>-linux-<arch>.tar.gz
splice-v<version>-macos-<arch>.tar.gz
splice-v<version>-windows-<arch>.zip
```

The release archive includes the platform helpers needed by the sandbox. Keep
`splice` and its helper binaries together when copying them to a directory on
`PATH`.

## Build from source

Source builds require Go 1.25 or newer.

```bash
git clone https://github.com/Taf0711/splice.git
cd splice
go build -o splice ./cmd/splice
./splice
```

To run without creating a binary:

```bash
go run ./cmd/splice
```

### Linux helpers

Build the native sandbox helper beside the main binary:

```bash
go build -o splice ./cmd/splice
go build -o splice-linux-sandbox ./cmd/splice-linux-sandbox
go build -o splice-seccomp ./cmd/splice-seccomp
```

`splice-seccomp` is an optional compatibility wrapper. Native Linux sandboxing
also requires [Bubblewrap](https://github.com/containers/bubblewrap) to be
installed. macOS uses the system sandbox and does not need an extra helper.

### Windows helpers

The main `splice.exe` can run source builds through its built-in dispatch path.
For a release-style layout, build the standalone helpers beside it:

```powershell
go build -o splice.exe ./cmd/splice
go build -o splice-windows-command-runner.exe ./cmd/splice-windows-command-runner
go build -o splice-windows-sandbox-setup.exe ./cmd/splice-windows-sandbox-setup
```

### Optional memory sidecar

The pipeline can use `splice-memd` to persist useful observations between
sessions. It is optional. Without it, Splice still runs normally.

```bash
make install-memd
```

That command installs the sidecar from the separate `memd/` Go module. Splice
finds it on `PATH` or beside the main binary. To select an explicit binary:

```bash
export SPLICE_MEMD_BIN=/path/to/splice-memd
```

The sidecar stores a SQLite database and listens on a Unix socket. Defaults are:

- macOS: `~/Library/Application Support/splice/`
- Linux: `~/.local/share/splice/` or `$XDG_DATA_HOME/splice/`

Override either location when needed:

```bash
export SPLICE_MEMD_SOCKET=/path/to/mem.sock
export SPLICE_MEMD_DB=/path/to/mem.db
```

## After installation

Configure a provider:

```bash
splice setup
splice providers list
splice models list
splice doctor
```

Or use a local model through Ollama or LM Studio. Model-backed pipeline stages
need tool-calling support. Splice reports invalid typed responses instead of
silently changing providers.

To inspect permissions before allowing a run:

```bash
splice sandbox policy
splice sandbox grants list
```

For a first headless run:

```bash
splice exec "summarize this repository"
```

## Platform notes

### Termux on Android

Android is not a published binary target. A source build can work in Termux,
but it is an unsupported platform and may require `proot` for DNS resolution.
For supported installations, use Linux, macOS, or Windows as listed above.

### Custom install directories

For a source build, copy the binary and its helpers to any directory on `PATH`.
`~/.local/bin` is a common choice on Linux and macOS. On Windows, use a
PowerShell profile or a system PATH entry for the directory containing
`splice.exe`.

## Updating

Check for an update from the installed binary:

```bash
splice update --check
```

Then install the newer npm package or download the matching release archive.
Source builds update by pulling the repository and rebuilding:

```bash
git pull
go build -o splice ./cmd/splice
```

## Troubleshooting

### `No native binary found next to the npm wrapper`

The package lifecycle script did not run. With Bun, trust the package as shown
above. With npm, reinstall the package and check that the install process can
reach GitHub Releases.

### A local model returns invalid stage output

The selected model must support tool calling. Run `splice doctor`, choose a model
with tool-call support, and retry. Splice allows corrective retries, then stops
with the invalid field in the error.

### Linux sandbox is unavailable

Build `splice-linux-sandbox`, install Bubblewrap, and keep the helper beside
`splice` on `PATH`. Inspect the active policy with `splice sandbox policy`.
