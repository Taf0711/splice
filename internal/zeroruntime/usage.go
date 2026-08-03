package zeroruntime

import (
	"fmt"
	"strings"
)

// NormalizeUsage converts provider token aliases into the shared runtime shape.
func NormalizeUsage(input TokenUsage) (Usage, error) {
	inputTokens, err := providerAlias(input.InputTokens, input.PromptTokens, "input tokens")
	if err != nil {
		return Usage{}, err
	}

	outputTokens, err := providerAlias(input.OutputTokens, input.CompletionTokens, "output tokens")
	if err != nil {
		return Usage{}, err
	}

	cachedInputTokens, err := nonNegative(input.CachedInputTokens, "cached input tokens")
	if err != nil {
		return Usage{}, err
	}
	if cachedInputTokens > inputTokens {
		return Usage{}, fmt.Errorf("cached input tokens %d exceeds input tokens %d", cachedInputTokens, inputTokens)
	}

	cacheWriteTokens, err := nonNegative(input.CacheWriteTokens, "cache write tokens")
	if err != nil {
		return Usage{}, err
	}
	// Cache-read and cache-write are disjoint subsets of the input; together they
	// can never exceed it.
	if cacheWriteTokens > inputTokens-cachedInputTokens {
		return Usage{}, fmt.Errorf("cache write tokens %d plus cached input tokens %d exceeds input tokens %d", cacheWriteTokens, cachedInputTokens, inputTokens)
	}

	reasoningTokens, err := nonNegative(input.ReasoningTokens, "reasoning tokens")
	if err != nil {
		return Usage{}, err
	}
	if reasoningTokens > outputTokens {
		return Usage{}, fmt.Errorf("reasoning tokens %d exceeds output tokens %d", reasoningTokens, outputTokens)
	}
	webSearchRequests, err := nonNegative(input.WebSearchRequests, "web search requests")
	if err != nil {
		return Usage{}, err
	}

	return Usage{
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		PromptTokens:      inputTokens,
		CompletionTokens:  outputTokens,
		CachedInputTokens: cachedInputTokens,
		CacheWriteTokens:  cacheWriteTokens,
		ReasoningTokens:   reasoningTokens,
		WebSearchRequests: webSearchRequests,
		WebSearchEngine:   strings.TrimSpace(input.WebSearchEngine),
	}, nil
}

func providerAlias(primary int, alias int, label string) (int, error) {
	if _, err := nonNegative(primary, label); err != nil {
		return 0, err
	}
	if _, err := nonNegative(alias, label+" alias"); err != nil {
		return 0, err
	}
	if primary != 0 {
		return primary, nil
	}
	return alias, nil
}

func nonNegative(value int, label string) (int, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s must be non-negative", label)
	}
	return value, nil
}
