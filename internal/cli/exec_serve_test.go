package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/web"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// surfaceURL returns the bootstrap URL that startExecSurface printed.
func surfaceURL(t *testing.T, stderr string) string {
	t.Helper()
	const prefix = "[splice] surface: "
	idx := strings.Index(stderr, prefix)
	if idx < 0 {
		t.Fatalf("stderr has no surface line: %q", stderr)
	}
	line := stderr[idx+len(prefix):]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	return line
}

func TestExecSurfaceStreamsFramesToSession(t *testing.T) {
	var stderr bytes.Buffer
	surface, err := startExecSurface(context.Background(), &stderr)
	if err != nil {
		t.Fatalf("startExecSurface: %v", err)
	}
	t.Cleanup(surface.stop)
	boot := surfaceURL(t, stderr.String())
	base := boot[:strings.Index(boot, "?token=")] // origin and the prefix path

	// Redeem the bootstrap URL to get the session cookie.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(boot)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	resp.Body.Close()
	if len(resp.Cookies()) == 0 {
		t.Fatal("bootstrap set no cookie")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"events", nil)
	req.AddCookie(resp.Cookies()[0])
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	if _, err := reader.ReadString('\n'); err != nil { // ": connected"
		t.Fatalf("read preamble: %v", err)
	}

	// The hub flushes on a 50 ms tick, so publish until the frame arrives.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				surface.sink()(agent.StageEvent{Name: "code_writer", Iteration: 1, Status: "completed", Detail: "wrote main.go", Progress: 100, ChangedFiles: []string{"main.go"}})
			}
		}
	}()

	var frame web.StageFrame
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("no frame arrived")
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatalf("decode frame %q: %v", line, err)
		}
		break
	}
	if frame.Kind != "stage" || frame.Name != "code_writer" || frame.Iteration != 1 || frame.Status != "completed" ||
		frame.Detail != "wrote main.go" || frame.Progress != 100 || len(frame.ChangedFiles) != 1 || frame.ChangedFiles[0] != "main.go" {
		t.Fatalf("frame = %+v", frame)
	}
}

func TestExecSurfaceSinkIsNilWithoutServe(t *testing.T) {
	var surface *execSurface // --serve off
	if surface.sink() != nil {
		t.Fatal("sink must be nil when --serve is off")
	}
	surface.stop() // nil-safe
}

func TestRunExecServePrintsURLOnStderrAndStopsAfterRun(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--serve", "add a Hello function"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return newExecStageAwareProvider(execStageProviderOptions{}), nil
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "surface") {
		t.Fatalf("surface URL leaked to stdout: %s", stdout.String())
	}
	boot := surfaceURL(t, stderr.String())
	if !strings.HasPrefix(boot, "http://127.0.0.1:") {
		t.Fatalf("surface URL = %q, want loopback origin", boot)
	}

	// The run has returned, so the server must be gone.
	client := &http.Client{Timeout: 2 * time.Second}
	parsed, err := url.Parse(boot)
	if err != nil {
		t.Fatalf("parse surface URL: %v", err)
	}
	if resp, err := client.Get(parsed.Scheme + "://" + parsed.Host + "/healthz"); err == nil {
		resp.Body.Close()
		t.Fatalf("server still answers after the run (status %d)", resp.StatusCode)
	}
}

func TestRunExecServeRefusesUseSpec(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--serve", "--use-spec", "add a Hello function"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	})
	if exitCode != exitUsage {
		t.Fatalf("exitCode = %d, want %d; stderr=%s", exitCode, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--serve cannot be combined with --use-spec.") {
		t.Fatalf("stderr = %q, want the --serve and --use-spec conflict", stderr.String())
	}
	if strings.Contains(stderr.String(), "surface:") {
		t.Fatal("refused run must not start a surface")
	}
}

func TestRunExecServeRefusesLoop(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--serve", "--loop", "add a Hello function"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
	})
	if exitCode != exitUsage {
		t.Fatalf("exitCode = %d, want %d; stderr=%s", exitCode, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--serve cannot be combined with --loop.") {
		t.Fatalf("stderr = %q, want the --serve and --loop conflict", stderr.String())
	}
	if strings.Contains(stderr.String(), "[splice] surface: ") {
		t.Fatal("refused run must not start a surface")
	}
}

