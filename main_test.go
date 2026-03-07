package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func floatPtr(f float64) *float64 {
	return &f
}

func TestShellescape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "hello", "'hello'"},
		{"empty", "", "''"},
		{"with spaces", "hello world", "'hello world'"},
		{"single quote", "it's", "'it'\\''s'"},
		{"multiple quotes", "a'b'c", "'a'\\''b'\\''c'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellescape(tt.in)
			if got != tt.want {
				t.Errorf("shellescape(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"absolute path", "/Users/hays/Projects", "Users_hays_Projects"},
		{"no leading slash", "foo/bar", "foo_bar"},
		{"root", "/", ""},
		{"nested", "/a/b/c/d", "a_b_c_d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizePath(tt.in)
			if got != tt.want {
				t.Errorf("sanitizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"regular path", "/Users/hays/Projects", "/Users/hays/Projects"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePath(tt.in)
			if got != tt.want {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func baseContext() RenderContext {
	return RenderContext{
		Input: StatusInput{
			Version: "1.0.0",
		},
		UserName: "alice",
		HostName: "box",
		HomeDir:  "/home/alice",
		Now:      time.Date(2026, 3, 6, 14, 30, 0, 0, time.UTC),
	}
}

func TestRenderBasic(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/home/alice/projects/foo"
	ctx.Input.Model.DisplayName = "Opus"

	got := renderStatusline(ctx)
	plain := stripANSI(got)

	if !strings.HasPrefix(plain, "Opus") {
		t.Errorf("expected model prefix, got: %s", plain)
	}
	if !strings.Contains(plain, "alice@box:~/projects/foo") {
		t.Errorf("expected user@host:dir in output, got: %s", plain)
	}
	if !strings.Contains(plain, "03/06 14:30") {
		t.Errorf("expected formatted time, got: %s", plain)
	}
}

func TestRenderUserHostSuppression(t *testing.T) {
	tests := []struct {
		name       string
		expectUser string
		expectHost string
		wantInDir  string
	}{
		{"both shown", "", "", "alice@box:/tmp"},
		{"user hidden", "alice", "", "box:/tmp"},
		{"host hidden", "", "box", "alice:/tmp"},
		{"both hidden", "alice", "box", "│ /tmp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := baseContext()
			ctx.Input.Workspace.CurrentDir = "/tmp"
			ctx.Input.Model.DisplayName = "Opus"
			ctx.ExpectUser = tt.expectUser
			ctx.ExpectHost = tt.expectHost

			got := stripANSI(renderStatusline(ctx))
			if !strings.Contains(got, tt.wantInDir) {
				t.Errorf("expected %q in output, got: %s", tt.wantInDir, got)
			}
		})
	}
}

func TestRenderHomeDirShortening(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/home/alice/projects/foo"
	ctx.Input.Workspace.ProjectDir = "/home/alice/projects/foo"
	ctx.Input.Model.DisplayName = "Opus"

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "~/projects/foo") {
		t.Errorf("expected ~ shortening, got: %s", got)
	}
}

func TestRenderProjectRelativeSubdir(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.ProjectDir = "/home/alice/projects/foo"
	ctx.Input.Workspace.CurrentDir = "/home/alice/projects/foo/src/lib"
	ctx.Input.Model.DisplayName = "Opus"

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "~/projects/foo") {
		t.Errorf("expected project dir, got: %s", got)
	}
	if !strings.Contains(got, "(src/lib)") {
		t.Errorf("expected relative subdir, got: %s", got)
	}
}

func TestRenderGitInfo(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:feature-branch*"

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "feature-branch*") {
		t.Errorf("expected git info, got: %s", got)
	}
}

func TestRenderGitInfoWithPR(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:feature PR #42 #10,#11"

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "PR #42") {
		t.Errorf("expected PR info, got: %s", got)
	}
	if !strings.Contains(got, "#10,#11") {
		t.Errorf("expected issue refs, got: %s", got)
	}
}

func TestRenderModelThinkingLevel(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		thinking    bool
		effort      string
		wantContain string
	}{
		{"no thinking", "Opus", false, "", "Opus"},
		{"thinking default", "Opus", true, "", "Opus T"},
		{"thinking high", "Sonnet", true, "high", "Sonnet H"},
		{"thinking low", "Claude", true, "low", "Claude L"},
		{"non-claude model", "GPT-4", true, "high", "GPT-4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := baseContext()
			ctx.Input.Workspace.CurrentDir = "/tmp"
			ctx.Input.Model.DisplayName = tt.model
			ctx.Settings.AlwaysThinkingEnabled = tt.thinking
			ctx.Settings.EffortLevel = tt.effort

			got := stripANSI(renderStatusline(ctx))
			if !strings.Contains(got, tt.wantContain) {
				t.Errorf("expected %q in output, got: %s", tt.wantContain, got)
			}
		})
	}
}

