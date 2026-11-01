// Package projectdocs loads repository instruction files for agent prompts.
package projectdocs

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Taf0711/splice/internal/projectfile"
)

// ProjectContextFiles lists instruction files in precedence order. The first
// matching file at each directory wins.
var ProjectContextFiles = []string{"AGENTS.md", "SPLICE.md", ".splice/AGENTS.md", "CLAUDE.md"}

const (
	// MaxProjectContextBytes caps one instruction file.
	MaxProjectContextBytes = 8 << 10
	// MaxProjectContextTotalBytes caps all instruction files for one workspace.
	MaxProjectContextTotalBytes = 32 << 10
)

const truncationMarker = "\n… (truncated)"

// Load returns formatted repository instructions for workDir. It returns an
// empty string when no instruction file exists.
func Load(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	return LoadFrom(workDir, FindGitRoot(workDir))
}

// LoadFrom returns formatted repository instructions using gitRoot as the
// root-to-leaf boundary. It is useful to preserve the loader's tested seams.
func LoadFrom(cwd, gitRoot string) string {
	dirs := GuidelineDirs(cwd, gitRoot)
	if len(dirs) == 0 {
		return ""
	}
	var b strings.Builder
	totalUsed := 0
	for _, dir := range dirs {
		if totalUsed >= MaxProjectContextTotalBytes {
			break
		}
		match := FindContextFile(dir)
		if match == "" {
			continue
		}
		data, err := projectfile.Read(match, containmentRoot(dir, gitRoot))
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		limit := MaxProjectContextBytes
		if remaining := MaxProjectContextTotalBytes - totalUsed; remaining < limit {
			limit = remaining
		}
		content = TruncateContent(content, limit)
		label := GuidelineLabel(match, gitRoot)
		b.WriteString("\n\n## Project guidelines (" + label + ")\n\n" + content)
		totalUsed += len(content)
	}
	return b.String()
}

// containmentRoot returns the directory an instruction file must stay inside:
// the git root when dir sits under it, otherwise dir itself. A symlink that
// points outside the repo is then not loaded into the prompt.
func containmentRoot(dir, gitRoot string) string {
	if gitRoot != "" {
		if rel, err := filepath.Rel(gitRoot, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return gitRoot
		}
	}
	return dir
}

// TruncateContent caps content without splitting a UTF-8 rune.
func TruncateContent(content string, limit int) string {
	if len(content) <= limit {
		return content
	}
	if limit <= 0 {
		return ""
	}
	cut := limit - len(truncationMarker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	if truncated := content[:cut] + truncationMarker; len(truncated) <= limit {
		return truncated
	}
	end := limit
	for end > 0 && !utf8.RuneStart(content[end]) {
		end--
	}
	return content[:end]
}

// GuidelineDirs returns the directory chain from gitRoot to cwd.
func GuidelineDirs(cwd, gitRoot string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	gitRoot = strings.TrimSpace(gitRoot)
	if gitRoot == "" {
		return []string{cwd}
	}
	rel, err := filepath.Rel(gitRoot, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return []string{cwd}
	}
	if rel == "." {
		return []string{gitRoot}
	}
	dirs := []string{gitRoot}
	cur := gitRoot
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		dirs = append(dirs, cur)
	}
	return dirs
}

// GuidelineLabel returns a short path for an instruction file.
func GuidelineLabel(match, gitRoot string) string {
	if gitRoot == "" {
		return filepath.Base(match)
	}
	rel, err := filepath.Rel(gitRoot, match)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(match)
	}
	return rel
}

// FindContextFile returns the first matching instruction file in dir.
func FindContextFile(dir string) string {
	for _, name := range ProjectContextFiles {
		parent, ok := ResolveDirCaseInsensitive(filepath.Dir(filepath.Join(dir, name)), dir)
		if !ok {
			continue
		}
		if match := findCaseInsensitiveFile(parent, filepath.Base(name)); match != "" {
			return match
		}
	}
	return ""
}

// FindGitRoot returns the nearest ancestor with valid git metadata.
func FindGitRoot(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	cur := cwd
	for {
		if HasGitMetadata(cur) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// HasGitMetadata reports whether dir has a usable .git entry.
func HasGitMetadata(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	if info.IsDir() {
		_, err := os.Stat(filepath.Join(gitPath, "HEAD"))
		return err == nil
	}
	data, err := projectfile.Read(gitPath, dir)
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(data)), "gitdir: ")
}

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

// ResolveDirCaseInsensitive resolves a path below anchor with case-insensitive
// directory matching.
func ResolveDirCaseInsensitive(target, anchor string) (string, bool) {
	if target == "" || target == "." {
		return anchor, true
	}
	rel, err := filepath.Rel(anchor, target)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return anchor, true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	cur := anchor
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return "", false
		}
		found := false
		for _, e := range entries {
			if e.IsDir() && strings.EqualFold(e.Name(), part) {
				cur = filepath.Join(cur, e.Name())
				found = true
				break
			}
		}
		if !found {
			return "", false
		}
	}
	return cur, true
}
