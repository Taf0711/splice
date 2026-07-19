# npm Wrapper Smoke Checklist

Run this checklist when a PR changes npm distribution files such as
`package.json`, `bin/splice.js`, Go build or release tooling, or the npm `bin`
wrapper.

## Required Checks

```bash
go test ./internal/npmwrapper ./internal/release
go run ./cmd/splice-release build
go run ./cmd/splice-release smoke
```

Also run the Go checks when the PR changes Go entrypoint, CLI, or release
artifact behavior:

```bash
go test ./...
go run ./cmd/splice-release build
go run ./cmd/splice-release smoke
```

## Checklist

- `package.json` has the expected package name (`@taf0711/splice`), version,
  `bin.splice` entry, and exactly one `scripts` entry — the `postinstall` hook.
- `scripts/postinstall.mjs` resolves the correct release asset name/URL per
  platform (`SPLICE_INSTALL_DRY_RUN=1` prints the plan), verifies the downloaded
  archive's SHA-256, and extracts only the known binary basenames into place.
  `SPLICE_SKIP_DOWNLOAD=1` opts out cleanly (exit 0) and an unsupported
  platform/arch is a non-fatal skip. These environment variables retain the
  upstream ZERO_ prefix; a rename to SPLICE_ is planned.
- The wrapper binary resolves through the package `bin` entry and
  `node_modules/.bin` in a package-install smoke test.
- The built binary exits 0 for `splice --version` or `splice --help`.
- `splice --version` reports `splice <package.json version>`.
- Release packaging still emits the expected archive and checksum names when
  package release files change.
