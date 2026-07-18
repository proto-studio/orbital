'use strict';
// Self-test runner for the *mocha* core-package suite.
//
// Mocha's own unit specs (test/unit/*.spec.js) are run through Mocha itself,
// but a package cannot resolve itself by name (`require('mocha')` fails inside
// the mocha repo in Node too), and the specs rely on Mocha's test bootstrap
// (global `expect`, provided by test/setup.js). This runner therefore:
//
//   1. Loads Mocha from the repo's own build:  <cwd>/lib/mocha.js
//   2. Applies a setup file first (default test/setup.js) so the assertion
//      globals the specs expect exist before any spec is evaluated.
//   3. Runs the given spec files and exits non-zero if any test fails.
//
// This is not a shim: describe/it/hooks/reporters/timeouts/async handling are
// all real Mocha. It only fixes up self-resolution + bootstrap.
//
// Usage:  orbital support/mocha-self-run.js <spec-file> [<spec-file> ...]
// Env:    MOCHA_SETUP     setup file to require first (default "test/setup.js")
//         MOCHA_REPORTER  reporter (default "spec")
//         MOCHA_UI        interface (default "bdd")
//         MOCHA_TIMEOUT   per-test timeout in ms (default 2000)
const path = require('path');

function main() {
  const specs = process.argv.slice(2);
  if (specs.length === 0) {
    console.error('usage: mocha-self-run.js <spec> [<spec> ...]');
    process.exitCode = 2;
    return;
  }

  const cwd = process.cwd();
  const Mocha = require(path.join(cwd, 'lib', 'mocha.js'));

  const mocha = new Mocha({
    reporter: process.env.MOCHA_REPORTER || 'spec',
    ui: process.env.MOCHA_UI || 'bdd',
    timeout: parseInt(process.env.MOCHA_TIMEOUT || '2000', 10)
  });

  const setup = process.env.MOCHA_SETUP || 'test/setup.js';
  if (setup) mocha.addFile(path.resolve(cwd, setup));

  for (const spec of specs) {
    mocha.addFile(path.resolve(cwd, spec));
  }

  mocha.run(function (failures) {
    process.exitCode = failures ? 1 : 0;
  });
}

main();
