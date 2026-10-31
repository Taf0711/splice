package agent

import (
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/projectdocs"
	"github.com/Taf0711/splice/internal/projectfile"
	"github.com/Taf0711/splice/internal/repomap"
	"github.com/Taf0711/splice/internal/workspaceseed"
)

// coreSystemPrompt is the de-branded coding-craft instruction set: identity,
// autonomy, workflow, editing discipline, the testing gate, tool use, and
// communication style.
//
//go:embed system_prompt.md
var coreSystemPrompt string

// confirmationPolicy is the de-branded safety policy appended to the system
// prompt so the model self-polices before risky actions. The sandbox enforces a
// subset of these rules, but the model applies judgement first.
//
//go:embed confirmation_policy.md
var confirmationPolicy string

// fallbackSystemPrompt is used only if the embedded core prompt is somehow empty
// (it never should be) so a run always has a non-empty system turn.
const fallbackSystemPrompt = "You are Splice, a terminal coding agent. Help with the current workspace and use tools when needed."

// userContextFile is the per-user instruction file, resolved under
// config.UserConfigDir()/splice/ alongside the rest of Splice's per-user config
// (config.json, commands, specialists) so users can keep personal guidance out
// of individual repositories.
const userContextFile = "SPLICE.md"

var userConfigDirForPrompt = config.UserConfigDir

// maxProjectContextBytes caps how much of a single project doc is injected so
// a large guidelines file can't blow the context budget.
const maxProjectContextBytes = projectdocs.MaxProjectContextBytes // 8 KiB per file

// maxProjectContextTotalBytes caps the total bytes across all matched
// project guideline files. With path-walking, multiple files can match (root
// + sub-tree), so a per-file cap alone is not enough.
const maxProjectContextTotalBytes = projectdocs.MaxProjectContextTotalBytes // 32 KiB total

const truncationMarker = "\n… (truncated)"

// maxRepoMapContextBytes keeps the repository map useful but compact enough to
// remain stable across normal agent turns.
const maxRepoMapContextBytes = 4 << 10 // 4 KiB

const (
	workspaceSeedMaxLines = 12
	workspaceSeedWidth    = 100
)

// buildSystemPrompt assembles the full system prompt for a run: the core
// coding-craft instructions, dynamic workspace context (cwd, git branch, project
// guidelines), and the safety confirmation policy. It is built once per run so
// every turn shares one (cacheable) system turn.
func buildSystemPrompt(options Options) string {
	core := strings.TrimSpace(options.SystemPrompt)
	if core == "" {
		core = strings.TrimSpace(coreSystemPrompt)
	}
	if core == "" {
		core = fallbackSystemPrompt
	}
	sections := []string{core}
	if addendum := modelPromptAddendum(options.Model); addendum != "" {
		sections = append(sections, addendum)
	}
	if extra := strings.TrimSpace(options.SystemAddendum); extra != "" {
		sections = append(sections, extra)
	}
	if session := sessionRuntimeContext(options); session != "" {
		sections = append(sections, session)
	}
	if prefixes := approvedCommandPrefixContext(options); prefixes != "" {
		sections = append(sections, prefixes)
	}
	if seed := workspaceSeedContext(options.Cwd); seed != "" {
		sections = append(sections, seed)
	}
	// User guidelines are injected before workspace/project guidelines so the
	// project's AGENTS.md/SPLICE.md is the later, more specific instruction
	// block. See userGuidelines for the explicit precedence note carried in
	// the section text itself.
	if user := userGuidelines(); user != "" {
		sections = append(sections, user)
	}
	if ws := workspaceContext(options.Cwd); ws != "" {
		sections = append(sections, ws)
	}
	if delegation := specialistDelegationContext(options); delegation != "" {
		sections = append(sections, delegation)
	}
	if skillsBlock := skillsContext(options); skillsBlock != "" {
		sections = append(sections, skillsBlock)
	}
	if style := responseStyleContext(options); style != "" {
		sections = append(sections, style)
	}
	if policy := strings.TrimSpace(confirmationPolicy); policy != "" {
		sections = append(sections, policy)
	}
	return strings.Join(sections, "\n\n")
}

// responseStyleContext renders the operator-selected reply style (TUI /style) as
// a system-prompt directive so the choice actually shapes responses. "balanced"
// (the default) and unknown/empty values add nothing, keeping the prompt
// byte-identical to the pre-style behavior.
func responseStyleContext(options Options) string {
	switch strings.ToLower(strings.TrimSpace(options.ResponseStyle)) {
	case "concise":
		return "Response style: concise. Lead with the result and keep answers short and direct; omit preamble, restating the question, and any explanation not needed to be correct."
	case "explanatory":
		return "Response style: explanatory. Explain the reasoning behind your answers and changes, surface relevant trade-offs, and add brief context a learner would want — while staying on task and not padding."
	case "review":
		return "Response style: review. Work like a critical reviewer: call out risks, edge cases, and unstated assumptions, flag anything questionable, and prefer precise, evidence-backed statements over reassurance."
	default:
		return ""
	}
}

