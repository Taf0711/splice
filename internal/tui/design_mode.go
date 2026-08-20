package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/notify"
	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/usage"
	"github.com/Taf0711/splice/internal/worktrees"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

const designModeNotice = "Planning mode: the agent can read and search files, but it cannot edit files or run commands in this phase. Use /crystallize, then /approve, to create and execute a plan; use /exec <prompt> as a direct-run shortcut."

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
		m.designMode = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "session record error: " + err.Error()})
		return m
	}
	m.pendingPlan = nil
	m.pendingCritique = nil
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
	return m.startApproval(splicerun.DesignTransitionSourceManual)
}

// startApproval validates the approval preconditions. Manual approval starts
// execution. Agent-requested approval stops at an explicit user prompt.
func (m model) startApproval(source splicerun.DesignTransitionSource) (model, tea.Cmd) {
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
	if err := source.Validate(); err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve: " + err.Error()})
		return m, nil
	}
	// Reuse the persisted current revision PlanID so approval records the plan
	// the user reviewed. Fail loudly when no revision has been persisted.
	planID, err := m.currentPlanID()
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve: " + err.Error()})
		return m, nil
	}
	if planID == "" {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve: no persisted current design plan revision."})
		return m, nil
	}
	if m.sessionStore == nil || m.activeSession.SessionID == "" {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve: no active session store."})
		return m, nil
	}
	if source == splicerun.DesignTransitionSourceAgent {
		return m.requestDesignApproval(planID)
	}
	return m.startApprovalConfirmed(source, planID)
}

// requestDesignApproval stops an agent-requested transition at a user prompt.
// The plan runner stays inactive until resolvePermission receives Allow.
func (m model) requestDesignApproval(planID string) (model, tea.Cmd) {
	if m.pendingPermission != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve: another permission request is active."})
		return m, nil
	}
	request := agent.PermissionRequest{
		ToolCallID:         fmt.Sprintf("design-approval-%d", m.runID),
		ToolName:           "approve_design",
		Action:             agent.PermissionActionPrompt,
		Permission:         "execute_design_plan",
		PermissionMode:     agent.PermissionModeAsk,
		SideEffect:         string(tools.SideEffectWrite),
		Reason:             "The design agent requested execution of this plan.",
		Scope:              planID,
		Args:               map[string]any{"plan_id": planID},
		AvailableDecisions: []agent.PermissionDecisionAction{agent.PermissionDecisionAllow, agent.PermissionDecisionDeny},
	}
	updated, err := m.appendSessionEvent(sessions.EventPermissionRequest, request)
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve: persist user confirmation request: " + err.Error()})
		return m, nil
	}
	m = updated
	row := permissionTranscriptRow(permissionEventFromRequest(request))
	m.transcript = appendTranscriptRow(m.transcript, row)
	m.pendingPermission = &pendingPermissionPrompt{
		request:        request,
		designApproval: &designApprovalPrompt{planID: planID},
	}
	m.reportAgentLifecycle(herdrBlocked)
	return m, nil
}

