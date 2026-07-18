'use strict';
// Exercise the TypeScript *programmatic* compiler API on Orbital (the surface
// build tools like ts-node, ts-loader, esbuild plugins, etc. rely on): a
// transpile-only path plus a full createProgram type-check with the checker and
// default lib resolution. Loaded from the checkout's own build (lib/typescript.js).
const path = require('path');
const assert = require('assert');

const ts = require(path.join(process.cwd(), 'lib', 'typescript.js'));

// 1. transpileModule: syntactic downlevel emit, no type-checking / no host.
const transpiled = ts.transpileModule(
  'export const sum = (a: number, b: number): number => a + b;',
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }
);
assert.ok(
  /exports\.sum/.test(transpiled.outputText),
  'transpileModule should emit `exports.sum`, got:\n' + transpiled.outputText
);

// 2. createProgram: real parser + binder + checker + default lib resolution.
const fixture = path.join(process.env.CORE_PKG_DIR, 'support', 'tsc', 'good.ts');
const program = ts.createProgram([fixture], {
  strict: true,
  target: ts.ScriptTarget.ES2020,
  module: ts.ModuleKind.CommonJS,
  noEmit: true,
});
const diagnostics = ts.getPreEmitDiagnostics(program);
assert.strictEqual(
  diagnostics.length,
  0,
  'good.ts should type-check clean, got: ' +
    diagnostics
      .map((d) => ts.flattenDiagnosticMessageText(d.messageText, '\n'))
      .join('; ')
);

// 3. The checker must actually reject invalid code.
const badProgram = ts.createProgram(
  [path.join(process.env.CORE_PKG_DIR, 'support', 'tsc', 'bad.ts')],
  { strict: true, target: ts.ScriptTarget.ES2020, noEmit: true }
);
const badDiagnostics = ts.getPreEmitDiagnostics(badProgram);
assert.ok(
  badDiagnostics.some((d) => d.code === 2322),
  'bad.ts should produce a TS2322 diagnostic, got codes: ' +
    badDiagnostics.map((d) => d.code).join(',')
);

console.log('tsc programmatic API OK (transpile + createProgram + checker)');
