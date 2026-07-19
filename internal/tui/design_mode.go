package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// enterDesignMode sets designMode, ensures an active session, records the
// design_mode_entered lifecycle event, and appends the supplied orientation
// notice. The caller decides whether the notice is shown (long notice once per
// session) or always (short /design message).
func (m model) enterDesignMode(notice string) model {
	m.designMode = true
	var err error
	m, err = m.ensureActiveSession("Design conversation")
	if err != nil {
		m.designMode = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "session create error: " + err.Error()})
		return m
	}
	m, err = m.appendSessionEvent(sessions.EventDesignModeEntered, nil)
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "session record error: " + err.Error()})
	}
	if notice != "" {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: notice})
	}
	return m
}

func (m model) handleDesignCommand() (model, tea.Cmd) {
	if m.pending {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot enter design mode while a run is active."})
		return m, nil
	}
	m = m.enterDesignMode("Design conversation mode. Type /crystallize to produce a plan, or /exec to run a prompt through the pipeline.")
	return m, nil
}

func (m model) handleExecCommand(text string) (model, tea.Cmd) {
	text = strings.TrimSpace(text)
	m.designMode = false
	if text == "" {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Execution mode. Type a prompt to run it through the pipeline, or /design to return to design conversation."})
		return m, nil
	}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/exec " + text})
	return m.launchPrompt(text)
}

func (m model) handleApproveCommand() (model, tea.Cmd) {
	if m.pending {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve while a run is active."})
		return m, nil
	}
	if m.pendingPlan == nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "No pending plan. Type /crystallize to create one."})
		return m, nil
	}
	if m.pendingCritique != nil && m.pendingCritique.MustFixBeforeExecution {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Plan has must-fix issues. Revise and re-run /crystallize."})
		return m, nil
	}
	if m.provider == nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "No provider configured."})
		return m, nil
	}

	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/approve"})
	runCtx, cancel := context.WithCancel(m.ctx)
	m = m.beginRun(cancel)

	// Snapshot data for the goroutine.
	plan := *m.pendingPlan
	provider := m.provider
	cwd := m.cwd
	runID := m.activeRunID
	store := m.sessionStore
	sessionID := m.activeSession.SessionID

	// Build agent.Options for the runner. Reuse the model's agentOptions
	// but set the registry and cwd.
	options := m.agentOptions
	options.Registry = m.registry
	options.Cwd = cwd
	options.PermissionMode = m.permissionMode
	options.SessionID = sessionID
	options.ProviderName = m.providerName
	options.Model = m.modelName
	options.ReasoningEffort = string(m.reasoningEffort)

	// Resolve memory (best-effort, same as the normal pipeline path).
	memClient, _ := tuiResolveMemory(runCtx)
	var mem splicerun.MemoryStore
	if memClient != nil {
		mem = memClient
	}

	// Generate a unique plan revision ID before persisting the approval event.
	planID := "plan-" + strconv.FormatInt(m.now().UnixNano(), 16)

	// Persist plan_approved event before execution.
	if store != nil && sessionID != "" {
		_, _ = store.AppendEvent(sessionID, sessions.AppendEventInput{
			Type:    sessions.EventPlanApproved,
			Payload: splicerun.PlanApprovedPayload{PlanID: planID},
		})
	}

	// Build the lifecycle callback: persists task events and emits progress.
	onTaskLifecycle := func(task schemas.Task, taskRunID string, pipelineResult schemas.PipelineResult) {
		if store != nil && sessionID != "" {
			if pipelineResult.Status == "completed" {
				_, _ = store.AppendEvent(sessionID, sessions.AppendEventInput{
					Type:    sessions.EventTaskCompleted,
					Payload: splicerun.TaskCompletedPayload{TaskID: task.ID, RunID: taskRunID},
				})
			} else {
				_, _ = store.AppendEvent(sessionID, sessions.AppendEventInput{
					Type:    sessions.EventTaskFailed,
					Payload: splicerun.TaskFailedPayload{TaskID: task.ID, RunID: taskRunID},
				})
			}
		}
	}

	return m, tea.Batch(
		func() tea.Msg {
			result, err := splicerun.RunDesignPlanWithResume(runCtx, plan, provider, options, mem, nil, splicerun.RunDesignPlanOptions{
				PlanID:          planID,
				OnTaskLifecycle: onTaskLifecycle,
			})
			return planExecutionResultMsg{runID: runID, result: result, err: err, store: store, sessionID: sessionID}
		},
		m.spinner.Tick,
	)
}

