# Design: Add Tests

**Issue:** None (exploratory)
**Date:** 2026-03-06
**Branch:** add-tests

## Refactoring

Extract `renderStatusline` from `main()`. All formatting logic moves into a pure function that takes a `RenderContext` struct and returns a string. `main()` becomes: read stdin, parse JSON, gather context, call `renderStatusline`, print.

```go
type RenderContext struct {
    Input     StatusInput
    Settings  Settings
    UserName  string
    HostName  string
    HomeDir   string
    GitInfo   string
    LatestVer string
    Now       time.Time
}

func renderStatusline(ctx RenderContext) string
```

## Unit Tests (table-driven, `main_test.go`)

- `shellescape` — single quotes, embedded quotes, empty string
- `normalizePath` — `/private/tmp` shortening, passthrough
- `sanitizePath` — slash replacement, leading slash removal
- `renderStatusline` — bulk of tests:
  - Basic user@host:dir
  - Home directory shortening
  - Project-relative subdirectory
  - Git branch + dirty indicator
  - PR info formatting
  - Model with thinking level / effort
  - Context window usage with color thresholds (exact ANSI match)
  - Version outdated warning
  - Optional extras (vim, agent, worktree, style)
  - Cost and line churn
  - Session duration

## E2E Test

Build binary, pipe JSON to stdin, assert stdout. Strip ANSI for content validation.

## Out of scope

- No mocking of cachedRun/bgRefresh/filesystem
- No golden files
- No separate package