func TestRenderContextWindowColors(t *testing.T) {
	tests := []struct {
		name      string
		remaining float64
		wantColor string
		wantPct   string
	}{
		{"low usage", 90, "", "10%"},
		{"medium usage", 30, yellow, "70%"},
		{"high usage", 20, red, "80%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := baseContext()
			ctx.Input.Workspace.CurrentDir = "/tmp"
			ctx.Input.Model.DisplayName = "Opus"
			ctx.Input.ContextWindow.RemainingPercentage = &tt.remaining

			got := renderStatusline(ctx)
			plain := stripANSI(got)
			if !strings.Contains(plain, tt.wantPct) {
				t.Errorf("expected %q in output, got: %s", tt.wantPct, plain)
			}
			// Check bar contains blocks
			if !strings.Contains(plain, "▓") && !strings.Contains(plain, "░") {
				t.Errorf("expected context bar with ▓ or ░, got: %s", plain)
			}
			if tt.wantColor != "" && !strings.Contains(got, tt.wantColor) {
				t.Errorf("expected color %q in output, got: %s", tt.wantColor, got)
			}
		})
	}
}

func TestRenderVersionOutdated(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Version = "1.0.0"
	ctx.LatestVer = "1.1.0"

	got := renderStatusline(ctx)
	if !strings.Contains(got, yellow+"v1.0.0"+reset) {
		t.Errorf("expected yellow version warning, got: %s", got)
	}
}

func TestRenderVersionCurrent(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Version = "1.1.0"
	ctx.LatestVer = "1.1.0"

	got := stripANSI(renderStatusline(ctx))
	if strings.Contains(got, "v1.1.0") {
		t.Errorf("expected no version when current, got: %s", got)
	}
}

func TestRenderVersionNewerThanCache(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Version = "1.1.0"
	ctx.LatestVer = "1.0.0"

	got := stripANSI(renderStatusline(ctx))
	if strings.Contains(got, "v1.1.0") {
		t.Errorf("expected no version when local is newer than cache, got: %s", got)
	}
}

func TestRenderProjectsDirStripsPrefix(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/home/alice/Projects/myapp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.ProjectsDir = "/home/alice/Projects"

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "alice@box:myapp") {
		t.Errorf("expected stripped dir 'myapp', got: %s", got)
	}
	if strings.Contains(got, "/home/alice/Projects/myapp") {
		t.Errorf("expected prefix stripped, got: %s", got)
	}
}

func TestRenderProjectsDirNoMatchFallsBack(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/other"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.ProjectsDir = "/home/alice/Projects"

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "alice@box:/tmp/other") {
		t.Errorf("expected full path when outside projects dir, got: %s", got)
	}
}

func TestRenderOptionalExtras(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.OutputStyle.Name = "compact"
	ctx.Input.Vim = &struct {
		Mode string `json:"mode"`
	}{Mode: "normal"}
	ctx.Input.Agent = &struct {
		Name string `json:"name"`
	}{Name: "explorer"}
	ctx.Input.Worktree.Name = "feat-x"

	got := stripANSI(renderStatusline(ctx))
	for _, want := range []string{"style:compact", "vim:normal", "agent:explorer", "wt:feat-x"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got: %s", want, got)
		}
	}
}

func TestRenderWorktreeBranchFallback(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Worktree.Branch = "my-branch"

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "wt:my-branch") {
		t.Errorf("expected worktree branch fallback, got: %s", got)
	}
}

func TestRenderDefaultStyleOmitted(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.OutputStyle.Name = "default"

	got := stripANSI(renderStatusline(ctx))
	if strings.Contains(got, "style:") {
		t.Errorf("expected default style omitted, got: %s", got)
	}
}

func TestRenderCostAndChurn(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Cost.TotalCostUSD = floatPtr(1.50)
	ctx.Input.Cost.TotalLinesAdded = 42
	ctx.Input.Cost.TotalLinesRemoved = 7

	got := renderStatusline(ctx)
	plain := stripANSI(got)
	if !strings.Contains(plain, "$1.50") {
		t.Errorf("expected cost, got: %s", plain)
	}
	if !strings.Contains(plain, "+42/-7") {
		t.Errorf("expected churn, got: %s", plain)
	}
	// Verify colors on churn
	if !strings.Contains(got, green+"+42"+reset) {
		t.Errorf("expected green on added lines, got: %s", got)
	}
	if !strings.Contains(got, red+"-7"+reset) {
		t.Errorf("expected red on removed lines, got: %s", got)
	}
}

