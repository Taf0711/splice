package doctor

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/lsp"
	"github.com/Taf0711/splice/internal/sandbox"
)

// sandboxBackendCheck reports whether the selected platform has a native
// sandbox backend ready. A missing backend is a WARN because setup may be
// optional for non-shell workflows, but sandboxed shell execution remains
// unavailable until a native backend is installed.
func sandboxBackendCheck(goos string, lookup func(string) (string, error), workspaceRoot string, sandboxConfig config.SandboxConfig) Check {
	if goos == "" {
		goos = runtime.GOOS
	}
	if lookup == nil {
		lookup = exec.LookPath
	}
	backend := sandbox.SelectBackend(sandbox.BackendOptions{GOOS: goos, LookupExecutable: lookup})
	if backend.Available {
		if setupCheck := windowsSandboxSetupCheck(goos, backend, workspaceRoot, sandboxConfig); setupCheck != nil {
			return *setupCheck
		}
		return check("sandbox.backend", "Sandbox backend", StatusPass, fmt.Sprintf("Native sandbox backend %s is available.", backend.Name), map[string]any{
			"backend":      string(backend.Name),
			"platform":     goos,
			"supportLevel": string(backend.SupportLevel()),
		})
	}
	remedy := sandboxRemedy(goos, backend)
	return check("sandbox.backend", "Sandbox backend", StatusWarn, sandboxBackendWarning(goos, backend), map[string]any{
		"backend":         string(backend.Name),
		"platform":        goos,
		"supportLevel":    string(backend.SupportLevel()),
		"downgradeReason": backend.DowngradeReason(sandbox.DefaultPolicy()),
		"remedy":          remedy,
	})
}

func sandboxBackendWarning(goos string, backend sandbox.Backend) string {
	if goos == "linux" {
		reason := backend.Message
		if reason == "" {
			reason = "no native backend"
		}
		return fmt.Sprintf("Native sandbox backend unavailable on linux: %s. Pipeline stages and command nodes cannot run; %s.", reason, sandbox.LinuxBackendInstallHint)
	}
	if backend.Message != "" {
		return fmt.Sprintf("Native sandbox backend unavailable on %s: %s.", goos, backend.Message)
	}
	return fmt.Sprintf("Native sandbox backend unavailable on %s; sandboxed shell execution is unavailable.", goos)
}

// sandboxRemedy returns the platform-specific, actionable step to obtain a
// native sandbox backend or complete required setup.
func sandboxRemedy(goos string, backend sandbox.Backend) string {
	switch goos {
	case "linux":
		return sandbox.LinuxBackendInstallHint
	case "darwin":
		return "sandbox-exec ships with macOS; ensure /usr/bin is on PATH so `sandbox-exec` resolves"
	case "windows":
		if backend.Message != "" {
			return "install the Windows sandbox command runner and setup helper together, then run `splice sandbox setup`"
		}
		return "run `splice sandbox setup` to prepare the Windows native sandbox"
	default:
		return "no native sandbox adapter exists for " + goos + "; run inside Linux (bubblewrap) or macOS (sandbox-exec) for native isolation"
	}
}

func windowsSandboxSetupCheck(goos string, backend sandbox.Backend, workspaceRoot string, sandboxConfig config.SandboxConfig) *Check {
	if goos != "windows" || backend.Name != sandbox.BackendWindowsRestrictedToken {
		return nil
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil
	}
	scope, err := sandbox.NewScope(workspaceRoot, sandboxConfig.AdditionalWriteRoots)
	if err != nil {
		result := check("sandbox.backend", "Sandbox backend", StatusWarn, "Windows sandbox setup could not be checked because configured write roots are invalid: "+err.Error(), map[string]any{
			"backend":      string(backend.Name),
			"platform":     goos,
			"supportLevel": string(backend.SupportLevel()),
			"setupStatus":  "invalid-config",
			"remedy":       "fix sandbox.additionalWriteRoots, then run `splice sandbox setup`",
		})
		return &result
	}
	sandboxHome, err := sandbox.ResolveWindowsSandboxHome(nil)
	if err != nil {
		result := check("sandbox.backend", "Sandbox backend", StatusWarn, "Windows sandbox setup could not be checked: "+err.Error(), map[string]any{
			"backend":      string(backend.Name),
			"platform":     goos,
			"supportLevel": string(backend.SupportLevel()),
			"setupStatus":  "unknown",
			"remedy":       "run `splice sandbox setup` after fixing the sandbox home path",
		})
		return &result
	}
	profile := sandbox.PermissionProfileFromPolicy(workspaceRoot, doctorSandboxPolicy(sandboxConfig), scope)
	setupConfig := sandbox.WindowsSandboxSetupConfig{
		SandboxHome:       sandboxHome,
		CommandCWD:        workspaceRoot,
		WorkspaceRoots:    []string{workspaceRoot},
		PermissionProfile: profile,
	}
	if err := sandbox.ValidateWindowsSandboxSetupMarker(setupConfig); err != nil {
		result := check("sandbox.backend", "Sandbox backend", StatusWarn, fmt.Sprintf("Native sandbox backend %s is installed, but Windows sandbox setup is missing or out of date: %v.", backend.Name, err), map[string]any{
			"backend":      string(backend.Name),
			"platform":     goos,
			"supportLevel": string(backend.SupportLevel()),
			"setupStatus":  "missing-or-out-of-date",
			"remedy":       "run `splice sandbox setup` to prepare the Windows native sandbox",
		})
		return &result
	}
	return nil
}

