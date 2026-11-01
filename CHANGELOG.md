# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 0.3.0 (2026-11-01)


### Features

* **agenteval:** carry the resolved topology name into the eval ledger ([b677154](https://github.com/Taf0711/splice/commit/b677154d4634e4dbcb51d78ff4f2951e04a1d89d))
* **agenteval:** derive eval cost from the request samples ([9e137e0](https://github.com/Taf0711/splice/commit/9e137e06e41f84c81b098330a90a3509e94be8da))
* **agenteval:** parse usage events while the agent runs ([15dc68e](https://github.com/Taf0711/splice/commit/15dc68e46697619e790b8e4c272c67379f29f9b4))
* **agenteval:** publish the benchmark v2 report contract ([eed860d](https://github.com/Taf0711/splice/commit/eed860d790cf85895de8ff4653c7cc2bc6ed8396))
* **agenteval:** revision stamping on eval reports (E0 exit item 4) ([bc84877](https://github.com/Taf0711/splice/commit/bc8487705d89f8f4d4b6328dcc658521f7d4585e))
* **agenteval:** unified per-attempt ReportRow + failure taxonomy (E0 exit items 1+5) ([56c1d08](https://github.com/Taf0711/splice/commit/56c1d08745307a9ddae37b35949c3878f0bbc040))
* **cli:** --pipeline flag and pipeline list/show (T6b-i) ([b4f2885](https://github.com/Taf0711/splice/commit/b4f2885029ceb72a923650a8252f9f71eecc3665))
* **cli:** origin-annotated config describe (T7) ([7d3a08e](https://github.com/Taf0711/splice/commit/7d3a08e828d94933d3fc36c5825a474da94b5e50))
* **cli:** pipeline use/export/import with a hardened import client (T6b-ii) ([8259b56](https://github.com/Taf0711/splice/commit/8259b56509dbc409edc97545758b83ea4da658a1))
* **cli:** report eval runner, routed models, and cost coverage ([a740bb7](https://github.com/Taf0711/splice/commit/a740bb7869527c40a0d982faec5a7bd84bab1d1c))
* **cli:** run eval benchmarks through the production pipeline by default ([4ff0a2d](https://github.com/Taf0711/splice/commit/4ff0a2d47ce40d524794aab0a3901bf5325fdbc0))
* **cli:** say when an update is available, and add splice --update ([de0792c](https://github.com/Taf0711/splice/commit/de0792c92a0707cc1c9e2c36585df5f1d7860fc1))
* **cognition:** enable graph capture on the exec memory path ([618259b](https://github.com/Taf0711/splice/commit/618259be861dae796dbfd964bd6ac95ba4640e05))
* **cognition:** run-local replay guard - deliver each item to a stage once per run ([fd7c21b](https://github.com/Taf0711/splice/commit/fd7c21bbcaadbf4e81cb157420341643ddc50b1a))
* **cognition:** vertical slice - repair intelligence + typed cognition graph + capture ([69abcd5](https://github.com/Taf0711/splice/commit/69abcd52b4dc41621ffb528010663351ea012b40))
* **compaction:** keep the agent inside the cheapest pricing tier ([eee9f67](https://github.com/Taf0711/splice/commit/eee9f67cc6691049576565ac8347d7edd5c7135d))
* complete attributed pipeline cost accounting ([7440a8e](https://github.com/Taf0711/splice/commit/7440a8ed8bc5b79db5931e2fb386c6a4c68dc27b))
* configurable topology, agent nodes, browser pipeline surfaces, and the trajectory classifier ([c21c615](https://github.com/Taf0711/splice/commit/c21c6151d38b08ffea50159d6fe17af339ee9a6d))
* **design:** add agent-requested transitions and revision context ([3a1421f](https://github.com/Taf0711/splice/commit/3a1421f8479a8c6cb4681518690fc05d920702d4))
* **design:** ask the provider to search the web ([9c5651d](https://github.com/Taf0711/splice/commit/9c5651d81b7822a0a8ab202f91c2493f8c1ce230))
* **design:** emit task_started before each plan task dispatches ([09b3826](https://github.com/Taf0711/splice/commit/09b3826c99d5103147a26977999f3d61f065ff13))
* **design:** give the design agent research tools and read-only roots ([4e44b86](https://github.com/Taf0711/splice/commit/4e44b86950f00eb6f85715284bd1158f2712850a))
* **design:** open-question runtime state — raise, persist, reconstruct, render (§7.1) ([b2a262e](https://github.com/Taf0711/splice/commit/b2a262ea3748326c4cd28402b14e2e8eff2b7f5d))
* **eval:** add durable eval v2 identity ([0962e75](https://github.com/Taf0711/splice/commit/0962e757c87ff5cab571ff47c8a9c982a93185bf))
* **eval:** add frozen memory policy ([de0d0de](https://github.com/Taf0711/splice/commit/de0d0de565628fb83a763deb39687690004bc115))
* **eval:** add isolated environment schemas ([0974877](https://github.com/Taf0711/splice/commit/0974877a2fa709ed1e2382fb28a181b735cedfe3))
* **eval:** add symmetric telemetry checks ([3c7efbe](https://github.com/Taf0711/splice/commit/3c7efbe8eeaf41f3366784a947961adda595e40e))
* **eval:** causal rollout loop + families runner (cognition Q3 harness) ([f56103e](https://github.com/Taf0711/splice/commit/f56103eb4d3bc11c1cccc048907e77a8ea4dc8ca))
* **eval:** decide the cost gate on matched-success pairs ([731d199](https://github.com/Taf0711/splice/commit/731d199afb53215e39d684bf03b2204d11407e19))
* **eval:** paired-arm eval harness (PE v1) ([e8fdf28](https://github.com/Taf0711/splice/commit/e8fdf2808daa86facb6d8060bf94c98526f0d66c))
* **eval:** wire the section-30 attempts log into the eval runner (EVAL-V0 G) ([f9d8497](https://github.com/Taf0711/splice/commit/f9d849720fd8adf6689a45ccad34eb6003bcd2dc))
* **flags:** add a typed feature-flag registry and wire the pipeline ([c4a25cc](https://github.com/Taf0711/splice/commit/c4a25cc797039a9fdfa11d22a5870c176c9fcf55))
* initial public release of Splice ([480083e](https://github.com/Taf0711/splice/commit/480083e74785fe9af85938a1a1f15960b51e7823))
* **learn:** calibrate per-stage budgets from run traces (LN2) ([8610484](https://github.com/Taf0711/splice/commit/8610484b642d329e2d25e9dd6853d9b72f304d73))
* **memd:** exact topic lookup over observations ([4a6c9ec](https://github.com/Taf0711/splice/commit/4a6c9eceb04916b3a7912e39e8e8240cbac1b168))
* **memd:** expose FTS rank on /search_ranked (C1c D1) ([d6eb25b](https://github.com/Taf0711/splice/commit/d6eb25b29601a6003e4f2fd8ee92d48c87f690ed))
* **memd:** idle tracker for sidecar shutdown (groundwork) ([28fae83](https://github.com/Taf0711/splice/commit/28fae838e4f2428ef1f9469c94f44e19099e8d1b))
* **modelregistry:** price long-context tiers from models.dev ([5d6e762](https://github.com/Taf0711/splice/commit/5d6e7625c99e586b4fc98f7e81dbd18603b0b285))
* **modelregistry:** price models the curated catalog does not carry ([00d93f2](https://github.com/Taf0711/splice/commit/00d93f2e2256f55b868550524a24d733ff26ae13))
* **modelregistry:** ship a models.dev snapshot and stop curating prices ([8b3ad05](https://github.com/Taf0711/splice/commit/8b3ad055de41ee858d6c096afb3dc06c3199bba4))
* **openrouter:** run web search on the provider ([fe19ea3](https://github.com/Taf0711/splice/commit/fe19ea395cec2ee62a310ff5aeac68d987284d9e))
* **pipeline:** classify agent options for pipeline runs ([8cf01bd](https://github.com/Taf0711/splice/commit/8cf01bdde98f8df19980dba76047aa244b1eaaa8))
* **pipeline:** declare stage capabilities ([4c90115](https://github.com/Taf0711/splice/commit/4c901150ff3b64ae8560669014ab93eb92697bc4))
* **pipeline:** emit typed stage events ([5b4f2ee](https://github.com/Taf0711/splice/commit/5b4f2ee91b4e7aa8a7c8037feb0c90053a5fb626))
* **pipeline:** enforce tool filters and hooks ([fa1727a](https://github.com/Taf0711/splice/commit/fa1727a7655b6e95cccc21d7e90266322e6529d4))
* **presentation:** phase x health x gate + cancelled receipt ([435f040](https://github.com/Taf0711/splice/commit/435f0400a6d7ccc4d6a0fd31583c2a54b285da2c))
* **presentation:** typed presentation state skeleton ([3357777](https://github.com/Taf0711/splice/commit/3357777049bef97042930740e714253c6448a470))
* **providers:** let a probed tool-less local model contradict the base grant (LL4) ([5013996](https://github.com/Taf0711/splice/commit/501399606131fb82f8020e6674166bf8d4bbb977))
* **providers:** map reasoning effort for local reasoning models (LL3) ([b89494f](https://github.com/Taf0711/splice/commit/b89494f808d0f64977d4effc9a2c4f02e8eb6637))
* **providers:** probe local model capabilities and surface installed models ([7c661bd](https://github.com/Taf0711/splice/commit/7c661bd27c0438b0ac659ed8feb2202de532d64e))
* **runtime:** add plumbing for provider-executed web search ([3976759](https://github.com/Taf0711/splice/commit/39767591db35d960ed07b4d1a5885c6c83349773))
* **sandbox:** audit hooks and imageinput spawns through procrun ([0499c11](https://github.com/Taf0711/splice/commit/0499c11a9ed2be3c62034feb4cb442386b65beec))
* **sandbox:** route all deterministic stage subprocesses through one audited chokepoint ([846d6b3](https://github.com/Taf0711/splice/commit/846d6b353be2c7b7b9e811f13a5b16db2c3e037f))
* **security:** workspace trust gate, env scrubbing, sandbox hardening (Track S, v0.1.2) ([#6](https://github.com/Taf0711/splice/issues/6)) ([2479d6a](https://github.com/Taf0711/splice/commit/2479d6a91a543767a42e885d60bead40232776b7))
* **sessions:** record origin_cwd for worktree session identity (TW4) ([b537c96](https://github.com/Taf0711/splice/commit/b537c96999352fbe7e6c69a11fb698a4b3e29233))
* **splice:** add typed StageMessage schema for inter-stage revision requests (DM1) ([0a6e638](https://github.com/Taf0711/splice/commit/0a6e63873276ad1cb530a33ace335c8a2b3df376))
* **splice:** canonical verification collection and hash fallback (T8) ([320a9de](https://github.com/Taf0711/splice/commit/320a9dea5330e61810dc4c921700db9d026ce84e))
* **splice:** cognition fast path test matrix ([a12c85c](https://github.com/Taf0711/splice/commit/a12c85c6565290b2480f6a6cd4202e798ffbb49a))
* **splice:** command nodes run through the registry (T5a) ([e18660d](https://github.com/Taf0711/splice/commit/e18660d92233f604238bd60c7aa0c1fff023a3d9))
* **splice:** compact over-budget stage input instead of aborting; raise light tier from run-3 data ([904c570](https://github.com/Taf0711/splice/commit/904c5700394a34440d9a088e4816f6f212287be5))
* **splice:** constrain memory replay and gate exemplar delivery ([263ccc0](https://github.com/Taf0711/splice/commit/263ccc004e2bceff14ce22a00534bde5c41c4aa9))
* **splice:** count mid-run prompt layout flips ([5e627cf](https://github.com/Taf0711/splice/commit/5e627cf9b6b2744007e6d386970ce1a1c41e2eed))
* **splice:** define bounded reasoning-memory admission and review types ([8a4f9ae](https://github.com/Taf0711/splice/commit/8a4f9ae02db5e952d0833bf56d3ace338d499cb5))
* **splice:** deterministic memory reranker (C1c D2) ([950d5e2](https://github.com/Taf0711/splice/commit/950d5e23ce3eb714eea307f6ff3b23796a7b8496))
* **splice:** direct cognition fast path in prepareStageInput ([1096060](https://github.com/Taf0711/splice/commit/10960606efabccc9590c119f6c265f494448fb27))
* **splice:** emit presentation state snapshots ([2109776](https://github.com/Taf0711/splice/commit/2109776b48a42cbc90339c63bae601d62e351355))
* **splice:** enforce splice_min_version (M1) ([4b98215](https://github.com/Taf0711/splice/commit/4b982157cbad3aff2b071d2c0da426ef1e1e6292))
* **splice:** executor honors the topology DAG (T3a) ([54c6407](https://github.com/Taf0711/splice/commit/54c6407f9b39fd610ebf9a143d62b66e52e1e8a0))
* **splice:** exemplar ablation modes (C1c D4) ([ad8c06a](https://github.com/Taf0711/splice/commit/ad8c06aac69e25aa0f0e70f124e994d9eb75a41e))
* **splice:** gate trajectory budgets on generation, account input without aborting ([71ba623](https://github.com/Taf0711/splice/commit/71ba623731cadbd871e0c41e65da1b4111aeb1d4))
* **splice:** in-pass repair loop for failing tests (DM2) ([f3ecfba](https://github.com/Taf0711/splice/commit/f3ecfba93722698ff13ad0fc87262c1031bb75a9))
* **splice:** key stage state by name and iteration (T3b) ([81b10ba](https://github.com/Taf0711/splice/commit/81b10bad77ee4b4f6688449dfbe4c31be8b2d989))
* **splice:** make the three-state memory status real ([1e372f8](https://github.com/Taf0711/splice/commit/1e372f883f00ab37d0970bf672459cf10b90554b))
* **splice:** miss-path telemetry in stage trace meta (E0 exit item 2) ([e9627d6](https://github.com/Taf0711/splice/commit/e9627d63f3fc855c0cff30c03fa3a9d86d1720ef))
* **splice:** name the running stage's action in the pipeline panel ([561e52b](https://github.com/Taf0711/splice/commit/561e52b0d320dc9dd0e9f895903b0812a8525013))
* **splice:** node capabilities drive the executor (T4a) ([3267784](https://github.com/Taf0711/splice/commit/3267784474c2c96c3262c0dd1fe7e4d5dbb8db0a))
* **splice:** optional topic-lookup memory capability ([8030e9c](https://github.com/Taf0711/splice/commit/8030e9c305e3107473f2d07a8e5eb09b940b6804))
* **splice:** persist repair interactions in the run trace (DM3) ([a3e7d90](https://github.com/Taf0711/splice/commit/a3e7d90058702cf44033c15801365986fafdc20c))
* **splice:** persist run traces incrementally (F4) ([74873a9](https://github.com/Taf0711/splice/commit/74873a930c1d9df91a1cb12a95a15b902b288131))
* **splice:** persist run_outcome traces and post-run verdicts (LN1) ([60ddf34](https://github.com/Taf0711/splice/commit/60ddf34c33e1b30f429cd55e5b5cbb1fc622c4de))
* **splice:** pipeline topology schema and compiler (T1, T2) ([56c9628](https://github.com/Taf0711/splice/commit/56c962860a8311d2a20979bead8ae9b494d759eb))
* **splice:** preflight substrate interference before the run ([46b2c7b](https://github.com/Taf0711/splice/commit/46b2c7b80552a1ad7a9998470bff498312a4c266))
* **splice:** prepare and trace reasoning memory in pass and repair paths ([4f2e189](https://github.com/Taf0711/splice/commit/4f2e18997df06b644bae54e48aa1cff7655be856))
* **splice:** prompt nodes run a bounded model call (T5b) ([fa0b392](https://github.com/Taf0711/splice/commit/fa0b39210269974ab0a11dc046bfd02f4d79ce0c))
* **splice:** record cache_hit and memory_position per request ([0e6911c](https://github.com/Taf0711/splice/commit/0e6911c598c885ca73ce9810d371e2879f869f98))
* **splice:** record the prompt layout hash per provider request ([cfcaec8](https://github.com/Taf0711/splice/commit/cfcaec8119b96bc9fbdf4afba8f6a173ef0927ca))
* **splice:** resolve the active topology from config (T6a) ([20e399d](https://github.com/Taf0711/splice/commit/20e399dae107b40587183a7a4bad355bee257047))
* **splice:** retrieve kept-run exemplars into stage memory bundles (PC3) ([084211e](https://github.com/Taf0711/splice/commit/084211e31dbc7dd2604129340bb8560214a9ab57))
* **splice:** structural cognition keys and freshness gate ([bc9ce31](https://github.com/Taf0711/splice/commit/bc9ce313e74e946c4fe58e7441fb4af0d1c8c1bc))
* **splice:** the model wizard reads the active topology (T4b) ([9b63355](https://github.com/Taf0711/splice/commit/9b63355735327683cd25702b81adfa578ee10c38))
* **splice:** token-budget memory admission (C1c D3) ([8465cc9](https://github.com/Taf0711/splice/commit/8465cc98521ec2ba966ca773a6954e939ad002d0))
* **splice:** warn once when run-trace persistence fails ([5fe5502](https://github.com/Taf0711/splice/commit/5fe5502e6a9f77250244e3634a45e0a82071a0a6))
* **splice:** wire reranker into the miss path (C1c D2/D3 integration) ([e1b5ffe](https://github.com/Taf0711/splice/commit/e1b5ffe499f39fee69f335b7fe379d05bf8364da))
* **splice:** wire the node model ladder end to end (T6c-ii) ([8668b6c](https://github.com/Taf0711/splice/commit/8668b6ceb23df7d8de2b34ce3b191c2c56f93159))
* **stages:** check the acceptance criteria a plan was approved on ([062e15c](https://github.com/Taf0711/splice/commit/062e15cab9598a7a0fc945ceb5037635ad0fcc3e))
* **stages:** require memory consideration without retrying core output ([e9c0bd8](https://github.com/Taf0711/splice/commit/e9c0bd84bb9d9fa60fb4a4d5a873148e728bb29d))
* **tools:** reduce exec_command output natively and keep diffs whole ([c0107ee](https://github.com/Taf0711/splice/commit/c0107eeeda112de491910af1042475902f02f14d))
* **trajectory:** abort after repeated no-progress passes ([9dccad0](https://github.com/Taf0711/splice/commit/9dccad055ea7463e9fccebb4b301022db237b501))
* **trajectory:** attribute cycle evidence ([ac07c49](https://github.com/Taf0711/splice/commit/ac07c498af0437bd31792eed02f736205bb9c5d2))
* **trust:** manage workspace access in TUI ([558c1d9](https://github.com/Taf0711/splice/commit/558c1d929c2956198f2428d3db92d3d553925e0c))
* **tui,harness:** truthful pipeline progress, pinned surfaces, narrow strip, harness seam with Pi bridge ([3d45df8](https://github.com/Taf0711/splice/commit/3d45df837dbbfc3409e130f098c857e74e725b50))
* **tui,procrun:** discoverable cancel hint, stable strip widths, default spawn audit in exec ([11528f2](https://github.com/Taf0711/splice/commit/11528f23dabf393bde6cd14aa5974b23d14309bd))
* **tui:** /layout presets over the one-column knobs (P3 GAP-K rest, DoD 36) ([1baeeef](https://github.com/Taf0711/splice/commit/1baeeef09cfb7d1ef095429f399798592fb8889a))
* **tui:** /model picker inline effort ring + cost rail (implementer session work) ([beae944](https://github.com/Taf0711/splice/commit/beae944c04911c71d79de3acc40462196a081101))
* **tui:** /stages is a one-surface editor with the /model interaction ([b807059](https://github.com/Taf0711/splice/commit/b80705902597c77e8c46108833147661eb28be7e))
* **tui:** /stages speaks the /model picker design language (P1.4 fold-in) ([e2140a6](https://github.com/Taf0711/splice/commit/e2140a68c21280b3c1490e3a296625d3e73096ed))
* **tui:** add a worktree-reject demo tape ([0efd91e](https://github.com/Taf0711/splice/commit/0efd91e294578dc57e8077ed4bc9ec162d9bb172))
* **tui:** ASCII glyph tier system (GAP-C, DoD 24) ([739b1b5](https://github.com/Taf0711/splice/commit/739b1b50e709f38b3a86a8463d8ece3fbbcc2d58))
* **tui:** ask for a reject reason before removing a worktree ([55b7958](https://github.com/Taf0711/splice/commit/55b795828f7c538a375785b631bce79b96ce8bcb))
* **tui:** ask_user gate elevation (GAP-E, audit finding 4) ([e9430f9](https://github.com/Taf0711/splice/commit/e9430f946143eb8eaa426633b17d73cdccc1b825))
* **tui:** compact-mode 80-column collapse (GAP-D, DoD 2/16/18) ([4e8c336](https://github.com/Taf0711/splice/commit/4e8c33633b7846bead6af619f83090a690598c14))
* **tui:** composer is a bare prompt row per the Pen frames ([8798980](https://github.com/Taf0711/splice/commit/8798980e074aa25bd9421d8fd08b527848f389ca))
* **tui:** Ctrl+E copies the hunk under the diff window (P3 GAP-G rest) ([7993d85](https://github.com/Taf0711/splice/commit/7993d85c29007b780bffd8f07aaa0a15e2900df5))
* **tui:** Ctrl+G detail/evidence pane (P3 GAP-G rest, §17) ([af2bbab](https://github.com/Taf0711/splice/commit/af2bbab7ef2daf5174e7947a2a447ebda3448cc2))
* **tui:** Ctrl+N verbosity cycle as a configurable binding (P3 GAP-L polish) ([0fa0674](https://github.com/Taf0711/splice/commit/0fa06747d256b4ddfc2745c3b51b0a60b7c757aa))
* **tui:** design-decisions ledger (GAP-E, P4 E1, contract 7.1) ([681f9c3](https://github.com/Taf0711/splice/commit/681f9c3b4c18875f66627d5ef16b5a2d4a45afbc))
* **tui:** diff review viewport (GAP-G, contract §11, frame tOwI1) ([d22a5fe](https://github.com/Taf0711/splice/commit/d22a5fea5c6b491fa17733fb23e71d16e3b2dd73))
* **tui:** diff view position readout + drop-whole keymap bar (UX ref) ([f6ebed6](https://github.com/Taf0711/splice/commit/f6ebed6b96c85eb447498985043f9a83a29e5787))
* **tui:** expose live tool activity ([7c481c1](https://github.com/Taf0711/splice/commit/7c481c1d1876686ecd61f4ceff52e7b8f418ad44))
* **tui:** give /exec CLI-exec parity, visible queue delivery, and a cold-memory knob ([8789bbd](https://github.com/Taf0711/splice/commit/8789bbdf91d7344c9fc8edc34e993d9a0ae4a9fb))
* **tui:** handoff [O] open-worktree key (GAP-F rest, DoD 26/28) ([8bb7be4](https://github.com/Taf0711/splice/commit/8bb7be4f825296b9007b2af1a5dfa2db9693f4be))
* **tui:** HANDOFF surface for exited lanes (GAP-F, DoD 28) ([49f75e2](https://github.com/Taf0711/splice/commit/49f75e204648f1d2679b024f3ac5d4197e83ffb9))
* **tui:** in-flow evidence band during execution (P3 GAP-K slice 2, owner Tension-3 decision) ([00fe19a](https://github.com/Taf0711/splice/commit/00fe19a89f8b24cdfb8a185c6680a658d5d128e5))
* **tui:** interactive handoff keys [M]/[X] (GAP-F, DoD 28) ([10f35b0](https://github.com/Taf0711/splice/commit/10f35b027dfecbb2ece614d4a38e0ad15bd8a148))
* **tui:** kind-driven node rendering ([bc7c652](https://github.com/Taf0711/splice/commit/bc7c652e4405bdd9a4d336a62f43e33d6f42b78f))
* **tui:** MCP staged-add card (P3 GAP-H, frame VCeGi, contract §15.2) ([4b570e5](https://github.com/Taf0711/splice/commit/4b570e58805af7f5936fc62678ea6da92ae1ca5d))
* **tui:** narration classes + live verbosity (P3 GAP-L, DoD 41-44, contract §2.5/§9.6) ([ffc85a9](https://github.com/Taf0711/splice/commit/ffc85a994532115d50cb7886ae681957c0094578))
* **tui:** offer accept, reject, or keep after a worktree run ([1ba00dd](https://github.com/Taf0711/splice/commit/1ba00ddd45f5c5059673570f3a559ce40971d9d6))
* **tui:** P1.4 ideal-iteration surfaces (sidebar decisions/run modules, trajectory trail, status-bar segments) ([949103e](https://github.com/Taf0711/splice/commit/949103ede11e1fb12b16f9f13f25aee11ff4b109))
* **tui:** P12 launch cockpit — information cockpit replaces the braid-splash launch ([cfc24f2](https://github.com/Taf0711/splice/commit/cfc24f2bd08da46d2a4f30db437f87ed93b3d402))
* **tui:** P12 launch contract band + health-in-status (frame kAYHl items 2 and 8) ([d3f7047](https://github.com/Taf0711/splice/commit/d3f70472cb387f1872d5aba601adb64008e3a9a5))
* **tui:** P12 launch-cockpit sidebar — two-column launch with launch modules ([c9f81bf](https://github.com/Taf0711/splice/commit/c9f81bfb047b3dce52ad64e39495bc2dacd64b0b))
* **tui:** P12b facts grid — two-column launch facts with branch (frame kAYHl) ([b388188](https://github.com/Taf0711/splice/commit/b3881889d710e25379ddfd8375fb710e136f33de))
* **tui:** P13-P15 command surfaces — five slices from the delegated batch ([6506d0a](https://github.com/Taf0711/splice/commit/6506d0a3e325ceb9e7c8ab75d1093ec7706e3b5d))
* **tui:** P15 ack grammar — one verb-column line per command ack ([a34f1b5](https://github.com/Taf0711/splice/commit/a34f1b585be19ccf7346aef85656e30b99dcd5c2))
* **tui:** P4 lifecycle cards - plan + critique + crystallizing (GAP-E) ([f91b0f1](https://github.com/Taf0711/splice/commit/f91b0f1b0e2ae12dcf7cf65411138e627a129cde))
* **tui:** Pen glyph tier — rich unicode set by default on UTF-8 terminals ([a5af527](https://github.com/Taf0711/splice/commit/a5af527fb4fb30ebeba3ee5457e16aca72d7dfcd))
* **tui:** presentation replay on resume (stabilization F3, §15) ([e6974ac](https://github.com/Taf0711/splice/commit/e6974ac9f96580a6dbf85143b41413d59c8aaa93))
* **tui:** render pipeline panels from presentation state ([c45144b](https://github.com/Taf0711/splice/commit/c45144b5f586cc35abb9571c2fa866b775cc146e))
* **tui:** repair-loop message cards in the pipeline panel (DM4) ([195ab88](https://github.com/Taf0711/splice/commit/195ab886f1a262c2f6a32874811371bd051dca54))
* **tui:** report lifecycle state to Herdr ([33c7995](https://github.com/Taf0711/splice/commit/33c799564d5d8bf480e7496311995390db4cdf55))
* **tui:** retain specialist tool activity ([6df95c5](https://github.com/Taf0711/splice/commit/6df95c5fc3340d4e8518cfdbacebfe17be95a526))
* **tui:** run pipeline turns in an isolated worktree ([f265ce9](https://github.com/Taf0711/splice/commit/f265ce91af12e5192cb0e415b20c6a93effe7adc))
* **tui:** show an untrusted workspace and let the user trust it ([901e6fa](https://github.com/Taf0711/splice/commit/901e6faab8428ce96fec3db24eb8f14d1cb10c80))
* **tui:** show honest session cost with explicit coverage ([0de1c81](https://github.com/Taf0711/splice/commit/0de1c81f84632f2bcd254111a9b2251b6ceb03ed))
* **tui:** stable narration block ids across resume (P3 GAP-L rest, DoD 43/45) ([bdc0360](https://github.com/Taf0711/splice/commit/bdc03602772d5234a2336781239ad119fff36fdb))
* **tui:** status line + hint bar match the Pen frame grammar (kAYHl) ([bc01bf9](https://github.com/Taf0711/splice/commit/bc01bf90ec7feb52a58b7418e370e682723c3538))
* **tui:** suggestion palette adopts the card grammar ([a3f5b3b](https://github.com/Taf0711/splice/commit/a3f5b3b669a48e242cabf0ad2e1126f63203352e))
* **tui:** surface memory-status transitions and exemplar injection ([3fab286](https://github.com/Taf0711/splice/commit/3fab28602fa1eac97570fd365ffc46671474c928))
* **tui:** surface trajectory guidance ([901d99f](https://github.com/Taf0711/splice/commit/901d99f73ce347ae00660f6a6b0db3553f3703e8))
* **tui:** terminal receipt cards (GAP-E, DoD 29 + audit finding 3) ([346eeac](https://github.com/Taf0711/splice/commit/346eeac541cde226b09a2ee5eb297f19d70cdce5))
* **tui:** trajectory inspection surface (GAP-G, DoD 11/12) ([a66b9e8](https://github.com/Taf0711/splice/commit/a66b9e8cb7d905a9d30148318e1ebdbb4dde2317))
* **tui:** untrusted project config evidence card + [V] view action (GAP-I, §15.1/§16, frame I1) ([48da3f7](https://github.com/Taf0711/splice/commit/48da3f7e1915557ddf20922303ca32ffc4c72449))
* **tui:** VERIFIED receipt emitter — completed runs project the third terminal card ([34da810](https://github.com/Taf0711/splice/commit/34da8106f3ffc6d317a952cf84413c0dff5f2879))
* **tui:** wire iteration recovery for worktree pipeline runs ([f03d163](https://github.com/Taf0711/splice/commit/f03d163e2150e6a31e407fad3ec7aa3fe4a125e5))
* **tui:** wire the pinned-decisions ledger into transcript + resume (P3 GAP-L rest, DoD 46) ([f9364e6](https://github.com/Taf0711/splice/commit/f9364e62d5ae26d070b718a7f5f44e8f4403352c))
* **tui:** workspace isolation badges (GAP-F, DoD 26) ([4692dbd](https://github.com/Taf0711/splice/commit/4692dbd582fa1b81d760a73d7ea3e84216de5fa5))
* **usage:** persist cost estimates with provenance and coverage ([0215c28](https://github.com/Taf0711/splice/commit/0215c28994ae1fbeae0957a2981b994322f763b2))
* **usage:** round a displayed cost to cents at a dollar and above ([0703156](https://github.com/Taf0711/splice/commit/07031566efc0f8b648c2dd8c10f62963f3246c0d))
* **usage:** show a cost figure when pricing coverage is partial ([6bad51a](https://github.com/Taf0711/splice/commit/6bad51ab921c49bab699eb2c8bb4fb17717b49e8))
* **wiring:** add a producer/consumer wiring guard and close the residue ([37e952c](https://github.com/Taf0711/splice/commit/37e952c4bd2fa07bc6c33f171af85955cba9f75f))
* **worktrees:** define one immutable review snapshot for review and apply ([5f98b38](https://github.com/Taf0711/splice/commit/5f98b389600e1863af88bdb98afd4f2ff3197ec4))
* **worktrees:** prune safe managed worktrees ([efcd1a5](https://github.com/Taf0711/splice/commit/efcd1a5f6cd11f3198b29c901bbd4065c1be9528))
* **worktrees:** seed manifest and trust inheritance (TW5) ([d1ac44c](https://github.com/Taf0711/splice/commit/d1ac44c3f5ae8559137ad41d48ca0bea091c123f))


### Bug Fixes

* **agent:** attribute compaction usage to a stage ([1113b5b](https://github.com/Taf0711/splice/commit/1113b5bb2c1e49e28ec73384314466c38d10281b))
* **auth:** select and report credential storage ([3cdee0b](https://github.com/Taf0711/splice/commit/3cdee0b463268b40b4c8ca5b657fd202a18d57d9))
* **ci:** npm trusted publishing needs Node 24 (npm CLI 11.5.1+) ([c5c6fd7](https://github.com/Taf0711/splice/commit/c5c6fd7dda13f4fd7a2b572da992f13da7951f8a))
* **cli:** ask before treating a workspace as untrusted ([8590900](https://github.com/Taf0711/splice/commit/8590900a231c7dc61ce7d1924c5e8b9f50c93cba))
* **cli:** keep plugin skills reachable in a headless run ([9285ff2](https://github.com/Taf0711/splice/commit/9285ff211065f9bdc8fb06d79a31a17201a08f23))
* **cli:** record usage for a plain exec run ([582519e](https://github.com/Taf0711/splice/commit/582519ee4329d467dc5ab42d324348fa6e3b2ebd))
* **cli:** warn when a merge-back verdict is skipped for a sessionless run ([ed1256f](https://github.com/Taf0711/splice/commit/ed1256f5564a95a16ed0e1a7158d62dd6f54c4ee))
* **cli:** warn when an obsolete ZERO_ variable is set ([ce12945](https://github.com/Taf0711/splice/commit/ce1294501cb0cae92ad8c81069a0e070619ea364))
* **cognition:** longest-first extension match + IP-host rejection; E1b keys eval suite (EVAL-V0 E) ([4cb97f7](https://github.com/Taf0711/splice/commit/4cb97f7ab3c8a0078d234767dfe0a0f109f55efd))
* **cognition:** route headRevision through the stage sandbox profile ([6538675](https://github.com/Taf0711/splice/commit/65386752e42f4fc84ccc2af02a9a6cbb7260a564))
* **context:** skip agent artifact trees ([9304d5e](https://github.com/Taf0711/splice/commit/9304d5ea22fea7bf92c8a4ebf74e95ac00221fec))
* **cost:** use the provider's reported charge instead of estimating ([7da9701](https://github.com/Taf0711/splice/commit/7da97011ae9d6c6c2bbff657715e6ecd667bf9b8))
* **design:** let the plan critic converge ([4ea931a](https://github.com/Taf0711/splice/commit/4ea931aac9ac2e69c2cec2cb8cf4c0bbda9f87f4))
* **design:** retain ask user history ([a4e257e](https://github.com/Taf0711/splice/commit/a4e257e0608575a04fee7f198f3d839f898c723b))
* **design:** select design-mode tools by what they do, not by name ([b026343](https://github.com/Taf0711/splice/commit/b02634337c8dedf2a455097a1947515667da09c5))
* **design:** settle the design before writing the plan ([6a6c298](https://github.com/Taf0711/splice/commit/6a6c298e3e13b746d364cb1483bb542948a64eb0))
* **design:** show the commands a plan will run before approving it ([0e1e0ac](https://github.com/Taf0711/splice/commit/0e1e0acac57711d9867896610a2bc1acc8fc3b84))
* **design:** show the critic what the design conversation found ([63e0bb0](https://github.com/Taf0711/splice/commit/63e0bb0b2c21b99a26e3b5f10f2f4b9a2e7a8e8d))
* **design:** stop the critic blocking on what it cannot see ([93fa43a](https://github.com/Taf0711/splice/commit/93fa43a3b18a29246142ff79fec1d591e4857add))
* **doctor,memoryreason:** fail loud on unreachable stored keys; guarantee one bounded observation fits ([22dc51b](https://github.com/Taf0711/splice/commit/22dc51b915a1c84e469f69851d9f4fa01b69a39c))
* **doctor:** correct the stale connectivity fallback message ([ce91459](https://github.com/Taf0711/splice/commit/ce914596fabf87f1cab49d1a68738c68f0746a27))
* **dtools:** read scanner JSON from stdout, not mixed with its log ([906cc1d](https://github.com/Taf0711/splice/commit/906cc1d1ddd7df16e2912d47190fbd5490466225))
* **dtools:** resolve the workspace root through symlinks ([e5d5542](https://github.com/Taf0711/splice/commit/e5d55422905b4f1a32e3d611de24f347ee820c43))
* **eval:** bind snapshots to locked manifests ([59921a7](https://github.com/Taf0711/splice/commit/59921a77ed97303569ec5b6df59795a729d12cb8))
* **eval:** enforce complete selection audits ([0c3bf88](https://github.com/Taf0711/splice/commit/0c3bf880b71cf7bcaf6c4f8bebc23267af490635))
* **eval:** enforce task qa sealing integrity ([7860649](https://github.com/Taf0711/splice/commit/7860649ce5d750e2d30c4a770c0b155c9db9954e))
* **eval:** fall back to stream usage records when a run writes no trace ([4f553e0](https://github.com/Taf0711/splice/commit/4f553e02e055faaabba730565009d2d832ebd01d))
* **eval:** keep stable arm roots, reset bytes per task pair ([ba278ba](https://github.com/Taf0711/splice/commit/ba278ba70a9d5e7a878619fbe2c07013cd7b0dde))
* **eval:** make snapshot provenance structural ([985b912](https://github.com/Taf0711/splice/commit/985b912a853e19325e0f8d5f4e7f433d1c976639))
* **eval:** materialize fresh arm copies per task pair ([5cf449e](https://github.com/Taf0711/splice/commit/5cf449e6df88a297f2aa2abe8500af32535963d0))
* **eval:** require explicit snapshot rekeying ([c09d508](https://github.com/Taf0711/splice/commit/c09d508800aa0df474be34dab5409fde071551dc))
* **eval:** resolve repo root symlinks before trace lookup and surface missing telemetry ([e54e1f4](https://github.com/Taf0711/splice/commit/e54e1f48b1ad131b2388c344e8f1ad19d535f603))
* **eval:** resume incomplete journal trials ([505816c](https://github.com/Taf0711/splice/commit/505816ccf8fb2cf1aa99eb753b5ecf5f09cd6c08))
* **eval:** survive per-run failures and persist pairs incrementally ([9dcd56c](https://github.com/Taf0711/splice/commit/9dcd56ce605ccdb919f32b137f1ec47e1c9c6b94))
* **eval:** trustworthy causal harness - timeout, verifiers, memory isolation ([f3f65f6](https://github.com/Taf0711/splice/commit/f3f65f6f8a831b40564ed968ccdb80a4b8108c4e))
* **eval:** try raw and resolved repo-root forms in the trace join ([66bb4b9](https://github.com/Taf0711/splice/commit/66bb4b911d3cd83692b2e222ff217c71fc60cf44))
* **exec:** honor compaction config in the spec-draft run ([391887c](https://github.com/Taf0711/splice/commit/391887c5bbd01926ed5f3958fef352a5a9fa4c94))
* **install:** make every installer agree with the release archive ([54f9d9d](https://github.com/Taf0711/splice/commit/54f9d9da7d1a50dfdf60db124e425be514166b02))
* **memd:** reject search control characters ([bf2809d](https://github.com/Taf0711/splice/commit/bf2809d09ddb7324f22ed0a51ae424ef06dc0421))
* **memd:** retry concurrent store startup ([0ae201f](https://github.com/Taf0711/splice/commit/0ae201fc2ebbb2ec9b5512a036cecbba85751c6c))
* **memory:** key project memory to the repo root, not the worktree ([6e5208d](https://github.com/Taf0711/splice/commit/6e5208dfed5a8cb4a0bc821bc77a8a35c51dca2b))
* **memory:** retrieve context only for consuming stages ([4f63d05](https://github.com/Taf0711/splice/commit/4f63d05a2ba59a1241ca32daa5cd31a5b5416b8f))
* **models:** let the fetch flag stop the fetch, not the cache ([d3790be](https://github.com/Taf0711/splice/commit/d3790be8c2beafec7610be61571df107a944db16))
* **oauth:** protect tokens at rest, and never move them on a read ([8b3ac0f](https://github.com/Taf0711/splice/commit/8b3ac0fc169dcc09a71b5f56779556bae7f28f79))
* **openai:** stop sending max_output_tokens to the Codex backend ([d37c3de](https://github.com/Taf0711/splice/commit/d37c3deb0773e402d4122c3416a1f2f84dccb9e1))
* **pipeline:** clamp stages to wall deadline ([8eb1561](https://github.com/Taf0711/splice/commit/8eb1561f62ab18f0c47bc5b87a280ea23ba22978))
* **pipeline:** correct cycle and grant event details ([095b1ac](https://github.com/Taf0711/splice/commit/095b1ac26dc4b7a4f1523a6811cbc3ea3c268ae7))
* **pipeline:** count budget tokens once ([5c39db2](https://github.com/Taf0711/splice/commit/5c39db20f9779d717d37e9e5418ceb1d7631f784))
* **pipeline:** enforce in-pass wall checks ([6d6a407](https://github.com/Taf0711/splice/commit/6d6a4078fda77876bab315a3d3b5b88733b12372))
* **pipeline:** enforce stage output caps ([7fbda8a](https://github.com/Taf0711/splice/commit/7fbda8a1566d74415756c470ab5167bc2e77e1c7))
* **pipeline:** make stage usage sum match the request ledger ([cb639c5](https://github.com/Taf0711/splice/commit/cb639c57e606f4e26b7059d6910dcfed65f8a2ef))
* **pipeline:** pass quality checks as bash strings ([4d69ea2](https://github.com/Taf0711/splice/commit/4d69ea25f3a4cef84aa94afb30e01f239a5de964))
* **pipeline:** report why a run failed ([4cb465a](https://github.com/Taf0711/splice/commit/4cb465a85d592fa2a550234578644722bd922d43))
* **pipeline:** separate pass and turn limits ([6a2f27c](https://github.com/Taf0711/splice/commit/6a2f27c4c1e98ec81d1499640a8b8339648e2b43))
* **pipeline:** stop repeated stage failure loops ([a6e011f](https://github.com/Taf0711/splice/commit/a6e011f20b833bd2f2a3681f2b841dae72f98fa5))
* **providers:** honour the highest reasoning efforts ([a5cf404](https://github.com/Taf0711/splice/commit/a5cf4045bcda1089dc19c4c3348dd2da379dc6cf))
* **providers:** omit unusable output caps ([f274921](https://github.com/Taf0711/splice/commit/f27492117b6c61cea10c290e6d48f91467f66f64))
* **providers:** stop reading the keychain on every request ([0c1e138](https://github.com/Taf0711/splice/commit/0c1e138a8365bfe69d916c028cce5636958e7cf5))
* **reasoning:** honor model-supported effort tiers ([1fe01ea](https://github.com/Taf0711/splice/commit/1fe01eab3990ca3075eb0c2c984554f3ae535f9e))
* **release:** check what an archive contains before publishing it ([469c25d](https://github.com/Taf0711/splice/commit/469c25d53b3fe891c165d520a643a9d2ea36f44a))
* **release:** stamp the version into released binaries ([532b615](https://github.com/Taf0711/splice/commit/532b6154c2b4f923a2e5b79c732427967098ce61))
* **runtime:** stop the usage collector counting a request twice ([770815f](https://github.com/Taf0711/splice/commit/770815f739927004971b1e388c068fc88532a719))
* **sandbox:** deny reads of credential paths by default ([cd3cf5f](https://github.com/Taf0711/splice/commit/cd3cf5f8dd5748af6ec0eb77fad58d752244841b))
* **sandbox:** enforce deterministic stage confinement ([89dda77](https://github.com/Taf0711/splice/commit/89dda779a7128e044d716df7a109667d1aeb2735))
* **sandbox:** evaluate stage tools against the run working directory ([4b41d56](https://github.com/Taf0711/splice/commit/4b41d56510a70d47a728345b63bd17dbe7e0d669))
* **sandbox:** give a sandboxed Go build a cache it can write ([8ad2e96](https://github.com/Taf0711/splice/commit/8ad2e96404c96811fda4d2afda38afb5a4370f06))
* **sandbox:** make the Linux sandbox work on a released install ([5c699f7](https://github.com/Taf0711/splice/commit/5c699f791198f50ef15aaa68881e6478c20a5da8))
* **sandbox:** re-root the caller engine for stage tool execution ([167a55b](https://github.com/Taf0711/splice/commit/167a55bf03f0f779d2a97a21c81be9790d405133))
* **sandbox:** repair linux bwrap confinement ([dc31faf](https://github.com/Taf0711/splice/commit/dc31faf5a692d3571603ffaec0e924ed594aba12))
* **sessions:** reclaim sessions that are no longer wanted ([384d125](https://github.com/Taf0711/splice/commit/384d12510c0c92daafd4f3a8ffc056e2cea1aa85))
* **setup:** retain stored credential marker ([ee5a404](https://github.com/Taf0711/splice/commit/ee5a404339b627fbcb419ce9d3be49e7b3758114))
* **splice,tools:** refuse blind overwrites of unread files in stage runs ([5b6d082](https://github.com/Taf0711/splice/commit/5b6d0825e09e8d8c4829c6ac8048c3bc454ed9a5))
* **splice:** carry a stage's web searches into its record ([04f25f9](https://github.com/Taf0711/splice/commit/04f25f9a7a554dea7fcf82145fea5fcc462134b0))
* **splice:** close reasoning-memory review gaps ([edad633](https://github.com/Taf0711/splice/commit/edad633acee0604626ffc890af5d6dbda102226c))
* **splice:** close the remaining review items (M3, L2, L3) ([32de436](https://github.com/Taf0711/splice/commit/32de436cd0f380cd3398b58647185643856bf56b))
* **splice:** close three wiring gaps in the topology track ([c30ead4](https://github.com/Taf0711/splice/commit/c30ead48462116a02b6d350cae9347c937935601))
* **splice:** enable the read guard before the runner is built ([362a243](https://github.com/Taf0711/splice/commit/362a243e0d032545e48adf156d2b6b5e170aa5ee))
* **splice:** extend deterministic test timeout ([77af0bc](https://github.com/Taf0711/splice/commit/77af0bca374cc355968d1b8f986e23e190bc2de4))
* **splice:** fall back to production source reads when the task names no path ([b8f47c2](https://github.com/Taf0711/splice/commit/b8f47c290ef122fe461981c61dcf8de30e23a251))
* **splice:** feed the static analyzer into the security auditor (H1) ([f2a63e7](https://github.com/Taf0711/splice/commit/f2a63e742a1e0d81c31e2d16cf09ea70d3d37b95))
* **splice:** finish carrying a search through the usage chain ([2b55735](https://github.com/Taf0711/splice/commit/2b55735f34230a513179011777b35a39fcf3b415))
* **splice:** harden the topology track after review (H1, H2, M2, M4) ([95927b6](https://github.com/Taf0711/splice/commit/95927b6c927f4659ddfe217c7d18a00c4756efaa))
* **splice:** keep the stage tool schema stable across memory states ([970510f](https://github.com/Taf0711/splice/commit/970510fbde72d17e37a6594f1d05a9a97c441f7a))
* **splice:** reject inert pull flags on nodes that cannot consume them ([af7c855](https://github.com/Taf0711/splice/commit/af7c8553e93db17ca1458190235ef32b1ba4cebb))
* **splice:** repair rerun replaces the stale failing output in passOutputs ([9d26a49](https://github.com/Taf0711/splice/commit/9d26a4942dcd7dd2da49c10b10d8283138de8cda))
* **splice:** report a repository under sandbox refusal ([c0ba692](https://github.com/Taf0711/splice/commit/c0ba69253c601dcfda34f99d83ad2bffe0611800))
* **splice:** require read-before-write in code writer and test generator prompts ([620ced9](https://github.com/Taf0711/splice/commit/620ced99af201a399327e1989ac0a5ee63317974))
* **splice:** surface bounded failing-test evidence into revision context ([584b1ea](https://github.com/Taf0711/splice/commit/584b1ea6104ade1536fb68a6f82b8d822ee6218c))
* **splice:** typed user abort so internal aborts project FAILED (GAP-E, DoD 29) ([53f4ce6](https://github.com/Taf0711/splice/commit/53f4ce6f17a03d6d428c7bc80ebab34545250b13))
* **splice:** wire the topology outputs the runtime was dropping ([2b834d0](https://github.com/Taf0711/splice/commit/2b834d0cdb873aeaea8778bc86f7a1e3d2a3a98d))
* **stages:** analyse the files a change created ([96989a2](https://github.com/Taf0711/splice/commit/96989a2ce00f57803c2f1f2b60b9f5f4f695e637))
* **stages:** fall back when forced tool choice is rejected ([afc62e0](https://github.com/Taf0711/splice/commit/afc62e082ea67cdb8970b1f435e419bf4f35bd69))
* **stages:** give each stage the roster its prompt describes ([29e4e13](https://github.com/Taf0711/splice/commit/29e4e1325648ccfd490751f41d041dc0b13b455c))
* **stages:** let a timed-out acceptance check finish before the next one ([89474f7](https://github.com/Taf0711/splice/commit/89474f7fe5f965bbd991ae41337ce9ef2c6bcdd3))
* **stages:** run the project's tests, not the first check found ([1befb20](https://github.com/Taf0711/splice/commit/1befb204f32387783c6dfe2224187930212aacef))
* **stages:** say what each stage should do, not what it must not ([3ad3503](https://github.com/Taf0711/splice/commit/3ad35033bc412e3e872e45c129eceda6376e3d21))
* **stages:** tell each agent what it cannot do ([67cbc63](https://github.com/Taf0711/splice/commit/67cbc63f8333d3804a5e2fb7de359daec4cb6995))
* **stages:** tell each stage who reads its output ([b678d74](https://github.com/Taf0711/splice/commit/b678d74676ccbf65da75f1d5e0d80aec7854e0e8))
* **stages:** tell the test generator what the writer actually wrote ([d7ee4c8](https://github.com/Taf0711/splice/commit/d7ee4c838af3d2e698ac5108463ded5db2e6740e))
* **test:** isolate cross-run exec fixture workspaces ([82901e6](https://github.com/Taf0711/splice/commit/82901e6beaf4fbd51cd680fdb9143c8c07a66a2f))
* **tools:** do not advertise capabilities with nothing behind them ([a257369](https://github.com/Taf0711/splice/commit/a2573690baf038cfa6f2ce78b42c5a7499e72b5f))
* **tui:** busy-state guards agree on compaction (stabilization P2 slice 2, §23) ([c5633f8](https://github.com/Taf0711/splice/commit/c5633f85c51e056d1d704594f87a262603abae7f))
* **tui:** cancel ask user runs with esc ([110a64a](https://github.com/Taf0711/splice/commit/110a64a62797330963e2e5f9256fff251d72021a))
* **tui:** cancelled receipt states the lane's worktree truth, not an unchecked disk claim ([d96430d](https://github.com/Taf0711/splice/commit/d96430d0fe2701a4031329a1156a3d688bccee88))
* **tui:** close the diff view when its lane's worktree is resolved (acceptance verifier finding) ([01e6209](https://github.com/Taf0711/splice/commit/01e620997da0b212010f71461746212c3a5c41e7))
* **tui:** confirm agent design approval ([6453fec](https://github.com/Taf0711/splice/commit/6453fec8d420e4d4b2daf0e90938070dc650199f))
* **tui:** contain ask user answers ([d9d793e](https://github.com/Taf0711/splice/commit/d9d793e64e89bdf2813701b1d2a994c64e8d5d21))
* **tui:** correct action ownership, cancellation, persistence, and replay ([986b41d](https://github.com/Taf0711/splice/commit/986b41d43afdbfd255ff66d72511240c5d0368bd))
* **tui:** decisions ledger survives quiet verbosity (review probe) ([cc2ca38](https://github.com/Taf0711/splice/commit/cc2ca38c739f65c89a7ad0d284e6305f5eb1ae13))
* **tui:** dispatch advertised receipt and lifecycle-card action keys ([fda17a4](https://github.com/Taf0711/splice/commit/fda17a4262a2ceaa813609d4fb114370de549eeb))
* **tui:** drop the 'status' word from the status line lead ([a02d983](https://github.com/Taf0711/splice/commit/a02d98325cb0997f4482db9f678cfb4b6b4d368d))
* **tui:** drop the slash palette toward the vertical middle ([2b9c8c4](https://github.com/Taf0711/splice/commit/2b9c8c448ca3917dc86418b0d72672985f019fcd))
* **tui:** Enter opens pipeline stage picker, Right advances to Safety ([fa8166b](https://github.com/Taf0711/splice/commit/fa8166be92752b734e0aeebdfc477835e5f38347))
* **tui:** evidence band hides while the detail pane owns the body (review probe) ([45c2a08](https://github.com/Taf0711/splice/commit/45c2a08881652a21e058ba3e40d2ee9eca749a6f))
* **tui:** finish incomplete pipeline stages ([ee19946](https://github.com/Taf0711/splice/commit/ee19946802a3bea742af15ceab3dcbd1e8e54261))
* **tui:** fold table covers the /model picker glyph set (frame j3ZQBu cell 3) ([3ea5e3f](https://github.com/Taf0711/splice/commit/3ea5e3f03a9158407fea9d0a222f2d5c8cd79ab7))
* **tui:** give the TUI catalog its provider profile ([2c3089b](https://github.com/Taf0711/splice/commit/2c3089b76b62ee73256b79cc17b64dc8c255404b))
* **tui:** handoff keys dispatch on real terminal key shapes (GAP-F, DoD 28) ([a714619](https://github.com/Taf0711/splice/commit/a714619810cef6d2a400ac42c9380087121859f8))
* **tui:** keep a critique that failed to persist ([887c97b](https://github.com/Taf0711/splice/commit/887c97b5e8bd11e3b30221031e0464b04eec8f39))
* **tui:** keep a crystallized plan when the critic fails ([c1f3b78](https://github.com/Taf0711/splice/commit/c1f3b782fae3cec53a434742a204c83684b031c3))
* **tui:** keep long sessions off the per-frame re-render path ([03fba62](https://github.com/Taf0711/splice/commit/03fba62893e812af470ae9724cdd36d099d86d91))
* **tui:** keep the session current when a plan run fails ([bcdc8fb](https://github.com/Taf0711/splice/commit/bcdc8fbd8b9078c1757643a5f9a8d03228d64437))
* **tui:** keep the sidebar during the palette, and stop doubling /exec prompts ([877365a](https://github.com/Taf0711/splice/commit/877365a2c5c10cacce3c3aa2dc1205854b59376c))
* **tui:** launch cockpit renders from the top of the body ([a381626](https://github.com/Taf0711/splice/commit/a381626e92a05b55c67284bb7dea23d4a86ceca2))
* **tui:** lifecycle cards draw exactly one border (double-box bug) ([35ce459](https://github.com/Taf0711/splice/commit/35ce45907495883ba747d421119d90b65c051aa9))
* **tui:** offer the plans already in this directory on launch ([77894eb](https://github.com/Taf0711/splice/commit/77894ebb4ecff1d51b0731f7839f92c7569f42be))
* **tui:** pad every remaining progress counter to a stable width ([3605f26](https://github.com/Taf0711/splice/commit/3605f26340ce5d8e8863f5c0bf33ce2a560a7da1))
* **tui:** persist attributed usage for non-pipeline runs ([d8e7574](https://github.com/Taf0711/splice/commit/d8e7574d85efb92ed0a6dc8caaba9acd81b70021))
* **tui:** pipeline picker detail line shows the model name ([96131d7](https://github.com/Taf0711/splice/commit/96131d74f2557ad9ada58bb3c274106eb1ae43c5))
* **tui:** pipeline picker shows discovered models, not just the catalog ([15e9a9b](https://github.com/Taf0711/splice/commit/15e9a9be56ff1a53e59a2bbf820bfa43f0c1e86a))
* **tui:** pipeline picker shows selected model detail line ([b3b9872](https://github.com/Taf0711/splice/commit/b3b987297e24ed3d5e02cabb955377a3ac92dfad))
* **tui:** refresh saved providers after sign-in ([1fc8b05](https://github.com/Taf0711/splice/commit/1fc8b0585dd3c5343a2f246038273bae1f22201a))
* **tui:** refresh stage model picker inventory ([9f8324a](https://github.com/Taf0711/splice/commit/9f8324a66a0520d034a2f835d5040063a9b10515))
* **tui:** register the presentation callback policy and plumb snapshots ([5e8bbce](https://github.com/Taf0711/splice/commit/5e8bbce1837ad6988a209903a7722442f23fc4c5))
* **tui:** render readable output on terminals without UTF-8 ([e506376](https://github.com/Taf0711/splice/commit/e506376d8cc0c6cd925eee2d299e6de010aabc5d))
* **tui:** render the slash palette whole and centered ([10e9b67](https://github.com/Taf0711/splice/commit/10e9b67f9e459992150c4babd86236d5e558fce9))
* **tui:** reset run-bound interaction state on session switch (stabilization F2) ([af83adb](https://github.com/Taf0711/splice/commit/af83adbb2f057338e289045753bb2c2b254dba5e))
* **tui:** restore reasoning and transcript output ([af29d48](https://github.com/Taf0711/splice/commit/af29d48effd973308f107f97d05f0a27590b56ff))
* **tui:** return the memories a search actually matches ([adae5a9](https://github.com/Taf0711/splice/commit/adae5a976b3e8efebd14e7c9b9200b43569db5fc))
* **tui:** route both run paths through one runtime wiring seam ([31b3f78](https://github.com/Taf0711/splice/commit/31b3f78a636dc47f14048a3452fb1820572c8f65))
* **tui:** setup pipeline picker shows count, scroll indicator, current mark ([a1676dd](https://github.com/Taf0711/splice/commit/a1676dd0dfde862fd0280bbbbb0ce2ea2b3d9b36))
* **tui:** setup wizard per-stage model picker uses search and filtered list ([#3](https://github.com/Taf0711/splice/issues/3)) ([da9f47a](https://github.com/Taf0711/splice/commit/da9f47a8310fe8185a6b118d30fe2ff528b221f3))
* **tui:** show a recap only when it says something new ([97fa65f](https://github.com/Taf0711/splice/commit/97fa65f7cc95d211d404f7f9a988dddb7fbaf7f4))
* **tui:** show plan execution and ask before it acts ([ffa277b](https://github.com/Taf0711/splice/commit/ffa277b42a8847dcdfc29e9072a750d8beeb6931))
* **tui:** slash palette renders with the cockpit and keeps the prompt row live ([c9c4c3c](https://github.com/Taf0711/splice/commit/c9c4c3c9d804b38aeaf7cc82d80e77c266e30664))
* **tui:** slash palette stays visible on tall terminals ([c62d381](https://github.com/Taf0711/splice/commit/c62d381659ad1b093714106dba0e5a5b4ab9f266))
* **tui:** slash palette stays visible on tall terminals ([0a9b968](https://github.com/Taf0711/splice/commit/0a9b968087d5fd21d0bb4d7ab1afc713dda63f50))
* **tui:** stabilize sidebar layout and scope swarm state to each run ([935e247](https://github.com/Taf0711/splice/commit/935e247f42578394a7dc21ed2a966e18f13c4e97))
* **tui:** stop the launch scan from stealing typed input ([edf68a4](https://github.com/Taf0711/splice/commit/edf68a420d6e0f61eb33c456994ad187de61ed9e))
* **tui:** stop tool output from driving the terminal ([9646d20](https://github.com/Taf0711/splice/commit/9646d206ae0b7405a72528c155f650aabb63cfa6))
* **tui:** stream crystallize output and record its cost ([48778b9](https://github.com/Taf0711/splice/commit/48778b9032aa3a6311a7745181b174318e437675))
* **update:** correct npm package name to @taf0711/splice (S0) ([#5](https://github.com/Taf0711/splice/issues/5)) ([37e7c62](https://github.com/Taf0711/splice/commit/37e7c629a36f07e70d2cee36a2d86be73629ad9b))
* **update:** stop canceled notices before cache writes ([5276f95](https://github.com/Taf0711/splice/commit/5276f957af81217e61fcabf36d191feaf9db8090))
* **usage:** price a web search by the engine that ran it ([2e8bd63](https://github.com/Taf0711/splice/commit/2e8bd635fc455f4c7aca491ed0b12bf3f8c310d4))
* **usage:** report usage by stage and model ([2725481](https://github.com/Taf0711/splice/commit/2725481e2d57f88c73a8524a2c687048c5f6aeb3))
* **usage:** show fewer cost digits and use one formatter ([f563ba6](https://github.com/Taf0711/splice/commit/f563ba6ef0a16e0afeb65a6e5d4ea131ed34fa05))
* **usage:** show which stage and model spent the tokens ([aaaf33f](https://github.com/Taf0711/splice/commit/aaaf33f9d1d58249a10e44eb676534aa0fd42e0c))
* **worktrees:** remove merged worktrees ([871dc41](https://github.com/Taf0711/splice/commit/871dc418aec0358e052a7684864ad4034f38f511))


### Performance Improvements

* **exec:** drop the unread context window from the pipeline run options ([e858f58](https://github.com/Taf0711/splice/commit/e858f5823ee9729de00fe40b03175e6b364e10f3))
* **splice:** batched, memoized freshness gate for cognition ([43a7766](https://github.com/Taf0711/splice/commit/43a776628f471c50ae019773caca41b6083fd359))
* **tui:** bake settled transcript heights into descriptors (stabilization F6 follow-up, §13) ([27bf028](https://github.com/Taf0711/splice/commit/27bf028057147c3a30138c09100383c21c27ec26))
* **tui:** launch sidebar modules stop reading the session store per frame ([0cae3d3](https://github.com/Taf0711/splice/commit/0cae3d310d167a6ec1308861fcc0f3a101779154))
* **tui:** move the resume session scan off the UI loop (F1) ([332c1af](https://github.com/Taf0711/splice/commit/332c1afdbb8c00f5e313d61ec25493bd572386fd))
* **tui:** stop re-measuring the transcript on every wheel tick ([c63edd0](https://github.com/Taf0711/splice/commit/c63edd04ca70d31dba260e305dd1748a991e10b9))


### Miscellaneous Chores

* hold the next release at 0.1.4 ([247a148](https://github.com/Taf0711/splice/commit/247a148ebf537c1199a07abfb97d9fe75bc9669b))
* release as 0.1.4 ([bc2e3a5](https://github.com/Taf0711/splice/commit/bc2e3a55e563aada908a30ff748e9876448f406f))
* **release:** cut 0.1.3 ([ad495db](https://github.com/Taf0711/splice/commit/ad495db473dd21af4f65815903fb0ba1a28d7811))
* **release:** cut 0.1.4 ([d3b5e44](https://github.com/Taf0711/splice/commit/d3b5e44d4687b7dbfa9908ad5c21db8b8a1bc484))

## [0.2.0](https://github.com/Taf0711/splice/compare/v0.1.4...v0.2.0) (2026-08-10)


### Features

* **design:** the design agent can now request to crystallize the plan or approve it for execution. Revisions keep the current plan and critique context. The approve transition stays available after a resume when no critique requires a fix. Splice records who requested each transition. ([3a1421f](https://github.com/Taf0711/splice/commit/3a1421f8479a8c6cb4681518690fc05d920702d4))


### Bug Fixes

* **memory:** only `code_writer` and `test_generator` query memory now. Other stages skip the sidecar search, so they do not pull in results that do not apply. ([4f63d05](https://github.com/Taf0711/splice/commit/4f63d05a2ba59a1241ca32daa5cd31a5b5416b8f))
* **reasoning:** a request for `xhigh` or `max` could give less thinking than `high`, including the least. An unsupported tier now steps to the nearest supported tier. Supported tiers now come from the models.dev snapshot when available. ([1fe01ea](https://github.com/Taf0711/splice/commit/1fe01eab3990ca3075eb0c2c984554f3ae535f9e))
* **tui:** `/stages` now refreshes model lists from each saved provider. The escalation and stage pickers previously showed only the short static catalog. ([9f8324a](https://github.com/Taf0711/splice/commit/9f8324a66a0520d034a2f835d5040063a9b10515))
* **tui:** the sidebar layout stays stable during brief content changes. The composer width follows the chat column, and swarm rows stay with their run. ([935e247](https://github.com/Taf0711/splice/commit/935e247f42578394a7dc21ed2a966e18f13c4e97))
* **update:** stop canceled notices before cache writes ([5276f95](https://github.com/Taf0711/splice/commit/5276f957af81217e61fcabf36d191feaf9db8090))

## [0.1.4](https://github.com/Taf0711/splice/compare/v0.1.3...v0.1.4) (2026-08-06)

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
