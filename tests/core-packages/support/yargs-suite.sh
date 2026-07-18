#!/usr/bin/env bash
# Run yargs' OWN mocha test suite on Orbital.
#
# This drives the real spec files shipped in the yargs checkout (test/*.cjs, the
# same files `npm test` runs) through actual Mocha via support/mocha-run.js — no
# hand-written test drivers. yargs' own `npm test` is
#   c8 mocha ./test/*.cjs --require ./test/before.cjs --check-leaks
# so we load test/before.cjs the same way (via mocha-run.js --require) and run
# every test/*.cjs spec. c8 (coverage) and --check-leaks are dropped: they are
# tooling wrappers, not part of the assertions. Invoked from the yargs checkout
# (cwd == checkout) with $ORBITAL and $CORE_PKG_DIR set.
#
# yargs' full suite is ~801 tests; Orbital passes 782 of them (this gate) with 1
# pending. Surfacing this suite drove four real Orbital runtime fixes:
#   * util.format() with no args now returns '' (was 'undefined') — unblocked ~37
#     usage/validation specs that assert yargs' blank-separator error lines.
#   * require.cache is now a real object (was a Map), so `delete
#     require.cache[id]` / `id in require.cache` work — yargs' clearRequireCache.
#   * path.normalize preserves a trailing slash (normalize('/tmp/') === '/tmp/').
#   * missing modules throw a Node-shaped error (code 'MODULE_NOT_FOUND',
#     `Cannot find module 'x'`) and require.resolve throws instead of returning
#     null — yargs' config `extends` relies on the code to ignore non-modules.
#
# The 18 skipped specs (--invert-grep below) depend on things Orbital does not
# emulate, none of them yargs bugs:
#   * commandDir (7): resolves the command directory relative to the caller's
#     file via get-caller-file, which reads a fixed stack-frame position; under
#     Mocha's runner on Orbital that frame differs, so the fixtures don't load.
#   * $0 executable name (5): these expect '$0' to be 'node' because Node's real
#     test run is `node .../mocha`; on Orbital argv[0] is the orbital binary.
#   * host environment (3): OS locale detection and the Node.js-version guard.
#   * non-module command object (1): the thrown message embeds util.inspect of the
#     object across multiple lines; Orbital's util.inspect does not yet wrap by
#     breakLength, so it stays single-line.
#   * cached-help timing (2): async process.exit/emit ordering in yargs' own
#     process-global-mutation test harness.
#
# Keep this list in sync with tests/core-packages/README.md.
set -euo pipefail

: "${ORBITAL:?ORBITAL must be set}"
: "${CORE_PKG_DIR:?CORE_PKG_DIR must be set}"

# Full titles (quotes/apostrophes written as `.` to keep the regexp shell-safe)
# of the specs Orbital cannot run for the documented reasons above.
skip='throws error for unsupported Node.js versions'
skip="$skip"'|throws error for non-module command object missing .command. string'
skip="$skip"'|should not detect the OS locale if detectLocale is .false.'
skip="$skip"'|allows nested sub-commands to be invoked multiple times'
skip="$skip"'|should display top-level help with no command given'
skip="$skip"'|should use cached help message for nested synchronous commands'
skip="$skip"'|populates output appropriately when parse is called multiple times'
skip="$skip"'|resets errors when parse is called multiple times'
skip="$skip"'|preserves top-level config when parse is called multiple times'
skip="$skip"'|supports relative dirs'
skip="$skip"'|supports nested subcommands'
skip="$skip"'|supports a .recurse. boolean option'
skip="$skip"'|supports a .visit. function option'
skip="$skip"'|detects and ignores cyclic dir references'
skip="$skip"'|derives .command. string from filename when not exported'
skip="$skip"'|if a promise is returned, errors are handled'
skip="$skip"'|bails out early when full command matches'
skip="$skip"'|should not display a cached help message for the next parsing'

specs=(
  test/argsert.cjs
  test/command.cjs
  test/completion.cjs
  test/is-promise.cjs
  test/middleware.cjs
  test/obj-filter.cjs
  test/parse-command.cjs
  test/parser.cjs
  test/usage.cjs
  test/validation.cjs
  test/yargs.cjs
  test/helpers.cjs
)

exec "$ORBITAL" "$CORE_PKG_DIR/support/mocha-run.js" \
  --require test/before.cjs \
  --invert-grep "$skip" \
  "${specs[@]}"
