#!/usr/bin/env bash
# Compute next semver version from git tags, optionally create the tag.
# Usage:
#   ./compute-version.sh              # print current version
#   ./compute-version.sh --bump patch # compute next patch version
#   ./compute-version.sh --bump minor # compute next minor version
#   ./compute-version.sh --bump major # compute next major version
#   ./compute-version.sh --update     # same as --bump but also creates git tag

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Check dependencies
for cmd in python3 git; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "Error: $cmd is required but not found" >&2
        exit 1
    fi
done

exec python3 "$SCRIPT_DIR/compute_version.py" "$@"
