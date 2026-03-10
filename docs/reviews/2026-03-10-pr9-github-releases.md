# PR #9 Review: Publish pre-built binaries as GitHub releases

**Date:** 2026-03-10
**PR:** https://github.com/stvhay/claude-statusline/pull/9
**Status:** Open (285 additions, 5 deletions)

## Overview

Adds a full release pipeline: `--version` flag, GoReleaser config for
cross-platform builds, GitHub Actions release workflow on `v*` tags, and an
updated `install.sh` that downloads pre-built binaries (no Go toolchain
required). README updated with release badge and new install instructions.

## Issues Found & Fixes Applied

### 1. Silent download failure (Medium) — FIXED

**File:** `install.sh:67`
**Problem:** `curl -sL` returns exit code 0 on HTTP errors (e.g. 404 for
unsupported arch), writing the HTML error page to the file instead of failing.
**Fix:** Changed to `curl -sfL` — the `-f` flag makes curl fail silently on
HTTP errors, returning a non-zero exit code.

### 2. TMPDIR variable shadowing (Low) — FIXED

**File:** `install.sh:63`
**Problem:** `TMPDIR` is a well-known POSIX environment variable used by
`mktemp` and other tools. Overwriting it can cause subtle issues if any
subprocesses rely on it.
**Fix:** Renamed to `WORK_DIR` throughout the script.

### 3. wget latest-tag detection unreliable (Low) — FIXED

**File:** `install.sh:43-47`
**Problem:** The original approach used redirect-following heuristics
(`curl -sI` parsing Location header, `wget --max-redirect=0` parsing stderr)
which is fragile — GitHub's redirect behavior can vary, and the wget path
with `--max-redirect=0` would fail since the redirect is required.
**Fix:** Switched both curl and wget paths to use the GitHub REST API
(`/repos/{owner}/{repo}/releases/latest`), parsing the `tag_name` field from
the JSON response. This is reliable and works identically for both tools.

## Nits (not fixed)

- **No checksum verification:** GoReleaser generates `checksums.txt` but the
  installer doesn't verify downloads against it. Worth adding for a
  `curl | bash` install pattern.
- **Large ignore matrix in .goreleaser.yml:** Could use a `targets` allowlist
  instead for conciseness.
- **Go version pinning:** `release.yml` pins `go-version: "1.23"` — consider
  matching `go.mod` or using `stable`.
