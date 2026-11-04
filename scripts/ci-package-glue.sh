#!/usr/bin/env bash
# Package a freshly built libv8go_glue.a with the rest of a target's libraries,
# writing dist/v8-<platform>.tar.gz (+ .sha256). Only the glue changes: the V8
# libraries come from an existing tarball.
#
# Usage: ci-package-glue.sh <platform> [base-run-id]
#
# With a run id, the V8 libraries come from that run's base-v8-<platform>
# artifact (a V8 update that has just built them). Without one, they come from
# the tarball the manifest pins, which is checked against its sha256.
set -euo pipefail

platform="$1"
base_run_id="${2:-}"
asset="v8-$platform.tar.gz"
manifest=internal/v8dist/manifest.json
base="$(mktemp -d)"

if [ -n "$base_run_id" ]; then
  gh run download "$base_run_id" -n "base-v8-$platform" -D "$base"
else
  goos="${platform%-*}"
  goarch="${platform##*-}"
  want="$(jq -r --arg goos "$goos" --arg goarch "$goarch" \
    '.targets[] | select(.goos == $goos and .goarch == $goarch) | .sha256' "$manifest")"
  run_id="$(jq -r .source_run_id "$manifest")"
  tag="$(jq -r .module_version "$manifest")"
  # Artifacts expire, so fall back to the Release, which holds the same tarball
  # if this pin has been published.
  gh run download "$run_id" -n "v8-$platform" -D "$base" \
    || gh release download "$tag" -p "$asset" -D "$base"
  echo "$want  $base/$asset" | shasum -a 256 -c -
fi

work="$(mktemp -d)"
tar -C "$work" -xzf "$base/$asset"
cp "deps/v8/$platform/lib/libv8go_glue.a" "$work/lib/libv8go_glue.a"

mkdir -p dist
tar -C "$work" -czf "dist/$asset" lib
( cd dist && shasum -a 256 "$asset" > "$asset.sha256" )
cat "dist/$asset.sha256"
