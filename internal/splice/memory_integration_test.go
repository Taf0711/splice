package splice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
)

// repoRoot returns the splice repository root by inspecting go env GOMOD.
func repoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "env", "GOMOD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	modPath := strings.TrimSpace(string(out))
	if modPath == "" {
		t.Fatal("go env GOMOD returned empty path")
	}
	return filepath.Dir(modPath)
}

// TestRealMemdSidecarMemoryRetrieval builds the real splice-memd binary,
// spawns it, upserts an observation, and verifies that the orchestrator's
// memory retrieval path injects the typed memory into a stage's
// HarnessStageInput and that selectMemory maps it to SelectedMemory entries.
func TestRealMemdSidecarMemoryRetrieval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Build splice-memd from the memd/ module into a temp dir.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "splice-memd")

	// memd/ is a separate Go module; build from its directory.
	memdDir := filepath.Join(repoRoot(t), "memd")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, ".")
	buildCmd.Dir = memdDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Skipf("could not build splice-memd: %v\n%s", err, string(out))
	}

	// 2. Set up a temp socket path. Create a temp file, close it, remove it
	// so the path is free for the daemon to bind.
	sockF, err := os.CreateTemp("", "memd-integration-*.sock")
	if err != nil {
		t.Fatalf("create socket temp: %v", err)
	}
	socketPath := sockF.Name()
	sockF.Close()
	os.Remove(socketPath)
	t.Cleanup(func() { os.Remove(socketPath) })

	// Set SPLICE_MEMD_SOCKET / SPLICE_MEMD_DB so the client and the test-owned
	// daemon process agree on where to bind and store. The DB lives in a temp
	// dir so the daemon creates its data directory there, never under the
	// socket's parent. (SPLICE_MEMD_BIN is deliberately NOT set: the client no
	// longer spawns the daemon here; the test starts its own process below and
	// must be its owner.)
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	t.Setenv("SPLICE_MEMD_SOCKET", socketPath)
	t.Setenv("SPLICE_MEMD_DB", dbPath)

	// 3. Start the daemon directly as a test-owned child process. Do NOT go
	// through memd.Resolve: it would spawn (or attach to) a daemon the test
	// does not own and cannot stop, leaking splice-memd processes across runs.
	// Cleanup signals SIGTERM (the sidecar shuts down gracefully on it), waits
	// bounded, and only force-kills a daemon that refuses to exit.
	daemon := exec.CommandContext(ctx, binPath, "--serve")
	daemon.Env = append(os.Environ(), "SPLICE_MEMD_SOCKET="+socketPath, "SPLICE_MEMD_DB="+dbPath)
	if err := daemon.Start(); err != nil {
		t.Fatalf("start splice-memd: %v", err)
	}
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemon.Wait() }()
	t.Cleanup(func() {
		if daemon.Process != nil {
			daemon.Process.Signal(syscall.SIGTERM) //nolint:errcheck
		}
		select {
		case err := <-daemonDone:
			_ = err
		case <-time.After(5 * time.Second):
			if daemon.Process != nil {
				daemon.Process.Kill() //nolint:errcheck
			}
			select {
			case err := <-daemonDone:
				_ = err
			case <-time.After(5 * time.Second):
				t.Error("splice-memd daemon did not exit after SIGKILL")
			}
		}
	})

	client := memd.NewClient(socketPath)
	healthDeadline := time.Now().Add(10 * time.Second)
	var healthErr error
	for {
		if healthErr = client.Health(ctx); healthErr == nil {
			break
		}
		if time.Now().After(healthDeadline) {
			t.Fatalf("splice-memd did not become healthy: %v", healthErr)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for splice-memd health: %v", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}

	// 4. Upsert a test observation through the real HTTP handler + SQLite store.
	workDir := t.TempDir()
	testContent := "Integration test: Prefer this implementation approach."
	upserted, err := client.Upsert(ctx, schemas.MemoryObservation{
		OwnerAgent:  "test_agent",
		Visibility:  "shareable",
		Scope:       "project",
		MemoryType:  "decision",
		Title:       "Integration test observation",
		Content:     testContent,
		ProjectPath: &workDir,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if upserted.ID == 0 {
		t.Fatal("expected non-zero observation ID after upsert")
	}

	// 5. Subtest: runPass with a capturing stage — verify MemoryBundle injection.
	t.Run("memory bundle injected via capturing stage", func(t *testing.T) {
		intent := "Prefer this implementation approach"
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: intent,
			Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		}
		var inputs []schemas.HarnessStageInput
		_, _, completed, err := runPass(ctx, "real-memd-test", 1, plan, stageRegistry{
			"code_writer": &capturingStage{inputs: &inputs},
		}, runFakeProvider{}, PipelineConfigFromAgentOptions(agent.Options{}), workDir, nil, time.Time{}, nil, client)
		if err != nil {
			t.Fatalf("runPass: %v", err)
		}
		if !completed {
			t.Fatal("expected completed pass")
		}
		if len(inputs) != 1 || inputs[0].MemoryBundle == nil {
			t.Fatalf("expected captured input with memory bundle, got %#v", inputs)
		}
		bundle := inputs[0].MemoryBundle
		if bundle.RequestingAgent != "code_writer" {
			t.Fatalf("requesting agent = %q, want code_writer", bundle.RequestingAgent)
		}
		if len(bundle.Observations) < 1 {
			t.Fatalf("expected at least 1 observation in bundle, got %d", len(bundle.Observations))
		}
		// Look up the Content that should match the FTS5 query derived from the intent.
		var found bool
		for _, obs := range bundle.Observations {
			if obs.Content == testContent {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("upserted observation not found in retrieved bundle (want content=%q)", testContent)
		}
	})

	// 6. Subtest: verify that selectMemory maps the MemoryBundle into
	// SelectedMemory entries that flow into the CodeWriter input.
	t.Run("selected memory flows into code writer", func(t *testing.T) {
		intent := "Prefer this implementation approach"
		plan := schemas.ExecutionPlan{
			Tier:          schemas.TierLight,
			RequestIntent: intent,
			Stages:        []schemas.ExecutionStage{{Name: "code_writer"}},
		}
		cwProvider := &captureRequestProvider{}
		fakeRunner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
			return ToolResult{OK: true, Output: ""}, nil
		})

		_, _, completed, err := runPass(ctx, "real-memd-select", 1, plan, stageRegistry{
			"code_writer": stages.CodeWriter{},
		}, cwProvider, PipelineConfigFromAgentOptions(agent.Options{}), workDir, fakeRunner, time.Time{}, nil, client)
		if err != nil {
			t.Fatalf("runPass: %v", err)
		}
		if !completed {
			t.Fatal("expected completed pass")
		}

		// The CodeWriter serializes its input as a JSON message to the provider.
		// Messages[1] is the user message containing the CodeWriterInput JSON.
		if len(cwProvider.request.Messages) < 2 {
			t.Fatalf("expected user message in captured payload, got %d messages",
				len(cwProvider.request.Messages))
		}
		payload := cwProvider.request.Messages[1].Content
		var cwInput schemas.CodeWriterInput
		if err := json.Unmarshal([]byte(payload), &cwInput); err != nil {
			t.Fatalf("unmarshal CodeWriterInput: %v", err)
		}
		if len(cwInput.Memory) < 1 {
			t.Fatalf("expected at least 1 SelectedMemory entry in CodeWriterInput, got %d",
				len(cwInput.Memory))
		}
		var found bool
		for _, m := range cwInput.Memory {
			if strings.Contains(m.Content, "Prefer this implementation approach") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("selected memory does not contain upserted observation content; got %+v",
				cwInput.Memory)
		}
	})
}