func TestRenderZeroCostOmitted(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Cost.TotalCostUSD = floatPtr(0)

	got := stripANSI(renderStatusline(ctx))
	if strings.Contains(got, "$") {
		t.Errorf("expected zero cost omitted, got: %s", got)
	}
}

func TestRenderSessionDuration(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Cost.TotalDurationMS = floatPtr(300000) // 5 minutes

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "(5m)") {
		t.Errorf("expected 5m duration, got: %s", got)
	}
}

func TestRenderShortDurationOmitted(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.Input.Cost.TotalDurationMS = floatPtr(30000) // 30 seconds

	got := stripANSI(renderStatusline(ctx))
	if strings.Contains(got, "(0m)") {
		t.Errorf("expected sub-minute duration omitted, got: %s", got)
	}
}

func TestRenderIssueMatchingBranch(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:feature-x"
	ctx.IssueInfo = &IssueInfo{Number: 42, Branch: "feature-x", RepoURL: "https://github.com/org/repo"}

	got := renderStatusline(ctx)
	plain := stripANSI(got)
	if !strings.Contains(plain, "#42") {
		t.Errorf("expected issue #42, got: %s", plain)
	}
	if !strings.Contains(got, green) {
		t.Errorf("expected green color for matching issue, got: %s", got)
	}
}

func TestRenderIssueMismatchedBranch(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:other-branch"
	ctx.IssueInfo = &IssueInfo{Number: 42, Branch: "feature-x", RepoURL: "https://github.com/org/repo"}

	got := renderStatusline(ctx)
	plain := stripANSI(got)
	if !strings.Contains(plain, "#42") {
		t.Errorf("expected issue #42, got: %s", plain)
	}
	if !strings.Contains(got, yellow) {
		t.Errorf("expected yellow color for mismatched issue, got: %s", got)
	}
}

func TestRenderNoIssueNoPR(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:feature-x"
	// No IssueInfo, no PR in GitInfo

	got := renderStatusline(ctx)
	plain := stripANSI(got)
	if !strings.Contains(plain, "(no issue)") {
		t.Errorf("expected '(no issue)' hint, got: %s", plain)
	}
}

func TestRenderPRTakesPriorityOverIssue(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:#10→PR/5"
	ctx.IssueInfo = &IssueInfo{Number: 42, Branch: "feature-x"}

	got := stripANSI(renderStatusline(ctx))
	if strings.Contains(got, "(no issue)") {
		t.Errorf("should not show (no issue) when PR exists, got: %s", got)
	}
	if !strings.Contains(got, "PR/5") {
		t.Errorf("expected PR info preserved, got: %s", got)
	}
}

func TestRenderOpenIssuesOnMain(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:main"
	ctx.OpenIssues = []OpenIssue{
		{Number: 43, URL: "https://github.com/org/repo/issues/43"},
		{Number: 42, URL: "https://github.com/org/repo/issues/42"},
		{Number: 41, URL: "https://github.com/org/repo/issues/41"},
	}
	ctx.HasMoreIssues = true

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "#43") || !strings.Contains(got, "#42") || !strings.Contains(got, "#41") {
		t.Errorf("expected all 3 issues, got: %s", got)
	}
	if !strings.Contains(got, "...") {
		t.Errorf("expected ... for more issues, got: %s", got)
	}
	if strings.Contains(got, "main") {
		t.Errorf("should not show branch name when issues are displayed, got: %s", got)
	}
}

func TestRenderOpenIssuesOnMainNoMore(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:main"
	ctx.OpenIssues = []OpenIssue{
		{Number: 42, URL: "https://github.com/org/repo/issues/42"},
	}
	ctx.HasMoreIssues = false

	got := stripANSI(renderStatusline(ctx))
	if !strings.Contains(got, "#42") {
		t.Errorf("expected issue #42, got: %s", got)
	}
	if strings.Contains(got, "...") {
		t.Errorf("should not show ... with <=3 issues, got: %s", got)
	}
}

func TestRenderMainBranchNoIssueHint(t *testing.T) {
	ctx := baseContext()
	ctx.Input.Workspace.CurrentDir = "/tmp/repo"
	ctx.Input.Model.DisplayName = "Opus"
	ctx.GitInfo = "git:main"
	// No OpenIssues, no IssueInfo

	got := stripANSI(renderStatusline(ctx))
	if strings.Contains(got, "(no issue)") {
		t.Errorf("should not show (no issue) on main, got: %s", got)
	}
}