// TestExecSurfaceFeedsThePipelineViewer pins the exec seam: the run's
// presentation callback still reaches the stream-json writer, and the
// pipeline viewer receives the snapshot and the final stage records.
func TestExecSurfaceFeedsThePipelineViewer(t *testing.T) {
	var stderr bytes.Buffer
	surface, err := startExecSurface(context.Background(), &stderr)
	if err != nil {
		t.Fatalf("startExecSurface: %v", err)
	}
	t.Cleanup(surface.stop)
	surface.configurePipeline(splicerun.TopologySources{UserConfigDir: t.TempDir()}, "", nil)

	var writerSaw int
	observe := surface.presentation(func(presentation.State) { writerSaw++ })
	state, err := presentation.Apply(presentation.State{}, presentation.PlanEvent{StageNames: []string{"code_writer"}})
	if err != nil {
		t.Fatal(err)
	}
	observe(state)
	if writerSaw != 1 {
		t.Fatalf("the writer saw %d snapshots, want 1", writerSaw)
	}
	result, _ := json.Marshal(schemas.PipelineResult{Status: "completed", Stages: []schemas.StageRecord{{Name: "code_writer", Status: schemas.StageCompleted, Iteration: 1}}})
	surface.finish(string(result), nil)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	landing, err := client.Get(surfaceURL(t, stderr.String()))
	if err != nil {
		t.Fatal(err)
	}
	landing.Body.Close()
	if !strings.HasSuffix(landing.Request.URL.Path, "/s/pipeline/") {
		t.Fatalf("the surface URL landed on %s, want the pipeline page", landing.Request.URL.Path)
	}
	timeline, _ := landing.Request.URL.Parse("timeline")
	resp, err := client.Get(timeline.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var data struct {
		Steps []struct {
			Caption string `json:"caption"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	// Step 0 is the plan, then one step per stage record.
	if len(data.Steps) != 2 || data.Steps[1].Caption != "code_writer · passed" {
		t.Fatalf("timeline = %+v", data.Steps)
	}

	var off *execSurface // --serve off: the writer callback passes through
	if off.presentation(nil) != nil {
		t.Fatal("presentation must return the writer callback unchanged without --serve")
	}
	off.finish(string(result), nil) // nil-safe
}

// TestPipelineViewSourcesShowTheDefaultForPlan pins that exec --serve --plan
// shows the topology that the run uses. A design plan runs the default
// topology, so the viewer must not show a trusted project topology.
func TestPipelineViewSourcesShowTheDefaultForPlan(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	writeExecCommandTopology(t, filepath.Join(root, ".splice", "pipeline.json"))
	for _, tc := range []struct {
		planPath, want string
	}{
		{"", "named"},
		{"plan.json", splicerun.DefaultTopologyName},
	} {
		topology, _, _, err := splicerun.ResolveTopology(pipelineViewSources(root, true, "", tc.planPath))
		if err != nil {
			t.Fatalf("plan %q: resolve: %v", tc.planPath, err)
		}
		if topology.Name != tc.want {
			t.Fatalf("plan %q: the viewer shows %q, want %q", tc.planPath, topology.Name, tc.want)
		}
	}
}

// sseTap is the stderr of an exec --serve run. When the surface line appears,
// it redeems the link and opens the event stream before the run goes on, so
// it sees every frame. lines closes when the stream ends.
type sseTap struct {
	t      *testing.T
	buf    bytes.Buffer
	opened bool
	lines  chan string
}

func (tap *sseTap) Write(p []byte) (int, error) {
	tap.buf.Write(p)
	if tap.opened || !strings.Contains(tap.buf.String(), "[splice] surface: ") || !strings.HasSuffix(tap.buf.String(), "\n") {
		return len(p), nil
	}
	tap.opened = true
	boot := surfaceURL(tap.t, tap.buf.String())
	base := boot[:strings.Index(boot, "?token=")]
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(boot)
	if err != nil || len(resp.Cookies()) == 0 {
		tap.t.Errorf("redeem: %v", err)
		close(tap.lines)
		return len(p), nil
	}
	resp.Body.Close()
	req, _ := http.NewRequest(http.MethodGet, base+"events", nil)
	req.AddCookie(resp.Cookies()[0])
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		tap.t.Errorf("events: %v", err)
		close(tap.lines)
		return len(p), nil
	}
	reader := bufio.NewReader(stream.Body)
	if _, err := reader.ReadString('\n'); err != nil { // ": connected"
		tap.t.Errorf("read preamble: %v", err)
	}
	go func() {
		defer close(tap.lines)
		defer stream.Body.Close()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: ") {
				tap.lines <- line
			}
		}
	}()
	return len(p), nil
}

// TestRunExecServeRedactsStoredKeyEndToEnd: the primary profile's key is in
// the credential store, and the code writer's stage summary holds that key.
// The browser stream gets the summary with the key redacted.
func TestRunExecServeRedactsStoredKeyEndToEnd(t *testing.T) {
	cfg := execResolvedConfig()
	cfg.Provider.APIKeyStored = true
	deps := appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return cfg, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return newExecStageAwareProvider(execStageProviderOptions{}), nil
		},
		keyStoreOpener: func() (config.APIKeyGetter, error) {
			return &fakeKeyStore{keys: map[string]string{"echo": storedKey}}, nil
		},
	}
	tap := &sseTap{t: t, lines: make(chan string, 1024)}
	var stdout bytes.Buffer
	if code := runWithDeps([]string{"exec", "--serve", "add a Hello function that returns " + storedKey}, &stdout, tap, deps); code != exitSuccess {
		t.Fatalf("exit %d; stdout=%s stderr=%s", code, stdout.String(), tap.buf.String())
	}
	if !tap.opened {
		t.Fatalf("no surface line on stderr: %s", tap.buf.String())
	}
	var frames []string
	timeout := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case line, ok := <-tap.lines:
			if !ok {
				done = true
				break
			}
			frames = append(frames, line)
		case <-timeout:
			t.Fatal("the event stream did not end after the run")
		}
	}
	all := strings.Join(frames, "")
	if strings.Contains(all, storedKey) {
		t.Fatalf("the stored key reached the browser stream:\n%s", all)
	}
	if !strings.Contains(all, `"name":"code_writer"`) || !strings.Contains(all, "[REDACTED]") {
		t.Fatalf("no redacted code_writer summary on the stream:\n%s", all)
	}
}
