#!/usr/bin/env bash
# Package what compiling the glue needs from a V8 checkout, writing
# dist/v8-glue-sdk-<platform>.tar.gz (+ .sha256). Run after gn gen.
#
# That's V8's compile command, the libc++ headers and the scripts that download V8's
# clang and sysroot at the revisions this V8 pins. Paths are the same as in the
# checkout, so the compile command works unchanged. scripts/install-glue-sdk.sh is
# the other half.
#
# Usage: package-glue-sdk.sh <platform>
set -euo pipefail

platform="$1"
goos="${platform%-*}"
goarch="${platform##*-}"
v8_arch="$goarch"
[ "$goarch" = amd64 ] && v8_arch=x64

src=v8-build/v8
out="out.gn/$goos-$v8_arch"
asset="v8-glue-sdk-$platform.tar.gz"
work="$(mktemp -d)"
mkdir -p "$work/$out"

# V8 updates compile through sccache, but the command has to start at the compiler.
python3 - "$src/$out/compile_commands.json" "$work/$out/compile_commands.json" <<'EOF'
import importlib.util
import json
import shlex
import sys

spec = importlib.util.spec_from_file_location("build_glue", "scripts/build-glue.py")
build_glue = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build_glue)

with open(sys.argv[1]) as f:
    picked = build_glue.pick_entry(json.load(f))
if not picked:
    sys.exit("ERROR: no suitable V8 C++ compile command found")
entry, tokens = picked
while not tokens[0].endswith("clang++"):
    tokens.pop(0)
with open(sys.argv[2], "w") as f:
    json.dump([{"directory": entry.get("directory", ""), "file": entry["file"], "command": shlex.join(tokens)}], f)
EOF

files=(
  third_party/libc++/src/include
  third_party/libc++abi/src/include
  buildtools/third_party/libc++
  tools/clang/scripts/update.py
)
if [ "$goos" = linux ]; then
  files+=(build/linux/sysroot_scripts/install-sysroot.py build/linux/sysroot_scripts/sysroots.json)
fi
tar -C "$src" -cf - "${files[@]}" | tar -C "$work" -xf -

mkdir -p dist
python3 scripts/reproducible-tar.py "dist/$asset" "$work" .
( cd dist && shasum -a 256 "$asset" > "$asset.sha256" )
ls -lh "dist/$asset"
cat "dist/$asset.sha256"
