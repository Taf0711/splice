// Package e2e drives the built splice binary end to end against a scripted
// fake provider. The tests are opt-in: set SPLICE_E2E=1 (or run `make e2e`).
// Without it, TestMain builds nothing and every test skips.
//
// # Harness API
//
// TestMain builds the binary once (version 0.3.0) and hashes the real user
// config.json, trust.json, stage-models.json, credentials.enc, and every file
// under pipelines/ before and after the suite. A change fails the suite. Each
// scenario also checks the hashes in its cleanup, because a -timeout panic
// stops the process before the final check.
//
// Start each scenario with NewScenario(t). It skips without SPLICE_E2E=1 and
// on Windows. When the native stage sandbox (sandbox-exec or bwrap) is not
// available, it fails on Linux CI (CI is set) and skips elsewhere. It
// creates a short temp root with a fake HOME and XDG dirs, a fake provider,
// a committed Go fixture workspace, and a user config.json whose one profile
// ("fake", openai-compatible) points at the fake provider:
//
//	s := NewScenario(t)
//	s.Provider.Script(StageCodeWriter, SubmitCode(File("main.go", src)))
//	r := s.Run("exec", "-o", "stream-json", "add a Hello function in main.go")
//	r.WantExit(t, 0)
//	final := r.Final(t) // the decoded final.text pipeline result
//
// Scenario fields: Root (temp root), Home, ConfigHome (XDG_CONFIG_HOME),
// ConfigDir (ConfigHome/splice), Workspace (the fixture repo), Provider,
// Env (extra child env values for this scenario), Timeout (per binary run,
// 120 s default).
//
// Scenario methods:
//   - Run(args...) and RunIn(dir, args...) run the binary and return a Result
//     (Stdout, Stderr, ExitCode, Events). Stdin is empty.
//   - WriteUserConfig(cfg) replaces config.json; DefaultUserConfig() is the
//     seeded value. WriteStageModels(v) writes stage-models.json.
//   - WriteFile(rel, content) writes a file under Workspace; Git(args...)
//     runs git in Workspace with the scenario env and core.fsmonitor=false.
//   - Marker(name) returns an absolute marker path under Root/markers;
//     MarkerExists(name) reports whether something created it.
//   - ChildEnv() returns the allowlisted child environment.
//
// Result helpers: EventsOfType(type), Reasoning() (all reasoning deltas
// joined), Final(t) (the decoded pipeline result), WantExit(t, code).
//
// Fake provider (FakeProvider): an OpenAI-compatible streaming chat API at
// Provider.BaseURL ("<server>/v1"). It keys each chat request by
// tools[0].function.name (StageCodeWriter, StageTestGenerator,
// StageStepBack, ...) or StagePrompt when the request has no tools. Script
// queues replies per key, in order. Reply builders: SubmitCode, SubmitTests,
// ToolCallReply, Answer, HTTPError. Requests() returns every recorded request.
// Any request with an empty queue, an unknown path, or an unexpected method
// fails the test and gets an HTTP 400 answer (not retried). The update check URL
// (SPLICE_UPDATE_RELEASE_URL) also points at the fake, so an update check
// fails the test too.
//
// Child environment: built from an allowlist (PATH, TMPDIR, LANG, TERM,
// GOCACHE, GOPATH, GOFLAGS) plus fixed isolation values and Scenario.Env.
// It never copies os.Environ, so SPLICE_TRUST_WORKSPACE and SPLICE_PROVIDER
// never leak from the developer shell.
//
// To add a scenario group, add one <group>_test.go file in this package.
package e2e
