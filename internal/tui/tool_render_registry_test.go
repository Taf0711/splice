package tui

import (
	"strings"
	"testing"
)

func TestDefaultToolBodyRegistrySelectsCoreRenderers(t *testing.T) {
	registry := newDefaultToolBodyRegistry()
	opts := cardRenderOptions{bodyCap: cardBodyMaxLines}

	tests := []struct {
		name   string
		hint   string
		arg    string
		detail string
		want   []string
	}{
		{
			name: "edit_file",
			detail: strings.Join([]string{
				"--- a/app.go",
				"+++ b/app.go",
				"@@ -1 +1 @@",
				"-old",
				"+new",
			}, "\n"),
			want: []string{"(+1 -1)", "-1", "+1", "new"},
		},
		{
			name: "apply_patch",
			detail: strings.Join([]string{
				"--- a/app.go",
				"+++ b/app.go",
				"@@ -1 +1 @@",
				"-old",
				"+new",
			}, "\n"),
			want: []string{"(+1 -1)", "-1", "+1", "new"},
		},
		{
			name:   "read_file",
			hint:   "README.md",
			detail: "File: README.md\n\n  7 | # Splice",
			want:   []string{"Read", "README.md"},
		},
		{
			name:   "bash",
			hint:   "go test ./internal/tui",
			detail: "stdout:\nok\nexit_code: 0",
			want:   []string{"ok"},
		},
		{
			name:   "grep",
			arg:    "func render",
			detail: "internal/tui/rendering.go:41: func render()",
			want:   []string{"Search", "func render"},
		},
		{
			name:   "unknown_tool",
			detail: "raw output",
			want:   []string{"raw output"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := registry.render(toolBodyRequest{
				name:   tt.name,
				hint:   tt.hint,
				arg:    tt.arg,
				detail: normalizeToolCardDetail(tt.detail),
				width:  96,
				opts:   opts,
			})
			got := plainRender(t, strings.Join(append(append([]string{}, body.lines...), body.headTag, body.footer), "\n"))
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("%s body = %q, missing %q", tt.name, got, want)
				}
			}
			if tt.name == "read_file" && strings.Contains(got, "# Splice") {
				t.Fatalf("read_file body = %q, must not expose read contents", got)
			}
			if tt.name == "grep" && strings.Contains(got, "internal/tui/rendering.go:41") {
				t.Fatalf("grep body = %q, must not expose raw search matches", got)
			}
		})
	}
}

func TestToolBodyRegistryReplacementIsScopedToOneTool(t *testing.T) {
	registry := newDefaultToolBodyRegistry()
	registry.register("grep", toolBodyRendererFunc(func(req toolBodyRequest) cardBody {
		return cardBody{lines: []string{zeroTheme.onPanel(zeroTheme.ink).Render("replacement grep body")}}
	}))

	opts := cardRenderOptions{bodyCap: cardBodyMaxLines}
	grepBody := registry.render(toolBodyRequest{
		name:   "grep",
		detail: "internal/tui/rendering.go:41: func render()",
		width:  96,
		opts:   opts,
	})
	if got := plainRender(t, strings.Join(grepBody.lines, "\n")); !strings.Contains(got, "replacement grep body") {
		t.Fatalf("grep replacement body = %q, want replacement", got)
	}

	bashBody := registry.render(toolBodyRequest{
		name:   "bash",
		hint:   "go test ./internal/tui",
		detail: normalizeToolCardDetail("stdout:\nok\nexit_code: 0"),
		width:  96,
		opts:   opts,
	})
	got := plainRender(t, strings.Join(append(append([]string{}, bashBody.lines...), bashBody.footer), "\n"))
	if strings.Contains(got, "replacement grep body") {
		t.Fatalf("bash body = %q, must not use grep replacement", got)
	}
	if !strings.Contains(got, "ok") || strings.Contains(got, "exit 0") {
		t.Fatalf("bash body = %q, want original bash renderer", got)
	}
}

func TestToolBodyRegistryTrimsRegisteredNames(t *testing.T) {
	registry := newToolBodyRegistry(unknownToolBodyRenderer{})
	registry.register(" grep ", toolBodyRendererFunc(func(req toolBodyRequest) cardBody {
		return cardBody{lines: []string{zeroTheme.onPanel(zeroTheme.ink).Render("trimmed grep body")}}
	}))

	body := registry.render(toolBodyRequest{
		name:   "grep",
		detail: "internal/tui/rendering.go:41: func render()",
		width:  96,
		opts:   cardRenderOptions{bodyCap: cardBodyMaxLines},
	})

	if got := plainRender(t, strings.Join(body.lines, "\n")); !strings.Contains(got, "trimmed grep body") {
		t.Fatalf("grep body = %q, want trimmed registered renderer", got)
	}
}

// The escape stripping first landed in genericCardBody only, which left the
// specialized renderers — bash, exec, grep, diff — still handing a hostile
// command's or file's raw bytes to the terminal. Every card dispatches through
// registry.render, so this pins the whole set at that one boundary.
func TestRegistryRenderSanitizesEveryToolRenderer(t *testing.T) {
	registry := newDefaultToolBodyRegistry()
	opts := cardRenderOptions{bodyCap: cardBodyMaxLines}

	for _, tt := range []struct {
		name   string
		hint   string
		detail string
	}{
		{name: "bash", hint: "ls", detail: "stdout:\nsafe\x1b[2Jtail\nexit_code: 0"},
		{name: "exec_command", hint: "ls", detail: "stdout:\nsafe\x1b[2Jtail\nexit_code: 0"},
		{name: "grep", detail: "internal/x.go:1: safe\x1b[2Jtail"},
		{name: "read_file", detail: "safe\x1b[2Jtail"},
		{name: "unknown_tool", detail: "safe\x1b[2Jtail"},
		{name: "bash_osc8", hint: "ls", detail: "stdout:\n\x1b]8;;http://evil\x07click\x1b]8;;\x07\nexit_code: 0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := registry.render(toolBodyRequest{
				name:   tt.name,
				hint:   tt.hint,
				detail: tt.detail,
				width:  96,
				opts:   opts,
			})
			got := plainRender(t, strings.Join(append(append([]string{}, body.lines...), body.headTag, body.footer), "\n"))
			if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b]8;") {
				t.Fatalf("%s body still carries an escape sequence: %q", tt.name, got)
			}
			// The escape must not survive as visible text either.
			if strings.Contains(got, "[2J") || strings.Contains(got, "]8;;") {
				t.Fatalf("%s body leaked the escape payload as text: %q", tt.name, got)
			}
		})
	}
}
