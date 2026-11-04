#!/usr/bin/env bash
# Print the v8setup environment for installing the libraries this checkout pins.
# The manifest can move ahead of the latest Release, because glue and V8 updates
# are pinned when they merge and only published when someone runs the Release
# workflow. Until then the libraries exist only as the pinning run's artifacts.
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
