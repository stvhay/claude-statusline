package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ANSI colors (theme-respecting base-16)
const (
	yellow = "\033[33m"
	red    = "\033[31m"
	green  = "\033[32m"
	dim    = "\033[2m"
	reset  = "\033[0m"
)

var (
	dot     = dim + "·" + reset
	pipe    = dim + "│" + reset
	issueRe = regexp.MustCompile(`#(\d+)`)
)

type StatusInput struct {
	Workspace struct {
		CurrentDir string `json:"current_dir"`
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Version     string `json:"version"`
	OutputStyle struct {
		Name string `json:"name"`
	} `json:"output_style"`
	ContextWindow struct {
		RemainingPercentage *float64 `json:"remaining_percentage"`
	} `json:"context_window"`
	Vim *struct {
		Mode string `json:"mode"`
	} `json:"vim"`
	Agent *struct {
		Name string `json:"name"`
	} `json:"agent"`
	Cost struct {
		TotalCostUSD      *float64 `json:"total_cost_usd"`
		TotalDurationMS   *float64 `json:"total_duration_ms"`
		TotalLinesAdded   int      `json:"total_lines_added"`
		TotalLinesRemoved int      `json:"total_lines_removed"`
	} `json:"cost"`
	Worktree struct {
		Name   string `json:"worktree_name"`
		Branch string `json:"worktree_branch"`
	} `json:"worktree"`
}

type Settings struct {
	AlwaysThinkingEnabled bool   `json:"alwaysThinkingEnabled"`
	EffortLevel           string `json:"effortLevel"`
}

// bgRefresh spawns a process that writes output to cachePath.
// Uses O_CREATE|O_EXCL lock file to prevent duplicate concurrent fetches.
// The child process survives after this Go process exits.
func bgRefresh(cachePath string, cmd string, args ...string) {
	lockPath := cachePath + ".lock"
	// Atomic lock: O_EXCL fails if file already exists
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		// Lock exists — check staleness
		if fi, statErr := os.Stat(lockPath); statErr == nil && time.Since(fi.ModTime()) < 30*time.Second {
			return
		}
		// Stale lock — remove; next invocation will retry
		os.Remove(lockPath)
		return
	}
	lockFile.Close()

	// Quote all arguments for shell redirection
	quoted := make([]string, len(args)+1)
	quoted[0] = shellescape(cmd)
	for i, a := range args {
		quoted[i+1] = shellescape(a)
	}
	shellCmd := strings.Join(quoted, " ") + " > " + shellescape(cachePath) + " 2>/dev/null; rm -f " + shellescape(lockPath)
	c := exec.Command("sh", "-c", shellCmd)
	if err := c.Start(); err != nil {
		os.Remove(lockPath)
	}
}

func shellescape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// cachedRun returns cached output if fresh, otherwise returns stale cache and refreshes in background.
// Returns ("", false) if no cache exists yet (first call triggers background fetch).
func cachedRun(cachePath string, ttl time.Duration, cmd string, args ...string) (string, bool) {
	if fi, err := os.Stat(cachePath); err == nil {
		b, _ := os.ReadFile(cachePath)
		val := strings.TrimSpace(string(b))
		if time.Since(fi.ModTime()) < ttl {
			return val, true
		}
		// Stale — refresh in background, return stale value
		bgRefresh(cachePath, cmd, args...)
		return val, val != ""
	}
	// No cache — trigger background fetch
	bgRefresh(cachePath, cmd, args...)
	return "", false
}

// versionLess returns true if version a < b using numeric segment comparison.
func versionLess(a, b string) bool {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var ai, bi int
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai != bi {
			return ai < bi
		}
	}
	return false
}

// normalizePath shortens macOS /private/tmp → /tmp and /private/var → /var
// so that paths from different sources compare equal after normalization.
func normalizePath(p string) string {
	for _, prefix := range []string{"/private/tmp", "/private/var"} {
		target := strings.TrimPrefix(prefix, "/private")
		fi, err := os.Lstat(target)
		if err == nil && fi.Mode()&os.ModeSymlink != 0 && strings.HasPrefix(p, prefix) {
			return target + strings.TrimPrefix(p, prefix)
		}
	}
	return p
}

func sanitizePath(p string) string {
	return strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "_")
}

func cleanOldFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > maxAge {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

type RenderContext struct {
	Input       StatusInput
	Settings    Settings
	UserName    string
	HostName    string
	HomeDir     string
	ProjectsDir string
	GitInfo     string
	LatestVer   string
	Now         time.Time
}

func renderStatusline(ctx RenderContext) string {
	dir := normalizePath(ctx.Input.Workspace.CurrentDir)
	projectDir := normalizePath(ctx.Input.Workspace.ProjectDir)

	// Build output
	var out strings.Builder
	baseDir := projectDir
	if baseDir == "" {
		baseDir = dir
	}
	dirDisplay := baseDir
	if ctx.ProjectsDir != "" && strings.HasPrefix(dirDisplay, ctx.ProjectsDir+"/") {
		dirDisplay = strings.TrimPrefix(dirDisplay, ctx.ProjectsDir+"/")
	} else if ctx.HomeDir != "" && strings.HasPrefix(dirDisplay, ctx.HomeDir) {
		dirDisplay = "~" + strings.TrimPrefix(dirDisplay, ctx.HomeDir)
	}
	if projectDir != "" && dir != projectDir {
		relative := strings.TrimPrefix(dir, projectDir+"/")
		if relative == dir {
			dirDisplay += " " + yellow + "(" + filepath.Base(dir) + ")" + reset
		} else {
			dirDisplay += " " + yellow + "(" + relative + ")" + reset
		}
	}

	out.WriteString(ctx.UserName + "@" + ctx.HostName + ":" + dirDisplay)
	if ctx.GitInfo != "" {
		out.WriteString(" " + dot + " " + ctx.GitInfo)
	}

	// Model with thinking level
	modelDisplay := ctx.Input.Model.DisplayName
	if ctx.Settings.AlwaysThinkingEnabled {
		if strings.Contains(modelDisplay, "Opus") || strings.Contains(modelDisplay, "Sonnet") || strings.Contains(modelDisplay, "Claude") {
			if ctx.Settings.EffortLevel != "" {
				effortShort := string(unicode.ToUpper(rune(ctx.Settings.EffortLevel[0])))
				modelDisplay += " (" + effortShort + ")"
			} else {
				modelDisplay += " (T)"
			}
		}
	}

	// Context usage
	if ctx.Input.ContextWindow.RemainingPercentage != nil {
		used := 100 - int(*ctx.Input.ContextWindow.RemainingPercentage)
		usedStr := strconv.Itoa(used) + "%"
		switch {
		case used > 75:
			modelDisplay += " " + red + "@ " + usedStr + reset
		case used > 65:
			modelDisplay += " " + yellow + "@ " + usedStr + reset
		default:
			modelDisplay += " @ " + usedStr
		}
	}
	out.WriteString(" " + pipe + " " + modelDisplay)

	// Version (only if outdated)
	if ctx.LatestVer != "" && versionLess(ctx.Input.Version, ctx.LatestVer) {
		out.WriteString(" " + dot + " " + yellow + "v" + ctx.Input.Version + reset)
	}

	// Conditional extras
	if ctx.Input.OutputStyle.Name != "" && ctx.Input.OutputStyle.Name != "default" {
		out.WriteString(" " + dot + " style:" + ctx.Input.OutputStyle.Name)
	}
	if ctx.Input.Vim != nil && ctx.Input.Vim.Mode != "" {
		out.WriteString(" " + dot + " vim:" + ctx.Input.Vim.Mode)
	}
	if ctx.Input.Agent != nil && ctx.Input.Agent.Name != "" {
		out.WriteString(" " + dot + " agent:" + ctx.Input.Agent.Name)
	}
	if ctx.Input.Worktree.Name != "" {
		out.WriteString(" " + dot + " wt:" + ctx.Input.Worktree.Name)
	} else if ctx.Input.Worktree.Branch != "" {
		out.WriteString(" " + dot + " wt:" + ctx.Input.Worktree.Branch)
	}

	// Cost & churn
	var costParts []string
	if ctx.Input.Cost.TotalCostUSD != nil && *ctx.Input.Cost.TotalCostUSD != 0 {
		costParts = append(costParts, fmt.Sprintf("$%.2f", *ctx.Input.Cost.TotalCostUSD))
	}
	if ctx.Input.Cost.TotalLinesAdded != 0 || ctx.Input.Cost.TotalLinesRemoved != 0 {
		churn := green + "+" + strconv.Itoa(ctx.Input.Cost.TotalLinesAdded) + reset +
			"/" + red + "-" + strconv.Itoa(ctx.Input.Cost.TotalLinesRemoved) + reset
		costParts = append(costParts, churn)
	}
	if len(costParts) > 0 {
		out.WriteString(" " + pipe + " " + strings.Join(costParts, " "+dot+" "))
	}

	// Time and session duration
	timeStr := ctx.Now.Format("01/02 15:04")
	if ctx.Input.Cost.TotalDurationMS != nil {
		mins := int(*ctx.Input.Cost.TotalDurationMS) / 60000
		if mins >= 1 {
			timeStr += fmt.Sprintf(" (%dm)", mins)
		}
	}
	out.WriteString(" " + pipe + " " + timeStr)

	return out.String()
}

func main() {
	data, _ := io.ReadAll(os.Stdin)

	var input StatusInput
	if err := json.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "statusline: invalid JSON: %v\n", err)
	}

	dir := normalizePath(input.Workspace.CurrentDir)

	// User/host (no subprocess, instant)
	var userName, hostName string
	if u, err := user.Current(); err == nil {
		userName = u.Username
	}
	if h, err := os.Hostname(); err == nil {
		if i := strings.IndexByte(h, '.'); i >= 0 {
			h = h[:i]
		}
		hostName = h
	}

	var (
		gitInfo  string
		settings Settings
	)

	// Settings (local file read, instant)
	home, _ := os.UserHomeDir()
	if f, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
		json.Unmarshal(f, &settings)
	}

	// Version check (cached, 1hr TTL, instant file read)
	latestVer, _ := cachedRun(filepath.Join(home, ".claude", ".latest-version-cache"), 1*time.Hour, "npm", "view", "@anthropic-ai/claude-code", "version")

	// Git info (cached, 1s TTL — avoids blocking on large repos/network mounts)
	gitCacheDir := filepath.Join(home, ".claude", ".git-cache", sanitizePath(dir))
	os.MkdirAll(gitCacheDir, 0755)

	gitDir, hasGit := cachedRun(filepath.Join(gitCacheDir, "git-dir"), 1*time.Second, "git", "-C", dir, "--no-optional-locks", "rev-parse", "--git-dir")
	if hasGit && gitDir != "" {
		branch, hasBranch := cachedRun(filepath.Join(gitCacheDir, "branch"), 1*time.Second, "git", "-C", dir, "--no-optional-locks", "rev-parse", "--abbrev-ref", "HEAD")
		if hasBranch && branch != "" {
			diffOut, _ := cachedRun(filepath.Join(gitCacheDir, "diff-index"), 1*time.Second, "git", "-C", dir, "--no-optional-locks", "diff-index", "HEAD", "--")
			if diffOut != "" {
				gitInfo = "git:" + branch + "*"
			} else {
				gitInfo = "git:" + branch
			}
			if branch != "main" && branch != "master" {
				prCacheDir := filepath.Join(home, ".claude", ".pr-cache")
				os.MkdirAll(prCacheDir, 0755)
				cleanOldFiles(prCacheDir, 7*24*time.Hour)
				safeBranch := strings.ReplaceAll(branch, "/", "_")
				cachePath := filepath.Join(prCacheDir, safeBranch+".json")
				prData, _ := cachedRun(cachePath, 10*time.Second, "gh", "pr", "view", branch, "--json", "number,state,body")
				if prData != "" {
					var pr struct {
						Number int    `json:"number"`
						State  string `json:"state"`
						Body   string `json:"body"`
					}
					if json.Unmarshal([]byte(prData), &pr) == nil {
						gitInfo += fmt.Sprintf(" PR#%d(%s)", pr.Number, strings.ToLower(pr.State))
						if matches := issueRe.FindAllString(pr.Body, 3); len(matches) > 0 {
							gitInfo += " " + strings.Join(matches, ",")
						}
					}
				}
			}
		}
	}

	projectsDir := os.Getenv("CLAUDE_STATUSLINE_PROJECTS_DIR")
	if projectsDir != "" {
		if strings.HasPrefix(projectsDir, "~/") {
			projectsDir = filepath.Join(home, projectsDir[2:])
		}
		projectsDir = normalizePath(projectsDir)
	}

	ctx := RenderContext{
		Input:       input,
		Settings:    settings,
		UserName:    userName,
		HostName:    hostName,
		HomeDir:     home,
		ProjectsDir: projectsDir,
		GitInfo:     gitInfo,
		LatestVer:   latestVer,
		Now:         time.Now(),
	}
	fmt.Println(renderStatusline(ctx))
}
