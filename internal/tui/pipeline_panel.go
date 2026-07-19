package tui

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	stageMarkerBegin = "\x00STAGE"
	stageMarkerEnd   = "\x00"
)

type pipelineStageStatus int

const (
	pipelineStagePending pipelineStageStatus = iota
	pipelineStageRunning
	pipelineStageCompleted
	pipelineStageFailed
	pipelineStageSkipped
)

type pipelineStageRow struct {
	name     string
	status   pipelineStageStatus
	detail   string
	progress int // 0-100
}

type pipelinePanelState struct {
	stages       []pipelineStageRow
	active       bool // true when a pipeline run is in progress
	changedFiles []string
}

type pipelineStageMarkerPayload struct {
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Detail       string   `json:"detail"`
	Progress     int      `json:"progress"`
	ChangedFiles []string `json:"changedFiles"`
}

// applyStageMarker consumes a structured stage marker from the reasoning stream.
func (s *pipelinePanelState) applyStageMarker(line string) bool {
	if !strings.HasPrefix(line, stageMarkerBegin) {
		return false
	}

	payloadText := strings.TrimPrefix(line, stageMarkerBegin)
	if idx := strings.Index(payloadText, stageMarkerEnd); idx >= 0 {
		payloadText = payloadText[:idx]
	}

	var payload pipelineStageMarkerPayload
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil || strings.TrimSpace(payload.Name) == "" {
		return true
	}

	status := pipelineStageStatusFromString(payload.Status)
	progress := payload.Progress
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}

	idx := -1
	for i := range s.stages {
		if s.stages[i].name == payload.Name {
			idx = i
			break
		}
	}
	if idx == -1 {
		s.stages = append(s.stages, pipelineStageRow{name: payload.Name, status: pipelineStagePending})
		idx = len(s.stages) - 1
	}
	s.stages[idx].status = status
	s.stages[idx].detail = payload.Detail
	s.stages[idx].progress = progress
	s.active = true
	if payload.ChangedFiles != nil {
		s.changedFiles = append([]string(nil), payload.ChangedFiles...)
	}
	return true
}

func pipelineStageStatusFromString(status string) pipelineStageStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return pipelineStageRunning
	case "completed":
		return pipelineStageCompleted
	case "failed":
		return pipelineStageFailed
	case "skipped":
		return pipelineStageSkipped
	default:
		return pipelineStagePending
	}
}

// reset clears stage rows and marks a new pipeline run active.
func (s *pipelinePanelState) reset() {
	s.stages = nil
	s.active = true
	s.changedFiles = nil
}

// clear removes all pipeline state and hides the panel.
func (s *pipelinePanelState) clear() {
	s.stages = nil
	s.active = false
	s.changedFiles = nil
}

func (s pipelinePanelState) isEmpty() bool {
	return !s.active || len(s.stages) == 0
}

func (s pipelinePanelState) headerLine(width int) string {
	if s.isEmpty() {
		return sidebarHeader("PIPELINE", width)
	}
	done, total, allDone := s.counts()
	style := zeroTheme.amber
	if allDone {
		style = zeroTheme.green
	}
	return sidebarHeaderWithCount("PIPELINE", fmt.Sprintf("%d/%d", done, total), style, width)
}

func (s pipelinePanelState) renderSection(width int, phase int) []string {
	if s.isEmpty() {
		return nil
	}
	room := maxInt(4, width-3)
	lines := make([]string, 0, len(s.stages)+5)
	var current *pipelineStageRow
	for i := range s.stages {
		stage := s.stages[i]
		glyph, bodyStyle := pipelineStageGlyphAndStyle(stage.status, phase)
		lines = append(lines, " "+glyph+" "+bodyStyle.Render(truncateStep(stage.name, room)))
		if stage.status == pipelineStageRunning && current == nil {
			current = &s.stages[i]
		}
	}
	if current != nil {
		lines = append(lines, "")
		lines = append(lines, zeroTheme.muted.Bold(true).Render("CURRENT"))
		lines = append(lines, " "+zeroTheme.faint.Render("stage: ")+zeroTheme.ink.Render(truncateStep(current.name, maxInt(4, width-8))))
		lines = append(lines, " "+zeroTheme.faint.Render("action: ")+zeroTheme.muted.Render(truncateStep(current.detail, maxInt(4, width-9))))
		if current.progress > 0 {
			lines = append(lines, " "+renderPipelineProgressBar(current.progress, width))
		}
	}
	return lines
}

func (s pipelinePanelState) counts() (done int, total int, allDone bool) {
	total = len(s.stages)
	if total == 0 {
		return 0, 0, false
	}
	allDone = true
	for _, stage := range s.stages {
		switch stage.status {
		case pipelineStageCompleted, pipelineStageFailed, pipelineStageSkipped:
			done++
		default:
			allDone = false
		}
	}
	return done, total, allDone
}

// arcFrames is the running-stage spinner (cli-spinners "arc" cycle), rendered
// in the accent color. The running glyph cycles through these six arcs, one
// frame per shared spinnerPhase tick (~80ms); done stages keep ✓, pending ○.
var arcFrames = []string{"◜", "◠", "◝", "◞", "◡", "◟"}

func pipelineStageGlyphAndStyle(status pipelineStageStatus, phase int) (string, interface{ Render(...string) string }) {
	switch status {
	case pipelineStageCompleted:
		return zeroTheme.green.Render("✓"), zeroTheme.muted
	case pipelineStageRunning:
		return zeroTheme.amber.Render(arcFrames[phase%len(arcFrames)]), zeroTheme.ink
	case pipelineStageFailed:
		return zeroTheme.red.Render("✗"), zeroTheme.red
	case pipelineStageSkipped:
		return zeroTheme.amber.Render("↩"), zeroTheme.muted
	default:
		return zeroTheme.faint.Render("○"), zeroTheme.faint
	}
}

func renderPipelineProgressBar(progress, width int) string {
	barWidth := width - 8
	if barWidth > 16 {
		barWidth = 16
	}
	if barWidth < 4 {
		barWidth = 4
	}
	filled := (progress * barWidth) / 100
	if filled > barWidth {
		filled = barWidth
	}
	bar := zeroTheme.amber.Render(strings.Repeat("█", filled)) + zeroTheme.faint.Render(strings.Repeat("░", barWidth-filled))
	return bar + " " + zeroTheme.faint.Render(fmt.Sprintf("%d%%", progress))
}
