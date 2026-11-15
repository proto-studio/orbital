// Runs unchanged in Node and in Orbital. crypto_random_int.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoRandomInt_MatchesNode'
const crypto = require('crypto');
const results = {};

function outcome(f) {
  try {
    return { value: f() };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}
const inRange = (n, lo, hi) => Number.isInteger(n) && n >= lo && n < hi;

// Arguments Node rejects, and the errors it throws.
const bad = {
  none: [], equal: [5, 5], reversed: [5, 4], equalNegative: [-1, -1], negativeZero: [-0, 0],
  fraction: [1.5], fractionMax: [0, 1.5], string: ['5'], stringMax: [0, '5'],
  maxTooLarge: [2 ** 48], rangeTooLarge: [0, 2 ** 48], rangeTooLargeNegative: [-1, 2 ** 48 - 1],
  unsafeMin: [-(2 ** 53), 0], unsafeMax: [2 ** 53], nan: [NaN], infinity: [Infinity],
  stringSecond: [10, 'x'], badCallback: [0, 10, 'x'], nullCallback: [0, 10, null],
  bigint: [10n], nullMax: [null], undefinedMin: [undefined, 5], object: [{}], boolean: [true],
};
for (const [name, args] of Object.entries(bad)) results[name] = outcome(() => crypto.randomInt(...args));

// Values in range, including the edges of what Node allows.
results.oneArg = outcome(() => inRange(crypto.randomInt(10), 0, 10));
results.negativeRange = outcome(() => inRange(crypto.randomInt(-10, -5), -10, -5));
results.onlyZero = outcome(() => crypto.randomInt(1));
results.onlySeven = outcome(() => crypto.randomInt(7, 8));
results.lowestSafe = outcome(() => crypto.randomInt(-(2 ** 53) + 1, -(2 ** 53) + 2));
results.largestRange = outcome(() => inRange(crypto.randomInt(0, 2 ** 48 - 1), 0, 2 ** 48 - 1));
results.nearMaxSafe = outcome(() => inRange(crypto.randomInt(2 ** 53 - 2 ** 48, 2 ** 53 - 1), 2 ** 53 - 2 ** 48, 2 ** 53 - 1));
results.everyValue = outcome(() => {
  const seen = new Set();
  for (let i = 0; i < 2000; i++) seen.add(crypto.randomInt(3, 9));
  return [...seen].sort().join(',');
});

// The callback form returns undefined and calls back later.
const order = [];
results.callbackReturn = outcome(() => String(crypto.randomInt(10, (err, n) => order.push(['max', err, inRange(n, 0, 10)]))));
results.callbackMinMaxReturn = outcome(() => String(crypto.randomInt(5, 6, (err, n) => order.push(['minMax', err, n]))));
order.push('sync');

setTimeout(() => {
  results.callbackOrder = order.map((x) => (Array.isArray(x) ? x.map(String).join(' ') : x));
  if (globalThis.done) done(results);
  else process.stdout.write(JSON.stringify(results));
}, 10);
