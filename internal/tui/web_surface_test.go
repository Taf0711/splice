package tui

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/oauth"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// newWebTestModel returns a model whose browser opener is a recorder, and whose
// terminal check returns the given value.
func newWebTestModel(t *testing.T, interactive bool) (model, *[]string) {
	t.Helper()
	m := newModel(context.Background(), Options{})
	var opened []string
	m.surface.openBrowser = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	m.surface.interactive = func() bool { return interactive }
	t.Cleanup(m.surface.stop)
	return m, &opened
}

// noSecrets returns a run sink that loads no credentials. Tests of plain event
// flow use it, and they do not depend on the store.
func noSecrets(m model) func(agent.StageEvent) {
	return m.surface.sink(func() ([]string, error) { return nil, nil }, nil)
}

// lastSystemRow returns the text of the last system row in the transcript.
func lastSystemRow(t *testing.T, m model) string {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == rowSystem {
			return m.transcript[i].text
		}
	}
	t.Fatal("transcript has no system row")
	return ""
}

func TestWebCommandIsRegistered(t *testing.T) {
	command := parseCommand("/web")
	if command.kind != commandWeb {
		t.Fatalf("/web parsed as kind %v, want commandWeb", command.kind)
	}
}

func TestWebCommandHeadlessShowsURLAndNeverOpensBrowser(t *testing.T) {
	m, opened := newWebTestModel(t, false)
	m = m.openWebSurface()

	row := lastSystemRow(t, m)
	if !strings.Contains(row, "http://127.0.0.1:") || !strings.Contains(row, "/?token=") {
		t.Fatalf("system row lacks the one-time URL: %q", row)
	}
	if len(*opened) != 0 {
		t.Fatalf("browser opened in a headless run: %v", *opened)
	}
}

func TestWebCommandOpensBrowserOnlyWhenInteractive(t *testing.T) {
	m, opened := newWebTestModel(t, true)
	m = m.openWebSurface()

	row := lastSystemRow(t, m)
	if len(*opened) != 1 {
		t.Fatalf("browser opened %d times, want 1", len(*opened))
	}
	if !strings.Contains(row, (*opened)[0]) {
		t.Fatalf("opened URL %q is not the one in the row %q", (*opened)[0], row)
	}
}

func TestWebCommandAlwaysShowsURLWhenBrowserFails(t *testing.T) {
	m, _ := newWebTestModel(t, true)
	m.surface.openBrowser = func(string) error { return errors.New("no browser") }
	m = m.openWebSurface()

	if row := lastSystemRow(t, m); !strings.Contains(row, "Open the link above") {
		t.Fatalf("expected a fallback row after a failed open, got %q", row)
	}
	if m.surface.running() == nil {
		t.Fatal("surface should keep running when the browser fails to open")
	}
}

func TestWebCommandReissuesLinkAndServesTheSession(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	m = m.openWebSurface()
	first := lastSystemRow(t, m)
	m = m.openWebSurface()
	second := lastSystemRow(t, m)
	if first == second {
		t.Fatal("second /web returned the same link")
	}
	if m.surface.running() == nil {
		t.Fatal("surface should be running after /web")
	}
}

func TestQuitStopsWebSurface(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	m = m.openWebSurface()
	srv := m.surface.running()
	if srv == nil {
		t.Fatal("surface not running after /web")
	}
	m.quit()
	select {
	case <-srv.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("surface still running after quit")
	}
	if m.surface.running() != nil {
		t.Fatal("surface still reports running after quit")
	}
}

func TestWebSurfaceRestartsAfterIdleStop(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	m = m.openWebSurface()
	// The server stops on its own after the idle timeout. Simulate that stop.
	m.surface.running().Stop()
	if m.surface.running() != nil {
		t.Fatal("running() reports a stopped server as live")
	}
	m = m.openWebSurface()
	if m.surface.running() == nil {
		t.Fatal("/web did not start a new server after the idle stop")
	}
}

