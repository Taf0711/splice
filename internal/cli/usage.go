package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/usage"
	"github.com/Taf0711/splice/internal/zerogit"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type usageOptions struct {
	json      bool
	days      int
	since     string
	sessionID string
}

// usageEventSet keeps the persisted events paired with the session metadata they
// were read alongside, so cost reconstruction can resolve each event's owning
// model id during aggregation.
type usageEventSet struct {
	events  []sessions.Event
	meta    []sessions.Metadata
	skipped int
}

type usageCoverage struct {
	PersistedCount     int    `json:"persistedCount"`
	ReconstructedCount int    `json:"reconstructedCount"`
	UnpricedCount      int    `json:"unpricedCount"`
	ErrorCount         int    `json:"errorCount"`
	CostCoverage       string `json:"costCoverage"`
}

type usageReportJSON struct {
	usage.Report
	usageCoverage
}

type historicalUsagePayload struct {
	PromptTokens      int      `json:"promptTokens"`
	CompletionTokens  int      `json:"completionTokens"`
	CachedInputTokens int      `json:"cachedInputTokens"`
	CacheWriteTokens  int      `json:"cacheWriteTokens"`
	ReasoningTokens   int      `json:"reasoningTokens"`
	WebSearchRequests int      `json:"webSearchRequests"`
	WebSearchEngine   string   `json:"webSearchEngine"`
	Model             string   `json:"model"`
	CostUSD           *float64 `json:"costUsd"`
	CostStatus        string   `json:"costStatus"`
}

// collectUsageData reads every persisted session's events (optionally limited to
// a single session id) and returns them flattened alongside the matching session
// metadata. It mirrors runSearch's persisted-event traversal over the injected
// session store. Sessions whose event log cannot be read are skipped (and
// counted) so one corrupt session can't abort the whole report.
func collectUsageData(store *sessions.Store, sessionFilter string) (usageEventSet, error) {
	metadata, err := store.List()
	if err != nil {
		return usageEventSet{}, err
	}
	set := usageEventSet{}
	for _, meta := range metadata {
		if sessionFilter != "" && meta.SessionID != sessionFilter {
			continue
		}
		sessionEvents, err := store.ReadEvents(meta.SessionID)
		if err != nil {
			set.skipped++
			continue
		}
		set.meta = append(set.meta, meta)
		set.events = append(set.events, sessionEvents...)
	}
	return set, nil
}

