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

func main() {
	data, _ := io.ReadAll(os.Stdin)

	var input StatusInput
	if err := json.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "statusline: invalid JSON: %v\n", err)
	}

	dir := normalizePath(input.Workspace.CurrentDir)
	projectDir := normalizePath(input.Workspace.ProjectDir)

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

	// Build output
	var out strings.Builder
	baseDir := projectDir
	if baseDir == "" {
		baseDir = dir
	}
	dirDisplay := baseDir
	if home != "" && strings.HasPrefix(dirDisplay, home) {
		dirDisplay = "~" + strings.TrimPrefix(dirDisplay, home)
	}
	if projectDir != "" && dir != projectDir {
		relative := strings.TrimPrefix(dir, projectDir+"/")
		if relative == dir {
			dirDisplay += " " + yellow + "(" + filepath.Base(dir) + ")" + reset
		} else {
			dirDisplay += " " + yellow + "(" + relative + ")" + reset
		}
	}

	out.WriteString(userName + "@" + hostName + ":" + dirDisplay)
	if gitInfo != "" {
		out.WriteString(" " + dot + " " + gitInfo)
	}

	// Model with thinking level
	modelDisplay := input.Model.DisplayName
	if settings.AlwaysThinkingEnabled {
		if strings.Contains(modelDisplay, "Opus") || strings.Contains(modelDisplay, "Sonnet") || strings.Contains(modelDisplay, "Claude") {
			if settings.EffortLevel != "" {
				effortShort := string(unicode.ToUpper(rune(settings.EffortLevel[0])))
				modelDisplay += " (" + effortShort + ")"
			} else {
				modelDisplay += " (T)"
			}
		}
	}

	// Context usage
	if input.ContextWindow.RemainingPercentage != nil {
		used := 100 - int(*input.ContextWindow.RemainingPercentage)
		usedStr := strconv.Itoa(used) + "%"
		switch {
		case used > 60:
			modelDisplay += " " + red + "@ " + usedStr + reset
		case used > 30:
			modelDisplay += " " + yellow + "@ " + usedStr + reset
		default:
			modelDisplay += " @ " + usedStr
		}
	}
	out.WriteString(" " + pipe + " " + modelDisplay)

	// Version (only if outdated)
	if latestVer != "" && input.Version != latestVer {
		out.WriteString(" " + dot + " " + yellow + "v" + input.Version + reset)
	}

	// Conditional extras
	if input.OutputStyle.Name != "" && input.OutputStyle.Name != "default" {
		out.WriteString(" " + dot + " style:" + input.OutputStyle.Name)
	}
	if input.Vim != nil && input.Vim.Mode != "" {
		out.WriteString(" " + dot + " vim:" + input.Vim.Mode)
	}
	if input.Agent != nil && input.Agent.Name != "" {
		out.WriteString(" " + dot + " agent:" + input.Agent.Name)
	}
	if input.Worktree.Name != "" {
		out.WriteString(" " + dot + " wt:" + input.Worktree.Name)
	} else if input.Worktree.Branch != "" {
		out.WriteString(" " + dot + " wt:" + input.Worktree.Branch)
	}

	// Cost & churn
	var costParts []string
	if input.Cost.TotalCostUSD != nil && *input.Cost.TotalCostUSD != 0 {
		costParts = append(costParts, fmt.Sprintf("$%.2f", *input.Cost.TotalCostUSD))
	}
	if input.Cost.TotalLinesAdded != 0 || input.Cost.TotalLinesRemoved != 0 {
		churn := green + "+" + strconv.Itoa(input.Cost.TotalLinesAdded) + reset +
			"/" + red + "-" + strconv.Itoa(input.Cost.TotalLinesRemoved) + reset
		costParts = append(costParts, churn)
	}
	if len(costParts) > 0 {
		out.WriteString(" " + pipe + " " + strings.Join(costParts, " "+dot+" "))
	}

	// Time and session duration
	timeStr := time.Now().Format("01/02 15:04")
	if input.Cost.TotalDurationMS != nil {
		mins := int(*input.Cost.TotalDurationMS) / 60000
		if mins >= 1 {
			timeStr += fmt.Sprintf(" (%dm)", mins)
		}
	}
	out.WriteString(" " + pipe + " " + timeStr)

	fmt.Println(out.String())
}
