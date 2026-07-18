#!/usr/bin/env bash
# Set up an ISOLATED install of the published react/react-dom packages for the
# Orbital SSR smoke test, run from the facebook/react checkout (cwd == checkout).
#
# Why not build React from source? facebook/react is a yarn monorepo whose build
# (flow + rollup + a bespoke bundler) can't run on Orbital, and its test suite is
# Jest-based — neither runs on the Orbital runtime. What we actually want to
# validate is that Orbital can execute the exact prebuilt CommonJS bundles npm
# users ship to production, so we install the published packages (pinned to the
# same version as the checkout's tag) into a nested ".ssr" directory.
#
# The nested dir gets its own package.json so `npm install` resolves react/
# react-dom cleanly instead of trying to resolve the monorepo's huge dev tree
# (which fails with ERESOLVE). ".ssr" is not matched by the monorepo's npm
# workspace globs, so npm treats it as a standalone project.
set -euo pipefail

REACT_VERSION="${REACT_VERSION:-19.2.7}"
SSR_DIR=".ssr"

rm -rf "$SSR_DIR"
mkdir -p "$SSR_DIR"
cat > "$SSR_DIR/package.json" <<EOF
{
  "name": "orbital-react-ssr-smoke",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "react": "$REACT_VERSION",
    "react-dom": "$REACT_VERSION"
  }
}
EOF

cd "$SSR_DIR"
npm install --no-audit --no-fund
