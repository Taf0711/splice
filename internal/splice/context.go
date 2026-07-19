package splice

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

const (
	listToolName  = "list_directory"
	readToolName  = "read_file"
	grepToolName  = "grep"
	listRecDepth  = 5
	listRecPath   = "."
	listRecActive = true
)

// FulfillContextRequest fulfills a bounded context request through deterministic tools.
func FulfillContextRequest(ctx context.Context, request schemas.ContextRequest, runner ToolRunner) (schemas.ContextBundle, error) {
	if err := request.Validate(); err != nil {
		return schemas.ContextBundle{}, err
	}
	items := make([]schemas.ContextItem, 0, len(request.Queries))
	for _, query := range request.Queries {
		item, err := fulfillQuery(ctx, query, runner)
		if err != nil {
			return schemas.ContextBundle{}, fmt.Errorf("query type %s: %w", query.QueryType, err)
		}
		items = append(items, item)
	}
	return schemas.ContextBundle{Request: request, Items: items}, nil
}

func fulfillQuery(ctx context.Context, query schemas.ContextQuery, runner ToolRunner) (schemas.ContextItem, error) {
	switch query.QueryType {
	case schemas.ContextListFiles:
		return fulfillListFiles(ctx, query, runner)
	case schemas.ContextReadFile:
		return fulfillReadFile(ctx, query, runner)
	case schemas.ContextOutline:
		return fulfillOutline(ctx, query, runner)
	case schemas.ContextSearch:
		return fulfillSearch(ctx, query, runner)
	case schemas.ContextFindSymbol:
		return fulfillFindSymbol(ctx, query, runner)
	case schemas.ContextGetSymbol:
		return fulfillGetSymbol(query)
	default:
		return contextErrorItem(query, fmt.Sprintf("unknown context query type %q", query.QueryType)), nil
	}
}

func contextErrorItem(query schemas.ContextQuery, message string) schemas.ContextItem {
	summary := "Context query failed."
	return schemas.ContextItem{
		Query:   query,
		Summary: summary,
		Payload: map[string]any{"text": message},
		Error:   &message,
	}
}

func fulfillListFiles(ctx context.Context, query schemas.ContextQuery, runner ToolRunner) (schemas.ContextItem, error) {
	res, err := runner.RunTool(ctx, listToolName, map[string]any{
		"path":      listRecPath,
		"recursive": listRecActive,
		"max_depth": listRecDepth,
	})
	if err != nil {
		return schemas.ContextItem{}, err
	}
	if !res.OK {
		return contextErrorItem(query, res.Output), nil
	}
	// Deviations from Python's flat relative listing preserved in memory:
	// - Entries are indented base names (indentation conveys hierarchy).
	// - Content below max_depth=5 is absent without a truncation signal.
	trimmed, truncated := trimToFileLines(res.Output, query.MaxResults)
	emptyDir := res.Output == "" || strings.HasPrefix(res.Output, "Directory is empty")
	if emptyDir {
		trimmed = res.Output
		truncated = false
	}
	return schemas.ContextItem{
		Query:     query,
		Summary:   fmt.Sprintf("Listed directory contents (%d file lines).", countFileLines(trimmed)),
		Payload:   map[string]any{"text": trimmed},
		Truncated: truncated || res.Truncated,
	}, nil
}

func fulfillReadFile(ctx context.Context, query schemas.ContextQuery, runner ToolRunner) (schemas.ContextItem, error) {
	if query.Path == nil || *query.Path == "" {
		return schemas.ContextItem{}, fmt.Errorf("read_file requires path")
	}
	res, err := runner.RunTool(ctx, readToolName, map[string]any{"path": *query.Path})
	if err != nil {
		return schemas.ContextItem{}, err
	}
	if !res.OK {
		return contextErrorItem(query, res.Output), nil
	}
	text, truncated := truncateRunes(res.Output, query.MaxChars)
	return schemas.ContextItem{
		Query:     query,
		Summary:   fmt.Sprintf("Read %s.", *query.Path),
		Payload:   map[string]any{"text": text},
		Truncated: truncated || res.Truncated,
	}, nil
}