func (m model) handleCrystallizeCommand() (model, tea.Cmd) {
	if m.pending {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot crystallize while a run is active."})
		return m, nil
	}
	if !m.designMode {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Must be in design mode. Type /design to enter."})
		return m, nil
	}
	if m.provider == nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "No provider configured."})
		return m, nil
	}
	if m.sessionStore == nil || m.activeSession.SessionID == "" {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "No active session."})
		return m, nil
	}

	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/crystallize"})
	runCtx, cancel := context.WithCancel(m.ctx)
	m = m.beginRun(cancel)

	// Snapshot the data the goroutine needs. model is a value type; copying
	// it captures the session store, provider, session ID, events, etc.
	store := m.sessionStore
	sessionID := m.activeSession.SessionID
	events := append([]sessions.Event(nil), m.sessionEvents...)
	provider := m.provider
	cwd := m.cwd
	runID := m.activeRunID

	planID := "plan-" + strconv.FormatInt(m.now().UnixNano(), 16)

	resolver := m.stageModelResolver
	if resolver == nil {
		stageConfigPath := filepath.Join(filepath.Dir(m.userConfigPath), "stage-models.json")
		stageConfig, err := schemas.LoadStageModelConfig(stageConfigPath)
		if err != nil {
			stageConfig = schemas.StageModelConfigFile{}
		}
		profiles := append([]config.ProviderProfile(nil), m.savedProviders...)
		if m.providerProfile.Name != "" {
			found := false
			for _, p := range profiles {
				if p.Name == m.providerProfile.Name {
					found = true
					break
				}
			}
			if !found {
				profiles = append(profiles, m.providerProfile)
			}
		}
		tierResolverConfig := splicerun.TierResolverConfig{
			PrimaryProfile: m.providerProfile,
			Registry:       &m.modelCatalog,
		}
		resolver, _ = splicerun.BuildStageModelResolvers(stageConfig, profiles, m.newProvider, tierResolverConfig)
		m.stageModelResolver = resolver
	}

	return m, tea.Batch(
		func() tea.Msg {
			wf := splicerun.NewDesignWorkflow(store, sessionID, planID).WithPrimaryModel(m.modelName)
			plan, critique, err := wf.CrystallizeAndCritique(runCtx, events, provider, resolver, zeroruntime.CollectOptions{}, cwd, nil)
			return crystallizeResultMsg{runID: runID, plan: plan, critique: critique, err: err, store: store, sessionID: sessionID}
		},
		m.spinner.Tick,
	)
}

type crystallizeResultMsg struct {
	runID     int
	plan      schemas.DesignPlan
	critique  schemas.PlanCritique
	err       error
	store     *sessions.Store
	sessionID string
}

type planExecutionResultMsg struct {
	runID     int
	result    agent.Result
	err       error
	store     *sessions.Store
	sessionID string
}