// startApprovalConfirmed records approval and starts the write-capable plan
// runner. Call it only after manual approval or the explicit confirmation gate.
func (m model) startApprovalConfirmed(source splicerun.DesignTransitionSource, planID string) (model, tea.Cmd) {
	updated, err := m.appendSessionEvent(sessions.EventPlanApproved, splicerun.PlanApprovedPayload{PlanID: planID, Source: source})
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot approve: persist plan_approved: " + err.Error()})
		m.reportAgentLifecycle(herdrIdle)
		return m, nil
	}
	m = updated
	if source == splicerun.DesignTransitionSourceAgent {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "The design agent requested plan approval."})
	} else {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/approve"})
	}
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

	// The approved plan runs through the same callback seam as a normal TUI
	// pipeline run. Keep the accumulated events with the result so the update
	// path can persist them together with the completed plan.
	sessionEvents := []pendingSessionEvent{}
	estimator := usage.NewCostEstimator(&m.modelCatalog)
	callSeq := map[string]int{}
	onReasoning := options.OnReasoning
	options.OnReasoning = func(delta string) {
		if onReasoning != nil {
			onReasoning(delta)
		}
	}
	onToolOutput := options.OnToolOutput
	options.OnToolOutput = func(snapshot tools.OutputSnapshot) {
		if m.runtimeMessageSink != nil {
			id := effectiveToolRowID(snapshot.ToolCallID, callSeq[snapshot.ToolCallID])
			m.runtimeMessageSink(toolOutputSnapshotMsg{runID: runID, id: id, snapshot: snapshot.Output})
		}
		if onToolOutput != nil {
			onToolOutput(snapshot)
		}
	}

	// Keep this guard and cancellation branch aligned with runAgentWithOptions.
	// The buffered channel lets a late UI decision be discarded without blocking
	// the callback after the run context is cancelled.
	onPermissionRequest := options.OnPermissionRequest
	options.OnPermissionRequest = func(ctx context.Context, request agent.PermissionRequest) (agent.PermissionDecision, error) {
		if onPermissionRequest != nil {
			return onPermissionRequest(ctx, request)
		}
		if m.runtimeMessageSink == nil {
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "permission prompt unavailable"}, nil
		}
		if m.notifier != nil {
			m.notifier.Notify(notify.AwaitingInput, notify.DefaultMessage(notify.AwaitingInput))
		}
		decisionCh := make(chan agent.PermissionDecision, 1)
		m.sendPermissionRequest(runID, request, func(decision agent.PermissionDecision) {
			select {
			case decisionCh <- decision:
			default:
			}
		})
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type:    sessions.EventPermissionRequest,
			Payload: request,
		})
		select {
		case decision := <-decisionCh:
			if strings.TrimSpace(decision.Reason) == "" {
				decision.Reason = permissionDecisionReason(permissionDecision(decision.Action))
			}
			return decision, nil
		case <-ctx.Done():
			return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: ctx.Err().Error()}, ctx.Err()
		}
	}

	onAskUser := options.OnAskUser
	options.OnAskUser = func(ctx context.Context, request agent.AskUserRequest) (agent.AskUserResponse, error) {
		if onAskUser != nil {
			return onAskUser(ctx, request)
		}
		if m.runtimeMessageSink == nil {
			return agent.AskUserResponse{}, fmt.Errorf("ask_user prompt unavailable")
		}
		if m.notifier != nil && len(request.Questions) > 0 {
			m.notifier.Notify(notify.AwaitingInput, notify.DefaultMessage(notify.AwaitingInput))
		}
		answerCh := make(chan []string, 1)
		m.sendAskUserRequest(runID, request, func(answers []string) {
			select {
			case answerCh <- answers:
			default:
			}
		})
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type:    sessions.EventMessage,
			Payload: askUserSessionPayload(request),
		})
		select {
		case answers := <-answerCh:
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type: sessions.EventMessage,
				Payload: map[string]any{
					"role":       "ask_user_answers",
					"toolCallId": request.ToolCallID,
					"answers":    answers,
				},
			})
			return agent.AskUserResponse{Answers: answers}, nil
		case <-ctx.Done():
			return agent.AskUserResponse{}, ctx.Err()
		}
	}

	onToolCall := options.OnToolCall
	options.OnToolCall = func(call agent.ToolCall) {
		callSeq[call.ID]++
		row := transcriptRow{
			kind:   rowToolCall,
			id:     effectiveToolRowID(call.ID, callSeq[call.ID]),
			text:   "tool call: " + call.Name,
			tool:   call.Name,
			detail: argHint(call.Arguments),
			arg:    argHintSecondary(call.Arguments),
			runID:  runID,
		}
		if !toolCardSuppressedInTranscript(call.Name) {
			m.sendAgentRow(runID, row)
		}
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type: sessions.EventToolCall,
			Payload: map[string]any{
				"id":        call.ID,
				"name":      call.Name,
				"arguments": call.Arguments,
			},
		})
		if store != nil && sessionID != "" {
			var args map[string]any
			if call.Arguments != "" {
				_ = json.Unmarshal([]byte(call.Arguments), &args)
			}
			if targets := tools.MutationTargets(cwd, call.Name, args); len(targets) > 0 {
				if payload, ok := store.SnapshotForCheckpoint(sessionID, cwd, call.Name, targets); ok {
					sessionEvents = append(sessionEvents, pendingSessionEvent{
						Type:    sessions.EventSessionCheckpoint,
						Payload: payload,
					})
				}
			}
		}
		if onToolCall != nil {
			onToolCall(call)
		}
	}

	onToolResult := options.OnToolResult
	options.OnToolResult = func(result agent.ToolResult) {
		row := transcriptRow{
			kind:         rowToolResult,
			id:           effectiveToolRowID(result.ToolCallID, callSeq[result.ToolCallID]),
			text:         toolResultRowText(result),
			tool:         result.Name,
			status:       result.Status,
			detail:       toolResultDetail(result),
			runID:        runID,
			changedFiles: result.ChangedFiles,
		}
		if !toolCardSuppressedInTranscript(result.Name) {
			m.sendAgentRow(runID, row)
		}
		payload := map[string]any{
			"toolCallId": result.ToolCallID,
			"name":       result.Name,
			"status":     string(result.Status),
			"output":     result.Output,
		}
		if result.Redacted {
			payload["redacted"] = true
		}
		if len(result.Meta) > 0 {
			payload["meta"] = result.Meta
		}
		if len(result.ChangedFiles) > 0 {
			payload["changedFiles"] = result.ChangedFiles
		}
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type:    sessions.EventToolResult,
			Payload: payload,
		})
		if onToolResult != nil {
			onToolResult(result)
		}
	}

	onPermission := options.OnPermission
	options.OnPermission = func(event agent.PermissionEvent) {
		if permissionEventIsNoteworthy(event) {
			row := permissionTranscriptRow(event)
			row.runID = runID
			m.sendAgentRow(runID, row)
		}
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type:    tuiPermissionEventType(event),
			Payload: event,
		})
		if onPermission != nil {
			onPermission(event)
		}
	}
	options = (runtimeWiring{runID: runID, send: m.runtimeMessageSink}).decorate(options)

	options.EstimateUsageCost = estimator
	onAttributedUsage := options.OnAttributedUsage
	options.OnAttributedUsage = func(attributed agent.AttributedUsage) {
		cost := attributed.Cost
		if cost.Status == "" {
			cost = estimator(attributed.Model, attributed.Usage, attributed.UsageReported)
			attributed.Cost = cost
		}
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type:    sessions.EventUsage,
			Payload: usage.AttributedUsagePayload(attributed),
		})
		m.sendAgentUsage(runID, attributed.Model, attributed.Usage, &cost)
		if onAttributedUsage != nil {
			onAttributedUsage(attributed)
		}
	}

	// Resolve memory (best-effort, same as the normal pipeline path).
	memClient, _ := tuiResolveMemory(runCtx)
	var mem splicerun.MemoryStore
	if memClient != nil {
		mem = memClient
	}

	// Build the start callback: records task_started before the task dispatches,
	// so the accumulated event order matches execution when it is persisted.
	onTaskStart := func(task schemas.Task, taskRunID string) {
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type:    sessions.EventTaskStarted,
			Payload: splicerun.TaskStartedPayload{TaskID: task.ID, RunID: taskRunID},
		})
	}

	// Build the lifecycle callback: records task events in execution order.
	onTaskLifecycle := func(task schemas.Task, taskRunID string, pipelineResult schemas.PipelineResult) {
		if pipelineResult.Status == "completed" {
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventTaskCompleted,
				Payload: splicerun.TaskCompletedPayload{TaskID: task.ID, RunID: taskRunID},
			})
		} else {
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventTaskFailed,
				Payload: splicerun.TaskFailedPayload{TaskID: task.ID, RunID: taskRunID},
			})
		}
	}

	return m, tea.Batch(
		func() tea.Msg {
			prepared, notice, bindErr := m.bindPipelineWorktree(runCtx, &options)
			if bindErr != nil {
				return planExecutionResultMsg{runID: runID, err: bindErr, store: store, sessionID: sessionID, sessionEvents: sessionEvents, worktreeNotice: notice}
			}
			var preparedPtr *worktrees.Result
			if prepared.Path != "" {
				copy := prepared
				preparedPtr = &copy
			}
			if m.captureRunOptions != nil {
				m.captureRunOptions(options)
			}
			result, err := splicerun.RunDesignPlanWithResume(runCtx, plan, provider, options, mem, nil, splicerun.RunDesignPlanOptions{
				PlanID:          planID,
				OnTaskStart:     onTaskStart,
				OnTaskLifecycle: onTaskLifecycle,
			})
			if store != nil && sessionID != "" {
				inputs := make([]sessions.AppendEventInput, 0, len(sessionEvents))
				for _, event := range flushableSessionEvents(sessionEvents) {
					inputs = append(inputs, sessions.AppendEventInput{Type: event.Type, Payload: event.Payload})
				}
				if len(inputs) > 0 {
					if _, appendErr := store.AppendEvents(sessionID, inputs); appendErr != nil {
						if err != nil {
							err = fmt.Errorf("%w; persist approve events: %v", err, appendErr)
						} else {
							err = fmt.Errorf("persist approve events: %w", appendErr)
						}
					}
				}
			}
			return planExecutionResultMsg{runID: runID, result: result, err: err, store: store, sessionID: sessionID, sessionEvents: sessionEvents, worktree: preparedPtr, worktreeNotice: notice, sourceDirty: inspectSourceDirty(prepared)}
		},
		m.spinner.Tick,
	)
}