func fulfillOutline(ctx context.Context, query schemas.ContextQuery, runner ToolRunner) (schemas.ContextItem, error) {
	if query.Path == nil || *query.Path == "" {
		return schemas.ContextItem{}, fmt.Errorf("outline requires path")
	}
	// Deterministic approximation: grep for top-level declarations.
	// AST-based outline is deferred per the F-Zero plan Problem 7.
	pattern := `^(func |type |def |class |async def )`
	res, err := runner.RunTool(ctx, grepToolName, map[string]any{
		"pattern":     pattern,
		"path":        *query.Path,
		"output_mode": "content",
		"head_limit":  query.MaxResults,
	})
	if err != nil {
		return schemas.ContextItem{}, err
	}
	if !res.OK {
		return contextErrorItem(query, res.Output), nil
	}
	text, truncated := truncateRunes(res.Output, query.MaxChars)
	return schemas.ContextItem{
		Query:     query,
		Summary:   fmt.Sprintf("Pattern-based outline of %s (top-level declarations).", *query.Path),
		Payload:   map[string]any{"text": text},
		Truncated: truncated || res.Truncated,
	}, nil
}

func fulfillSearch(ctx context.Context, query schemas.ContextQuery, runner ToolRunner) (schemas.ContextItem, error) {
	if query.Pattern == nil || *query.Pattern == "" {
		return schemas.ContextItem{}, fmt.Errorf("search requires pattern")
	}
	pattern := *query.Pattern
	if !query.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	res, err := runner.RunTool(ctx, grepToolName, map[string]any{
		"pattern":     pattern,
		"path":        listRecPath,
		"output_mode": "content",
		"head_limit":  query.MaxResults,
	})
	if err != nil {
		return schemas.ContextItem{}, err
	}
	if !res.OK {
		return contextErrorItem(query, res.Output), nil
	}
	text, truncated := truncateRunes(res.Output, query.MaxChars)
	return schemas.ContextItem{
		Query:     query,
		Summary:   fmt.Sprintf("Found search matches for pattern (up to %d).", query.MaxResults),
		Payload:   map[string]any{"text": text},
		Truncated: truncated || res.Truncated,
	}, nil
}

func fulfillFindSymbol(ctx context.Context, query schemas.ContextQuery, runner ToolRunner) (schemas.ContextItem, error) {
	if query.Symbol == nil || *query.Symbol == "" {
		return schemas.ContextItem{}, fmt.Errorf("find_symbol requires symbol")
	}
	pattern := `\b` + regexp.QuoteMeta(*query.Symbol) + `\b`
	res, err := runner.RunTool(ctx, grepToolName, map[string]any{
		"pattern":     pattern,
		"path":        listRecPath,
		"output_mode": "content",
		"head_limit":  query.MaxResults,
	})
	if err != nil {
		return schemas.ContextItem{}, err
	}
	if !res.OK {
		return contextErrorItem(query, res.Output), nil
	}
	text, truncated := truncateRunes(res.Output, query.MaxChars)
	return schemas.ContextItem{
		Query:     query,
		Summary:   fmt.Sprintf("Found symbol locations for %s.", *query.Symbol),
		Payload:   map[string]any{"text": text},
		Truncated: truncated || res.Truncated,
	}, nil
}

func fulfillGetSymbol(query schemas.ContextQuery) (schemas.ContextItem, error) {
	message := "get_symbol requires AST inspection, deferred for v1; use find_symbol + read_file"
	return contextErrorItem(query, message), nil
}

func countFileLines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasSuffix(trimmed, "/") || strings.HasPrefix(trimmed, "Contents of ") {
			continue
		}
		count++
	}
	return count
}

func trimToFileLines(text string, max int) (string, bool) {
	lines := strings.Split(text, "\n")
	count := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasSuffix(trimmed, "/") || strings.HasPrefix(trimmed, "Contents of ") {
			// Header, empty, and directory lines are preserved in context but do not count toward the limit.
			if trimmed == "" && count >= max {
				return strings.Join(lines[:i], "\n"), true
			}
			continue
		}
		if count == max-1 {
			return strings.Join(lines[:i+1], "\n"), true
		}
		count++
	}
	return text, false
}

func truncateRunes(text string, max int) (string, bool) {
	if max <= 0 {
		return text, false
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text, false
	}
	return string(runes[:max]), true
}