func doctorSandboxPolicy(cfg config.SandboxConfig) sandbox.Policy {
	policy := sandbox.DefaultPolicy()
	policy.AllowRead = append([]string{}, cfg.AllowRead...)
	switch sandbox.NetworkMode(cfg.Network) {
	case sandbox.NetworkAllow, sandbox.NetworkDeny:
		policy.Network = sandbox.NetworkMode(cfg.Network)
	}
	policy.MonitorDenials = cfg.MonitorDenials
	return policy
}

// lspServersCheck reports which language servers SPLICE would use are present on
// PATH. Missing servers are not a failure — SPLICE degrades to text-only edits for
// those languages — so the worst status is WARN, and each missing server gets an
// actionable install command keyed by its binary name. Only the tier-1 servers
// (lsp.CoreServerBinaries) drive the status: the long-tail servers configured
// for breadth are listed informationally under missingOptional, because warning
// about a missing zls on a machine with no Zig code would be permanent noise.
func lspServersCheck(lookup func(string) (string, error)) Check {
	if lookup == nil {
		lookup = exec.LookPath
	}
	core := map[string]bool{}
	for _, binary := range lsp.CoreServerBinaries() {
		core[binary] = true
	}
	present := map[string]any{}
	missing := map[string]any{}
	missingOptional := map[string]any{}
	for _, binary := range lsp.ServerBinaries() {
		if _, err := lookup(binary); err == nil {
			present[binary] = "on PATH"
			continue
		}
		if core[binary] {
			missing[binary] = lspRemedy(binary)
		} else {
			missingOptional[binary] = lspRemedy(binary)
		}
	}
	details := map[string]any{"present": present}
	if len(missingOptional) > 0 {
		details["missingOptional"] = missingOptional
	}
	if len(missing) == 0 {
		message := "All core language servers are available on PATH."
		if len(missingOptional) > 0 {
			message = fmt.Sprintf("All core language servers are available on PATH (%d optional server(s) not installed).", len(missingOptional))
		}
		return check("lsp.servers", "LSP servers", StatusPass, message, details)
	}
	details["missing"] = missing
	return check("lsp.servers", "LSP servers", StatusWarn, fmt.Sprintf("%d language server(s) missing from PATH; affected files degrade to text-only edits.", len(missing)), details)
}

// pipelineTools maps each program the pipeline stages call on PATH to the work
// that needs it. The Bandit scan runs as "python -m bandit", so it is not listed
// here: PATH lookup cannot see the module.
var pipelineTools = []struct{ name, use string }{
	{"git", "change detection in the stages"},
	{"go", "Go test runs and Go checks"},
	{"gosec", "security scan of Go files"},
	{"trivy", "security scan of the workspace"},
	{"python", "Python syntax checks, pytest, and Bandit"},
	{"node", "JavaScript syntax checks"},
	{"npx", "eslint SARIF scan of JavaScript and TypeScript"},
}

// pipelineToolchainCheck reports which pipeline tools are on PATH. Every tool is
// optional. A missing tool makes its stage report the check as incomplete,
// unavailable, or failed, so the check only warns.
func pipelineToolchainCheck(lookup func(string) (string, error)) Check {
	if lookup == nil {
		lookup = exec.LookPath
	}
	present := map[string]any{}
	missing := map[string]any{}
	for _, tool := range pipelineTools {
		if _, err := lookup(tool.name); err == nil {
			present[tool.name] = "on PATH"
			continue
		}
		// The stages call "python". python3 alone does not satisfy them.
		if tool.name == "python" {
			if _, err := lookup("python3"); err == nil {
				missing[tool.name] = "only python3 is on PATH; the stages call python"
				continue
			}
		}
		missing[tool.name] = tool.use
	}
	details := map[string]any{"present": present}
	if len(missing) == 0 {
		return check("pipeline.toolchain", "Pipeline toolchain", StatusPass, "All pipeline tools are on PATH.", details)
	}
	details["missing"] = missing
	return check("pipeline.toolchain", "Pipeline toolchain", StatusWarn, fmt.Sprintf("%d pipeline tool(s) missing from PATH; stages that need a missing tool report their check as incomplete, unavailable, or failed.", len(missing)), details)
}

// lspRemedy returns an actionable install command for a missing language-server
// binary. It is provider/tooling neutral and names the ecosystem's standard
// installer.
func lspRemedy(binary string) string {
	switch binary {
	case "gopls":
		return "install with `go install golang.org/x/tools/gopls@latest` (ensure $GOBIN is on PATH)"
	case "typescript-language-server":
		return "install with `npm install -g typescript typescript-language-server`"
	case "pyright-langserver":
		return "install with `npm install -g pyright` (or `pipx install pyright`)"
	case "rust-analyzer":
		return "install with `rustup component add rust-analyzer`"
	default:
		return "install the " + binary + " language server and ensure it is on PATH"
	}
}
