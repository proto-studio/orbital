#!/usr/bin/env bash
# Drive the real TypeScript compiler (the LKG build shipped in the repo at
# lib/tsc.js) on Orbital: CLI version, emit + run, diagnostics, and the
# programmatic API. Run from the checked-out TypeScript repo (cwd == checkout).
set -euo pipefail

: "${ORBITAL:?ORBITAL must be set (path to the Orbital binary)}"
: "${CORE_PKG_DIR:?CORE_PKG_DIR must be set (tests/core-packages dir)}"

TSC="$PWD/lib/tsc.js"
FIX="$CORE_PKG_DIR/support/tsc"
OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

echo "== tsc --version =="
ver="$("$ORBITAL" "$TSC" --version)"
echo "$ver"
echo "$ver" | grep -Eq '^Version 5\.' || { echo "FAIL: unexpected tsc version"; exit 1; }

echo "== compile good.ts -> JS and run it on Orbital =="
"$ORBITAL" "$TSC" --strict --target ES2020 --module commonjs --outDir "$OUT" "$FIX/good.ts"
test -f "$OUT/good.js" || { echo "FAIL: tsc did not emit good.js"; exit 1; }
run_out="$("$ORBITAL" "$OUT/good.js")"
echo "$run_out"
echo "$run_out" | grep -q 'TSC_OK 5 2 12' || { echo "FAIL: emitted program produced wrong output"; exit 1; }

echo "== type errors are reported (bad.ts) =="
set +e
err_out="$("$ORBITAL" "$TSC" --noEmit --strict "$FIX/bad.ts" 2>&1)"
rc=$?
set -e
echo "$err_out"
[ "$rc" -ne 0 ] || { echo "FAIL: tsc should exit non-zero on type errors"; exit 1; }
echo "$err_out" | grep -q 'TS2322' || { echo "FAIL: expected a TS2322 diagnostic"; exit 1; }

echo "== programmatic API (createProgram + checker + emitter) =="
"$ORBITAL" "$CORE_PKG_DIR/support/tsc-api.js"

echo "PASS: tsc"
