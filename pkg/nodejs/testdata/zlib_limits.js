// Runs unchanged in Node and in Orbital. zlib_limits.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestZlib_OutputLimitMatchesNode'
const zlib = require('zlib');
const results = {};

function outcome(f) {
  try {
    const value = f();
    return { length: value.length };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

const zeros = Buffer.alloc(5000);
const inputs = {
  gunzipSync: zlib.gzipSync(zeros),
  inflateSync: zlib.deflateSync(zeros),
  inflateRawSync: zlib.deflateRawSync(zeros),
  unzipSync: zlib.gzipSync(zeros),
  gzipSync: Buffer.alloc(100000, 1),
  deflateSync: Buffer.alloc(100000, 1),
  deflateRawSync: Buffer.alloc(100000, 1),
};
for (const [name, input] of Object.entries(inputs)) {
  results[name + 'OverLimit'] = outcome(() => zlib[name](input, { maxOutputLength: 10 }));
}
results.unzipSyncDeflateOverLimit = outcome(() => zlib.unzipSync(zlib.deflateSync(zeros), { maxOutputLength: 10 }));

const hello = zlib.gzipSync('hello');
results.exactLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 5 }));
results.fractionalLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 1.5 }));
results.fractionalAboveOutput = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 5.5 }));
results.nanLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: NaN }));
results.noOptions = outcome(() => zlib.gunzipSync(hello));
results.stringLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 'x' }));
results.nullLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: null }));
results.infiniteLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: Infinity }));
results.negativeInfiniteLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: -Infinity }));
results.objectLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: {} }));
results.zeroLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 0 }));
results.maxLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 2 ** 32 }));
results.overMaxLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 2 ** 32 + 1 }));
results.hugeLimit = outcome(() => zlib.gunzipSync(hello, { maxOutputLength: 2 ** 53 }));

// The callback forms throw a bad option at once, report an output over the limit through
// the callback, and return undefined.
const order = [];
results.asyncBadOption = outcome(() => zlib.gunzip(hello, { maxOutputLength: 'x' }, () => order.push('called')));
results.asyncReturn = String(zlib.gunzip(inputs.gunzipSync, { maxOutputLength: 10 }, (err, res) => {
  order.push(['gunzip', err && err.name, err && err.code, err && err.message, res === undefined]);
}));
results.asyncOkReturn = String(zlib.inflate(inputs.inflateSync, { maxOutputLength: 5000 }, (err, res) => {
  order.push(['inflate', err, res.length]);
}));
order.push('sync');

setTimeout(() => {
  results.callbacks = order.map((x) => (Array.isArray(x) ? x.map(String).join(' ') : x));
  if (globalThis.done) done(results);
  else process.stdout.write(JSON.stringify(results));
}, 20);