func TestSessionChangeRotatesWebLinks(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	// No server runs yet, so a session change has nothing to rotate.
	before := len(m.transcript)
	m = m.rotateWebSession()
	if len(m.transcript) != before {
		t.Fatal("session change added a row with no server running")
	}

	m = m.openWebSurface()
	old := lastSystemRow(t, m)
	m = m.rotateWebSession()
	row := lastSystemRow(t, m)
	if !strings.Contains(row, "Old links no longer work") || !strings.Contains(row, "/?token=") {
		t.Fatalf("session change row = %q", row)
	}
	if strings.Contains(row, strings.TrimPrefix(old, "Web surface: ")) {
		t.Fatal("session change repeated the old link")
	}
}

func TestPipelineSinkIsSafeWithAndWithoutServer(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	sink := noSecrets(m)
	// No server runs yet, so the sink must do nothing and must not panic.
	sink(agent.StageEvent{Name: "code_writer", Status: "running"})

	m = m.openWebSurface()
	sink(agent.StageEvent{Name: "code_writer", Status: "running"})
	sink(agent.StageEvent{Kind: "run", Status: "completed"})
	m.surface.stop()
	sink(agent.StageEvent{Name: "code_writer", Status: "completed"}) // stopped again: no panic
}

func TestDesignRunOptionsCarryEventSink(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}}}
	m := newDesignModeTestModel(t.TempDir(), provider, testSessionStore(t))
	m.designMode = true
	m.surface.interactive = func() bool { return false }
	t.Cleanup(m.surface.stop)
	var captured agent.Options
	m.captureRunOptions = func(options agent.Options) { captured = options }
	m.input.SetValue("research this change")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected design prompt to start an agent run")
	}
	_, _ = updated.(model).Update(execCmd(cmd))
	if captured.EventSink == nil {
		t.Fatal("design run has no EventSink")
	}
}

// streamFrames reads SSE data lines from the surface until it finds one that
// contains want, or the stream ends. It returns every data line it read.
func streamFrames(t *testing.T, origin, cookie string, lines chan<- string, done <-chan struct{}) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, origin+"events", nil)
	req.Header.Set("Cookie", cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("events: %v", err)
		return
	}
	go func() {
		<-done
		resp.Body.Close()
	}()
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			close(lines)
			return
		}
		if strings.HasPrefix(line, "data: ") {
			lines <- line
		}
	}
}

// sessionCookie redeems a one-time URL and returns the Cookie header value.
func sessionCookie(t *testing.T, bootstrap string) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(bootstrap)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	resp.Body.Close()
	if len(resp.Cookies()) == 0 {
		t.Fatal("redeem set no cookie")
	}
	return resp.Cookies()[0].Name + "=" + resp.Cookies()[0].Value
}

func TestRunStartedBeforeSessionChangeStaysOut(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	first, err := m.surface.open("session-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	oldSink := noSecrets(m) // a run started in session 1
	oldCookie := sessionCookie(t, first)
	origin := first[:strings.Index(first, "?token=")] // origin and the prefix path

	oldLines := make(chan string, 64)
	oldDone := make(chan struct{})
	t.Cleanup(func() { close(oldDone) })
	go streamFrames(t, origin, oldCookie, oldLines, oldDone)
	time.Sleep(50 * time.Millisecond) // let the stream subscribe

	// Session change: the browser gets a new link. The old stream closes.
	next, err := m.surface.sessionChanged("session-2")
	if err != nil || next == "" {
		t.Fatalf("sessionChanged = (%q, %v)", next, err)
	}
	newCookie := sessionCookie(t, next)
	newBase := next[:strings.Index(next, "?token=")] // the rotated prefix
	newLines := make(chan string, 64)
	newDone := make(chan struct{})
	t.Cleanup(func() { close(newDone) })
	go streamFrames(t, newBase, newCookie, newLines, newDone)
	time.Sleep(50 * time.Millisecond)

	// The old run publishes after the change. Then a fresh run publishes a marker.
	oldSink(agent.StageEvent{Name: "old_stage", Status: "running", Detail: "old-run-after-change"}) // distinct key: latest-wins must not hide it
	noSecrets(m)(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "new-run-marker"})

	deadline := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-newLines:
			if !ok {
				t.Fatal("new stream closed before the marker")
			}
			if strings.Contains(line, "old-run-after-change") {
				t.Fatalf("old-session run reached the new session: %q", line)
			}
			if strings.Contains(line, "new-run-marker") {
				return
			}
		case <-deadline:
			t.Fatal("marker never reached the new session")
		}
	}
}

