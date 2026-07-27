package agenteval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/streamjson"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type AgentRunInput struct {
	TaskID        string
	Prompt        string
	WorkspacePath string
	Model         string
}

// StageBreakdown is the per-stage token/cost ledger parsed from a pipeline
// run's final event. It mirrors the relevant fields of schemas.StageRecord
// without coupling agenteval (Zero substrate) to the Splice pipeline layer.
// Non-pipeline agents produce no stages (nil).

type AgentRunResult struct {
	ExitCode          int     `json:"exitCode"`
	Stdout            string  `json:"stdout,omitempty"`
	Stderr            string  `json:"stderr,omitempty"`
	Error             string  `json:"error,omitempty"`
	InputTokens       int     `json:"inputTokens,omitempty"`
	OutputTokens      int     `json:"outputTokens,omitempty"`
	CachedInputTokens int     `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  int     `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens   int     `json:"reasoningTokens,omitempty"`
	CostUSD           float64 `json:"costUsd,omitempty"`
	LatencyMs         int64   `json:"latencyMs,omitempty"`
	// Truncated is set when captured stdout/stderr exceeded the runner's
	// OutputLimit and some output was dropped.
	Truncated bool `json:"truncated,omitempty"`
	// Stages carries per-stage token/cost breakdown when the agent is the
	// Splice pipeline (parsed from the stream-json final event's PipelineResult).
	Stages []StageBreakdown `json:"stages,omitempty"`
	// UsageSamples retains each usage event parsed while the agent ran. Existing
	// accounting fields continue to be populated from Stdout for compatibility.
	UsageSamples []UsageSample `json:"usageSamples,omitempty"`
}

// UsageSample is one usage event emitted by an agent's stream-json output.
// PromptTokens and CompletionTokens preserve the event fields. InputTokens and
// OutputTokens contain the effective values used by the runtime accounting.
type UsageSample struct {
	InputTokens       int     `json:"inputTokens,omitempty"`
	OutputTokens      int     `json:"outputTokens,omitempty"`
	PromptTokens      int     `json:"promptTokens,omitempty"`
	CompletionTokens  int     `json:"completionTokens,omitempty"`
	CachedInputTokens int     `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  int     `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens   int     `json:"reasoningTokens,omitempty"`
	CostUSD           float64 `json:"costUsd,omitempty"`
	Stage             string  `json:"stage,omitempty"`
	Iteration         *int    `json:"iteration,omitempty"`
	UsageSequence     *int    `json:"usageSequence,omitempty"`
}