// currentPlanID returns the persisted PlanID of the current reconstructed
// design revision, or "" when no revision has been persisted. Reusing it
// across re-crystallizations keeps the Revision counter monotonic and lets
// approval record the exact plan the user reviewed.
func (m model) currentPlanID() (string, error) {
	state, err := splicerun.ReconstructDesignState(m.sessionEvents)
	if err != nil {
		return "", fmt.Errorf("reconstruct current design plan: %w", err)
	}
	return state.Revision.PlanID, nil
}

// startDesignTransition dispatches a queued design-agent transition to the
// same typed controllers the manual commands use. It is called from the
// agentResponseMsg update path after the design turn succeeds and pending
// clears, so the transition never nests inside the agent run.
func (m model) startDesignTransition(req splicerun.DesignTransitionRequest) (model, tea.Cmd) {
	if err := req.Validate(); err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Design transition error: " + err.Error()})
		return m, nil
	}
	switch req.Action {
	case splicerun.DesignTransitionCrystallize:
		return m.startCrystallization(req.Source, req.ApproveIfReady)
	case splicerun.DesignTransitionApprove:
		return m.startApproval(req.Source)
	default:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Unknown design transition action: " + string(req.Action)})
		return m, nil
	}
}

func (m model) handleCrystallizeCommand() (model, tea.Cmd) {
	return m.startCrystallization(splicerun.DesignTransitionSourceManual, false)
}

