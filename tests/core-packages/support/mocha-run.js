'use strict';
// Real-mocha runner for the core-package suites.
//
// This runs the given spec files through the *actual* `mocha` package (the one
// installed in the target project's node_modules) using Mocha's public
// programmatic API — the same API `mocha`'s own bin uses to execute a run. It is
// not a shim: describe/it/hooks, reporters, timeouts, async handling, etc. are
// all provided by real Mocha. It exits non-zero if any test fails.
//
// Usage:  orbital support/mocha-run.js [--require <file>] [--invert-grep <re>] <spec-file> ...
//   --require/-r <file>   module to load before the specs (repeatable), mirroring
//                         `mocha --require`; used e.g. for a project's test setup.
//   --invert-grep <re>    run every test EXCEPT those whose full title matches the
//                         regexp (i.e. `mocha --grep <re> --invert`). Used to skip
//                         a small, documented set of specs a package's suite runs
//                         that depend on capabilities Orbital does not emulate
//                         (host OS locale detection, Electron bundling, etc.).
// Env:    MOCHA_REPORTER  (default "spec")
//         MOCHA_UI        (default "bdd")
const path = require('path');
const { createRequire } = require('module');

function main() {
  const argv = process.argv.slice(2);
  const specs = [];
  const requires = [];
  let invertGrep = null;
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--require' || argv[i] === '-r') {
      requires.push(argv[++i]);
    } else if (argv[i] === '--invert-grep') {
      invertGrep = argv[++i];
    } else {
      specs.push(argv[i]);
    }
  }
  if (specs.length === 0) {
    console.error('usage: mocha-run.js [--require <file>] [--invert-grep <re>] <spec> ...');
    process.exitCode = 2;
    return;
  }

  // Resolve Mocha (and any --require modules) from the project under test (cwd),
  // not from this support dir, so each package exercises its own installed Mocha.
  const requireFromCwd = createRequire(path.join(process.cwd(), '__mocha_runner__.js'));
  const Mocha = requireFromCwd('mocha');

  // Load setup modules (`--require`) before the specs, like Mocha's own bin.
  for (const r of requires) {
    requireFromCwd(path.resolve(process.cwd(), r));
  }

  const mocha = new Mocha({
    reporter: process.env.MOCHA_REPORTER || 'spec',
    ui: process.env.MOCHA_UI || 'bdd'
  });

  if (invertGrep) {
    mocha.grep(new RegExp(invertGrep));
    mocha.invert();
  }

  for (const spec of specs) {
    mocha.addFile(path.resolve(process.cwd(), spec));
  }

  mocha.run(function (failures) {
    // Force exit after the run completes, like `mocha --exit`. Suites that boot
    // real servers (e.g. Express + supertest) can leave listening sockets / accept
    // loops open, which keep the event loop alive and would otherwise hang the
    // process after all tests finish. A short delay lets buffered reporter output
    // flush before we exit.
    const code = failures ? 1 : 0;
    setTimeout(function () { process.exit(code); }, 50);
  });
}

main();
