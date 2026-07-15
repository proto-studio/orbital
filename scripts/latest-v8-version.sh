#!/bin/bash
set -euo pipefail

# Resolve the V8 version that ships with the current stable Chrome release.
#
# Prints the version string (e.g. "15.0.245.19") to stdout and nothing else,
# so it can be captured by the Makefile:
#
#   V8_VERSION=$(scripts/latest-v8-version.sh)
#
# All diagnostics go to stderr.

DASH="https://chromiumdash.appspot.com"

log() { echo "$@" >&2; }

# 1) Latest stable Chrome version. Mac/Linux/Windows all share the same V8, so
#    the platform here is arbitrary; Mac tends to be published promptly.
chrome_version=$(
    curl -fsSL "${DASH}/fetch_releases?channel=Stable&platform=Mac&num=1" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["version"])'
)

if [ -z "${chrome_version:-}" ]; then
    log "Error: could not determine latest stable Chrome version."
    exit 1
fi
log ">>> Latest stable Chrome: ${chrome_version}"

# 2) Map the Chrome version to its bundled V8 version.
v8_version=$(
    curl -fsSL "${DASH}/fetch_version?version=${chrome_version}" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin)["v8_version"])'
)

if [ -z "${v8_version:-}" ]; then
    log "Error: could not determine V8 version for Chrome ${chrome_version}."
    exit 1
fi
log ">>> Latest stable V8:     ${v8_version}"

echo "$v8_version"
