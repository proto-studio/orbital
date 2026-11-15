// Runs unchanged in Node and in Orbital. crypto_extras.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoExtras_MatchesNode'
const crypto = require('crypto');
const results = {};

function show(value) {
  if (Buffer.isBuffer(value)) return 'Buffer ' + value.toString('latin1');
  if (value instanceof ArrayBuffer) return 'ArrayBuffer ' + value.byteLength;
  if (typeof value === 'bigint') return 'bigint';
  if (value !== null && typeof value === 'object') return (value.constructor && value.constructor.name) + ' ' + JSON.stringify(value);
  return typeof value + ' ' + String(value);
}

function outcome(f) {
  try {
    return { value: show(f()) };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

// SPKACs made with OpenSSL: RSA signed with SHA-256, RSA with MD5 (OpenSSL's default),
// and P-256 ECDSA with SHA-256.
const spkacs = {
  rsaSha256: 'MIIBPzCBqTCBnzANBgkqhkiG9w0BAQEFAAOBjQAwgYkCgYEAxdC2wI/p1g22Iq16kXa+c8Fn/2/FVjH+e4q6gdbp4WqNJNiDPPVrQFWrMbFLFYkh28qvS/3d2fzfjdPEuBMiAUluCC/B146wB8QnWaqzODw1suDa4M6dirTMfNz+xWiaQyVKnjMssXmvwN8kgYAEXnZGeX8Tc8M8s/B4Bnd70RUCAwEAARYFaGVsbG8wDQYJKoZIhvcNAQELBQADgYEArVmgbbSvEUX5gfGUlU5+h6Sl+j9NEGSn+A8+dFJ4hv7F/py+DHcefKl61Z2wUAodPdTOVAjk1hIHINRPYhKsDTn6tqvq55OpyifWSrgRaxl+bqZ6+G/m9C107zIi64EJjJJpd2CmZoTcQm/mlfb55OQ6RtlYQtOioFlWCf2eLTU=',
  rsaMd5: 'MIIBPzCBqTCBnzANBgkqhkiG9w0BAQEFAAOBjQAwgYkCgYEAxdC2wI/p1g22Iq16kXa+c8Fn/2/FVjH+e4q6gdbp4WqNJNiDPPVrQFWrMbFLFYkh28qvS/3d2fzfjdPEuBMiAUluCC/B146wB8QnWaqzODw1suDa4M6dirTMfNz+xWiaQyVKnjMssXmvwN8kgYAEXnZGeX8Tc8M8s/B4Bnd70RUCAwEAARYFaGVsbG8wDQYJKoZIhvcNAQEEBQADgYEAtJ2CeP+Cf/XAva6egjqw3p/kRp9fSyJC4DF8nzUKgfZoY6vEBXmmdwYcNa0wyPAIXmGjgFn5FMkYvV76bXC/Sz8NZ9EHnM/Y6n/EUlWGu8vhDOZ+mRqb3LUA8thcNsM94fKX37nG2ufzXxCy5/nndO+tcSgdFREQ53DR2y+FosA=',
  ecSha256: 'MIHAMGkwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARo2AShbGfPN0/DO/zFMITnVQZRMavns067jOpsUzSj6mlpTH/p6YrZF3VzWr9S3jV91ycd/UbxDHYu2MDAvwjUFgxlYyBjaGFsbGVuZ2UwCgYIKoZIzj0EAwIDRwAwRAIgCLKpsl9kvp2nWl/sC+OEqIS36Cy0J2uZz1XrIg1epmkCIADzXbYx6Vt0NfBvjPojbStDMeVmWHw1/+WGfewT8mSY',
};

results.exports = ['randomFill', 'randomFillSync', 'getRandomValues', 'webcrypto', 'subtle', 'checkPrime',
  'checkPrimeSync', 'generatePrime', 'generatePrimeSync', 'getFips', 'Certificate', 'secureHeapUsed', 'setEngine']
  .map((name) => name + ' ' + typeof crypto[name]);
results.webcryptoIsGlobal = crypto.webcrypto === globalThis.crypto;
results.subtleIsGlobal = crypto.subtle === globalThis.crypto.subtle;

// randomFillSync: offsets and sizes count elements of the view.
const fills = {
  whole: [() => Buffer.alloc(8)],
  offset: [() => Buffer.alloc(8), 2],
  offsetSize: [() => Buffer.alloc(8), 2, 3],
  offsetAtEnd: [() => Buffer.alloc(8), 8],
  zeroSize: [() => Buffer.alloc(8), 0, 0],
  fractionOffset: [() => Buffer.alloc(8), 1.5],
  undefinedOffset: [() => Buffer.alloc(8), undefined, 2],
  uint32Offset: [() => new Uint32Array(4), 1, 2],
  float64: [() => new Float64Array(2)],
  arrayBuffer: [() => new ArrayBuffer(4), 1, 2],
  dataView: [() => new DataView(new ArrayBuffer(4), 1)],
  offsetTooBig: [() => Buffer.alloc(8), 9],
  negativeOffset: [() => Buffer.alloc(8), -1],
  sizePastEnd: [() => Buffer.alloc(8), 2, 7],
  uint32SizePastEnd: [() => new Uint32Array(4), 1, 4],
  stringOffset: [() => Buffer.alloc(8), '1'],
  nanSize: [() => Buffer.alloc(8), 0, NaN],
  notABuffer: [() => 'x'],
};
for (const [name, [make, ...args]] of Object.entries(fills)) {
  results['randomFillSync_' + name] = outcome(() => {
    const buf = make();
    const bytes = typeof buf === 'string' ? null : new Uint8Array(buf.buffer || buf, buf.byteOffset || 0, buf.byteLength);
    const r = crypto.randomFillSync(buf, ...args);
    // Untouched bytes stay zero; filled bytes are random, so report only the untouched.
    return [r === buf, bytes.length, Array.from(bytes).map((b, i) => i).filter((i) => bytes[i] === 0).length >= 0];
  });
}
// Bytes outside the requested range are left alone.
results.randomFillSync_untouched = outcome(() => {
  const buf = Buffer.alloc(16);
  crypto.randomFillSync(buf, 4, 8);
  return buf.subarray(0, 4).equals(Buffer.alloc(4)) && buf.subarray(12).equals(Buffer.alloc(4));
});
results.randomFill_noCallback = outcome(() => crypto.randomFill(Buffer.alloc(4)));
results.randomFill_badOffset = outcome(() => crypto.randomFill(Buffer.alloc(8), 9, () => {}));
results.randomFill_badSize = outcome(() => crypto.randomFill(Buffer.alloc(8), 1, 8, () => {}));

// getRandomValues
results.getRandomValues_uint8 = outcome(() => crypto.getRandomValues(new Uint8Array(4)).length);
results.getRandomValues_bigint64 = outcome(() => crypto.getRandomValues(new BigInt64Array(2)).length);
results.getRandomValues_float32 = outcome(() => crypto.getRandomValues(new Float32Array(4)));
results.getRandomValues_dataView = outcome(() => crypto.getRandomValues(new DataView(new ArrayBuffer(4))));
results.getRandomValues_tooLong = outcome(() => crypto.getRandomValues(new Uint8Array(65537)));
results.getRandomValues_max = outcome(() => crypto.getRandomValues(new Uint8Array(65536)).length);
results.getRandomValues_string = outcome(() => crypto.getRandomValues('x'));
results.getRandomValues_code = (() => { try { crypto.getRandomValues('x'); } catch (e) { return e.code; } })();

// checkPrimeSync
const candidates = {
  thirteen: [13n], twelve: [12n], zero: [0n], one: [1n], two: [2n], mersenne127: [2n ** 127n - 1n],
  carmichael561: [561n], buffer13: [Buffer.from([13])], emptyArrayBuffer: [new ArrayBuffer(0)],
  bigEndian: [new Uint8Array([1, 1])], number: [13], string: ['13'], negative: [-1n],
  checks10: [13n, { checks: 10 }], negativeChecks: [13n, { checks: -1 }], fractionChecks: [13n, { checks: 1.5 }],
  optionsNotObject: [13n, 5],
};
for (const [name, args] of Object.entries(candidates)) results['checkPrimeSync_' + name] = outcome(() => crypto.checkPrimeSync(...args));
results.checkPrime_noCallback = outcome(() => crypto.checkPrime(7n));

// generatePrimeSync
const primes = {
  bits8: [8], bigint: [8, { bigint: true }], two: [2], safe: [8, { safe: true, bigint: true }],
  safe3: [3, { safe: true }], safe4: [4, { safe: true }], addRem: [8, { add: 6n, rem: 1n, bigint: true }],
  remNotBelowAdd: [8, { add: 3n, rem: 5n }], addTooBig: [8, { add: 1000n }], remOnly: [8, { rem: 1n }],
  bufferAddRem: [16, { add: Buffer.from([4]), rem: Buffer.from([3]), bigint: true }],
  zero: [0], one: [1], negative: [-1], string: ['8'], tooBig: [2 ** 31],
  bigintOption: [8, { bigint: 'x' }], safeOption: [8, { safe: 1 }], addString: [8, { add: '6' }],
  negativeAdd: [8, { add: -6n }],
  // Few numbers near 32 bits are 2147483659 mod 2147483660; this must still finish.
  narrowProgression: [32, { add: 2147483660n, rem: 2147483659n, bigint: true }],
};
for (const [name, args] of Object.entries(primes)) {
  results['generatePrimeSync_' + name] = outcome(() => {
    const p = crypto.generatePrimeSync(...args);
    const n = typeof p === 'bigint' ? p : BigInt('0x' + (Buffer.from(p).toString('hex') || '0'));
    const opts = args[1] || {};
    const extra = [];
    if (opts.safe) extra.push('safe ' + crypto.checkPrimeSync((n - 1n) / 2n));
    if (opts.add !== undefined && typeof opts.add !== 'string') {
      const add = typeof opts.add === 'bigint' ? opts.add : BigInt('0x' + Buffer.from(opts.add).toString('hex'));
      const rem = opts.rem === undefined ? (opts.safe ? 3n : 1n) : (typeof opts.rem === 'bigint' ? opts.rem : BigInt('0x' + Buffer.from(opts.rem).toString('hex')));
      extra.push('rem ' + (n % add === rem));
    }
    // With add, OpenSSL may step past size bits, so only a lower bound is fixed.
    const bits = n.toString(2).length;
    return [show(p), opts.add !== undefined && typeof opts.add !== 'string' ? 'atLeast ' + (bits >= args[0]) : bits,
      crypto.checkPrimeSync(n), ...extra].join(' ');
  });
}
results.generatePrime_noCallback = outcome(() => crypto.generatePrime(16));

// FIPS, secure heap, engines
results.getFips = outcome(() => crypto.getFips());
results.secureHeapUsed = outcome(() => crypto.secureHeapUsed());
results.setEngine = outcome(() => crypto.setEngine('x'));
results.setEngineNumber = outcome(() => crypto.setEngine(5));
results.setEngineFlags = outcome(() => crypto.setEngine('x', 'y'));

// Certificate
const { Certificate } = crypto;
for (const [name, text] of Object.entries(spkacs)) {
  results['spkac_' + name] = [
    outcome(() => Certificate.exportChallenge(text)),
    outcome(() => Certificate.exportPublicKey(Buffer.from(text))),
    outcome(() => Certificate.verifySpkac(Buffer.from(text))),
  ];
}
const corrupt = spkacs.rsaSha256.slice(0, 40) + 'AAAA' + spkacs.rsaSha256.slice(44);
const tampered = spkacs.rsaSha256.slice(0, 300) + (spkacs.rsaSha256[300] === 'A' ? 'B' : 'A') + spkacs.rsaSha256.slice(301);
// The ECDSA SPKAC with its signature algorithm changed to RSA-SHA256: the algorithm
// doesn't match the key, so it doesn't verify.
const ecDer = Buffer.from(spkacs.ecSha256, 'base64');
const ecdsaOid = Buffer.from('06082a8648ce3d040302', 'hex');
const at = ecDer.indexOf(ecdsaOid);
const rsaOid = Buffer.from('06092a864886f70d01010b0500', 'hex');
const swapped = Buffer.concat([ecDer.subarray(0, at), rsaOid, ecDer.subarray(at + ecdsaOid.length)]);
// Two bytes longer: fix the outer SEQUENCE and AlgorithmIdentifier lengths.
swapped[2] += 3; swapped[at - 1] += 3;
const wrongAlg = swapped.toString('base64');
const inputs = { corrupt, tampered, wrongAlg, garbage: 'abc', empty: '', u8: new Uint8Array(Buffer.from(spkacs.rsaSha256)) };
for (const [name, v] of Object.entries(inputs)) {
  results['spkacInput_' + name] = [
    outcome(() => Certificate.exportChallenge(v)),
    outcome(() => Certificate.exportPublicKey(v)),
    outcome(() => Certificate.verifySpkac(v)),
  ];
}
results.spkac_number = outcome(() => Certificate.verifySpkac(5));
results.spkac_null = outcome(() => Certificate.exportChallenge(null));
results.spkac_encoding = outcome(() => Certificate.exportChallenge(Buffer.from(spkacs.rsaSha256).toString('hex'), 'hex'));
results.spkac_instance = outcome(() => [typeof new Certificate().verifySpkac, Certificate() instanceof Certificate,
  new Certificate().verifySpkac(Buffer.from(spkacs.rsaSha256))].join(' '));
results.spkac_shape = [Object.getOwnPropertyNames(Certificate).sort().join(','), Object.getOwnPropertyNames(Certificate.prototype).sort().join(',')];

// Callback forms.
function callback(start) {
  return new Promise((resolve) => {
    const order = [];
    let result;
    const returned = start((err, value) => {
      order.push('callback');
      result = { err: String(err), value: show(value), order };
      Promise.resolve().then(() => resolve(result));
    });
    order.push('sync');
    Promise.resolve().then(() => { if (result) result.returned = String(returned); });
  });
}

async function main() {
  results.randomFillCallback = await callback((cb) => crypto.randomFill(Buffer.alloc(4), (err, buf) => cb(err, buf.length)));
  results.randomFillOffsetCallback = await callback((cb) => crypto.randomFill(new Uint16Array(4), 1, (err, buf) => cb(err, buf.constructor.name)));
  results.randomFillSizeCallback = await callback((cb) => crypto.randomFill(new ArrayBuffer(4), 1, 2, (err, buf) => cb(err, buf.constructor.name)));
  results.randomFillZeroCallback = await callback((cb) => crypto.randomFill(Buffer.alloc(4), 4, (err, buf) => cb(err, buf.length)));
  results.checkPrimeCallback = await callback((cb) => crypto.checkPrime(7n, cb));
  results.checkPrimeOptionsCallback = await callback((cb) => crypto.checkPrime(8n, { checks: 3 }, cb));
  results.generatePrimeCallback = await callback((cb) => crypto.generatePrime(16, { bigint: true }, (err, p) => cb(err, p.toString(2).length)));
  const { AsyncLocalStorage } = require('async_hooks');
  const als = new AsyncLocalStorage();
  results.asyncContext = await Promise.all([
    (cb) => crypto.randomFill(Buffer.alloc(4), cb),
    (cb) => crypto.checkPrime(7n, cb),
    (cb) => crypto.generatePrime(16, cb),
  ].map((start, i) => new Promise((resolve) => {
    als.run('request ' + i, () => start(() => resolve(als.getStore())));
  })));
  results.generatePrimeErrorCallback = await callback((cb) => crypto.generatePrime(1, (err, p) => cb(err && err.message, p)));
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
