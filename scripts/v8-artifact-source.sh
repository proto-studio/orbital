#!/usr/bin/env bash
# Print the v8setup environment for installing the libraries this checkout pins.
# The manifest can move ahead of the latest Release, on a pull request's branch or
# while the Release workflow runs after a merge. Until it finishes, the libraries
# exist only as the pinning run's artifacts.
set -euo pipefail

manifest="$(dirname "$0")/../internal/v8dist/manifest.json"

field() { sed -nE "s/.*\"$1\": \"([^\"]*)\".*/\1/p" "$manifest"; }

url="https://github.com/$(field owner)/$(field repo)/releases/download/$(field module_version)/manifest.json"
published="$(curl -fsSL "$url" 2>/dev/null | grep '"sha256"' || true)"

if [ "$published" = "$(grep '"sha256"' "$manifest")" ]; then
  echo "V8_ARTIFACT_SOURCE=release"
else
  echo "V8_ARTIFACT_SOURCE=actions"
  echo "V8_ACTIONS_RUN_ID=$(field source_run_id)"
  # The run belongs to the manifest's repo, which may not be this clone's remote.
  echo "GH_REPO=$(field owner)/$(field repo)"
fi