// E2E test: build the binary and pipe JSON through it
func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	binPath := t.TempDir() + "/statusline"
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	input := StatusInput{
		Version: "1.0.0",
	}
	input.Workspace.CurrentDir = "/tmp/test-project"
	input.Workspace.ProjectDir = "/tmp/test-project"
	input.Model.DisplayName = "Claude Opus"
	input.ContextWindow.RemainingPercentage = floatPtr(75.0)

	jsonData, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = strings.NewReader(string(jsonData))
	// Prevent the binary from using real git/gh caches
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary failed: %v", err)
	}

	plain := stripANSI(strings.TrimSpace(string(out)))

	// Verify key components are present in the output
	checks := []string{
		"/tmp/test-project",
		"Claude Opus",
		"25%",
	}
	for _, want := range checks {
		if !strings.Contains(plain, want) {
			t.Errorf("e2e output missing %q, got: %s", want, plain)
		}
	}
}

func TestE2EProjectsDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	binPath := t.TempDir() + "/statusline"
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	input := StatusInput{
		Version: "1.0.0",
	}
	input.Workspace.CurrentDir = "/tmp/Projects/myapp"
	input.Workspace.ProjectDir = "/tmp/Projects/myapp"
	input.Model.DisplayName = "Opus"

	jsonData, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}

	tmpHome := t.TempDir()
	cmd := exec.Command(binPath)
	cmd.Stdin = strings.NewReader(string(jsonData))
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "CLAUDE_STATUSLINE_PROJECTS_DIR=/tmp/Projects")

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary failed: %v", err)
	}

	plain := stripANSI(strings.TrimSpace(string(out)))
	if !strings.Contains(plain, "myapp") {
		t.Errorf("expected stripped dir 'myapp', got: %s", plain)
	}
	if strings.Contains(plain, "/tmp/Projects/myapp") {
		t.Errorf("expected projects dir prefix stripped, got: %s", plain)
	}
}

func TestE2EIssueFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	binPath := t.TempDir() + "/statusline"
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Create a fake project dir with a .issue file
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, ".issue"), []byte("42,feature-x\n"), 0644)

	input := StatusInput{Version: "1.0.0"}
	input.Workspace.CurrentDir = projectDir
	input.Workspace.ProjectDir = projectDir
	input.Model.DisplayName = "Opus"

	jsonData, _ := json.Marshal(input)

	cmd := exec.Command(binPath)
	cmd.Stdin = strings.NewReader(string(jsonData))
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary failed: %v", err)
	}

	// The binary won't have git info in this temp dir, so .issue won't trigger
	// (git check will fail). This test mainly verifies the binary doesn't crash
	// with a .issue file present.
	_ = stripANSI(strings.TrimSpace(string(out)))
}

func TestHookMainOnMain(t *testing.T) {
	dir := t.TempDir()
	// Create .issue file that should be deleted
	issuePath := filepath.Join(dir, ".issue")
	os.WriteFile(issuePath, []byte("42,old-branch\n"), 0644)

	msg := runHook("main", dir)
	if msg != "" {
		t.Errorf("expected no output on main, got: %q", msg)
	}
	if _, err := os.Stat(issuePath); !os.IsNotExist(err) {
		t.Errorf("expected .issue to be deleted on main")
	}
}

func TestHookMainOnMaster(t *testing.T) {
	dir := t.TempDir()
	msg := runHook("master", dir)
	if msg != "" {
		t.Errorf("expected no output on master, got: %q", msg)
	}
}

func TestHookFeatureBranchNoIssue(t *testing.T) {
	dir := t.TempDir()
	msg := runHook("feature-x", dir)
	if msg == "" {
		t.Errorf("expected message when no .issue on feature branch")
	}
	if !strings.Contains(msg, "feature-x") {
		t.Errorf("expected branch name in message, got: %q", msg)
	}
}

func TestHookFeatureBranchMatchingIssue(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".issue"), []byte("42,feature-x\n"), 0644)

	msg := runHook("feature-x", dir)
	if msg != "" {
		t.Errorf("expected no output when .issue matches, got: %q", msg)
	}
}

func TestHookFeatureBranchMismatchedIssue(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".issue"), []byte("42,old-branch\n"), 0644)

	msg := runHook("feature-y", dir)
	if msg == "" {
		t.Errorf("expected warning when .issue mismatches")
	}
	if !strings.Contains(msg, "42") && !strings.Contains(msg, "old-branch") {
		t.Errorf("expected issue number and old branch in warning, got: %q", msg)
	}
}

func TestE2EHookNoIssue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	binPath := t.TempDir() + "/statusline"
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Run --hook in the current repo (which is a git repo)
	cmd := exec.Command(binPath, "--hook")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v\n%s", err, out)
	}
	// We're on some branch — just verify it doesn't crash
	// The output depends on branch and .issue state, which we can't control in this test
	_ = string(out)
}

// runHook is a test helper that calls hookMain with the given branch and project dir
func runHook(branch, projectDir string) string {
	var buf strings.Builder
	hookMain(branch, projectDir, &buf)
	return buf.String()
}
