# statusline

## Purpose

A zero-dependency Go binary that renders a rich terminal statusline for Claude Code sessions. It reads JSON session state from stdin, gathers git/PR/issue context via cached background subprocesses, and outputs a single ANSI-formatted line to stdout. The design prioritizes never blocking the terminal — all external commands (git, gh, npm) use file-based caching with background refresh so the binary returns instantly even when network calls are in flight.

## Core Mechanism

The binary operates as a pure pipeline: JSON in, formatted string out, with side effects limited to cache management and stats file writes. External data is fetched via `cachedRun`, which returns stale data immediately and spawns a detached child process to refresh the cache file. An atomic lock file (`O_CREATE|O_EXCL`) prevents duplicate concurrent fetches.

**Key files:**
- `main.go` — entire implementation: types, caching, git/PR/issue gathering, rendering, hook mode, stats writer, entrypoint
- `main_test.go` — table-driven unit tests for rendering and E2E tests for hook mode
- `install.sh` — platform-detecting installer that downloads release binaries and configures Claude Code settings
- `.goreleaser.yml` — cross-platform build matrix and release configuration

## Public Interface

| Export | Used By | Contract |
|---|---|---|
| `statusline` binary (stdin→stdout) | Claude Code `status_line_command` setting | Reads JSON matching `StatusInput` schema, prints one ANSI-formatted line |
| `--version` / `-v` flag | Users, install.sh | Prints `statusline <version>` to stdout |
| `--hook` flag | Claude Code `UserPromptSubmit` hook | Enforces `.issue` file on feature branches, silent on main/master |
| `.claude/.statusline-stats` file | External tools (tmux, shell prompts) | Writes `context_percent=N` and optionally `cost_usd=N.NN` as key=value lines |
| Environment variables | User configuration | `CLAUDE_STATUSLINE_USER`, `CLAUDE_STATUSLINE_HOSTNAME`, `CLAUDE_STATUSLINE_PROJECTS_DIR` |

## Invariants

| ID | Invariant | Enforcement | Why It Matters |
|---|---|---|---|
| INV-1 | `renderStatusline` is a pure function of `RenderContext` — no I/O, no globals, no time calls | structural | Enables deterministic table-driven testing without mocks |
| INV-2 | All external commands execute via `cachedRun`/`bgRefresh`, never inline in the render path | reasoning-required | Violating this blocks the terminal while waiting for git/gh/npm |
| INV-3 | `bgRefresh` uses `O_CREATE\|O_EXCL` atomic lock files with 30-second staleness detection | structural | Prevents duplicate concurrent fetches and recovers from crashed processes |
| INV-4 | Cache files are plain text (command stdout), not structured formats owned by the binary | structural | Any cache file can be deleted without data loss — background refresh regenerates it |
| INV-5 | The `--hook` flag writes to stdout only when user action is needed, silent otherwise | reasoning-required | Hook output is shown to the user as a system prompt; unnecessary output is noise |
| INV-6 | `writeStatsFile` uses atomic write (write to .tmp, rename) | structural | Prevents readers from seeing partial writes |
| INV-7 | Version is injected at build time via `-X main.version` ldflags; source default is `"dev"` | structural | Binary always knows its version without runtime lookups; GoReleaser sets it automatically |

**Enforcement classification:**
- **structural** — enforced by type system, API design, or code structure; pattern-matchable and universally respected
- **reasoning-required** — needs architectural understanding; model-tier dependent

Prioritize converting reasoning-required invariants to structural via API design.

## Failure Modes

| ID | Symptom | Cause | Fix |
|---|---|---|---|
| FAIL-1 | Statusline shows stale git branch or PR state | Cache TTL hasn't expired; background refresh hasn't completed | Wait for TTL expiry (1s for git, 10s for PR, 30s for issues) or delete cache files in `~/.claude/.git-cache/` |
| FAIL-2 | Statusline shows no git info on first invocation | No cache exists yet; `cachedRun` returns empty and triggers background fetch | Run statusline again after ~1 second; the background process will have written the cache |
| FAIL-3 | Lock file prevents background refresh permanently | Process crashed between lock creation and cleanup | Lock has 30-second staleness check; will auto-recover on next invocation after 30s |
| FAIL-4 | PR info missing on feature branch | `gh` CLI not authenticated or not installed | Run `gh auth login`; the binary gracefully degrades (shows branch without PR) |
| FAIL-5 | Version outdated warning never appears | `npm` not installed or `npm view` fails | Install npm; the version check is best-effort with 1-hour cache TTL |
| FAIL-6 | `.statusline-stats` file not written | `projectDir` is empty or context window data unavailable | Ensure Claude Code provides `project_dir` and `context_window.remaining_percentage` in the JSON input |

## Decision Framework

| Situation | Action | Invariant |
|---|---|---|
| Adding a new external data source (e.g., new CLI command) | Use `cachedRun` with an appropriate TTL; never call the command synchronously in the render path | INV-2 |
| Adding output to `--hook` mode | Only emit text when the user needs to take action; verify the silent path in tests | INV-5 |

## Testing

Tests live in `main_test.go` using Go's standard `testing` package.

- **INV-1:** All rendering tests call `renderStatusline(ctx)` with fully constructed `RenderContext` values — no subprocess mocking needed
- **INV-3, INV-4:** Not directly unit-tested; structural guarantees from `O_EXCL` flag and plain-text cache format
- **INV-5:** E2E tests for hook mode verify stdout output for each branch/issue-file scenario
- **INV-6:** Structural — `os.Rename` is atomic on POSIX; no test needed
- **INV-7:** Build-time injection; verified by `--version` flag

**Convention:** `go test -v ./...` must pass before merge. CI enforces this via the `test` job in `.github/workflows/ci.yml`.

## Dependencies

| Dependency | Type | SPEC.md Path |
|---|---|---|
| Go standard library | external | N/A — stdlib |
| `gh` CLI | external | N/A — GitHub CLI for PR/issue data |
| `git` CLI | external | N/A — git operations |
| `npm` CLI | external | N/A — version check only |
| GoReleaser | external | N/A — build/release tooling |
| Claude Code JSON schema | external | N/A — undocumented; reverse-engineered from `StatusInput` struct |
