#!/usr/bin/env bash
# Set up everything `make v8-glue` needs, without a V8 checkout: the pinned glue SDK
# and V8 headers, plus V8's clang (and sysroot on Linux) from Chromium's buckets, at
# the revisions the SDK's own download scripts pin. Downloads are also copied to dist/,
# so each build's artifacts include them and they outlive the run that first built them.
#
# Usage: install-glue-sdk.sh <platform> [base-run-id]
#
# With a run id, the SDK and headers come from that run's base-* artifacts (a V8 update
# that has just built them). Without one, they're the ones the manifest pins.
set -euo pipefail

platform="$1"
base_run_id="${2:-}"
goos="${platform%-*}"
goarch="${platform##*-}"
v8_arch="$goarch"
[ "$goarch" = amd64 ] && v8_arch=x64

manifest=internal/v8dist/manifest.json
src=v8-build/v8
out="$src/out.gn/$goos-$v8_arch"
downloads="$(mktemp -d)"

# fetch <artifact name>: into $downloads, then dist/.
fetch() {
  local name="$1" asset="$1.tar.gz" want run_id tag
  if [ -n "$base_run_id" ]; then
    gh run download "$base_run_id" -n "base-$name" -D "$downloads"
    ( cd "$downloads" && shasum -a 256 -c "$asset.sha256" )
  else
    want="$(jq -r --arg asset "$asset" '.glue_build_files[$asset]' "$manifest")"
    run_id="$(jq -r .source_run_id "$manifest")"
    tag="$(jq -r .module_version "$manifest")"
    # Artifacts expire, so fall back to the Release, which holds the same file if
    # this pin has been published.
    gh run download "$run_id" -n "$name" -D "$downloads" \
      || gh release download "$tag" -p "$asset" -D "$downloads"
    echo "$want  $downloads/$asset" | shasum -a 256 -c -
  fi
  mkdir -p dist
  cp "$downloads/$asset" dist/
  ( cd dist && shasum -a 256 "$asset" > "$asset.sha256" )
}

fetch "v8-glue-sdk-$platform"
fetch v8-headers

mkdir -p "$src" deps/v8
tar -C "$src" -xzf "$downloads/v8-glue-sdk-$platform.tar.gz"
tar -C deps/v8 -xzf "$downloads/v8-headers.tar.gz"

python3 "$src/tools/clang/scripts/update.py"

if [ "$goos" = linux ]; then
  python3 "$src/build/linux/sysroot_scripts/install-sysroot.py" --arch="$goarch"
else
  # The compile command reaches the macOS SDK through a link that gn gen creates.
  sdk_link="$(python3 -c '
import json, shlex, sys
tokens = shlex.split(json.load(open(sys.argv[1]))[0]["command"])
print(tokens[tokens.index("-isysroot") + 1])
' "$out/compile_commands.json")"
  mkdir -p "$out/$(dirname "$sdk_link")"
  ln -sfn "$(xcrun --show-sdk-path)" "$out/$sdk_link"
fi