// lazyFixture is a model with a browser surface, a recorded credential loader,
// and a recorder for the rows the run sends to the UI.
type lazyFixture struct {
	m     model
	loads *int
	rows  *[]agentRowMsg
}

func newLazyFixture(t *testing.T, load func() ([]string, error)) lazyFixture {
	t.Helper()
	m, _ := newWebTestModel(t, false)
	var loads int
	var rows []agentRowMsg
	m.surface.loadSecrets = func(model) ([]string, error) {
		loads++
		return load()
	}
	m.runtimeMessageSink = func(msg tea.Msg) {
		if row, ok := msg.(agentRowMsg); ok {
			rows = append(rows, row)
		}
	}
	return lazyFixture{m: m, loads: &loads, rows: &rows}
}

func TestNoServerNeverReadsCredentials(t *testing.T) {
	fx := newLazyFixture(t, func() ([]string, error) { return []string{"zq7Lm2Vt9Rp4Xw8K"}, nil })
	var options agent.Options
	fx.m.applyRunEvents(&options, 1)
	for i := 0; i < 3; i++ {
		options.EventSink(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "zq7Lm2Vt9Rp4Xw8K"})
	}
	if *fx.loads != 0 {
		t.Fatalf("credentials read %d times with no browser surface running", *fx.loads)
	}
}

func TestBrokenStoreWithServerPausesRunOnce(t *testing.T) {
	fx := newLazyFixture(t, func() ([]string, error) {
		return nil, errors.New("keychain locked")
	})
	first, err := fx.m.surface.open("session-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cookie := sessionCookie(t, first)
	base := first[:strings.Index(first, "?token=")]
	lines := make(chan string, 64)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go streamFrames(t, base, cookie, lines, done)
	time.Sleep(50 * time.Millisecond)

	var options agent.Options
	fx.m.applyRunEvents(&options, 7)
	for i := 0; i < 3; i++ {
		options.EventSink(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "paused-detail"})
	}

	if *fx.loads != 1 {
		t.Fatalf("credentials read %d times, want once for the run", *fx.loads)
	}
	var pauses int
	for _, row := range *fx.rows {
		if strings.Contains(row.row.text, "Browser surface paused for this run") {
			pauses++
			if !strings.Contains(row.row.text, "keychain locked") || row.runID != 7 {
				t.Fatalf("pause row = %+v", row)
			}
		}
	}
	if pauses != 1 {
		t.Fatalf("pause rows = %d, want exactly 1", pauses)
	}
	select {
	case line := <-lines:
		if strings.Contains(line, "paused-detail") {
			t.Fatalf("frame published for a paused run: %q", line)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func TestRunSinkRedactsLoadedKeyInFrames(t *testing.T) {
	const key = "zq7Lm2Vt9Rp4Xw8K"
	fx := newLazyFixture(t, func() ([]string, error) { return []string{key}, nil })
	first, err := fx.m.surface.open("session-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cookie := sessionCookie(t, first)
	base := first[:strings.Index(first, "?token=")]
	lines := make(chan string, 64)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go streamFrames(t, base, cookie, lines, done)
	time.Sleep(50 * time.Millisecond)

	var options agent.Options
	fx.m.applyRunEvents(&options, 1)
	options.EventSink(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "calling with " + key})

	deadline := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before the frame")
			}
			if strings.Contains(line, key) {
				t.Fatalf("key reached a frame: %q", line)
			}
			if strings.Contains(line, "[REDACTED]") {
				return
			}
		case <-deadline:
			t.Fatal("no redacted frame arrived")
		}
	}
}