type StageBreakdown struct {
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	Model        string  `json:"model,omitempty"`
	TokensInput  int     `json:"tokens_input"`
	TokensOutput int     `json:"tokens_output"`
	TokensCached int     `json:"tokens_cached"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyMs    int64   `json:"latency_ms"`
}

type AgentRunner interface {
	Run(context.Context, AgentRunInput) AgentRunResult
}

type AgentRunnerFunc func(context.Context, AgentRunInput) AgentRunResult

func (fn AgentRunnerFunc) Run(ctx context.Context, input AgentRunInput) AgentRunResult {
	return fn(ctx, input)
}

// defaultAgentOutputLimit caps captured stdout/stderr per stream so a chatty or
// runaway agent cannot exhaust memory or bloat the benchmark report.
const defaultAgentOutputLimit = 8 << 20 // 8 MiB per stream (pipeline runs emit hundreds of reasoning deltas)

// defaultAgentJSONLLineLimit bounds the incremental collector's partial line.
// It matches the diagnostic output limit while allowing many small events.
const defaultAgentJSONLLineLimit = 8 << 20

var errUsageJSONLLineTooLong = errors.New("JSONL line exceeds the collector limit")

type CommandAgentRunner struct {
	Command []string
	// OutputLimit caps captured stdout/stderr per stream in bytes. Splice applies
	// defaultAgentOutputLimit; a negative value disables the cap.
	OutputLimit int
}

func (runner CommandAgentRunner) Run(ctx context.Context, input AgentRunInput) AgentRunResult {
	result := AgentRunResult{ExitCode: -1}
	if len(runner.Command) == 0 || strings.TrimSpace(runner.Command[0]) == "" {
		result.Error = "agent command is required"
		return result
	}
	if strings.TrimSpace(input.WorkspacePath) == "" {
		result.Error = "workspace path is required"
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limit := runner.OutputLimit
	if limit == 0 {
		limit = defaultAgentOutputLimit
	}
	command := expandAgentCommand(runner.Command, input)
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = input.WorkspacePath
	stdout := &capWriter{limit: limit}
	stderr := &capWriter{limit: limit}
	collector := newUsageCollector(defaultAgentJSONLLineLimit)
	cmd.Stdout = io.MultiWriter(stdout, collector)
	cmd.Stderr = stderr

	started := time.Now()
	err := cmd.Run()
	if flushErr := collector.Flush(); flushErr != nil && collector.err == nil {
		collector.err = flushErr
	}
	result.LatencyMs = elapsedMillis(time.Since(started))
	result.Stdout = stdout.buf.String()
	result.Stderr = stderr.buf.String()
	result.Truncated = stdout.truncated || stderr.truncated
	result.UsageSamples = collector.samples
	populateAgentRunUsage(&result)
	if len(result.Stages) == 0 && len(collector.finalStages) > 0 {
		result.Stages = collector.finalStages
	}
	if err == nil {
		result.ExitCode = 0
	} else {
		// A canceled or timed-out context kills the process, surfacing as a signal
		// exit; report the context error explicitly instead of "exited with code -1".
		if ctxErr := ctx.Err(); ctxErr != nil {
			result.Error = ctxErr.Error()
		} else {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.Error = err.Error()
			}
		}
	}
	if collector.err != nil {
		result.Error = collector.err.Error()
	}
	return result
}

func parseUsageFromStdout(stdout string) (inputTokens, outputTokens, cachedInput, cacheWrite, reasoning int) {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event streamjson.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type != streamjson.EventUsage {
			continue
		}
		usage := usageFromEvent(event)
		inputTokens += usage.EffectiveInputTokens()
		outputTokens += usage.EffectiveOutputTokens()
		cachedInput += usage.CachedInputTokens
		cacheWrite += usage.CacheWriteTokens
		reasoning += usage.ReasoningTokens
	}
	return inputTokens, outputTokens, cachedInput, cacheWrite, reasoning
}

func usageFromEvent(event streamjson.Event) zeroruntime.Usage {
	return zeroruntime.Usage{
		PromptTokens:      intValue(event.PromptTokens),
		CompletionTokens:  intValue(event.CompletionTokens),
		CachedInputTokens: intValue(event.CachedInputTokens),
		CacheWriteTokens:  intValue(event.CacheWriteTokens),
		ReasoningTokens:   intValue(event.ReasoningTokens),
	}
}

func populateAgentRunUsage(result *AgentRunResult) {
	if result == nil {
		return
	}
	inputTokens, outputTokens, cachedInput, cacheWrite, reasoning := parseUsageFromStdout(result.Stdout)
	result.InputTokens = inputTokens
	result.OutputTokens = outputTokens
	result.CachedInputTokens = cachedInput
	result.CacheWriteTokens = cacheWrite
	result.ReasoningTokens = reasoning
	result.Stages = parsePipelineStagesFromStdout(result.Stdout)
}

// parsePipelineStagesFromStdout extracts the per-stage token/cost ledger from
// a pipeline run's stream-json final event. The final event's text field
// carries the PipelineResult JSON (with Stages []StageRecord). Non-pipeline
// agents or failed runs produce nil (graceful no-op).

// parsePipelineStagesFromStdout extracts the per-stage token/cost ledger from
// a pipeline run's stream-json final event. The final event's text field
// carries the PipelineResult JSON (with Stages []StageRecord). Non-pipeline
// agents or failed runs produce nil (graceful no-op).
func parsePipelineStagesFromStdout(stdout string) []StageBreakdown {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		if stages := parsePipelineStagesFromJSONLLine([]byte(line)); len(stages) > 0 {
			return stages
		}
	}
	return nil
}

func parsePipelineStagesFromJSONLLine(line []byte) []StageBreakdown {
	var event struct {
		Type streamjson.EventType `json:"type"`
		Text string               `json:"text"`
	}
	if err := json.Unmarshal(line, &event); err != nil || event.Type != streamjson.EventFinal {
		return nil
	}
	var result struct {
		Stages []StageBreakdown `json:"stages"`
	}
	if err := json.Unmarshal([]byte(event.Text), &result); err != nil {
		return nil
	}
	return result.Stages
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func elapsedMillis(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	milliseconds := duration.Milliseconds()
	if milliseconds == 0 {
		return 1
	}
	return milliseconds
}

func expandAgentCommand(command []string, input AgentRunInput) []string {
	// strings.NewReplacer performs a single left-to-right pass and never
	// re-scans replaced text, so a placeholder value that itself contains
	// "{workspace}"/"{task_id}"/"{model}" is not re-expanded.
	replacer := strings.NewReplacer(
		"{prompt}", input.Prompt,
		"{workspace}", input.WorkspacePath,
		"{task_id}", input.TaskID,
		"{model}", input.Model,
	)
	expanded := make([]string, len(command))
	for i, arg := range command {
		expanded[i] = replacer.Replace(arg)
	}
	return expanded
}

// capWriter buffers writes up to limit bytes and records whether any data was
// dropped. A non-positive limit means unbounded.
type capWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

type usageCollector struct {
	limit       int
	partial     []byte
	discarding  bool
	lineNumber  int
	samples     []UsageSample
	finalStages []StageBreakdown
	err         error
}

func newUsageCollector(limit int) *usageCollector {
	return &usageCollector{limit: limit}
}

func (c *usageCollector) Write(p []byte) (int, error) {
	originalLen := len(p)
	for len(p) > 0 {
		if c.discarding {
			newline := bytes.IndexByte(p, '\n')
			if newline < 0 {
				break
			}
			c.lineNumber++
			c.discarding = false
			p = p[newline+1:]
			continue
		}
		newline := bytes.IndexByte(p, '\n')
		if newline < 0 {
			if err := c.appendPartial(p); err != nil {
				c.recordError(err)
				c.discarding = true
				c.partial = nil
			}
			break
		}
		if err := c.appendPartial(p[:newline]); err != nil {
			c.recordError(err)
			c.discarding = true
			c.partial = nil
			c.lineNumber++
			c.discarding = false
			p = p[newline+1:]
			continue
		}
		c.lineNumber++
		c.processLine(c.partial)
		c.partial = c.partial[:0]
		p = p[newline+1:]
	}
	return originalLen, nil
}

func (c *usageCollector) recordError(err error) {
	if c.err == nil {
		c.err = err
	}
}

func (c *usageCollector) appendPartial(p []byte) error {
	needed := len(c.partial) + len(p)
	if c.limit > 0 && needed > c.limit {
		return fmt.Errorf("usage accounting: JSONL line %d exceeds %d-byte limit (%d bytes): %w", c.lineNumber+1, c.limit, needed, errUsageJSONLLineTooLong)
	}
	if c.limit > 0 && cap(c.partial) < needed {
		capacity := cap(c.partial) * 2
		if capacity < needed {
			capacity = needed
		}
		if capacity > c.limit {
			capacity = c.limit
		}
		partial := make([]byte, len(c.partial), capacity)
		copy(partial, c.partial)
		c.partial = partial
	}
	c.partial = append(c.partial, p...)
	return nil
}

func (c *usageCollector) Flush() error {
	if !c.discarding && len(c.partial) > 0 {
		c.lineNumber++
		c.processLine(c.partial)
		c.partial = c.partial[:0]
	}
	return c.err
}

func (c *usageCollector) processLine(line []byte) {
	line = bytes.TrimSpace(bytes.TrimSuffix(line, []byte{'\r'}))
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var event streamjson.Event
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}
	if event.Type == streamjson.EventUsage {
		usage := usageFromEvent(event)
		c.samples = append(c.samples, UsageSample{
			InputTokens:       usage.EffectiveInputTokens(),
			OutputTokens:      usage.EffectiveOutputTokens(),
			PromptTokens:      intValue(event.PromptTokens),
			CompletionTokens:  intValue(event.CompletionTokens),
			CachedInputTokens: usage.CachedInputTokens,
			CacheWriteTokens:  usage.CacheWriteTokens,
			ReasoningTokens:   usage.ReasoningTokens,
			CostUSD:           floatValue(event.CostUSD),
			Stage:             event.Stage,
			Iteration:         event.Iteration,
			UsageSequence:     event.UsageSequence,
		})
	}
	if event.Type == streamjson.EventFinal {
		if stages := parsePipelineStagesFromJSONLLine(line); len(stages) > 0 {
			c.finalStages = stages
		}
	}
}

func floatValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return w.buf.Write(p)
	}
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		if _, err := w.buf.Write(p[:remaining]); err != nil {
			return 0, err
		}
		w.truncated = true
		return len(p), nil
	}
	return w.buf.Write(p)
}
