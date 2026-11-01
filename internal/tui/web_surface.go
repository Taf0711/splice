package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/browser"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/web"
)

// tuiSurface owns the browser server of one TUI. The server starts on the first
// /web command. It stops when the TUI exits, or after its idle timeout.
type tuiSurface struct {
	mu     sync.Mutex
	server *web.Server
	cancel context.CancelFunc
	// gen moves on every session change and stop. A run's sink drops events once
	// its generation is old, so a run from the previous session cannot publish.
	gen uint64
	// feeds counts the run feeds made. A mirror feed publishes only while it is
	// the latest feed, so a slow mirror cannot replace a newer run's state.
	feeds uint64
	// mirrors tracks the mirror loads that run off the UI loop. Tests wait on it.
	mirrors sync.WaitGroup
	// loadSecrets is a test seam. The default reads the model's stored keys.
	loadSecrets func(model) ([]string, error)
	// pipeline is the pipeline viewer. It outlives each server, so a server
	// that starts after a run still shows that run of this session.
	pipeline *web.PipelineView
	// editor is the topology editor. It keeps no run state: its drafts are files.
	editor *web.Editor

	// openBrowser and interactive are seams for tests. The defaults open a real
	// browser, and only when stdin and stdout are both terminals.
	openBrowser func(url string) error
	interactive func() bool
}

func newTUISurface() *tuiSurface {
	return &tuiSurface{
		pipeline:    web.NewPipelineView(splicerun.TopologySources{}, ""),
		editor:      web.NewEditor(web.EditorConfig{}),
		openBrowser: browser.OpenURL,
		interactive: stdioInteractive,
	}
}

// stdioInteractive reports whether stdin and stdout are both terminals.
func stdioInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// running returns the live server, or nil when none runs. The caller holds mu.
func (s *tuiSurface) running() *web.Server {
	if s.server == nil {
		return nil
	}
	select {
	case <-s.server.Done():
		return nil
	default:
		return s.server
	}
}

// open starts the server when none runs, then returns a fresh one-time URL.
func (s *tuiSurface) open(sessionID string) (string, error) {
	return s.openAt(sessionID, "", nil)
}

