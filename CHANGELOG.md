# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.4](https://github.com/Taf0711/splice/compare/v0.1.3...v0.1.4) (2026-08-06)


### Miscellaneous Chores

* **release:** cut 0.1.4 ([d3b5e44](https://github.com/Taf0711/splice/commit/d3b5e44d4687b7dbfa9908ad5c21db8b8a1bc484))

## [0.1.3](https://github.com/Taf0711/splice/compare/v0.1.2...v0.1.3) (2026-07-30)


### Breaking Changes

* All environment variables now use the `SPLICE_` prefix. Splice does not read the old `ZERO_` names, and it gives no warning when it finds them. Rename these variables in your shell, your CI configuration, and your scripts. For example, change `ZERO_API_KEY` to `SPLICE_API_KEY`. ([3b3b01c](https://github.com/Taf0711/splice/commit/3b3b01c0e65d035605cc036c41864197458e5287))


### Documentation

* The README, the security policy, and the install guide are rewritten. ([b4e3388](https://github.com/Taf0711/splice/commit/b4e3388))


### Features

* **agenteval:** derive eval cost from the request samples ([9e137e0](https://github.com/Taf0711/splice/commit/9e137e06e41f84c81b098330a90a3509e94be8da))
* **agenteval:** parse usage events while the agent runs ([15dc68e](https://github.com/Taf0711/splice/commit/15dc68e46697619e790b8e4c272c67379f29f9b4))
* **agenteval:** publish the benchmark v2 report contract ([eed860d](https://github.com/Taf0711/splice/commit/eed860d790cf85895de8ff4653c7cc2bc6ed8396))
* **cli:** report eval runner, routed models, and cost coverage ([a740bb7](https://github.com/Taf0711/splice/commit/a740bb7869527c40a0d982faec5a7bd84bab1d1c))
* **cli:** run eval benchmarks through the production pipeline by default ([4ff0a2d](https://github.com/Taf0711/splice/commit/4ff0a2d47ce40d524794aab0a3901bf5325fdbc0))
* **compaction:** keep the agent inside the cheapest pricing tier ([eee9f67](https://github.com/Taf0711/splice/commit/eee9f67cc6691049576565ac8347d7edd5c7135d))
* complete attributed pipeline cost accounting ([7440a8e](https://github.com/Taf0711/splice/commit/7440a8ed8bc5b79db5931e2fb386c6a4c68dc27b))
* **design:** ask the provider to search the web ([9c5651d](https://github.com/Taf0711/splice/commit/9c5651d81b7822a0a8ab202f91c2493f8c1ce230))
* **design:** emit task_started before each plan task dispatches ([09b3826](https://github.com/Taf0711/splice/commit/09b3826c99d5103147a26977999f3d61f065ff13))
* **design:** give the design agent research tools and read-only roots ([4e44b86](https://github.com/Taf0711/splice/commit/4e44b86950f00eb6f85715284bd1158f2712850a))
* **modelregistry:** price long-context tiers from models.dev ([5d6e762](https://github.com/Taf0711/splice/commit/5d6e7625c99e586b4fc98f7e81dbd18603b0b285))
* **modelregistry:** price models the curated catalog does not carry ([00d93f2](https://github.com/Taf0711/splice/commit/00d93f2e2256f55b868550524a24d733ff26ae13))
* **modelregistry:** ship a models.dev snapshot and stop curating prices ([8b3ad05](https://github.com/Taf0711/splice/commit/8b3ad055de41ee858d6c096afb3dc06c3199bba4))
* **openrouter:** run web search on the provider ([fe19ea3](https://github.com/Taf0711/splice/commit/fe19ea395cec2ee62a310ff5aeac68d987284d9e))
* **runtime:** add plumbing for provider-executed web search ([3976759](https://github.com/Taf0711/splice/commit/39767591db35d960ed07b4d1a5885c6c83349773))
* **tui:** show honest session cost with explicit coverage ([0de1c81](https://github.com/Taf0711/splice/commit/0de1c81f84632f2bcd254111a9b2251b6ceb03ed))
* **usage:** persist cost estimates with provenance and coverage ([0215c28](https://github.com/Taf0711/splice/commit/0215c28994ae1fbeae0957a2981b994322f763b2))
* **usage:** round a displayed cost to cents at a dollar and above ([0703156](https://github.com/Taf0711/splice/commit/07031566efc0f8b648c2dd8c10f62963f3246c0d))
* **usage:** show a cost figure when pricing coverage is partial ([6bad51a](https://github.com/Taf0711/splice/commit/6bad51ab921c49bab699eb2c8bb4fb17717b49e8))


### Bug Fixes

* **doctor:** correct the stale connectivity fallback message ([ce91459](https://github.com/Taf0711/splice/commit/ce914596fabf87f1cab49d1a68738c68f0746a27))
* **dtools:** resolve the workspace root through symlinks ([e5d5542](https://github.com/Taf0711/splice/commit/e5d55422905b4f1a32e3d611de24f347ee820c43))
* **exec:** honor compaction config in the spec-draft run ([391887c](https://github.com/Taf0711/splice/commit/391887c5bbd01926ed5f3958fef352a5a9fa4c94))
* **tui:** give the TUI catalog its provider profile ([2c3089b](https://github.com/Taf0711/splice/commit/2c3089b76b62ee73256b79cc17b64dc8c255404b))
* **tui:** keep a critique that failed to persist ([887c97b](https://github.com/Taf0711/splice/commit/887c97b5e8bd11e3b30221031e0464b04eec8f39))
* **tui:** keep a crystallized plan when the critic fails ([c1f3b78](https://github.com/Taf0711/splice/commit/c1f3b782fae3cec53a434742a204c83684b031c3))
* **tui:** persist attributed usage for non-pipeline runs ([d8e7574](https://github.com/Taf0711/splice/commit/d8e7574d85efb92ed0a6dc8caaba9acd81b70021))
* **tui:** stream crystallize output and record its cost ([48778b9](https://github.com/Taf0711/splice/commit/48778b9032aa3a6311a7745181b174318e437675))
* **usage:** show fewer cost digits and use one formatter ([f563ba6](https://github.com/Taf0711/splice/commit/f563ba6ef0a16e0afeb65a6e5d4ea131ed34fa05))


### Performance Improvements

* **exec:** drop the unread context window from the pipeline run options ([e858f58](https://github.com/Taf0711/splice/commit/e858f5823ee9729de00fe40b03175e6b364e10f3))
* **tui:** stop re-measuring the transcript on every wheel tick ([c63edd0](https://github.com/Taf0711/splice/commit/c63edd04ca70d31dba260e305dd1748a991e10b9))


### Miscellaneous Chores

* **release:** cut 0.1.3 ([ad495db](https://github.com/Taf0711/splice/commit/ad495db473dd21af4f65815903fb0ba1a28d7811))

## [Unreleased]

### Added

* **cli:** Splice tells you when a newer release exists. A run checks at most once a day and prints one line naming the command that suits how you installed it. The check never delays or fails a run. Only a terminal sees it: piped output, `splice exec` in protocol mode, and the interactive TUI carry no notice. `SPLICE_DISABLE_UPDATE_NOTICE` turns the notice off and leaves `splice update` working; `SPLICE_DISABLE_UPDATES` turns off both. `splice --update` checks and reports; installing stays `splice update --apply`.
* **stages:** a plan's acceptance criteria now run. A criterion carrying a command was reaching the code writer as prose and nothing ever executed it, so a run could report success on code that compiled, passed its tests, and did not do what was asked. A verification stage runs each criterion that has a command and reports one result each. A criterion nobody automated is recorded as skipped, not failed.
* **tui:** an untrusted workspace is now marked in the footer, and `/trust` records the decision. Previously a user who declined trust, or who was defaulted to untrusted, had no way to see it and no way to change it without editing a file. The decision takes effect on restart, because the session already decided at startup whether to load project commands, hooks, MCP servers, and plugins.

### Security

* **auth:** OAuth tokens are protected at rest. They defaulted to a plaintext file while API keys defaulted to the keychain or an encrypted file, so the stronger secret had the weaker protection. Tokens now resolve the same policy API keys use, and existing tokens move across at startup. Set `SPLICE_OAUTH_STORAGE=file` to keep plaintext. Migration keeps the plaintext copy until the protected write is read back and verified, so a locked keychain leaves the login working.
* **sandbox:** credential directories are read-denied by default: SSH, cloud, GPG, Kubernetes, container registry, GitHub CLI, and Splice's own token store. The sandbox previously granted read access across the filesystem, so any sandboxed command could read these. Add `sandbox.allowRead` to the global user config to re-include a path. Project config cannot grant it.

### Fixed

* **stages:** the security audit stage failed on any machine with `gosec`, `bandit`, or a SARIF scanner installed. The scanners write log lines to stderr and their report to stdout, and Splice read both together, so the report could not be parsed. The stage failed, the writer retried, and the run ended before its test stages. Substantial and architectural runs were affected.
* **sandbox:** a sandboxed `go` build could not write its build cache, because the cache sits outside the sandbox. One stage passed and the next failed on a cache entry that was never written. Splice now points `GOCACHE` at a directory the sandbox can write. An explicit `GOCACHE` still wins.
* **stages:** a pipeline run could fail to finish even when every stage ran. The test generator was told what the code writer did in prose, not which files it produced, so it wrote tests against names that did not exist and the run failed on undefined symbols. On a retry it could not rewrite the test file it had written itself. It now receives the writer's actual paths, and a retry replaces its own earlier file.
* **design:** the plan critic could block a plan indefinitely. It never saw its own earlier critiques, so each revision drew new objections, and a medium-severity concern could stop execution. It now sees the previous plan and critique, only high and critical severity blocks, and it reads what the design conversation established rather than guessing.

### Changed

* **sandbox (Linux):** the sandbox now enforces. It located its helper on `$PATH` only, and no release archive ships that helper, so enforcement degraded to unconfined without saying so. Commands that ran before may now run confined wherever `bwrap` is installed.
* **sandbox:** SSH-based Git inside the sandbox needs `~/.ssh` in `sandbox.allowRead`. Approving network access does not restore a denied key.

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