// TestSlowSecretLoadDoesNotBlockSessionChange: a credential load can read the
// keychain and refresh an OAuth token over the network. While it runs for a
// run event or a run snapshot, a session change must not wait for it.
func TestSlowSecretLoadDoesNotBlockSessionChange(t *testing.T) {
	for _, path := range []string{"event", "state"} {
		t.Run(path, func(t *testing.T) {
			m, _ := newWebTestModel(t, false)
			entered := make(chan struct{})
			release := make(chan struct{})
			m.surface.loadSecrets = func(model) ([]string, error) {
				close(entered)
				<-release
				return []string{"zq7Lm2Vt9Rp4Xw8K"}, nil
			}
			if _, err := m.surface.open("session-1"); err != nil {
				t.Fatalf("open: %v", err)
			}
			var options agent.Options
			m.applyRunEvents(&options, 1)
			fed := make(chan struct{})
			go func() {
				defer close(fed)
				if path == "event" {
					options.EventSink(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "slow"})
				} else {
					options.OnPresentationState(viewerRunState(t, "slow"))
				}
			}()
			<-entered
			changed := make(chan struct{})
			go func() {
				defer close(changed)
				_, _ = m.surface.sessionChanged("session-2")
			}()
			select {
			case <-changed:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("the session change waited for the credential load")
			}
			close(release)
			<-fed
		})
	}
}

// TestRunSinkRedactsNodeNameAndFilePaths: a loaded key in a node name or in
// a changed file path does not reach a frame.
func TestRunSinkRedactsNodeNameAndFilePaths(t *testing.T) {
	const key = "zq7Lm2Vt9Rp4Xw8K"
	fx := newLazyFixture(t, func() ([]string, error) { return []string{key}, nil })
	first, err := fx.m.surface.open("session-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cookie := sessionCookie(t, first)
	base := first[:strings.Index(first, "?token=")]
	lines := make(chan string, 64)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go streamFrames(t, base, cookie, lines, done)
	time.Sleep(50 * time.Millisecond)

	var options agent.Options
	fx.m.applyRunEvents(&options, 1)
	options.EventSink(agent.StageEvent{Name: "node_" + key, Status: "completed", Detail: "path-marker", ChangedFiles: []string{"keys/" + key + ".txt"}})

	deadline := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before the frame")
			}
			if strings.Contains(line, key) {
				t.Fatalf("key reached a frame: %q", line)
			}
			if strings.Contains(line, "path-marker") {
				return
			}
		case <-deadline:
			t.Fatal("no frame arrived")
		}
	}
}

// viewerClient redeems a fresh one-time URL and returns a client with the
// session cookie and the page where the link landed.
func viewerClient(t *testing.T, m model) (*http.Client, *url.URL) {
	t.Helper()
	m.surface.mu.Lock()
	boot, err := m.surface.running().BootstrapURL()
	m.surface.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, err := client.Get(boot)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return client, resp.Request.URL
}

// viewerGet fetches a path relative to the landing page, the pipeline page.
func viewerGet(t *testing.T, client *http.Client, landing *url.URL, rel string) string {
	t.Helper()
	target, err := landing.Parse(rel)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", rel, resp.StatusCode, body)
	}
	return string(body)
}

