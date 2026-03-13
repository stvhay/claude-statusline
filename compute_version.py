#!/usr/bin/env python3
"""Compute next semver version from git tags for Go projects.

This project uses GoReleaser with git tags as the version source.
Version is injected at build time via -X main.version={{.Version}}.
"""

import argparse
import re
import subprocess
import sys


def get_latest_tag() -> str:
    """Get the latest semver tag from git."""
    try:
        result = subprocess.run(
            ["git", "describe", "--tags", "--abbrev=0", "--match", "v*"],
            capture_output=True, text=True, check=True,
        )
        return result.stdout.strip()
    except subprocess.CalledProcessError:
        return "v0.0.0"


def parse_semver(tag: str) -> tuple[int, int, int]:
    """Parse a semver tag like v1.2.3 into (major, minor, patch)."""
    m = re.match(r"v?(\d+)\.(\d+)\.(\d+)", tag)
    if not m:
        print(f"Error: cannot parse version tag: {tag}", file=sys.stderr)
        sys.exit(1)
    return int(m.group(1)), int(m.group(2)), int(m.group(3))


def bump_version(major: int, minor: int, patch: int, bump_type: str) -> str:
    """Compute the next version."""
    if bump_type == "major":
        return f"v{major + 1}.0.0"
    elif bump_type == "minor":
        return f"v{major}.{minor + 1}.0"
    elif bump_type == "patch":
        return f"v{major}.{minor}.{patch + 1}"
    else:
        print(f"Error: unknown bump type: {bump_type}", file=sys.stderr)
        sys.exit(1)


def create_tag(version: str) -> None:
    """Create and optionally push a git tag."""
    subprocess.run(["git", "tag", version], check=True)
    print(f"Created tag: {version}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Compute semver version from git tags")
    parser.add_argument("--bump", choices=["major", "minor", "patch"],
                        help="Bump type to compute next version")
    parser.add_argument("--update", action="store_true",
                        help="Bump and create git tag")
    args = parser.parse_args()

    current_tag = get_latest_tag()
    major, minor, patch = parse_semver(current_tag)

    if args.update:
        bump_type = args.bump or "patch"
        next_version = bump_version(major, minor, patch, bump_type)
        create_tag(next_version)
        print(next_version)
    elif args.bump:
        next_version = bump_version(major, minor, patch, args.bump)
        print(next_version)
    else:
        print(current_tag)


if __name__ == "__main__":
    main()
