#!/usr/bin/env bash
# Print the next semantic module version tag based on the latest v* git tag.
# Usage: next-module-version.sh [patch|minor|major]   (default: patch)
# Requires tags to be fetched (e.g. `git fetch --tags`).
set -euo pipefail

bump="${1:-patch}"

latest="$(git tag --list 'v*' --sort=-v:refname | head -n1)"
if [ -z "$latest" ]; then
  latest="v0.0.0"
fi

ver="${latest#v}"
IFS='.' read -r major minor patch <<< "$ver"
major="${major:-0}"; minor="${minor:-0}"; patch="${patch:-0}"

case "$bump" in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
  *) echo "unknown bump level: $bump (want patch|minor|major)" >&2; exit 1 ;;
esac

echo "v${major}.${minor}.${patch}"
