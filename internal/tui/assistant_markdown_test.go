package tui

import "strings"

func renderMarkdownInline(text string) string {
	segments := parseMarkdownInline(text)
	var builder strings.Builder
	for _, segment := range segments {
		builder.WriteString(renderMarkdownInlineSegment(segment))
	}
	return builder.String()
}
