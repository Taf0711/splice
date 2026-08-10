# npm wrapper smoke checklist

Use this checklist when a change affects npm files, release archives, install
scripts, or the CLI entry point.

## Local checks

```bash
go test ./internal/npmwrapper ./internal/release
go run ./cmd/splice-release build
go run ./cmd/splice-release smoke
```

Run the wider gate when Go entry-point or archive behavior changes:

```bash
go test ./...
go vet ./...
go run ./cmd/splice-release build
go run ./cmd/splice-release smoke
```

## Package checks

Confirm these facts:

- The package name is `@taf0711/splice`.
- `bin.splice` points to `bin/splice.js`.
- The package has the required `postinstall` script.
- The Node.js requirement matches [Install Splice](INSTALL.md).
- The package includes only its declared wrapper and install files.
- The release workflow sets the package version from the approved release tag.

Keep the checked-in package version aligned with the current release. The
release workflow sets it from the approved tag again before `npm publish`.

## Download checks

Use dry-run mode to inspect each supported target without a download:

```bash
SPLICE_INSTALL_DRY_RUN=1 node scripts/postinstall.mjs
```

Test platform overrides where relevant:

```bash
SPLICE_INSTALL_DRY_RUN=1 SPLICE_INSTALL_PLATFORM=linux SPLICE_INSTALL_ARCH=x64 node scripts/postinstall.mjs
SPLICE_INSTALL_DRY_RUN=1 SPLICE_INSTALL_PLATFORM=darwin SPLICE_INSTALL_ARCH=arm64 node scripts/postinstall.mjs
SPLICE_INSTALL_DRY_RUN=1 SPLICE_INSTALL_PLATFORM=win32 SPLICE_INSTALL_ARCH=x64 node scripts/postinstall.mjs
```

Confirm that the installer:

- selects the archive for the package version;
- uses HTTPS unless a test explicitly permits another scheme;
- verifies the archive SHA-256 value;
- copies only known binary names;
- rejects an oversized download;
- handles `SPLICE_SKIP_DOWNLOAD=1`; and
- reports an unsupported target without a partial install.

Windows arm64 is a release target, but the npm installer skips it. Test the
PowerShell install path separately for that target.

## Published-package smoke test

Use a clean temporary prefix after npm publication:

```bash
PREFIX=$(mktemp -d)
npm install --prefix "$PREFIX" @taf0711/splice@X.Y.Z
"$PREFIX/node_modules/.bin/splice" --version
"$PREFIX/node_modules/.bin/splice" exec --help
```

Confirm that:

- the installed package version matches the release tag;
- `splice --version` reports the same version;
- the binary came from the matching release archive;
- npm shows the expected provenance; and
- the package contains no development credentials or local files.

## Release archive checks

Confirm all six archives and six checksum files before npm publication. Open one
archive for each operating system and inspect its file names.

The current release contract requires the main binary and `splice-memd`.
Separate sandbox helpers are optional archive entries.