// filterEventsSince drops events whose UTC calendar date precedes the inclusive
// lower bound. An empty since returns the events unchanged.
func filterEventsSince(events []sessions.Event, since string) []sessions.Event {
	if since == "" {
		return events
	}
	filtered := make([]sessions.Event, 0, len(events))
	for _, event := range events {
		if eventUTCDate(event.CreatedAt) >= since {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func classifyUsageCoverage(events []sessions.Event, meta []sessions.Metadata, registry *modelregistry.Registry) (usageCoverage, error) {
	modelBySession := map[string]string{}
	for _, item := range meta {
		modelBySession[item.SessionID] = item.ModelID
	}
	coverage := usageCoverage{CostCoverage: usage.CostCoverageNotApplicable}
	for _, event := range events {
		if event.Type != sessions.EventUsage {
			continue
		}
		var payload historicalUsagePayload
		if len(event.Payload) > 0 {
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return usageCoverage{}, fmt.Errorf("usage event %d: decode payload: %w", event.Sequence, err)
			}
		}
		switch payload.CostStatus {
		case usage.CostStatusPriced:
			if payload.CostUSD == nil || math.IsNaN(*payload.CostUSD) || math.IsInf(*payload.CostUSD, 0) || *payload.CostUSD < 0 {
				return usageCoverage{}, fmt.Errorf("usage event %d: priced cost must be a finite non-negative cost_usd", event.Sequence)
			}
			coverage.PersistedCount++
		case usage.CostStatusUnpriced:
			coverage.UnpricedCount++
		case usage.CostStatusError:
			coverage.ErrorCount++
		case "":
			modelID := payload.Model
			if modelID == "" {
				modelID = modelBySession[event.SessionID]
			}
			if modelID == "" || registry == nil {
				coverage.UnpricedCount++
				continue
			}
			model, err := registry.Require(modelID)
			if err != nil {
				coverage.UnpricedCount++
				continue
			}
			if _, err := modelregistry.CalculateCost(model, zeroruntime.Usage{
				InputTokens:       payload.PromptTokens,
				OutputTokens:      payload.CompletionTokens,
				CachedInputTokens: payload.CachedInputTokens,
				CacheWriteTokens:  payload.CacheWriteTokens,
				ReasoningTokens:   payload.ReasoningTokens,
				WebSearchRequests: payload.WebSearchRequests,
				WebSearchEngine:   payload.WebSearchEngine,
			}); err != nil {
				_, searchPriced, searchRateErr := modelregistry.WebSearchPricingRate(model, payload.WebSearchEngine)
				if payload.WebSearchRequests > 0 && (searchRateErr != nil || !searchPriced) {
					coverage.UnpricedCount++
				} else {
					coverage.ErrorCount++
				}
				continue
			}
			coverage.ReconstructedCount++
		default:
			return usageCoverage{}, fmt.Errorf("usage event %d: unknown cost_status %q", event.Sequence, payload.CostStatus)
		}
	}
	priced := coverage.PersistedCount + coverage.ReconstructedCount
	total := priced + coverage.UnpricedCount + coverage.ErrorCount
	switch {
	case total == 0:
		coverage.CostCoverage = usage.CostCoverageNotApplicable
	case priced == total:
		coverage.CostCoverage = usage.CostCoverageComplete
	case priced > 0:
		coverage.CostCoverage = usage.CostCoveragePartial
	default:
		coverage.CostCoverage = usage.CostCoverageUnavailable
	}
	return coverage, nil
}

// eventUTCDate maps an RFC3339 timestamp to its UTC calendar date (YYYY-MM-DD)
// so the --since/--days cutoff compares against the same UTC day the report
// buckets under. On a parse failure it falls back to the leading-10 slice.
func eventUTCDate(createdAt string) string {
	if parsed, err := time.Parse(time.RFC3339, createdAt); err == nil {
		return parsed.UTC().Format("2006-01-02")
	}
	if len(createdAt) >= 10 {
		return createdAt[:10]
	}
	return createdAt
}

func runUsage(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	subcommand := "report"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		subcommand = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	if subcommand == "help" {
		if err := writeUsageHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if subcommand != "report" {
		return writeExecUsageError(stderr, fmt.Sprintf("unknown usage command %q", subcommand))
	}

	options, help, err := parseUsageArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeUsageHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	set, err := collectUsageData(deps.newSessionStore(), options.sessionID)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	since := usageSinceCutoff(options, deps)
	events := filterEventsSince(set.events, since)

	// The net-LOC column is best-effort garnish on a token report: outside a
	// git repository (or on any git failure) it degrades to splice instead of
	// aborting the entire report.
	diff := zerogit.DiffStat{}
	if workspaceRoot, err := resolveWorkspaceRoot("", deps); err == nil {
		if summary, err := deps.inspectChanges(context.Background(), zerogit.InspectOptions{Cwd: workspaceRoot}); err == nil {
			// The --stat summary line ("N files changed, A insertions(+), B
			// deletions(-)") carries no secret-bearing tokens, so parsing the
			// already-redacted DiffStat returned by zerogit.Inspect is safe.
			diff = zerogit.ParseDiffStat(summary.DiffStat)
		}
	}

	providerProfile := ""
	if workspaceRoot, rootErr := resolveWorkspaceRoot("", deps); rootErr == nil {
		if resolved, configErr := deps.resolveConfig(workspaceRoot, config.Overrides{}); configErr == nil {
			providerProfile = resolved.Provider.Name
		}
	}
	var registry modelregistry.Registry
	var registryErr error
	if strings.TrimSpace(providerProfile) == "" {
		registry, registryErr = modelregistry.DefaultRegistry()
	} else {
		registry, registryErr = modelregistry.DefaultRegistry(providerProfile)
	}
	if registryErr != nil {
		return writeAppError(stderr, registryErr.Error(), exitCrash)
	}
	report, err := usage.BuildReport(events, set.meta, &registry, diff.NetLOC())
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	coverage, err := classifyUsageCoverage(events, set.meta, &registry)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	if options.json {
		if err := writePrettyJSON(stdout, usageReportJSON{Report: report, usageCoverage: coverage}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, FormatReport(report, diff.Insertions, diff.Deletions, coverage)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// usageSinceCutoff resolves the inclusive lower-bound date (YYYY-MM-DD). An
// explicit --since wins; otherwise --days N derives a cutoff relative to the
// injected clock. An empty result means "no lower bound".
func usageSinceCutoff(options usageOptions, deps appDeps) string {
	if strings.TrimSpace(options.since) != "" {
		return options.since
	}
	if options.days > 0 {
		cutoff := deps.now().UTC().AddDate(0, 0, -(options.days - 1))
		return cutoff.Format("2006-01-02")
	}
	return ""
}

func parseUsageArgs(args []string) (usageOptions, bool, error) {
	options := usageOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--days":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			days, err := parsePositiveOrZeroInt(value, "--days")
			if err != nil {
				return options, false, err
			}
			options.days = days
			index = next
		case strings.HasPrefix(arg, "--days="):
			days, err := parsePositiveOrZeroInt(strings.TrimSpace(strings.TrimPrefix(arg, "--days=")), "--days")
			if err != nil {
				return options, false, err
			}
			options.days = days
		case arg == "--since":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			if _, parseErr := time.Parse("2006-01-02", value); parseErr != nil {
				return options, false, execUsageError{fmt.Sprintf("invalid --since %q: expected YYYY-MM-DD", value)}
			}
			options.since = value
			index = next
		case strings.HasPrefix(arg, "--since="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--since="))
			if _, parseErr := time.Parse("2006-01-02", value); parseErr != nil {
				return options, false, execUsageError{fmt.Sprintf("invalid --since %q: expected YYYY-MM-DD", value)}
			}
			options.since = value
		case arg == "--session" || arg == "--session-id":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			if strings.TrimSpace(value) == "" {
				return options, false, execUsageError{fmt.Sprintf("%s requires a value", arg)}
			}
			options.sessionID = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--session="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--session="))
			if value == "" {
				return options, false, execUsageError{"--session requires a value"}
			}
			options.sessionID = value
		case strings.HasPrefix(arg, "--session-id="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--session-id="))
			if value == "" {
				return options, false, execUsageError{"--session-id requires a value"}
			}
			options.sessionID = value
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown usage flag %q", arg)}
		}
	}
	return options, false, nil
}

// FormatReport renders a usage Report as a per-day table plus totals and net-LOC
// efficiency ratios. Cost labels identify persisted and reconstructed estimates.
// Legacy events without a persisted estimate are reconstructed from session
// metadata. Net-LOC is a working-tree diff proxy.
func FormatReport(report usage.Report, insertions int, deletions int, coverage ...usageCoverage) string {
	var builder strings.Builder
	var costLabel = "cost is a reconstructed estimate"
	if len(coverage) > 0 {
		costLabel = historicalCostLabel(coverage[0])
	}
	builder.WriteString("Usage report (" + costLabel + ")\n\n")
	builder.WriteString(fmt.Sprintf("%-12s %10s %14s %14s\n", "date", "requests", "tokens", "est. cost"))
	for _, bucket := range report.Buckets {
		builder.WriteString(fmt.Sprintf("%-12s %10d %14s %14s\n",
			bucket.Date, bucket.Requests, groupThousands(bucket.TotalTokens), formatUSD(bucket.TotalCost)))
	}
	builder.WriteString(fmt.Sprintf("\n%-12s %10d %14s %14s\n",
		"total", report.Total.Requests, groupThousands(report.Total.TotalTokens), formatUSD(report.Total.TotalCost)))
	if len(report.WorkUnits) > 0 {
		workUnits := append([]usage.WorkUnit(nil), report.WorkUnits...)
		sort.SliceStable(workUnits, func(left int, right int) bool {
			if workUnits[left].TotalCost != workUnits[right].TotalCost {
				return workUnits[left].TotalCost > workUnits[right].TotalCost
			}
			if workUnits[left].Model != workUnits[right].Model {
				return workUnits[left].Model < workUnits[right].Model
			}
			if workUnits[left].Stage != workUnits[right].Stage {
				return workUnits[left].Stage < workUnits[right].Stage
			}
			return workUnits[left].Provider < workUnits[right].Provider
		})
		displayWorkUnitValue := func(value string) string {
			if strings.TrimSpace(value) == "" {
				return "-"
			}
			return value
		}
		builder.WriteString("\nwork units:\n")
		builder.WriteString(fmt.Sprintf("%-14s %-21s %-10s %8s %12s %10s\n",
			"stage", "model", "provider", "requests", "tokens", "est. cost"))
		for _, unit := range workUnits {
			builder.WriteString(fmt.Sprintf("%-14s %-21s %-10s %8d %12s %10s\n",
				fitCell(displayWorkUnitValue(unit.Stage), 14),
				fitCell(displayWorkUnitValue(unit.Model), 21),
				fitCell(displayWorkUnitValue(unit.Provider), 10),
				unit.Requests, groupThousands(unit.TotalTokens), formatUSD(unit.TotalCost)))
		}
	}
	if len(coverage) > 0 {
		metrics := coverage[0]
		builder.WriteString(fmt.Sprintf("\npricing: %s (persisted %d, reconstructed %d, unpriced %d, errors %d)\n",
			metrics.CostCoverage, metrics.PersistedCount, metrics.ReconstructedCount, metrics.UnpricedCount, metrics.ErrorCount))
	}

	builder.WriteString(fmt.Sprintf("\nnet LOC (estimate): +%d / -%d = %d\n",
		insertions, deletions, report.NetLOC))
	if report.NetLOCPositive {
		builder.WriteString(fmt.Sprintf("tokens per net LOC: %.1f\n", report.TokensPerNetLOC))
		builder.WriteString(fmt.Sprintf("est. cost per net LOC: %s\n", formatUSD(report.CostPerNetLOC)))
	} else {
		builder.WriteString("tokens per net LOC: n/a (net LOC <= 0)\n")
		builder.WriteString("est. cost per net LOC: n/a (net LOC <= 0)\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func historicalCostLabel(coverage usageCoverage) string {
	switch coverage.CostCoverage {
	case usage.CostCoverageComplete:
		switch {
		case coverage.PersistedCount > 0 && coverage.ReconstructedCount > 0:
			return "cost uses persisted and reconstructed estimates"
		case coverage.PersistedCount > 0:
			return "cost is a persisted estimate"
		case coverage.ReconstructedCount > 0:
			return "cost is a reconstructed estimate"
		}
	case usage.CostCoveragePartial:
		switch {
		case coverage.PersistedCount > 0 && coverage.ReconstructedCount > 0:
			return "cost uses persisted and reconstructed estimates with partial coverage"
		case coverage.PersistedCount > 0:
			return "cost uses persisted estimates with partial coverage"
		case coverage.ReconstructedCount > 0:
			return "cost uses reconstructed estimates with partial coverage"
		}
	}
	return "cost unavailable"
}

func formatUSD(value float64) string {
	formatted, err := modelregistry.FormatCostUSD(value)
	if err != nil {
		return "$0.0000"
	}
	return formatted
}

// fitCell caps a table cell at width display columns, marking a shortened value
// with a trailing "...". Padding verbs widen a cell that is too long instead of
// clipping it, which shifts every later column on that row and shears the table;
// model ids like "deepseek/deepseek-v4-flash" are long enough to do it.
func fitCell(value string, width int) string {
	if runewidth.StringWidth(value) <= width {
		return value
	}
	return runewidth.Truncate(value, width, "...")
}

// groupThousands renders an integer with comma thousands separators, preserving
// a leading minus sign.
func groupThousands(value int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := fmt.Sprintf("%d", value)
	if len(digits) <= 3 {
		return sign + digits
	}
	var out []byte
	prefix := len(digits) % 3
	if prefix == 0 {
		prefix = 3
	}
	out = append(out, digits[:prefix]...)
	for index := prefix; index < len(digits); index += 3 {
		out = append(out, ',')
		out = append(out, digits[index:index+3]...)
	}
	return sign + string(out)
}

func writeUsageHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  splice usage report [flags]

Summarizes token usage and reconstructed (estimated) cost from persisted local
Splice session usage events, plus a working-tree net-LOC efficiency estimate.

Flags:
      --json                 Print JSON report
      --days <number>        Only include the most recent N days
      --since <YYYY-MM-DD>    Only include events on or after this date
      --session <id>         Limit the report to one session
  -h, --help                 Show this help

Cost is reconstructed from session model metadata and is an estimate; net LOC
is a working-tree diff proxy and is labeled as an estimate.
`)
	return err
}