func approvedCommandPrefixContext(options Options) string {
	if options.Sandbox == nil {
		return ""
	}
	prefixes := options.Sandbox.ApprovedCommandPrefixes()
	if len(prefixes) == 0 {
		return ""
	}
	lines := make([]string, 0, len(prefixes))
	for _, grant := range prefixes {
		if len(grant.Prefix) == 0 {
			continue
		}
		lines = append(lines, "- "+grant.ToolName+": "+strings.Join(grant.Prefix, " "))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Approved Command Prefixes\n\nThe following command prefixes have already been approved and do not need another permission prompt:\n" + strings.Join(lines, "\n")
}

// specialistDelegationContext nudges the orchestrator to offload read-heavy or
// parallelizable work to a specialist sub-agent via the Task tool, keeping large
// tool outputs out of the main context. It renders only when specialists are
// known (which is only where the Task tool is actually registered), so a run with
// no delegatable specialists produces the previous prompt unchanged.
func specialistDelegationContext(options Options) string {
	if len(options.Specialists) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<specialists>\n")
	b.WriteString("Delegate focused or read-heavy work to a specialist sub-agent with the Task tool instead of doing it inline. ")
	b.WriteString("When a request matches a specialist's purpose, delegate to it proactively — you do not need the user to ask first. ")
	b.WriteString("This keeps large tool outputs — searches, file dumps, multi-step exploration — out of your own context, so you stay fast and token-efficient. ")
	b.WriteString("Prefer delegating codebase search and exploration; for independent subtasks, launch several specialists in parallel. Handle small, direct edits yourself.\n")
	b.WriteString("Available specialists (call Task with the matching name when the task fits its purpose):\n")
	for _, info := range options.Specialists {
		name := strings.TrimSpace(info.Name)
		if name == "" {
			continue
		}
		if desc := strings.TrimSpace(info.WhenToUse); desc != "" {
			b.WriteString("- " + name + ": " + desc + "\n")
		} else {
			b.WriteString("- " + name + "\n")
		}
	}
	b.WriteString("</specialists>")
	return b.String()
}

// skillsContext lists the reusable skills the model can pull in on demand via the
// skill tool, so it invokes the right one on the first try instead of guessing a
// name, failing, and reading the error. It mirrors specialistDelegationContext:
// names + one-line descriptions only (never skill bodies), and it renders nothing
// when no skills are installed, so a skill-less run reproduces the previous prompt
// byte-for-byte.
// skillsContextListBudget bounds the bytes spent listing individual skills so a
// workspace with an extreme number of them can't bloat every turn's prompt.
// Skills past the budget are summarized as a count rather than dropped — the
// model can still load any of them by name (an unknown name returns the full
// list from the skill tool).
//
// The budget is deliberately generous — roughly 18 skills with maximally long
// (200-rune, ASCII) descriptions, or 40+ with typical terser ones; non-ASCII
// descriptions consume it faster since the budget counts bytes. This list is
// the model's ONLY discovery surface, so a skill squeezed out of it effectively
// never triggers. The previous 640-byte cap silently broke invocation for
// anyone with more than ~6 skills — the model cannot match a request against a
// skill it cannot see.
const skillsContextListBudget = 4096

func skillsContext(options Options) string {
	if len(options.Skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	b.WriteString("Reusable, on-demand instruction sets you can load with the skill tool. Before acting on a request, scan this list; when the request matches a skill's name or description, call skill with its exact name FIRST and follow the loaded guidance — do not guess names, do not skip a matching skill, and do not substitute your own approach for its instructions.\n")
	listed, spent, omitted := 0, 0, 0
	for _, info := range options.Skills {
		name := strings.TrimSpace(info.Name)
		if name == "" {
			continue
		}
		line := "- " + name
		if desc := strings.TrimSpace(info.Description); desc != "" {
			line += ": " + truncateForSkillLine(desc)
		}
		line += "\n"
		// Always list at least one skill; past the budget, summarize the remainder
		// as a count instead of silently dropping it.
		if listed > 0 && spent+len(line) > skillsContextListBudget {
			omitted++
			continue
		}
		b.WriteString(line)
		spent += len(line)
		listed++
	}
	if omitted > 0 {
		b.WriteString("- …and " + strconv.Itoa(omitted) + " more (call skill with a name; an unknown name lists them all)\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// truncateForSkillLine keeps a skill's one-line description short so a single
// verbose description can't dominate the skills-list budget. The cap is roomy on
// purpose: the description carries the skill's trigger conditions ("use when…"),
// and truncating those is what stops the model from ever invoking the skill.
func truncateForSkillLine(desc string) string {
	const maxDescRunes = 200
	runes := []rune(desc)
	if len(runes) <= maxDescRunes {
		return desc
	}
	return strings.TrimSpace(string(runes[:maxDescRunes])) + "…"
}

func sessionRuntimeContext(options Options) string {
	provider := strings.TrimSpace(options.ProviderName)
	model := strings.TrimSpace(options.Model)
	if provider == "" && model == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<session>\n")
	if provider != "" {
		b.WriteString("Active provider: " + provider + "\n")
	}
	if model != "" {
		b.WriteString("Active model: " + model + "\n")
	}
	b.WriteString("Use the active provider/model above when answering questions about what is currently running. Persisted config commands may show saved defaults that differ from this live run/session.\n")
	b.WriteString("</session>")
	return b.String()
}

// workspaceContext returns an <environment> block describing the working
// directory, git branch, and any project guideline doc, so the model grounds its
// work in the actual repo. Returns "" when cwd is unset (keeps headless/test
// runs deterministic).
func workspaceContext(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<environment>\n")
	b.WriteString("Working directory: " + cwd + "\n")
	b.WriteString("Operating system: " + runtime.GOOS + "\n")
	if runtime.GOOS == "windows" {
		b.WriteString("Shell syntax: Windows cmd.exe syntax for exec_command/bash tools; prefer the workdir/cwd argument instead of cd when changing directories. Do not pipe to or invoke POSIX coreutils from Git for Windows (usr\\bin head/grep/tail/cat/...): they are MSYS binaries and fail under the write-restricted sandbox; use native Splice tools (grep, read_file, list_directory, glob) or cmd.exe findstr/more instead, or sandbox_permissions require_escalated only when host-level execution is truly required.\n")
	} else {
		b.WriteString("Shell syntax: /bin/sh syntax for exec_command/bash tools; prefer the workdir/cwd argument instead of cd when changing directories.\n")
	}
	if branch := gitBranchForPrompt(cwd); branch != "" {
		b.WriteString("Git branch: " + branch + "\n")
	}
	b.WriteString("</environment>")

	b.WriteString(projectdocs.Load(cwd))
	if repoMap := repoMapContext(cwd); repoMap != "" {
		b.WriteString("\n\n## Repo map\n\n" + repoMap)
	}
	return b.String()
}

// projectGuidelines walks the directory chain from the git root to cwd
// (inclusive), finds the first matching project context file at each level
// (case-insensitive basename match), reads it, and returns the joined
// content in general-to-specific order — the most general file (at the git
// root) appears first, the most specific (at cwd) last. Each file is capped
// at maxProjectContextBytes; the total across all files is capped at
// maxProjectContextTotalBytes. Returns "" when no file matches.
func projectGuidelines(cwd, gitRoot string) string {
	return projectdocs.LoadFrom(cwd, gitRoot)
}

// userGuidelines returns the per-user SPLICE.md instructions block, if present.
// The file lives in config.UserConfigDir()/splice/ next to Splice's other
// per-user config; the basename match is case-insensitive so a file saved as
// splice.md still resolves on case-sensitive filesystems, mirroring the project
// guideline loader. The section carries an explicit precedence note because
// this is a global, personal preferences file: it is injected earlier in the
// prompt than the project's AGENTS.md/SPLICE.md (see buildSystemPrompt), and
// the note keeps that precedence unambiguous even if a model weighs later
// context more heavily than section order alone implies.
func userGuidelines() string {
	configDir, err := userConfigDirForPrompt()
	if err != nil {
		return ""
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return ""
	}
	match := findCaseInsensitiveFile(filepath.Join(configDir, "splice"), userContextFile)
	if match == "" {
		return ""
	}
	data, err := projectfile.Read(match, "")
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	content = truncateGuidelineContent(content, maxProjectContextBytes)
	return "## User guidelines (" + filepath.Base(match) + ")\n\n" +
		"These are the operator's personal preferences, not project policy. " +
		"Where they conflict with a repository's project guidelines below (AGENTS.md/SPLICE.md), the project guidelines take precedence.\n\n" +
		content
}

// truncateGuidelineContent caps content at limit bytes without splitting a
// UTF-8 rune, appending a truncation marker when anything was cut. Space for
// the marker is reserved before choosing the cut point. When limit is too
// small to fit the marker itself, the marker is dropped instead, so the
// returned string never exceeds limit for any non-negative limit.
func truncateGuidelineContent(content string, limit int) string {
	return projectdocs.TruncateContent(content, limit)
}

// findCaseInsensitiveFile returns the on-disk path of the regular file in dir
// whose name matches basename case-insensitively, or "" when absent.
func findCaseInsensitiveFile(dir, basename string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), basename) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// projectGuidelineDirs returns the directory chain from gitRoot to cwd
// (inclusive), in root-to-leaf order. If gitRoot is empty or unreachable
// from cwd, the chain collapses to [cwd].
func projectGuidelineDirs(cwd, gitRoot string) []string {
	return projectdocs.GuidelineDirs(cwd, gitRoot)
}

// projectGuidelineLabel renders the path of match as a short label relative
// to gitRoot. When gitRoot is empty, the basename is returned.
func projectGuidelineLabel(match, gitRoot string) string {
	return projectdocs.GuidelineLabel(match, gitRoot)
}

// findProjectContextFile returns the first matching project guideline file
// in dir. Lookup is case-insensitive on the basename and on the actual
// filename returned, so a git-tracked file like AGENTS.md still resolves to
// its true filename on case-sensitive filesystems — the returned path is what
// should appear in the project guidelines label.
// Returns "" when nothing matches.
func findProjectContextFile(dir string) string {
	return projectdocs.FindContextFile(dir)
}

// resolveDirCaseInsensitive walks from anchor to target, matching each
// segment case-insensitively. Returns the resolved absolute path and true on
// success, or "" and false if any segment cannot be found.
func resolveDirCaseInsensitive(target, anchor string) (string, bool) {
	return projectdocs.ResolveDirCaseInsensitive(target, anchor)
}

// findProjectGitRoot returns the nearest ancestor of cwd that contains a
// .git entry (file or directory). Returns "" when no git root is found, so
// the caller can fall back to cwd-only lookup.
func findProjectGitRoot(cwd string) string {
	return projectdocs.FindGitRoot(cwd)
}

func hasGitMetadata(dir string) bool {
	return projectdocs.HasGitMetadata(dir)
}

func workspaceSeedContext(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	seed, err := workspaceseed.BuildFromWorkspace(cwd, workspaceseed.GitInfo{
		Branch: gitBranchForPrompt(cwd),
	})
	if err != nil {
		return ""
	}
	rendered := strings.TrimSpace(workspaceseed.Render(seed, workspaceseed.RenderOptions{
		MaxLines: workspaceSeedMaxLines,
		Width:    workspaceSeedWidth,
	}))
	if rendered == "" {
		return ""
	}
	return "<workspace_seed>\n" + rendered + "\n</workspace_seed>"
}

func repoMapContext(cwd string) string {
	// repomap.Scan is best-effort supplemental context for the prompt. If it
	// fails, omit the repo map instead of failing the agent run; successful scans
	// are still capped by repomap.RenderPrompt and maxRepoMapContextBytes.
	snapshot, err := repomap.Scan(cwd, repomap.Options{
		MaxFiles: 300,
		MaxDepth: 5,
	})
	if err != nil {
		return ""
	}
	return repomap.RenderPrompt(snapshot, maxRepoMapContextBytes)
}

// gitBranchForPrompt reads the current branch (or short SHA when detached) for
// cwd, handling both a regular checkout (.git dir) and a worktree (.git file).
// Returns "" on any problem — the prompt simply omits the branch segment.
func gitBranchForPrompt(cwd string) string {
	gitPath := filepath.Join(cwd, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	headPath := filepath.Join(gitPath, "HEAD")
	headRoot := cwd
	if !info.IsDir() {
		data, err := projectfile.Read(gitPath, cwd)
		if err != nil {
			return ""
		}
		dir := strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir: ")
		if dir == "" {
			return ""
		}
		// In worktree mode the gitdir is often RELATIVE (e.g.
		// "gitdir: ../.git/worktrees/<name>") — resolve it against cwd, not the
		// process working directory, or HEAD lookup fails and we drop the branch.
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		headPath = filepath.Join(dir, "HEAD")
		headRoot = dir
	}
	// A worktree's git dir comes from the .git file and may name a location
	// outside cwd, so HEAD is contained by that git dir. A plain .git dir must
	// stay inside cwd.
	data, err := projectfile.Read(headPath, headRoot)
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(data))
	if strings.HasPrefix(ref, "ref: ") {
		return strings.TrimPrefix(strings.TrimPrefix(ref, "ref: "), "refs/heads/")
	}
	if len(ref) >= 7 {
		return ref[:7]
	}
	return ref
}
