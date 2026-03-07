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
	yellow    = "\033[33m"
	red       = "\033[31m"
	green     = "\033[32m"
	cyan      = "\033[36m"
	magenta   = "\033[35m"
	dim       = "\033[2m"
	underline = "\033[4m"
	reset     = "\033[0m"
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

type IssueInfo struct {
	Number  int
	Branch  string
	RepoURL string // e.g. "https://github.com/org/repo"
}

type OpenIssue struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type RenderContext struct {
	Input         StatusInput
	Settings      Settings
	UserName      string
	HostName      string
	HomeDir       string
	ProjectsDir   string
	ExpectUser    string
	ExpectHost    string
	GitInfo       string
	GitBranch     string // parsed branch name (e.g. "main", "feature-x")
	GitDirty      bool   // working tree has uncommitted changes
	LatestVer     string
	Now           time.Time
	IssueInfo     *IssueInfo
	OpenIssues    []OpenIssue
	HasMoreIssues bool
}

// parseIssueFile reads and parses a .issue file. Returns issue number and branch name.
func parseIssueFile(path string) (int, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), ",", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid .issue format")
	}
	num, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid issue number: %w", err)
	}
	return num, parts[1], nil
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

	// === Section 1: Model, thinking level, context bar ===
	modelDisplay := ctx.Input.Model.DisplayName
	// Strip version from model name (e.g. "Opus 4.6" → "Opus")
	for _, name := range []string{"Opus", "Sonnet", "Haiku"} {
		if strings.HasPrefix(modelDisplay, name) {
			modelDisplay = name
			break
		}
	}
	if ctx.Settings.AlwaysThinkingEnabled {
		if strings.Contains(modelDisplay, "Opus") || strings.Contains(modelDisplay, "Sonnet") || strings.Contains(modelDisplay, "Claude") {
			if ctx.Settings.EffortLevel != "" {
				effortShort := string(unicode.ToUpper(rune(ctx.Settings.EffortLevel[0])))
				modelDisplay += " " + effortShort
			} else {
				modelDisplay += " T"
			}
		}
	}

	if ctx.Input.ContextWindow.RemainingPercentage != nil {
		used := 100 - int(*ctx.Input.ContextWindow.RemainingPercentage)
		usedStr := strconv.Itoa(used) + "%"
		// Build context bar: 4 blocks, each 25%
		filled := (used + 12) / 25 // 0-4 filled blocks
		if filled > 4 {
			filled = 4
		}
		bar := strings.Repeat("▓", filled) + strings.Repeat("░", 4-filled)
		var coloredBar string
		switch {
		case used > 75:
			coloredBar = red + bar + " " + usedStr + reset
		case used > 65:
			coloredBar = yellow + bar + " " + usedStr + reset
		default:
			coloredBar = bar + " " + usedStr
		}
		modelDisplay += " " + coloredBar
	}

	// Version warning
	if ctx.LatestVer != "" && versionLess(ctx.Input.Version, ctx.LatestVer) {
		modelDisplay += " " + yellow + "v" + ctx.Input.Version + reset
	}

	out.WriteString(modelDisplay)

	// === Section 2: Dir, git, extras, churn ===
	hasPR := strings.Contains(ctx.GitInfo, "PR/")
	isMain := ctx.GitBranch == "main" || ctx.GitBranch == "master"

	// Git info merged into dir display
	// On main with open issues: skip branch name (issues imply main) but keep dirty marker
	if ctx.GitInfo != "" && isMain && !hasPR && len(ctx.OpenIssues) > 0 {
		if ctx.GitDirty {
			dirDisplay += yellow + "*" + reset
		}
	} else if ctx.GitInfo != "" {
		gitShort := strings.TrimPrefix(ctx.GitInfo, "git:")
		if strings.HasPrefix(gitShort, "dirty") {
			dirDisplay += yellow + "*" + reset + " " + strings.TrimPrefix(gitShort, "dirty")
		} else if ctx.GitDirty {
			dirDisplay += " " + strings.TrimSuffix(gitShort, "*") + yellow + "*" + reset
		} else {
			dirDisplay += " " + gitShort
		}
	}

	if !hasPR && ctx.GitInfo != "" {
		if isMain && len(ctx.OpenIssues) > 0 {
			// Show open issues on main (branch name already suppressed)
			var issueStrs []string
			for _, oi := range ctx.OpenIssues {
				label := fmt.Sprintf("#%d", oi.Number)
				if oi.URL != "" {
					label = fmt.Sprintf("\033]8;;%s\a#%d\033]8;;\a", oi.URL, oi.Number)
				}
				issueStrs = append(issueStrs, cyan+label+reset)
			}
			dirDisplay += " " + strings.Join(issueStrs, " ")
			if ctx.HasMoreIssues {
				dirDisplay += " " + dim + "…" + reset
			}
		} else if !isMain {
			if ctx.IssueInfo != nil {
				issueColor := green
				if ctx.IssueInfo.Branch != ctx.GitBranch {
					issueColor = yellow
				}
				label := fmt.Sprintf("#%d", ctx.IssueInfo.Number)
				if ctx.IssueInfo.RepoURL != "" {
					label = fmt.Sprintf("\033]8;;%s/issues/%d\a#%d\033]8;;\a", ctx.IssueInfo.RepoURL, ctx.IssueInfo.Number, ctx.IssueInfo.Number)
				}
				dirDisplay += " " + issueColor + label + reset
			} else {
				dirDisplay += " " + dim + "(no issue)" + reset
			}
		}
	}

	// User@host: only show components that differ from expected
	var prefix string
	showUser := ctx.ExpectUser == "" || ctx.UserName != ctx.ExpectUser
	showHost := ctx.ExpectHost == "" || ctx.HostName != ctx.ExpectHost
	switch {
	case showUser && showHost:
		prefix = ctx.UserName + "@" + ctx.HostName + ":"
	case showUser:
		prefix = ctx.UserName + ":"
	case showHost:
		prefix = ctx.HostName + ":"
	}

	dirSection := prefix + dirDisplay

	// Line churn next to dir
	if ctx.Input.Cost.TotalLinesAdded != 0 || ctx.Input.Cost.TotalLinesRemoved != 0 {
		churn := green + "+" + strconv.Itoa(ctx.Input.Cost.TotalLinesAdded) + reset +
			"/" + red + "-" + strconv.Itoa(ctx.Input.Cost.TotalLinesRemoved) + reset
		dirSection += " " + churn
	}

	// Conditional extras
	if ctx.Input.OutputStyle.Name != "" && ctx.Input.OutputStyle.Name != "default" {
		dirSection += " " + dot + " style:" + ctx.Input.OutputStyle.Name
	}
	if ctx.Input.Vim != nil && ctx.Input.Vim.Mode != "" {
		dirSection += " " + dot + " vim:" + ctx.Input.Vim.Mode
	}
	if ctx.Input.Agent != nil && ctx.Input.Agent.Name != "" {
		dirSection += " " + dot + " agent:" + ctx.Input.Agent.Name
	}
	if ctx.Input.Worktree.Name != "" {
		dirSection += " " + dot + " wt:" + ctx.Input.Worktree.Name
	} else if ctx.Input.Worktree.Branch != "" {
		dirSection += " " + dot + " wt:" + ctx.Input.Worktree.Branch
	}

	out.WriteString(" " + pipe + " " + dirSection)

	// === Section 3: Cost · Time ===
	var rightParts []string
	if ctx.Input.Cost.TotalCostUSD != nil && *ctx.Input.Cost.TotalCostUSD != 0 {
		rightParts = append(rightParts, fmt.Sprintf("$%.2f", *ctx.Input.Cost.TotalCostUSD))
	}
	timeStr := ctx.Now.Format("01/02 15:04")
	if ctx.Input.Cost.TotalDurationMS != nil {
		mins := int(*ctx.Input.Cost.TotalDurationMS) / 60000
		if mins >= 1 {
			timeStr += fmt.Sprintf(" (%dm)", mins)
		}
	}
	rightParts = append(rightParts, timeStr)
	out.WriteString(" " + pipe + " " + strings.Join(rightParts, " "+dot+" "))

	return out.String()
}

