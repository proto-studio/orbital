#!/bin/bash
set -euo pipefail

# Resolve the V8 version that ships with the newest stable Chrome release.
#
# Prints the version string (e.g. "15.0.245.19") to stdout and nothing else,
# so it can be captured by the Makefile:
#
#   V8_VERSION=$(scripts/latest-v8-version.sh)
#
# All diagnostics go to stderr.
#
# Chromium Dash orders Stable by publish time, not version. A security patch
# on an older milestone can appear first even when a newer milestone is also
# Stable. We take the highest Chrome version among recent Mac stables
# (Mac/Linux/Windows share V8).
#
# The Stable channel also carries a milestone's early stable release, which
# goes to a small share of users about two weeks before its stable date. Only
# milestones whose stable date has passed count, or a single early stable
# release would outrank every patch to the milestone most users are on.

DASH="https://chromiumdash.appspot.com"

log() { echo "$@" >&2; }

releases="$(curl -fsSL "${DASH}/fetch_releases?channel=Stable&platform=Mac&num=20")"

stable_milestones=""
for mstone in $(python3 -c '
import json, sys
print(" ".join(sorted({str(r["milestone"]) for r in json.loads(sys.argv[1])})))
' "$releases"); do
    stable_date="$(
        curl -fsSL "${DASH}/fetch_milestone_schedule?mstone=${mstone}" \
            | python3 -c 'import json,sys; print(json.load(sys.stdin)["mstones"][0]["stable_date"])'
    )"
    if [[ "$stable_date" < "$(date -u +%Y-%m-%dT%H:%M:%S)" ]]; then
        stable_milestones+=" ${mstone}"
    else
        log ">>> Skipping Chrome ${mstone}: early stable until ${stable_date}"
    fi
done

chrome_version=$(
    python3 -c '
import json, sys
stable = set(sys.argv[2].split())
releases = [r for r in json.loads(sys.argv[1]) if str(r["milestone"]) in stable]
if not releases:
    raise SystemExit("no stable Chrome releases")
def key(v):
    return [int(p) for p in v.split(".")]
print(max(releases, key=lambda r: key(r["version"]))["version"])
' "$releases" "$stable_milestones"
)

if [ -z "${chrome_version:-}" ]; then
    log "Error: could not determine latest stable Chrome version."
    exit 1
fi
log ">>> Latest stable Chrome: ${chrome_version}"

# Map the Chrome version to its bundled V8 version.
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
