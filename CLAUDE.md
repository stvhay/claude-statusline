# claude-statusline

A fast, zero-dependency Go binary that generates a rich statusline for Claude Code.

## Build & Test

```bash
go build -o statusline .
go test -v ./...
```

## Architecture

Single-file Go binary (`main.go`) — reads JSON session state from stdin, outputs a formatted statusline string. All expensive operations (git, gh, npm) use background-refresh caching.

## Release

Releases are managed by GoReleaser. Push a semver tag to trigger:

```bash
git tag v1.2.3
git push origin v1.2.3
```

GoReleaser builds cross-platform binaries and creates a GitHub Release. Version is injected via `-X main.version={{.Version}}` ldflags.

## Development Workflow

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow. In short: file an issue, create a branch, write a plan in `docs/plans/`, implement, verify, PR.