// findGitRoot walks up from dir looking for a .git directory.
func findGitRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hookMain implements the --hook logic. It writes output to w (stdout in production).
// branch is the current git branch, projectDir is where .issue lives.
func hookMain(branch, projectDir string, w io.Writer) {
	isMain := branch == "main" || branch == "master"
	issuePath := filepath.Join(projectDir, ".issue")

	if isMain {
		os.Remove(issuePath)
		return
	}

	// Feature branch: check .issue
	num, issueBranch, err := parseIssueFile(issuePath)
	if os.IsNotExist(err) {
		fmt.Fprintf(w, "You're on branch `%s` with no linked issue. "+
			"Which GitHub issue are you working on? "+
			"Once confirmed, create a `.issue` file in the project root with the format: `<issue-number>,%s`\n", branch, branch)
		return
	}
	if err != nil {
		fmt.Fprintf(w, "The `.issue` file has an invalid format. "+
			"Expected `<issue-number>,%s`. Please fix or delete it.\n", branch)
		return
	}

	if issueBranch != branch {
		fmt.Fprintf(w, "The `.issue` file references issue #%d on branch `%s`, "+
			"but you're on `%s`. Update the `.issue` file to `%d,%s` "+
			"or create a new issue for this branch.\n", num, issueBranch, branch, num, branch)
		return
	}

	// Matching — no output needed
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--hook" {
		// Get current branch
		branchBytes, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil {
			return // Not in a git repo, nothing to do
		}
		branch := strings.TrimSpace(string(branchBytes))

		// Find project root (walk up to find .git)
		dir, _ := os.Getwd()
		projectDir := findGitRoot(dir)
		if projectDir == "" {
			return
		}

		hookMain(branch, projectDir, os.Stdout)
		return
	}

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

	projectDir := normalizePath(input.Workspace.ProjectDir)

	var (
		gitInfo       string
		gitBranch     string
		gitDirty      bool
		settings      Settings
		issueInfo     *IssueInfo
		openIssues    []OpenIssue
		hasMoreIssues bool
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
			gitBranch = branch
			diffOut, _ := cachedRun(filepath.Join(gitCacheDir, "diff-index"), 1*time.Second, "git", "-C", dir, "--no-optional-locks", "diff-index", "HEAD", "--")
			gitDirty = diffOut != ""
			if gitDirty {
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
				prData, _ := cachedRun(cachePath, 10*time.Second, "gh", "pr", "view", branch, "--json", "number,state,body,url,reviewDecision,isDraft")
				if prData != "" {
					var pr struct {
						Number         int    `json:"number"`
						State          string `json:"state"`
						Body           string `json:"body"`
						URL            string `json:"url"`
						ReviewDecision string `json:"reviewDecision"`
						IsDraft        bool   `json:"isDraft"`
					}
					if json.Unmarshal([]byte(prData), &pr) == nil {
						prLabel := fmt.Sprintf("PR/%d", pr.Number)
						if pr.URL != "" {
							prLabel = fmt.Sprintf("\033]8;;%s\aPR/%d\033]8;;\a", pr.URL, pr.Number)
						}
						// Build issue→PR display
						repoURL := ""
						if i := strings.LastIndex(pr.URL, "/pull/"); i >= 0 {
							repoURL = pr.URL[:i]
						}

						issueCacheDir := filepath.Join(home, ".claude", ".issue-cache")
						os.MkdirAll(issueCacheDir, 0755)
						cleanOldFiles(issueCacheDir, 7*24*time.Hour)

						var issueLinks []string
						if matches := issueRe.FindAllString(pr.Body, 3); len(matches) > 0 {
							for _, m := range matches {
								num := strings.TrimPrefix(m, "#")
								// Fetch issue state
								issuePath := filepath.Join(issueCacheDir, num+".json")
								issueData, _ := cachedRun(issuePath, 30*time.Second, "gh", "issue", "view", num, "--json", "state")
								issueColor := green
								if issueData != "" {
									var issue struct {
										State string `json:"state"`
									}
									if json.Unmarshal([]byte(issueData), &issue) == nil && issue.State == "CLOSED" {
										issueColor = dim
									}
								}
								if repoURL != "" {
									issueLinks = append(issueLinks, issueColor+fmt.Sprintf("\033]8;;%s/issues/%s\a%s\033]8;;\a", repoURL, num, m)+reset)
								} else {
									issueLinks = append(issueLinks, issueColor+m+reset)
								}
							}
						}

						// PR color based on review state (matches Claude Code convention)
						prColor := yellow // pending review (default)
						switch {
						case pr.IsDraft:
							prColor = dim
						case pr.State == "MERGED":
							prColor = magenta
						case pr.ReviewDecision == "APPROVED":
							prColor = green
						case pr.ReviewDecision == "CHANGES_REQUESTED":
							prColor = red
						}

						dirty := ""
						if diffOut != "" {
							dirty = "dirty"
						}
						if len(issueLinks) > 0 {
							gitInfo = "git:" + dirty + strings.Join(issueLinks, ",") + dim + "→" + reset + prColor + prLabel + reset
						} else {
							gitInfo = "git:" + dirty + prColor + prLabel + reset
						}
					}
				}

				// If no PR found, check for .issue file
				if !strings.Contains(gitInfo, "PR/") {
					issueFilePath := filepath.Join(projectDir, ".issue")
					if projectDir == "" {
						issueFilePath = filepath.Join(dir, ".issue")
					}
					if num, issueBranch, err := parseIssueFile(issueFilePath); err == nil {
						issueInfo = &IssueInfo{
							Number: num,
							Branch: issueBranch,
						}
						// Try to get repo URL from git remote
						remoteCachePath := filepath.Join(gitCacheDir, "remote-url")
						if remoteURL, ok := cachedRun(remoteCachePath, 60*time.Second, "git", "-C", dir, "--no-optional-locks", "remote", "get-url", "origin"); ok && remoteURL != "" {
							repoURL := remoteURL
							repoURL = strings.TrimSuffix(repoURL, ".git")
							if strings.HasPrefix(repoURL, "git@") {
								repoURL = strings.Replace(repoURL, ":", "/", 1)
								repoURL = strings.Replace(repoURL, "git@", "https://", 1)
							}
							issueInfo.RepoURL = repoURL
						}
					}
				}
			} else {
				// On main/master: fetch open issues
				issueCacheDir := filepath.Join(home, ".claude", ".issue-list-cache")
				os.MkdirAll(issueCacheDir, 0755)
				cleanOldFiles(issueCacheDir, 7*24*time.Hour)
				repoKey := sanitizePath(dir)
				issueListPath := filepath.Join(issueCacheDir, repoKey+".json")
				// gh issue list defaults to newest-first sort; --sort flag not supported with --json
				issueListData, _ := cachedRun(issueListPath, 30*time.Second, "gh", "issue", "list", "--limit", "4", "--json", "number,url", "--state", "open")
				if issueListData != "" {
					var issues []OpenIssue
					if json.Unmarshal([]byte(issueListData), &issues) == nil && len(issues) > 0 {
						hasMore := len(issues) > 3
						if hasMore {
							openIssues = issues[:3]
						} else {
							openIssues = issues
						}
						hasMoreIssues = hasMore
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
		ExpectUser:  os.Getenv("CLAUDE_STATUSLINE_USER"),
		ExpectHost:  os.Getenv("CLAUDE_STATUSLINE_HOSTNAME"),
		GitInfo:       gitInfo,
		GitBranch:     gitBranch,
		GitDirty:      gitDirty,
		LatestVer:     latestVer,
		Now:           time.Now(),
		IssueInfo:     issueInfo,
		OpenIssues:    openIssues,
		HasMoreIssues: hasMoreIssues,
	}
	fmt.Println(renderStatusline(ctx))
}
