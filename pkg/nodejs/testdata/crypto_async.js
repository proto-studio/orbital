// Runs unchanged in Node and in Orbital. crypto_async.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoAsync_MatchesNode'
const crypto = require('crypto');
const util = require('util');
const results = {};

function outcome(f) {
  try {
    const value = f();
    return { value: Buffer.isBuffer(value) ? 'Buffer ' + value.length : String(value) };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

// randomBytes arguments Node rejects, and the sizes it accepts.
const sizes = {
  string: ['5'], negative: [-1], nan: [NaN], tooLarge: [2 ** 31], fractionTooLarge: [2 ** 31 - 0.5],
  undefinedSize: [undefined], badCallback: [4, 'x'], badBoth: ['x', 'y'], zero: [0], fraction: [1.5],
};
for (const [name, args] of Object.entries(sizes)) results['randomBytes_' + name] = outcome(() => crypto.randomBytes(...args));

async function main() {
  // The callback form returns undefined and calls back later with a Buffer.
  const order = [];
  results.randomBytesReturn = outcome(() => crypto.randomBytes(8, () => {}));
  const viaCallback = await new Promise((resolve) => {
    crypto.randomBytes(32, (err, buf) => resolve([String(err), Buffer.isBuffer(buf), buf.length]));
    order.push('sync');
  });
  order.push('callback');
  results.randomBytesCallback = viaCallback;
  results.randomBytesZero = await new Promise((resolve) => {
    crypto.randomBytes(0, (err, buf) => resolve([String(err), buf.length]));
  });
  results.randomBytesOrder = order;

  // Timers keep firing while a key is generated.
  let ticks = 0;
  const interval = setInterval(() => { ticks++; }, 1);
  const ticksDuring = await new Promise((resolve, reject) => {
    crypto.generateKeyPair('rsa', { modulusLength: 3072 }, (err, pub, priv) => {
      if (err) return reject(err);
      resolve([ticks > 0, pub.type, priv.type, pub.asymmetricKeyType]);
    });
  });
  clearInterval(interval);
  results.keygenWhileTimersRun = ticksDuring;

  results.keygenCallbackArgs = await new Promise((resolve) => {
    crypto.generateKeyPair('ec', { namedCurve: 'P-256' }, function (err, pub, priv) {
      resolve([String(err), arguments.length, pub.type, priv.type]);
    });
  });
  const pair = await util.promisify(crypto.generateKeyPair)('ed25519');
  results.keygenPromisify = [Object.keys(pair).sort().join(','), pair.publicKey.type, pair.privateKey.type];
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