// startCrystallization validates the crystallization preconditions, then
// begins the crystallization and critique run. source distinguishes a manual
// /crystallize from an agent-requested one for the transcript and the
// persisted lifecycle audit. approveIfReady schedules a follow-up approval
// only after a clean, successful critique with no must-fix issue.
func (m model) startCrystallization(source splicerun.DesignTransitionSource, approveIfReady bool) (model, tea.Cmd) {
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

	if err := source.Validate(); err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot crystallize: " + err.Error()})
		return m, nil
	}
	// Reuse the current reconstructed PlanID across re-crystallizations so the
	// Revision counter increments instead of restarting at 1 every time.
	planID, err := m.currentPlanID()
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendError, text: "Cannot crystallize: " + err.Error()})
		return m, nil
	}
	if planID == "" {
		planID = "plan-" + strconv.FormatInt(m.now().UnixNano(), 16)
	}
	if source == splicerun.DesignTransitionSourceAgent {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "The design agent requested crystallization."})
	} else {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "/crystallize"})
	}
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
			wf := splicerun.NewDesignWorkflow(store, sessionID, planID).WithPrimarySelection(m.providerName, m.modelName, string(m.reasoningEffort)).WithLiveState(m.pendingPlan, m.pendingCritique).WithSource(source)
			// Both design stages run sequentially inside this one goroutine. The
			// callback appends therefore need no mutex.
			sessionEvents := []pendingSessionEvent{}
			estimator := usage.NewCostEstimator(&m.modelCatalog)
			streamFactory := splicerun.StageStreamFactory(func(stageName string, selection agent.ModelSelection) zeroruntime.CollectOptions {
				return zeroruntime.CollectOptions{
					OnToolCallStart: func(id, name string) {
						m.sendToolCallStreamStart(runID, id, name)
					},
					OnToolCallDelta: func(id, fragment string) {
						m.sendToolCallStreamDelta(runID, id, fragment)
					},
					OnReasoning: func(delta string) {
						m.sendAgentReasoning(runID, delta)
					},
					OnUsage: func(event zeroruntime.Usage) {
						cost := estimator(selection.Model, event, true)
						attributed := agent.AttributedUsage{
							Usage:         event,
							UsageReported: true,
							ProviderName:  selection.ProviderName,
							Model:         selection.Model,
							Stage:         stageName,
							Cost:          cost,
						}
						sessionEvents = append(sessionEvents, pendingSessionEvent{
							Type:    sessions.EventUsage,
							Payload: usage.AttributedUsagePayload(attributed),
						})
						m.sendAgentUsage(runID, selection.Model, event, &cost)
					},
				}
			})
			plan, critique, err := wf.CrystallizeAndCritique(runCtx, events, provider, resolver, streamFactory, cwd, nil)
			return crystallizeResultMsg{runID: runID, plan: plan, critique: critique, err: err, store: store, sessionID: sessionID, sessionEvents: sessionEvents, source: source, approveIfReady: approveIfReady}
		},
		m.spinner.Tick,
	)
}