// openAt is open with a one-time URL that opens one surface page with a
// query. An empty surface opens the landing page, the pipeline viewer.
func (s *tuiSurface) openAt(sessionID, surface string, query url.Values) (string, error) {
	if s == nil {
		return "", errors.New("web surface is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	srv := s.running()
	if srv == nil {
		ctx, cancel := context.WithCancel(context.Background())
		srv = web.New(0)
		// Record the session before Start. A running server rotates on SetSession,
		// and that would invalidate the URL that Start returns.
		if _, err := srv.SetSession(sessionID); err != nil {
			cancel()
			return "", err
		}
		if err := s.pipeline.Attach(srv); err != nil {
			cancel()
			return "", err
		}
		if err := s.editor.Attach(srv); err != nil {
			cancel()
			return "", err
		}
		bootstrap, err := srv.Start(ctx)
		if err != nil {
			cancel()
			return "", err
		}
		s.server, s.cancel = srv, cancel
		if surface == "" {
			return bootstrap, nil
		}
	}
	if surface == "" {
		return srv.BootstrapURL()
	}
	return srv.BootstrapURLFor(surface, query)
}

// sessionChanged rotates the browser session when the TUI session changes. It
// returns the new one-time URL, or "" when no server runs.
func (s *tuiSurface) sessionChanged(sessionID string) (string, error) {
	if s == nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.pipeline.Reset() // the new session never sees the old session's run
	srv := s.running()
	if srv == nil {
		return "", nil
	}
	return srv.SetSession(sessionID)
}

// runSecrets loads one run's redaction list the first time the run needs it. The
// load runs at most once per run, so the store is read at most once.
type runSecrets struct {
	once   sync.Once
	values []string
	err    error
	load   func() ([]string, error)
}

// runFeed connects one pipeline run to the browser surface. The event sink
// and the pipeline viewer share its session generation and its secret list:
// nothing from the run reaches a later session, and the credentials load at
// most once, only when run text would reach a running server.
type runFeed struct {
	s         *tuiSurface
	gen       uint64
	secrets   *runSecrets
	reportErr func(error)
	// seq is the number of this feed in tuiSurface.feeds.
	seq uint64
	// mirror is set for a feed that mirrors the terminal state, not a run.
	mirror bool
	// begun is set by the first snapshot, which starts the run in the viewer.
	// The caller of state holds s.mu.
	begun bool
}

func (s *tuiSurface) newRunFeed(load func() ([]string, error), reportErr func(error)) *runFeed {
	s.mu.Lock()
	s.feeds++
	gen, seq := s.gen, s.feeds
	s.mu.Unlock()
	return &runFeed{s: s, gen: gen, seq: seq, secrets: &runSecrets{load: load}, reportErr: reportErr}
}

// loadSecrets loads the run's secret list once. failedNow is true for the
// call whose load failed, so the caller reports the failure exactly once.
func (f *runFeed) loadSecrets() (values []string, err error, failedNow bool) {
	f.secrets.once.Do(func() {
		f.secrets.values, f.secrets.err = f.secrets.load()
		failedNow = f.secrets.err != nil
	})
	return f.secrets.values, f.secrets.err, failedNow
}

// viewerSecrets is the viewer's secret source. The viewer calls it under its
// own lock, so a failure is reported on another goroutine: the report goes
// through the UI loop, which can need that lock.
func (f *runFeed) viewerSecrets() ([]string, error) {
	values, err, failedNow := f.loadSecrets()
	if failedNow && f.reportErr != nil {
		go f.reportErr(err)
	}
	return values, err
}

// sink returns the EventSink for one pipeline run. Credentials load lazily: no
// store access happens until an event would reach a running server. If the load
// fails, the sink drops every event for this run, and reportErr is called once.
// The run itself continues.
func (s *tuiSurface) sink(load func() ([]string, error), reportErr func(error)) func(agent.StageEvent) {
	if s == nil {
		return nil
	}
	return s.newRunFeed(load, reportErr).event
}

// live reports whether the run's generation is current and a server runs, so
// run text would reach a browser.
func (f *runFeed) live() bool {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	return f.s.gen == f.gen && f.s.running() != nil
}

// preloadSecrets loads the run's secrets without s.mu when run text would
// reach a browser. The load can read the keychain and refresh an OAuth token
// over the network. /new, /resume, /web, and quit need s.mu, so the load must
// not hold it. It returns the load result.
func (f *runFeed) preloadSecrets() (values []string, err error, failedNow bool, live bool) {
	if !f.live() {
		return nil, nil, false, false
	}
	values, err, failedNow = f.loadSecrets()
	return values, err, failedNow, true
}

func (f *runFeed) event(event agent.StageEvent) {
	values, err, failedNow, live := f.preloadSecrets()
	// Report outside the lock. The UI loop may need the lock to handle the message.
	if failedNow && f.reportErr != nil {
		defer f.reportErr(err)
	}
	if !live || err != nil {
		return
	}
	// The lock keeps the second generation check and the publish together, so
	// a session change during the load cannot slip in between.
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if f.s.gen != f.gen {
		return
	}
	srv := f.s.running()
	if srv == nil {
		return
	}
	srv.Sink()(splicerun.RedactStageEvent(event, values))
}

// state gives the pipeline viewer one presentation snapshot of the run, the
// snapshot that the terminal renders. The viewer redacts the run text with the
// run's secret list, which it loads only to send run text to a browser.
func (f *runFeed) state(state presentation.State) {
	// The viewer loads the secrets when it streams. Load them here first,
	// without s.mu, so the viewer finds them loaded.
	if _, err, failedNow, _ := f.preloadSecrets(); failedNow && f.reportErr != nil {
		f.reportErr(err)
	}
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if f.s.gen != f.gen || (f.mirror && f.s.feeds != f.seq) {
		return
	}
	if !f.begun {
		f.begun = true
		f.s.pipeline.BeginRun(f.viewerSecrets)
	}
	f.s.pipeline.Observe(state)
}

// finish gives the pipeline viewer the end of the run: the stage records of
// the result for its timeline, or, when the run returned an error, the same
// receipt the terminal shows (CANCELLED or FAILED). A cancelled run skips
// the pipeline's own end event, so without this the page would say that the
// run still runs. The run goroutine calls it.
func (f *runFeed) finish(finalAnswer string, runErr error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if f.s.gen != f.gen {
		return
	}
	if runErr != nil {
		status, detail := runEndReceipt(runErr)
		f.s.pipeline.EndRun(status, detail)
		return
	}
	f.s.pipeline.RecordResult(finalAnswer)
}

// runEndReceipt maps a run error to the receipt kind of pipelineTurnReceipt:
// a context cancel is CANCELLED, every other error is FAILED with its text.
func runEndReceipt(err error) (status, detail string) {
	if errors.Is(err, context.Canceled) {
		return "cancelled", ""
	}
	return "failed", err.Error()
}

// configurePipeline points the pipeline viewer at the workspace topology and
// at the stage-models.json path that its drawer reads.
func (s *tuiSurface) configurePipeline(cwd string, trusted bool, stageConfigPath string) {
	if s != nil {
		s.pipeline.Configure(splicerun.TopologySourcesFor(cwd, trusted), stageConfigPath)
	}
}

// stop shuts the server down. The TUI calls it on exit.
func (s *tuiSurface) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.pipeline.Reset()
	if s.server != nil {
		s.server.Stop()
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.server, s.cancel = nil, nil
}

// applyRunEvents wires one pipeline run to the browser surface: the event sink
// and the pipeline viewer. Credentials for redaction load lazily, through the
// run's feed. A load failure pauses the surface for this run and adds one error
// row. The run continues. It returns the function that gives the viewer the
// end of the run: its result, or its error.
func (m model) applyRunEvents(options *agent.Options, runID int) (finishRun func(finalAnswer string, runErr error)) {
	if m.surface == nil {
		return func(string, error) {}
	}
	load := m.surface.loadSecrets
	if load == nil {
		load = func(mm model) ([]string, error) { return mm.runSecretValues() }
	}
	feed := m.surface.newRunFeed(
		func() ([]string, error) { return load(m) },
		func(err error) {
			m.sendAgentRow(runID, transcriptRow{kind: rowSystem, text: "Browser surface paused for this run: " + err.Error(), runID: runID})
		},
	)
	options.EventSink = feed.event
	prior := options.OnPresentationState
	options.OnPresentationState = func(state presentation.State) {
		if prior != nil {
			prior(state)
		}
		feed.state(state)
	}
	m.surface.configurePipeline(m.cwd, m.trusted, stageModelConfigPath(m.userConfigPath))
	return feed.finish
}

// mirrorPipeline gives the pipeline viewer the state that the terminal now
// shows, after a change that no run reports: a new turn or design mode clears
// the last run, /resume replays a stored snapshot, and a session switch
// clears the state. It starts a new view run, so open pages load the graph
// again. Redaction loads the credentials lazily, as for a run. The load can
// read the keychain and refresh an OAuth token over the network, so when a
// server runs, the mirror loads and publishes off the UI loop.
func (m model) mirrorPipeline() {
	if m.surface == nil {
		return
	}
	load := m.surface.loadSecrets
	if load == nil {
		load = func(mm model) ([]string, error) { return mm.runSecretValues() }
	}
	feed := m.surface.newRunFeed(func() ([]string, error) { return load(m) }, nil)
	feed.mirror = true
	if !feed.live() {
		// No browser gets run text, so nothing loads. The view keeps the state
		// for a server that starts later.
		feed.state(m.lastState)
		return
	}
	state := m.lastState
	m.surface.mirrors.Add(1)
	go func() {
		defer m.surface.mirrors.Done()
		feed.state(state)
	}()
}

// runSecretValues reads the credentials that the run can send, with stored keys
// loaded. It is called only when an event would reach a running browser surface.
// The keys of the profiles that the run routes to must load. A saved profile
// that the run does not route to is best effort: the run never sends its key,
// so a read error for it is skipped. A run whose routes cannot be known
// treats every profile as routed.
func (m model) runSecretValues() ([]string, error) {
	primary, err := m.profileWithCredential(m.providerProfile)
	if err != nil {
		return nil, fmt.Errorf("cannot read the key for %s: %w", m.providerProfile.Name, err)
	}
	routes, known := splicerun.RunRouteProfiles(m.providerProfile.Name, m.doctorDecisionAdvisor.ProviderProfile, stageModelConfigPath(m.userConfigPath), splicerun.TopologySourcesFor(m.cwd, m.trusted))
	profiles := make([]config.ProviderProfile, 0, len(m.savedProviders))
	for _, saved := range m.savedProviders {
		hydrated, err := m.profileWithCredential(saved)
		if err != nil {
			if known && !routes[saved.Name] {
				continue
			}
			return nil, fmt.Errorf("cannot read the key for %s: %w", saved.Name, err)
		}
		profiles = append(profiles, hydrated)
	}
	return splicerun.RunSecretValues(primary, profiles), nil
}

// openWebSurface handles /web. It shows the one-time URL as a system row, and
// opens a browser as well when the terminal is interactive. A headless run only
// shows the row.
func (m model) openWebSurface() model {
	m.surface.configurePipeline(m.cwd, m.trusted, stageModelConfigPath(m.userConfigPath))
	url, err := m.surface.open(m.activeSession.SessionID)
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Web surface: " + err.Error()})
		return m
	}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Web surface: " + url + "\nThe link works once. Run /web again for a new link."})
	if m.surface.interactive() {
		if err := m.surface.openBrowser(url); err != nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Could not open a browser. Open the link above."})
		}
	}
	return m
}

