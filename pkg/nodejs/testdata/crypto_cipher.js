// Runs unchanged in Node and in Orbital. crypto_cipher.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoCipher_MatchesNode'
const crypto = require('crypto');
const results = {};

const bytes = (n, seed) => Buffer.from(Array.from({ length: n }, (_, i) => (i * 31 + seed) & 0xff));
function outcome(f) {
  try {
    const v = f();
    return { value: Buffer.isBuffer(v) ? v.toString('hex') : v };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

// Every cipher Orbital has, under the names Node takes for it.
const ciphers = {
  'aes-128-cbc': [16, 16], 'aes-192-cbc': [24, 16], 'aes-256-cbc': [32, 16], aes128: [16, 16], aes256: [32, 16],
  'AES-128-CBC': [16, 16], 'aes-128-ecb': [16, 0], 'aes-256-ecb': [32, 0], 'aes-128-ctr': [16, 16], 'aes-256-ctr': [32, 16],
  'aes-128-cfb': [16, 16], 'aes-256-cfb': [32, 16], 'aes-128-cfb8': [16, 16], 'aes-192-cfb8': [24, 16],
  'aes-128-ofb': [16, 16], 'aes-256-ofb': [32, 16], 'aes-128-gcm': [16, 12], 'aes-192-gcm': [24, 12],
  'aes-256-gcm': [32, 12], 'id-aes128-GCM': [16, 12], 'des-ede3-cbc': [24, 8], des3: [24, 8], 'des-ede3': [24, 0],
  'des-ede-cbc': [16, 8], 'des-ede': [16, 0],
};
for (const [name, [keyLen, ivLen]] of Object.entries(ciphers)) {
  const key = bytes(keyLen, 1);
  const iv = ivLen ? bytes(ivLen, 2) : null;
  const r = {};
  for (const n of [0, 1, 15, 16, 17, 33]) {
    r['len' + n] = outcome(() => {
      const plain = bytes(n, 3);
      const c = crypto.createCipheriv(name, key, iv);
      const ct = Buffer.concat([c.update(plain), c.final()]);
      const tag = name.toLowerCase().includes('gcm') ? c.getAuthTag() : null;
      // Decrypt in uneven pieces.
      const d = crypto.createDecipheriv(name, key, iv);
      if (tag) d.setAuthTag(tag);
      const parts = [];
      for (let i = 0; i < ct.length; i += 7) parts.push(d.update(ct.subarray(i, i + 7)));
      parts.push(d.final());
      const back = Buffer.concat(parts);
      return [ct.toString('hex'), tag ? tag.toString('hex') : null, back.equals(plain)];
    });
  }
  results['cipher_' + name] = r;
}

// Chunked updates give the same output as one.
results.chunked = outcome(() => {
  const key = bytes(16, 5), iv = bytes(16, 6), plain = bytes(50, 7);
  const c = crypto.createCipheriv('aes-128-cbc', key, iv);
  const out = [];
  for (const n of [1, 3, 16, 0, 20, 10]) out.push(c.update(plain.subarray(0, n)));
  out.push(c.final());
  return Buffer.concat(out).toString('hex') + ' ' + out.map((b) => b.length).join(',');
});

// GCM: AAD, tag lengths, other IV sizes, and authentication.
const gcm = {};
const gk = bytes(32, 8);
for (const [label, ivLen, tagLen] of [['iv12tag16', 12, undefined], ['iv12tag4', 12, 4], ['iv12tag8', 12, 8], ['iv12tag13', 12, 13],
  ['iv1', 1, undefined], ['iv16', 16, undefined], ['iv60', 60, undefined]]) {
  gcm[label] = outcome(() => {
    const iv = bytes(ivLen, 9);
    const opts = tagLen === undefined ? undefined : { authTagLength: tagLen };
    const c = crypto.createCipheriv('aes-256-gcm', gk, iv, opts);
    c.setAAD(Buffer.from('header'));
    const ct = Buffer.concat([c.update('hello gcm, a little longer than a block'), c.final()]);
    const tag = c.getAuthTag();
    const d = crypto.createDecipheriv('aes-256-gcm', gk, iv, opts);
    d.setAAD(Buffer.from('header'));
    d.setAuthTag(tag);
    const back = Buffer.concat([d.update(ct), d.final()]).toString();
    return [ct.toString('hex'), tag.toString('hex'), back];
  });
}
gcm.badTag = outcome(() => {
  const iv = bytes(12, 9);
  const c = crypto.createCipheriv('aes-256-gcm', gk, iv);
  const ct = Buffer.concat([c.update('secret'), c.final()]);
  const tag = c.getAuthTag();
  tag[0] ^= 1;
  const d = crypto.createDecipheriv('aes-256-gcm', gk, iv);
  d.setAuthTag(tag);
  const partial = d.update(ct).toString();
  return [partial, outcome(() => d.final())];
});
gcm.noTag = outcome(() => {
  const d = crypto.createDecipheriv('aes-256-gcm', gk, bytes(12, 9));
  return [d.update('00', 'hex').toString('hex'), outcome(() => d.final())];
});
gcm.wrongAAD = outcome(() => {
  const iv = bytes(12, 9);
  const c = crypto.createCipheriv('aes-256-gcm', gk, iv);
  c.setAAD(Buffer.from('a'));
  const ct = Buffer.concat([c.update('x'), c.final()]);
  const d = crypto.createDecipheriv('aes-256-gcm', gk, iv);
  d.setAAD(Buffer.from('b'));
  d.setAuthTag(c.getAuthTag());
  d.update(ct);
  return outcome(() => d.final());
});
gcm.stringAAD = outcome(() => {
  const c = crypto.createCipheriv('aes-128-gcm', bytes(16, 1), bytes(12, 2));
  c.setAAD('text aad');
  c.update('x');
  c.final();
  return c.getAuthTag();
});
results.gcm = gcm;

// State errors.
results.states = (() => {
  const key = bytes(16, 1), iv = bytes(16, 2);
  const e = crypto.createCipheriv('aes-128-cbc', key, iv);
  const r = [outcome(() => e.update('a'.repeat(20))), outcome(() => e.final()), outcome(() => e.final()),
    outcome(() => e.update('x')), outcome(() => e.setAutoPadding(false))];
  const g = crypto.createCipheriv('aes-128-gcm', key, bytes(12, 2));
  r.push(outcome(() => g.getAuthTag()));
  g.update('x');
  r.push(outcome(() => g.setAAD(Buffer.from('late'))));
  g.final();
  r.push(outcome(() => g.getAuthTag().length));
  const gd = crypto.createDecipheriv('aes-128-gcm', key, bytes(12, 2), { authTagLength: 8 });
  r.push(outcome(() => gd.setAuthTag(Buffer.alloc(16))), outcome(() => gd.setAuthTag(Buffer.alloc(8)) === gd),
    outcome(() => gd.setAuthTag(Buffer.alloc(8))));
  const gd2 = crypto.createDecipheriv('aes-128-gcm', key, bytes(12, 2));
  r.push(outcome(() => gd2.setAuthTag(Buffer.alloc(3))), outcome(() => gd2.setAuthTag(Buffer.alloc(4)) === gd2));
  r.push(typeof crypto.createCipheriv('aes-128-gcm', key, bytes(12, 2)).setAuthTag,
    typeof crypto.createDecipheriv('aes-128-gcm', key, bytes(12, 2)).getAuthTag);
  return r;
})();

// Padding.
results.padding = (() => {
  const key = bytes(16, 1), iv = bytes(16, 2);
  const r = [];
  const ns = crypto.createCipheriv('aes-128-cbc', key, iv);
  r.push(outcome(() => ns.setAutoPadding(false) === ns), outcome(() => ns.update('abc')), outcome(() => ns.final()));
  const ns2 = crypto.createCipheriv('aes-128-cbc', key, iv);
  ns2.setAutoPadding(false);
  r.push(outcome(() => Buffer.concat([ns2.update(bytes(32, 4)), ns2.final()])));
  const d = crypto.createDecipheriv('aes-128-cbc', key, iv);
  r.push(outcome(() => d.update(Buffer.alloc(16))), outcome(() => d.final()));
  const d2 = crypto.createDecipheriv('aes-128-cbc', key, iv);
  r.push(outcome(() => d2.update(Buffer.alloc(15))), outcome(() => d2.final()));
  const d3 = crypto.createDecipheriv('aes-128-cbc', key, iv);
  r.push(outcome(() => d3.final()));
  const d4 = crypto.createDecipheriv('aes-128-cbc', key, iv);
  d4.setAutoPadding(false);
  r.push(outcome(() => Buffer.concat([d4.update(Buffer.alloc(32)), d4.final()])));
  return r;
})();

// Encodings.
results.encodings = (() => {
  const key = bytes(16, 1), iv = bytes(16, 2);
  const c = crypto.createCipheriv('aes-128-cbc', key, iv);
  const r = [c.update('abc', 'utf8', 'base64'), c.final('base64')];
  const c2 = crypto.createCipheriv('aes-128-cbc', key, iv);
  r.push(c2.update('616263', 'hex', 'hex') + c2.final('hex'));
  // A UTF-8 character split across updates comes out whole.
  const ctr = (dec) => (dec ? crypto.createDecipheriv : crypto.createCipheriv)('aes-128-ctr', key, iv);
  const ct = ctr(false).update('héllo wörld');
  const d = ctr(true);
  r.push([d.update(ct.subarray(0, 2), undefined, 'utf8'), d.update(ct.subarray(2), undefined, 'utf8'), d.final('utf8')]);
  return r;
})();

// Arguments, errors and getCipherInfo. These run last: Node leaves OpenSSL's error from a
// bad key length queued, and reports it for a later failure.
function argumentErrors() {
  // getCipherInfo
  results.cipherInfo = {
    gcm: crypto.getCipherInfo('aes-128-gcm'),
    cbc: crypto.getCipherInfo('aes-256-cbc'),
    ecb: crypto.getCipherInfo('aes-128-ecb'),
    upper: crypto.getCipherInfo('AES-256-CBC'),
    des3: crypto.getCipherInfo('des3'),
    byNid: crypto.getCipherInfo(427),
    keyLengthMatch: crypto.getCipherInfo('aes-128-cbc', { keyLength: 16 }),
    keyLengthMismatch: String(crypto.getCipherInfo('aes-128-cbc', { keyLength: 32 })),
    gcmIvLength: crypto.getCipherInfo('aes-128-gcm', { ivLength: 16 }),
    unknown: String(crypto.getCipherInfo('nope')),
    badName: outcome(() => crypto.getCipherInfo(true)),
  };
const k16 = bytes(16, 1), iv16 = bytes(16, 2), iv12 = bytes(12, 2);
  results.args = {
  unknown: outcome(() => crypto.createCipheriv('nope', k16, iv16)),
  badKeyLen: outcome(() => crypto.createCipheriv('aes-128-cbc', bytes(32, 1), iv16)),
  badIvLen: outcome(() => crypto.createCipheriv('aes-128-cbc', k16, iv12)),
  bothBad: outcome(() => crypto.createCipheriv('aes-128-cbc', bytes(3, 1), bytes(3, 1))),
  nullIv: outcome(() => crypto.createCipheriv('aes-128-cbc', k16, null)),
  ecbNullIv: outcome(() => crypto.createCipheriv('aes-128-ecb', k16, null).constructor.name),
  ecbIv: outcome(() => crypto.createCipheriv('aes-128-ecb', k16, iv16)),
  ecbEmptyIv: outcome(() => crypto.createCipheriv('aes-128-ecb', k16, Buffer.alloc(0)).constructor.name),
  gcmEmptyIv: outcome(() => crypto.createCipheriv('aes-128-gcm', k16, Buffer.alloc(0))),
  gcmTag3: outcome(() => crypto.createCipheriv('aes-128-gcm', k16, iv12, { authTagLength: 3 })),
  gcmTag5: outcome(() => crypto.createCipheriv('aes-128-gcm', k16, iv12, { authTagLength: 5 })),
  gcmTagString: outcome(() => crypto.createCipheriv('aes-128-gcm', k16, iv12, { authTagLength: 'x' })),
  gcmTagNegative: outcome(() => crypto.createCipheriv('aes-128-gcm', k16, iv12, { authTagLength: -1 })),
  cbcTagOption: outcome(() => crypto.createCipheriv('aes-128-cbc', k16, iv16, { authTagLength: 8 }).constructor.name),
  stringKeyIv: outcome(() => crypto.createCipheriv('aes-128-cbc', 'k'.repeat(16), 'i'.repeat(16)).final()),
  arrayBufferKey: outcome(() => crypto.createCipheriv('aes-128-cbc', new Uint8Array(k16).buffer, iv16).final()),
  secretKeyObject: outcome(() => crypto.createCipheriv('aes-128-cbc', crypto.createSecretKey(k16), iv16).final()),
  publicKeyObject: outcome(() => crypto.createCipheriv('aes-128-cbc', crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' }).publicKey, iv16)),
  numberKey: outcome(() => crypto.createCipheriv('aes-128-cbc', 5, iv16)),
  numberIv: outcome(() => crypto.createCipheriv('aes-128-cbc', k16, 5)),
  numberCipher: outcome(() => crypto.createCipheriv(5, k16, iv16)),
  numberData: outcome(() => crypto.createCipheriv('aes-128-cbc', k16, iv16).update(5)),
  withoutNew: outcome(() => crypto.Cipheriv('aes-128-cbc', k16, iv16) instanceof crypto.Cipheriv),
  isTransform: crypto.createCipheriv('aes-128-cbc', k16, iv16) instanceof require('stream').Transform,
};
}

// As a stream.
function streamed() {
  return new Promise((resolve) => {
    const s = crypto.createCipheriv('aes-128-ctr', bytes(16, 1), bytes(16, 2));
    const out = [];
    s.on('data', (d) => out.push(d));
    s.on('end', () => resolve(Buffer.concat(out).toString('hex')));
    s.write('hello');
    s.end(' world');
  });
}

function streamError() {
  return new Promise((resolve) => {
    const d = crypto.createDecipheriv('aes-128-cbc', bytes(16, 1), bytes(16, 2));
    d.on('error', (e) => resolve([e.code, e.message]));
    d.on('data', () => {});
    d.end(Buffer.alloc(16));
  });
}

async function main() {
  results.stream = await streamed();
  results.streamError = await streamError();
  argumentErrors();
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
