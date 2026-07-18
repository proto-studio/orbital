#!/usr/bin/env bash
# Build yargs' CommonJS entry from source WITHOUT the repo's rollup-based
# `build:cjs` step.
#
# yargs is authored in TypeScript (lib/) and its published build/index.cjs is
# produced by rollup + @rollup/plugin-typescript. That toolchain is broken on
# modern hosts (rollup hands a non-string to a newer TypeScript and it dies with
# `TypeError: path.charCodeAt is not a function`) — a dev-tooling version drift
# that has nothing to do with Orbital. Plain `tsc` compiles the exact same
# sources to CommonJS just fine, so we drive tsc directly and then reconstruct
# the tiny CJS bootstrap the committed top-level entries load.
#
# Run from the checked-out yargs repo (cwd == checkout).
set -euo pipefail

# A tsconfig that mirrors the repo's but (a) emits CommonJS instead of ESM and
# (b) does NOT exclude lib/cjs.ts / lib/platform-shims/cjs.ts (the repo excludes
# them because rollup owns the CJS bundle). The Deno-only shim is still excluded
# because it references the `Deno` global and won't type-check under Node.
cat > tsconfig.orbital-cjs.json <<'JSON'
{
  "extends": "./tsconfig.json",
  "compilerOptions": { "module": "commonjs", "declaration": false },
  "exclude": ["lib/platform-shims/deno.ts"]
}
JSON

rm -rf build
./node_modules/.bin/tsc -p tsconfig.orbital-cjs.json

# rollup normally emits a single flattened build/index.cjs at the build/ root
# whose module.exports IS the object literal from lib/cjs.ts. tsc instead keeps
# the tree (build/lib/cjs.js) and turns `export default {…}` into
# `exports.default = {…}`. The committed top-level index.cjs and yargs both do
# `require('./build/index.cjs')` and destructure { Yargs, processArgv, … } off
# it, so recreate that shim by unwrapping the default export.
printf "module.exports = require('./lib/cjs.js').default;\n" > build/index.cjs

# yargs' i18n layer resolves locale JSON relative to the compiled platform shim
# (build/lib/platform-shims/../locales == build/lib/locales). rollup inlines the
# shim at build/ so ../locales lands on the committed repo-root locales/; with
# tsc's nested output that path moves, so mirror the JSON where the shim looks.
cp -R locales build/lib/locales

echo "yargs CJS build ready:"
ls -1 build/index.cjs build/lib/cjs.js build/lib/locales/en.json