// viewerRunState is a run where web_probe runs and reports a summary.
func viewerRunState(t *testing.T, summary string) presentation.State {
	t.Helper()
	state, err := presentation.Apply(presentation.State{}, presentation.PlanEvent{StageNames: []string{"code_writer", "web_probe"}, Dependencies: map[string][]string{"web_probe": {"code_writer"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []agent.StageEvent{{Name: "web_probe", Status: "running"}, {Name: "web_probe", Status: "failed", Detail: summary}} {
		if state, err = presentation.Apply(state, presentrun.AdaptStageEvent(event)); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

// TestPipelineViewerShowsTheRunSnapshot pins the one view model and the
// shared secret list: the run's presentation snapshot is the graph the page
// shows, and the sink and the viewer read the credentials once together.
func TestPipelineViewerShowsTheRunSnapshot(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	loads := 0
	m.surface.loadSecrets = func(model) ([]string, error) {
		loads++
		return []string{"sk-VIEWER-EXACT-SECRET"}, nil
	}
	m = m.openWebSurface()
	var options agent.Options
	m.applyRunEvents(&options, 1)
	options.EventSink(agent.StageEvent{Name: "web_probe", Status: "running"})
	options.OnPresentationState(viewerRunState(t, "probe failed near sk-VIEWER-EXACT-SECRET"))

	client, landing := viewerClient(t, m)
	if !strings.HasSuffix(landing.Path, "/s/pipeline/") {
		t.Fatalf("the one-time link landed on %s, want the pipeline page", landing.Path)
	}
	page := viewerGet(t, client, landing, "./")
	for _, want := range []string{`data-name="web_probe"`, `class="node s-failed"`, "Running."} {
		if !strings.Contains(page, want) {
			t.Fatalf("pipeline page lacks %q:\n%s", want, page)
		}
	}
	drawer := viewerGet(t, client, landing, "node?name=web_probe")
	if strings.Contains(drawer, "sk-VIEWER-EXACT-SECRET") || !strings.Contains(drawer, "[REDACTED]") {
		t.Fatalf("drawer did not redact the run's exact secret: %s", drawer)
	}
	if loads != 1 {
		t.Fatalf("credentials loaded %d times, want once for the sink and the viewer together", loads)
	}
}

// TestPipelineViewerKeepsAnOldRunOutOfANewSession pins the generation rule: a
// snapshot from a run started before a session change never reaches the new
// session, and the change clears the run the old session showed.
func TestPipelineViewerKeepsAnOldRunOutOfANewSession(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	m.surface.loadSecrets = func(model) ([]string, error) { return nil, nil }
	m = m.openWebSurface()
	var options agent.Options
	m.applyRunEvents(&options, 1)
	options.OnPresentationState(viewerRunState(t, "first"))

	m.activeSession.SessionID = "next"
	m = m.rotateWebSession()
	options.OnPresentationState(viewerRunState(t, "late"))

	client, landing := viewerClient(t, m)
	page := viewerGet(t, client, landing, "./")
	if strings.Contains(page, `data-name="web_probe"`) || !strings.Contains(page, "No run yet") {
		t.Fatalf("the new session shows the old run:\n%s", page)
	}
}

// TestPipelineViewerReadsNoCredentialWithoutAServer pins the lazy load, and
// that a failed load hides the run instead of showing unredacted text.
func TestPipelineViewerReadsNoCredentialWithoutAServer(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	loads := 0
	m.surface.loadSecrets = func(model) ([]string, error) {
		loads++
		return nil, errors.New("store locked")
	}
	var options agent.Options
	m.applyRunEvents(&options, 1)
	options.OnPresentationState(viewerRunState(t, "secret-free text"))
	if loads != 0 {
		t.Fatalf("credentials loaded %d times with no server", loads)
	}

	m = m.openWebSurface()
	client, landing := viewerClient(t, m)
	page := viewerGet(t, client, landing, "./")
	if loads != 1 || strings.Contains(page, `class="node s-failed"`) || !strings.Contains(page, "The run is hidden") {
		t.Fatalf("after a failed load (loads %d) the page must hide the run:\n%s", loads, page)
	}
}

// TestPipelineViewerEndsAnErroredRun pins that a cancelled or failed TUI run
// ends in the viewer with the terminal receipt instead of "Running.".
func TestPipelineViewerEndsAnErroredRun(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.Canceled, "Cancelled."},
		{errors.New("provider refused the request"), "Failed: provider refused the request"},
	} {
		m, _ := newWebTestModel(t, false)
		m.surface.loadSecrets = func(model) ([]string, error) { return nil, nil }
		m = m.openWebSurface()
		var options agent.Options
		finish := m.applyRunEvents(&options, 1)
		state, err := presentation.Apply(presentation.State{}, presentation.PlanEvent{StageNames: []string{"code_writer"}})
		if err != nil {
			t.Fatal(err)
		}
		if state, err = presentation.Apply(state, presentrun.AdaptStageEvent(agent.StageEvent{Name: "code_writer", Status: "running"})); err != nil {
			t.Fatal(err)
		}
		options.OnPresentationState(state)
		finish("", tc.err)

		client, landing := viewerClient(t, m)
		if page := viewerGet(t, client, landing, "./"); !strings.Contains(page, tc.want) || strings.Contains(page, "Running.") {
			t.Fatalf("after %v the page must say %q:\n%s", tc.err, tc.want, page)
		}
	}
}

// TestPipelineViewerFollowsReplayAndClear pins item 14 of the review: the
// viewer shows what the terminal shows after a /resume replay, after the
// session switch that follows it, and after a clear.
func TestPipelineViewerFollowsReplayAndClear(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	m.surface.loadSecrets = func(model) ([]string, error) { return nil, nil }
	m = m.openWebSurface()

	// /resume: the replay lands the stored snapshot, then the session rotates.
	m = m.applyPresentationSnapshot(viewerRunState(t, "replayed summary"))
	m.activeSession.SessionID = "resumed"
	m = m.rotateWebSession()
	m.surface.mirrors.Wait()
	client, landing := viewerClient(t, m)
	if page := viewerGet(t, client, landing, "./"); !strings.Contains(page, `class="node s-failed" data-name="web_probe"`) {
		t.Fatalf("after /resume the page lacks the replayed run:\n%s", page)
	}

	m = m.clearPresentationState()
	m.surface.mirrors.Wait()
	if page := viewerGet(t, client, landing, "./"); strings.Contains(page, `data-name="web_probe"`) || !strings.Contains(page, "No run yet") {
		t.Fatalf("after a clear the page still shows the run:\n%s", page)
	}
}

// TestMirrorPipelineDoesNotWaitOnTheSecretLoad pins that /resume and a
// session switch return while the credential load is slow. The load can read
// the keychain or refresh an OAuth token, so it runs off the UI loop. After
// the load ends, the viewer shows the latest mirrored state.
func TestMirrorPipelineDoesNotWaitOnTheSecretLoad(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	release := make(chan struct{})
	m.surface.loadSecrets = func(model) ([]string, error) {
		<-release
		return nil, nil
	}
	m = m.openWebSurface()
	done := make(chan model, 1)
	go func() {
		mm := m.applyPresentationSnapshot(viewerRunState(t, "replayed summary"))
		mm.activeSession.SessionID = "resumed"
		done <- mm.rotateWebSession()
	}()
	select {
	case m = <-done:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the mirror waited on the credential load")
	}
	close(release)
	m.surface.mirrors.Wait()
	client, landing := viewerClient(t, m)
	if page := viewerGet(t, client, landing, "./"); !strings.Contains(page, `class="node s-failed" data-name="web_probe"`) {
		t.Fatalf("after the load the page lacks the mirrored run:\n%s", page)
	}
}

// TestSlowMirrorDoesNotReplaceANewerRun pins the feed order rule: a mirror
// whose credential load ends after a run started does not publish over the
// run's state.
func TestSlowMirrorDoesNotReplaceANewerRun(t *testing.T) {
	m, _ := newWebTestModel(t, false)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	m.surface.loadSecrets = func(model) ([]string, error) {
		<-release
		return nil, nil
	}
	m = m.openWebSurface()
	m = m.clearPresentationState() // the mirror took the blocking loader
	// The run feed gets a loader that does not block.
	m.surface.loadSecrets = func(model) ([]string, error) { return nil, nil }
	var options agent.Options
	m.applyRunEvents(&options, 1)
	options.OnPresentationState(viewerRunState(t, "newer run"))
	close(release)
	m.surface.mirrors.Wait()
	client, landing := viewerClient(t, m)
	if page := viewerGet(t, client, landing, "./"); !strings.Contains(page, `data-name="web_probe"`) {
		t.Fatalf("the slow mirror replaced the newer run:\n%s", page)
	}
}

// TestPipelineEditOpensTheEditorOnADraft pins /pipeline edit <name>: it makes
// a draft of the library entry and shows a one-time link that opens the
// editor on that draft.
func TestPipelineEditOpensTheEditorOnADraft(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writeLibraryPipeline(t, base, "lint-only")
	m, opened := newWebTestModel(t, true)
	m.cwd = t.TempDir()
	m.input.SetValue("/pipeline edit lint-only")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("/pipeline edit must not start a run")
	}
	m = updated.(model)
	row := lastSystemRow(t, m)
	if !strings.HasPrefix(row, "Pipeline editor: http://127.0.0.1:") || len(*opened) != 1 || !strings.Contains(row, (*opened)[0]) {
		t.Fatalf("row = %q, opened = %v", row, *opened)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, err := client.Get((*opened)[0])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Request.URL.Path, "/s/editor/") || resp.Request.URL.Query().Get("draft") == "" {
		t.Fatalf("link opened %s (%d)", resp.Request.URL, resp.StatusCode)
	}
	if !strings.Contains(string(body), "data-boot=") || !strings.Contains(string(body), "lint-only") {
		t.Fatalf("editor page lacks the draft: %.300s", body)
	}
	drafts, _ := filepath.Glob(filepath.Join(base, "splice", "pipelines", ".drafts", "*.json"))
	if len(drafts) != 1 {
		t.Fatalf("drafts = %v, want one", drafts)
	}
}

// TestEditorUseNowGoesThroughPipelineUse pins that "Use it now" in the
// browser runs /pipeline use in the UI loop, with its refusal during a run.
func TestEditorUseNowGoesThroughPipelineUse(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	writeLibraryPipeline(t, base, "lint-only")
	m := newModel(context.Background(), Options{})
	m.cwd = t.TempDir()

	m.pending = true
	reply := make(chan editorUseReply, 1)
	updated, _ := m.Update(editorUseMsg{name: "lint-only", reply: reply})
	if r := <-reply; r.err == nil || !strings.Contains(r.err.Error(), "while a run is active") {
		t.Fatalf("use during a run = %+v, want the refusal", r)
	}
	if _, err := os.Stat(filepath.Join(base, "splice", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("the refusal wrote config.json: %v", err)
	}

	m = updated.(model)
	m.pending = false
	updated, _ = m.Update(editorUseMsg{name: "lint-only", reply: reply})
	if r := <-reply; r.err != nil || !strings.Contains(r.text, "Active pipeline: lint-only") {
		t.Fatalf("use = %+v", r)
	}
	assertContains(t, transcriptText(updated.(model).transcript), "active pipeline: lint-only")
	data, _ := os.ReadFile(filepath.Join(base, "splice", "config.json"))
	if !strings.Contains(string(data), `"active_pipeline": "lint-only"`) {
		t.Fatalf("config.json = %s", data)
	}

	// The editor's request side waits for the loop's reply.
	text, err := askUsePipeline(func(msg tea.Msg) {
		next, _ := m.Update(msg)
		m = next.(model)
	}, "lint-only")
	if err != nil || !strings.Contains(text, "lint-only") {
		t.Fatalf("askUsePipeline = %q, %v", text, err)
	}
}

// TestPipelineEditResumesUnlessNew pins that /pipeline edit resumes the
// draft of its source, and /pipeline edit --new makes another.
func TestPipelineEditResumesUnlessNew(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	m, _ := newWebTestModel(t, false)
	m.cwd = t.TempDir()
	drafts := func() int {
		found, _ := filepath.Glob(filepath.Join(base, "splice", "pipelines", ".drafts", "*.json"))
		return len(found)
	}
	for _, step := range []struct {
		input string
		want  int
	}{
		{"/pipeline edit default", 1},
		{"/pipeline edit default", 1},
		{"/pipeline edit --new default", 2},
	} {
		m.input.SetValue(step.input)
		updated, _ := m.Update(testKey(tea.KeyEnter))
		m = updated.(model)
		if got := drafts(); got != step.want {
			t.Fatalf("after %q: %d drafts, want %d (%s)", step.input, got, step.want, lastSystemRow(t, m))
		}
	}
	m.input.SetValue("/pipeline edit --yes")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	if text := transcriptText(updated.(model).transcript); !strings.Contains(text, "Usage: /pipeline edit [--new] [name]") {
		t.Fatalf("bad flag: %s", text)
	}
}

// credentialFixture points the key store and the OAuth store at temp files.
// It returns the user config directory.
func credentialFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("SPLICE_CRED_STORAGE", "file")
	t.Setenv("SPLICE_OAUTH_STORAGE", "file")
	t.Setenv("SPLICE_OAUTH_TOKENS_PATH", filepath.Join(root, "oauth.json"))
	dir := filepath.Join(root, "splice")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunSecretValuesSkipsOnlyUnusedProfiles: a saved profile whose key
// cannot be read pauses the surface only when the run routes to it.
func TestRunSecretValuesSkipsOnlyUnusedProfiles(t *testing.T) {
	dir := credentialFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := newWebTestModel(t, false)
	m.userConfigPath = filepath.Join(dir, "config.json")
	m.cwd = t.TempDir()
	m.providerProfile = config.ProviderProfile{Name: "inline", APIKey: "zq7Lm2Vt9Rp4Xw8K"}
	m.savedProviders = []config.ProviderProfile{{Name: "spare", APIKeyStored: true}}
	values, err := m.runSecretValues()
	if err != nil {
		t.Fatalf("an unused profile's read error paused the surface: %v", err)
	}
	if len(values) != 1 || values[0] != "zq7Lm2Vt9Rp4Xw8K" {
		t.Fatalf("values = %q, want the primary key", values)
	}
	stage := `{"stages":{"test_generator":{"provider_profile":"spare","model":"m"}}}`
	if err := os.WriteFile(filepath.Join(dir, "stage-models.json"), []byte(stage), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.runSecretValues(); err == nil || !strings.Contains(err.Error(), "spare") {
		t.Fatalf("routed profile error = %v, want a named read failure", err)
	}
}

// TestRunSinkRedactsStoredKeyAndOAuthLogin drives the real credential load:
// the primary key comes from the key store and a saved profile's bearer from
// its stored OAuth login. Neither reaches a published frame. Both values
// match no redaction pattern, so only the loaded list can redact them.
func TestRunSinkRedactsStoredKeyAndOAuthLogin(t *testing.T) {
	const key = "zq7Lm2Vt9Rp4Xw8K"
	const token = "Qm4Tz8Wc2Hn6Lp1R"
	dir := credentialFixture(t)
	keys, err := config.ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.Set("stored", key); err != nil {
		t.Fatal(err)
	}
	logins, err := oauth.NewStore(oauth.StoreOptions{FilePath: os.Getenv("SPLICE_OAUTH_TOKENS_PATH")})
	if err != nil {
		t.Fatal(err)
	}
	if err := logins.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	m, _ := newWebTestModel(t, false)
	m.userConfigPath = filepath.Join(dir, "config.json")
	m.cwd = t.TempDir()
	m.providerProfile = config.ProviderProfile{Name: "stored", APIKeyStored: true}
	m.savedProviders = []config.ProviderProfile{{Name: "chatgpt", CatalogID: "chatgpt"}}

	first, err := m.surface.open("session-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cookie := sessionCookie(t, first)
	base := first[:strings.Index(first, "?token=")]
	lines := make(chan string, 64)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go streamFrames(t, base, cookie, lines, done)
	time.Sleep(50 * time.Millisecond)

	var options agent.Options
	m.applyRunEvents(&options, 1)
	options.EventSink(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "key " + key + " bearer " + token})

	deadline := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before the frame")
			}
			if strings.Contains(line, key) || strings.Contains(line, token) {
				t.Fatalf("a credential reached a frame: %q", line)
			}
			if strings.Contains(line, "bearer [REDACTED]") && strings.Contains(line, "key [REDACTED]") {
				return
			}
		case <-deadline:
			t.Fatal("no redacted frame arrived")
		}
	}
}