type crystallizeResultMsg struct {
	runID         int
	plan          schemas.DesignPlan
	critique      schemas.PlanCritique
	err           error
	store         *sessions.Store
	sessionID     string
	sessionEvents []pendingSessionEvent
	// source and approveIfReady carry the transition context so the update path
	// can label the lifecycle events and auto-approve a clean critique.
	source         splicerun.DesignTransitionSource
	approveIfReady bool
}

type planExecutionResultMsg struct {
	runID          int
	result         agent.Result
	err            error
	store          *sessions.Store
	sessionID      string
	sessionEvents  []pendingSessionEvent
	worktree       *worktrees.Result
	worktreeNotice string
	sourceDirty    bool
}

// designCoverageWarning reports plan fields that the conversation did not
// settle. Empty optional fields can be correct, so this note never blocks
// review or approval.
func designCoverageWarning(plan schemas.DesignPlan) string {
	var unsettled []string
	if len(plan.OutOfScope) == 0 {
		unsettled = append(unsettled, "out of scope")
	}
	if strings.TrimSpace(plan.SystemDesign) == "" {
		unsettled = append(unsettled, "system design")
	}
	for _, task := range plan.Tasks {
		if len(task.AcceptanceFacts) == 0 {
			unsettled = append(unsettled, fmt.Sprintf("acceptance facts for task %q", task.Title))
			continue
		}
		for _, fact := range task.AcceptanceFacts {
			if fact.AutomatedVerification {
				continue
			}
			if fact.VerificationCommand == nil || strings.TrimSpace(*fact.VerificationCommand) == "" {
				unsettled = append(unsettled, fmt.Sprintf("acceptance fact %q on task %q has no automated verification command", fact.Statement, task.Title))
			}
		}
	}
	if len(unsettled) == 0 {
		return ""
	}
	// A plan of this size can carry a dozen unsettled criteria whose statements
	// run to a sentence each, and the note is read at a glance. Name the first
	// few and count the rest.
	const maxNamed = 3
	if extra := len(unsettled) - maxNamed; extra > 0 {
		unsettled = append(unsettled[:maxNamed:maxNamed], fmt.Sprintf("and %d more", extra))
	}
	return "Design coverage note: the conversation did not settle these: " + strings.Join(unsettled, "; ") + "."
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
			if len(t.AcceptanceFacts) == 0 {
				continue
			}
			b.WriteString("   Acceptance facts:\n")
			// A criterion carrying a command will be executed, so it is always
			// shown: eliding one would hide shell the user is about to approve.
			// Only the criteria nobody automated are capped, and those run
			// nothing.
			const maxRenderedManualFacts = 3
			manual := 0
			for _, fact := range t.AcceptanceFacts {
				command := ""
				if fact.AutomatedVerification && fact.VerificationCommand != nil {
					command = strings.TrimSpace(*fact.VerificationCommand)
				}
				if command == "" {
					manual++
					if manual > maxRenderedManualFacts {
						continue
					}
					b.WriteString("   - " + fact.Statement + "\n")
					continue
				}
				b.WriteString("   - " + fact.Statement + "\n")
				b.WriteString("     Automated verification command: " + command + "\n")
			}
			if extra := manual - maxRenderedManualFacts; extra > 0 {
				b.WriteString(fmt.Sprintf("   ... and %d more acceptance facts with no command\n", extra))
			}
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
	lines = append(lines, taskGraphLines(*m.pendingPlan, width-4)...)
	return borderedBlock(width, lines)
}

