# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.2](https://github.com/Taf0711/splice/compare/v0.1.1...v0.1.2) (2026-07-20)


### Security

* **cli:** workspace trust gate. Project-scope executables (MCP stdio servers, hooks, plugins) loaded from `.splice/` are no longer spawned automatically when the workspace is untrusted. Trust is resolved from CLI flags (`--trust` / `--no-trust`), the `SPLICE_TRUST_WORKSPACE` env var, the persisted `~/.config/splice/trust.json` store (ancestor lookup, parent trust covers children), and the `defaultProjectTrust` setting (`ask` / `always` / `never`, default `ask`). Untrusted workspaces skip project resources and print a warning; this closes a remote-code-execution vector where cloning a malicious repository and running splice would execute configured commands. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **secrets:** credential environment variables are now scrubbed from child processes (bash, exec, hooks, MCP stdio, plugins, sandbox runner). Known credential names (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `AWS_SECRET_ACCESS_KEY`, etc.) and suffix patterns (`_API_KEY`, `_TOKEN`, `_SECRET`, `_PASSWORD`) are stripped before spawn, with `SPLICE_CHILD_ENV_ALLOWLIST` for explicit passthrough. Prevents prompt-injected `env` / `printenv` from exfiltrating provider keys. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **sandbox:** unparseable / obfuscated shell commands now force an explicit approval prompt instead of being auto-allowed under an active native sandbox. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **sandbox:** the safe-git command classifier now rejects `--git-dir`, `--work-tree`, and `-c` (global and inline) so an approved command prefix cannot be used to operate on an arbitrary repository outside the workspace. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **dtools:** the deterministic-tool path resolver now calls `filepath.EvalSymlinks` and rejects symlinks pointing outside the workspace. Git preserves symlinks on clone, so a repository could previously ship a symlink to a file outside the workspace and have the security scanners read it. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **sandbox:** the opt-in seccomp Unix-socket block now fails closed (exit 125) instead of running the command without the filter. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **mcp:** plaintext `http://` MCP server URLs now emit a warning at config load (loopback / localhost excepted). ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))


### Bug Fixes

* **update:** correct the npm package name from `@gitlawb/splice` to `@taf0711/splice`. The npm update path referenced a package name the maintainer does not own; if unregistered, npm self-update would break, and if registered by a third party it was a supply-chain takeover vector. ([#5](https://github.com/Taf0711/splice/pull/5))
* **cli:** the `mcp tools list` command now resolves workspace trust instead of unconditionally loading project MCP servers, closing the last gate gap. ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **tui:** setup pipeline stage picker shows discovered models, count, scroll indicator, and current mark ([a1676dd](https://github.com/Taf0711/splice/commit/a1676dd0dfde862fd0280bbbbb0ce2ea2b3d9b36))
* **tui:** setup pipeline picker shows selected model detail line ([b3b9872](https://github.com/Taf0711/splice/commit/b3b987297e24ed3d5e02cabb955377a3ac92dfad))
* **tui:** pipeline picker shows discovered models, not just the catalog ([15e9a9b](https://github.com/Taf0711/splice/commit/15e9a9be56ff1a53e59a2bbf820bfa43f0c1e86a))
* **tui:** pipeline picker detail line shows the model name ([96131d7](https://github.com/Taf0711/splice/commit/96131d74f2557ad9ada58bb3c274106eb1ae43c5))
* **tui:** Enter opens pipeline stage picker, Right advances to Safety ([fa8166b](https://github.com/Taf0711/splice/commit/fa8166be92752b734e0aeebdfc477835e5f38347))
* **ci:** npm trusted publishing needs Node 24 (npm CLI 11.5.1+) ([c5c6fd7](https://github.com/Taf0711/splice/commit/c5c6fd7dda13f4fd7a2b572da992f13da7951f8a))
* **tui:** setup wizard per-stage model picker uses search and filtered list ([#3](https://github.com/Taf0711/splice/pull/3)) ([da9f47a](https://github.com/Taf0711/splice/commit/da9f47a8310fe8185a6b118d30fe2ff528b221f3))

## [0.1.1](https://github.com/Taf0711/splice/compare/v0.1.1...v0.1.1) (2026-07-19)


### Bug Fixes

* **ci:** npm trusted publishing needs Node 24 (npm CLI 11.5.1+) ([c5c6fd7](https://github.com/Taf0711/splice/commit/c5c6fd7dda13f4fd7a2b572da992f13da7951f8a))
* **tui:** setup wizard per-stage model picker uses search and filtered list ([#3](https://github.com/Taf0711/splice/issues/3)) ([da9f47a](https://github.com/Taf0711/splice/commit/da9f47a8310fe8a6b118d30fe2ff528b221f3))

## 0.1.1 (2026-07-19)


### Features

* initial public release of Splice ([480083e](https://github.com/Taf0711/splice/commit/480083e74785fe9af85938a1a1f15960b51e7823))