func formatDesignPlan(plan schemas.DesignPlan) string {
	var b strings.Builder
	b.WriteString("Plan: " + plan.Epic + "\n")
	if len(plan.Requirements) > 0 {
		b.WriteString("Requirements:\n")
		for _, r := range plan.Requirements {
			b.WriteString("- " + r + "\n")
		}
	}
	if len(plan.InScope) > 0 {
		b.WriteString("In scope:\n")
		for _, s := range plan.InScope {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(plan.OutOfScope) > 0 {
		b.WriteString("Out of scope:\n")
		for _, s := range plan.OutOfScope {
			b.WriteString("- " + s + "\n")
		}
	}
	if plan.SystemDesign != "" {
		b.WriteString("System design:\n" + plan.SystemDesign + "\n")
	}
	if len(plan.Tasks) > 0 {
		b.WriteString("Tasks:\n")
		for i, t := range plan.Tasks {
			b.WriteString(fmt.Sprintf("%d. %s", i+1, t.Title))
			if t.Intent != "" {
				b.WriteString(": " + t.Intent)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// handleLayoutCommand toggles the persistent plan panel and reports the new
// state. The panel pins the crystallized DesignPlan above the chat during
// design conversations so it survives transcript scroll during revisions.
func (m model) handleLayoutCommand() (model, tea.Cmd) {
	m.planPanelPersistent = !m.planPanelPersistent
	state := "off"
	if m.planPanelPersistent {
		state = "on"
	}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Persistent plan panel " + state + "."})
	return m, nil
}

// persistentPlanHeader renders the crystallized DesignPlan as a bordered
// header block above the chat column when the layout toggle is on, design mode
// is active, and a plan has been crystallized. Returns "" (render nothing)
// otherwise, so the toggle is inert outside its valid context.
func (m model) persistentPlanHeader(width int) string {
	if !m.planPanelPersistent || !m.designMode || m.pendingPlan == nil {
		return ""
	}
	body := formatDesignPlan(*m.pendingPlan)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	lines := append([]string{zeroTheme.faint.Render("Plan")}, strings.Split(body, "\n")...)
	return borderedBlock(width, lines)
}

func formatPlanCritique(critique schemas.PlanCritique) string {
	var b strings.Builder
	b.WriteString("Critique: " + critique.OverallAssessment + "\n")
	if critique.MustFixBeforeExecution {
		b.WriteString("Status: must-fix issues before execution\n")
	} else {
		b.WriteString("Status: ready to approve\n")
	}
	for _, c := range critique.Critiques {
		b.WriteString(fmt.Sprintf("- [%s / %s] %s", c.Category, c.Severity, c.Issue))
		if c.SuggestedMitigation != "" {
			b.WriteString(fmt.Sprintf(" (mitigation: %s)", c.SuggestedMitigation))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// reconstructDesignState rebuilds design mode state from the current
// session's lifecycle events. Called on /resume so design mode, pending plan,
// and critique survive across sessions. If the session has no design events,
// design mode is off and pending plan/critique are nil.
func (m model) reconstructDesignState() model {
	if m.sessionStore == nil || m.activeSession.SessionID == "" || len(m.sessionEvents) == 0 {
		m.designMode = false
		m.pendingPlan = nil
		m.pendingCritique = nil
		return m
	}
	state, err := splicerun.ReconstructDesignState(m.sessionEvents)
	if err != nil {
		// Malformed events don't crash resume; design state is just unavailable.
		m.designMode = false
		m.pendingPlan = nil
		m.pendingCritique = nil
		return m
	}
	m.pendingPlan = state.Plan
	m.pendingCritique = state.Critique
	switch state.Phase {
	case schemas.DesignPhaseConversation, schemas.DesignPhaseReview:
		m.designMode = true
	default:
		// executing or completed: the plan has been approved; design mode is off.
		m.designMode = false
	}
	return m
}

func designConversationRegistry(registry *tools.Registry) *tools.Registry {
	filtered := tools.NewRegistry()
	if registry == nil {
		return filtered
	}
	allowed := map[string]bool{"read_file": true, "list_directory": true, "grep": true, "ask_user": true}
	for _, tool := range registry.All() {
		if allowed[tool.Name()] {
			filtered.Register(tool)
		}
	}
	return filtered
}
