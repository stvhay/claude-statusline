# claude-statusline

[![Release](https://github.com/stvhay/claude-statusline/actions/workflows/release.yml/badge.svg)](https://github.com/stvhay/claude-statusline/actions/workflows/release.yml)

A fast, zero-dependency Go binary that generates a rich statusline for [Claude Code](https://claude.ai/claude-code).

Reads JSON session state from stdin, outputs a single formatted line with:

- `user@host:dir` with project-relative subdirectory (user/host conditionally hidden via env vars)
- Git branch, dirty status, and PR info (number, state, linked issues)
- Model name with thinking level and context window usage
- Version outdated warning
- Output style, vim mode, agent, worktree indicators
- Session cost, line churn (+added/-removed), duration

All expensive operations (git, `gh pr view`, `npm view`) use background-refresh caching so the statusline never blocks.

## Install

Download and install the latest release (no Go toolchain required):

```sh
curl -sL https://raw.githubusercontent.com/stvhay/claude-statusline/main/install.sh | bash
```

The install script automatically detects your OS and architecture, downloads the correct binary, and configures Claude Code settings.

## Build from source

If you prefer to build from source or your platform isn't supported:

```sh
go build -o statusline .
```

Or with nix:

```sh
nix develop  # provides Go toolchain
go build -o statusline .
```

Then run `./install.sh` from the repo to configure Claude Code settings, or manually configure as described below.

## Usage

Configure in Claude Code settings as the statusline command. It receives JSON on stdin and prints the formatted line to stdout.

## License

MIT
