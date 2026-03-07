# claude-statusline

A fast, zero-dependency Go binary that generates a rich statusline for [Claude Code](https://claude.ai/claude-code).

Reads JSON session state from stdin, outputs a single formatted line with:

- `user@host:dir` with project-relative subdirectory (user/host conditionally hidden via env vars)
- Git branch, dirty status, and PR info (number, state, linked issues)
- Model name with thinking level and context window usage
- Version outdated warning
- Output style, vim mode, agent, worktree indicators
- Session cost, line churn (+added/-removed), duration

All expensive operations (git, `gh pr view`, `npm view`) use background-refresh caching so the statusline never blocks.

## Build

```sh
go build -o statusline .
```

Or with nix:

```sh
nix develop  # provides Go toolchain
go build -o statusline .
```

## Usage

Configure in Claude Code settings as the statusline command. It receives JSON on stdin and prints the formatted line to stdout.

## License

MIT