// rotateWebSession tells the browser that the TUI session changed. Old links stop
// working, so it shows the new link when a server runs.
func (m model) rotateWebSession() model {
	url, err := m.surface.sessionChanged(m.activeSession.SessionID)
	// The change cleared the viewer. Show the new session's state: empty, or
	// the snapshot that /resume replayed before this call.
	m.mirrorPipeline()
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Web surface: " + err.Error()})
		return m
	}
	if url != "" {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Web surface moved to the new session. Old links no longer work. New link: " + url})
	}
	return m
}

// openPipelineEditor handles /pipeline edit [--new] [name]. It resumes the
// latest draft of the source, or with --new makes a fresh one, then shows a
// one-time link that opens the editor on it. The name is a library entry,
// "default", or none for the active topology.
func (m model) openPipelineEditor(args []string) model {
	ref, fresh, err := web.ParseEditArgs(args)
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Pipeline editor: " + err.Error() + ". Usage: /pipeline edit [--new] [name]"})
		return m
	}
	m.surface.configureEditor(m)
	id, err := m.surface.editor.OpenDraft(ref, fresh)
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Pipeline editor: " + err.Error()})
		return m
	}
	m.surface.configurePipeline(m.cwd, m.trusted, stageModelConfigPath(m.userConfigPath))
	link, err := m.surface.openAt(m.activeSession.SessionID, web.EditorSurface, url.Values{"draft": {id}})
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Pipeline editor: " + err.Error()})
		return m
	}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Pipeline editor: " + link + "\nThe link works once. The draft saves as you edit. Publish installs it in the user library; it does not change the active pipeline or a running run."})
	if m.surface.interactive() {
		if err := m.surface.openBrowser(link); err != nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Could not open a browser. Open the link above."})
		}
	}
	return m
}

