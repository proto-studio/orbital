#!/usr/bin/env bash
# Set up an ISOLATED install of the published `jose` package for the Orbital
# encryption smoke test, run from the panva/jose checkout (cwd == checkout).
#
# Why not run jose's own tests / build from source? jose v6 is authored in
# TypeScript and its test suite is a bespoke multi-runtime harness (Node/Deno/
# Bun/browsers/workerd, driven by esbuild + a custom runner) — none of which runs
# on the Orbital runtime. What we actually want to validate is that Orbital's
# native Web Crypto backend can execute the exact ESM artifacts npm ships to
# production, so we install the published package (pinned to the checkout's tag)
# into a nested ".smoke" directory.
#
# The smoke test is ESM (jose is ESM-only) and Orbital resolves a bare
# `import 'jose'` relative to the importing file's location, so the smoke script
# is copied INTO the install dir and run from there. The nested dir gets its own
# package.json ("type":"module") so `npm install` resolves jose cleanly.
set -euo pipefail

JOSE_VERSION="${JOSE_VERSION:-6.2.3}"
SMOKE_DIR=".smoke"

rm -rf "$SMOKE_DIR"
mkdir -p "$SMOKE_DIR"
cat > "$SMOKE_DIR/package.json" <<EOF
{
  "name": "orbital-jose-smoke",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "dependencies": {
    "jose": "$JOSE_VERSION"
  }
}
EOF

cp "$CORE_PKG_DIR/support/jose-smoke.mjs" "$SMOKE_DIR/"

cd "$SMOKE_DIR"
npm install --no-audit --no-fund
