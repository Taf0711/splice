package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/flags"
)

// Format-on-write for the mutating file tools. When enabled, a successful
// edit_file/write_file runs the language's standard formatter on the file it
// just wrote, so the model's output always lands in project-canonical style
// and never fails a CI format check it cannot see. Off by default (set
// SPLICE_FORMAT_ON_WRITE=1): auto-reformatting changes bytes the model did not
// write, which strict workflows may not want.
//
// Ordering matters: formatting runs BEFORE the FileTracker re-baseline, and
// the caller records the POST-format content. Formatting after the baseline
// would make the very next edit look like an external modification and trip
// the conflict guard.

// formatOnWriteTimeout bounds one formatter run; a wedged formatter must never
// hang a tool call. On timeout the unformatted write stands.
const formatOnWriteTimeout = 10 * time.Second

// formatterCommands maps a file extension to the formatter argv; the file path
// is appended as the final argument. Only in-place, config-respecting,
// community-standard formatters — a missing binary silently skips formatting.
var formatterCommands = map[string][]string{
	".go":    {"gofmt", "-w"},
	".rs":    {"rustfmt"},
	".py":    {"ruff", "format", "--quiet"},
	".ts":    {"prettier", "--log-level", "silent", "--write"},
	".tsx":   {"prettier", "--log-level", "silent", "--write"},
	".js":    {"prettier", "--log-level", "silent", "--write"},
	".jsx":   {"prettier", "--log-level", "silent", "--write"},
	".json":  {"prettier", "--log-level", "silent", "--write"},
	".css":   {"prettier", "--log-level", "silent", "--write"},
	".scss":  {"prettier", "--log-level", "silent", "--write"},
	".html":  {"prettier", "--log-level", "silent", "--write"},
	".md":    {"prettier", "--log-level", "silent", "--write"},
	".yaml":  {"prettier", "--log-level", "silent", "--write"},
	".yml":   {"prettier", "--log-level", "silent", "--write"},
	".zig":   {"zig", "fmt"},
	".dart":  {"dart", "format"},
	".tf":    {"terraform", "fmt"},
	".gleam": {"gleam", "format"},
	".sh":    {"shfmt", "-w"},
	".bash":  {"shfmt", "-w"},
	".c":     {"clang-format", "-i"},
	".h":     {"clang-format", "-i"},
	".cpp":   {"clang-format", "-i"},
	".hpp":   {"clang-format", "-i"},
	".cc":    {"clang-format", "-i"},
	".kt":    {"ktlint", "-F"},
	".swift": {"swiftformat"},
	".lua":   {"stylua"},
}

// formatOnWriteEnabled reports whether the resolved tools.format_on_write
// flag is on for this process.
func formatOnWriteEnabled() bool {
	return flags.Process().Enabled(flags.ToolsFormatOnWrite)
}

// formatSkippedUntrustedNotice tells the model why its write was not formatted.
const formatSkippedUntrustedNotice = " Format-on-write skipped: the workspace is not trusted."

// maybeFormatWrittenFile runs the configured formatter for absolutePath (when
// enabled and on PATH) and returns the file's content afterwards. Best-effort
// throughout: any failure — no formatter, formatter error, timeout, unreadable
// result — returns writtenContent so the caller's state matches the last write
// it performed itself.
//
// A formatter runs outside the sandbox and reads project files (for example a
// prettier config can load plugins), so it runs only in a trusted workspace.
// In an untrusted workspace it returns writtenContent and a notice for the
// tool result.
func maybeFormatWrittenFile(ctx context.Context, absolutePath string, writtenContent string, trusted bool) (string, string) {
	if !formatOnWriteEnabled() {
		return writtenContent, ""
	}
	command, ok := formatterCommands[strings.ToLower(filepath.Ext(absolutePath))]
	if !ok {
		return writtenContent, ""
	}
	if !trusted {
		return writtenContent, formatSkippedUntrustedNotice
	}
	if _, err := exec.LookPath(command[0]); err != nil {
		return writtenContent, ""
	}
	formatCtx, cancel := context.WithTimeout(ctx, formatOnWriteTimeout)
	defer cancel()
	arguments := append(append([]string(nil), command[1:]...), absolutePath)
	formatter := exec.CommandContext(formatCtx, command[0], arguments...)
	formatter.Dir = filepath.Dir(absolutePath)
	if err := formatter.Run(); err != nil {
		return writtenContent, ""
	}
	formatted, err := os.ReadFile(absolutePath)
	if err != nil {
		return writtenContent, ""
	}
	return string(formatted), ""
}