// configureEditor points the editor at the workspace topology, the stage-model
// options of /stages, and the /pipeline use path of this TUI.
func (s *tuiSurface) configureEditor(m model) {
	if s == nil {
		return
	}
	send := m.runtimeMessageSink
	var use func(string) (string, error)
	if send != nil {
		use = func(name string) (string, error) { return askUsePipeline(send, name) }
	}
	s.editor.Configure(web.EditorConfig{
		Sources: splicerun.TopologySourcesFor(m.cwd, m.trusted),
		Models:  m.editorModelChoices(),
		Use:     use,
	})
}

// editorModelChoices are the provider and model options that /stages offers.
func (m model) editorModelChoices() []web.ModelChoice {
	wizard, err := newStageModelWizard(m.userConfigPath, m.savedProviders, m.providerProfile)
	if err != nil {
		return nil
	}
	m.populateStageModelWizardModels(wizard)
	var choices []web.ModelChoice
	for _, profile := range wizard.providers {
		for _, option := range wizard.modelOptionsByProvider[strings.TrimSpace(profile.Name)] {
			owner := option.ownerProvider
			if owner == "" {
				owner = profile.Name
			}
			choices = append(choices, web.ModelChoice{Provider: owner, Model: option.value})
		}
	}
	return choices
}

// editorUseMsg asks the UI loop to make a library entry active, as
// /pipeline use does. The editor's request waits for the reply.
type editorUseMsg struct {
	name  string
	reply chan editorUseReply
}

type editorUseReply struct {
	text string
	err  error
}

// editorUseTimeout bounds the wait for the UI loop.
const editorUseTimeout = 10 * time.Second

func askUsePipeline(send func(tea.Msg), name string) (string, error) {
	reply := make(chan editorUseReply, 1)
	send(editorUseMsg{name: name, reply: reply})
	select {
	case r := <-reply:
		return r.text, r.err
	case <-time.After(editorUseTimeout):
		return "", errors.New("Splice did not answer. Run /pipeline use " + name + " in the TUI")
	}
}

// handleEditorUse runs the editor's "Use it now" through /pipeline use, with
// the same refusal while a run is active. The TUI shows the result too.
func (m model) handleEditorUse(msg editorUseMsg) model {
	card, text, err := m.usePipeline(msg.name)
	msg.reply <- editorUseReply{text: text, err: err}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: card})
	return m
}