// taskGraphLines renders the plan's task dependency DAG as ASCII art for the
// plan panel. The panel re-renders at View time with a real width, so box art
// is safe here (static transcript rows rewrap and would mangle it). Rendering
// is deterministic and token-free; failures degrade to one faint line instead
// of failing the header.
func taskGraphLines(plan schemas.DesignPlan, width int) []string {
	if len(plan.Tasks) == 0 {
		return nil
	}
	graph, err := splicerun.TaskGraphFromPlan(plan)
	if err == nil {
		var art string
		art, err = splicerun.RenderDiagram(graph, width)
		if err == nil {
			return append([]string{"", zeroTheme.faint.Render("Task graph")}, strings.Split(art, "\n")...)
		}
	}
	return []string{"", zeroTheme.faint.Render("Task graph unavailable: " + err.Error())}
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

// designConversationContext builds real user and assistant messages from the
// current design epoch. It also returns the complete typed plan and critique
// for injection into the system prompt. The final current-user event is not a
// prior message because agent.Run adds it as the last turn.
func designConversationContext(events []sessions.Event, livePlan *schemas.DesignPlan, liveCritique *schemas.PlanCritique) ([]zeroruntime.Message, string, error) {
	if len(events) == 0 {
		return nil, "", nil
	}
	prior := events
	if last := events[len(events)-1]; last.Type == sessions.EventMessage {
		var msg struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(last.Payload, &msg); err == nil && msg.Role == "user" {
			prior = events[:len(events)-1]
		}
	}
	ctx, err := splicerun.AssembleDesignContext(prior, livePlan, liveCritique)
	if err != nil {
		return nil, "", err
	}
	messages := make([]zeroruntime.Message, 0, len(ctx.History))
	for _, message := range ctx.History {
		messages = append(messages, zeroruntime.Message{
			Role:    zeroruntime.MessageRole(message.Role),
			Content: message.Content,
		})
	}
	state := ctx.State
	state.Plan = ctx.CurrentPlan
	state.Critique = ctx.CurrentCritique
	stateMessage, err := workflowStateMessage(state)
	if err != nil {
		return nil, "", err
	}
	return messages, stateMessage, nil
}

// workflowStateMessage serializes the current design state for the live design
// agent. It includes execution outcomes so later turns know which tasks ran.
func workflowStateMessage(state splicerun.DesignState) (string, error) {
	if state.Plan == nil && state.Critique == nil && len(state.TaskOutcomes) == 0 {
		return "", nil
	}
	modelState := struct {
		Phase           schemas.DesignPhase              `json:"phase,omitempty"`
		CurrentPlan     *schemas.DesignPlan              `json:"current_plan,omitempty"`
		CurrentCritique *schemas.PlanCritique            `json:"current_critique,omitempty"`
		TaskOutcomes    map[string]schemas.TaskRunStatus `json:"task_outcomes,omitempty"`
	}{
		Phase:           state.Phase,
		CurrentPlan:     state.Plan,
		CurrentCritique: state.Critique,
		TaskOutcomes:    state.TaskOutcomes,
	}
	payload, err := json.MarshalIndent(modelState, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal design revision context: %w", err)
	}
	return "<design_revision_context>\nPreserve settled plan fields and address every critique.\n" + string(payload) + "\n</design_revision_context>", nil
}

// designTransitionRegistry registers the design-tool transition tools on a
// freshly-built design registry. approve_design is exposed only when
// approveAvailable is true; crystallize_design is always exposed. Both share
// one per-turn recorder so the design agent queues at most one transition.
func designTransitionRegistry(registry *tools.Registry, approveAvailable bool, transition *splicerun.DesignTransitionRecorder) {
	if approveAvailable {
		registry.Register(splicerun.NewApproveDesignTool(transition))
	}
	registry.Register(splicerun.NewCrystallizeDesignTool(transition))
}

func designConversationRegistry(registry *tools.Registry) *tools.Registry {
	filtered := tools.NewRegistry()
	if registry == nil {
		return filtered
	}
	// Read-only tools are selected from their safety metadata so new read-only
	// tools enter design mode without a second hand-maintained name map.
	for _, tool := range registry.All() {
		if tool.Safety().SideEffect == tools.SideEffectRead {
			filtered.Register(tool)
		}
	}
	// These tools are safe design-mode additions, but are not core read tools.
	for _, name := range []string{"skill", "web_fetch", tools.ToolSearchToolName} {
		if tool, ok := registry.Get(name); ok {
			filtered.Register(tool)
		}
	}
	// request_permissions has no side effect until the runtime handles it. Keep
	// it available so the design agent can ask for access when a user decides.
	if tool, ok := registry.Get(tools.RequestPermissionsToolName); ok {
		filtered.Register(tool)
	}
	return filtered
}
