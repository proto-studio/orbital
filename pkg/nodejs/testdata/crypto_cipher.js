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
  'des-ede-cbc': [16, 8], 'des-ede': [16, 0], chacha20: [32, 16], 'CHACHA20': [32, 16], 'chacha20-poly1305': [32, 12],
  'ChaCha20-Poly1305': [32, 12],
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
      const tag = /gcm|poly1305/i.test(name) ? c.getAuthTag() : null;
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
gcm.encodedTagAndAAD = outcome(() => {
  const iv = bytes(12, 9);
  const c = crypto.createCipheriv('aes-256-gcm', gk, iv);
  c.update('');
  c.update(Buffer.alloc(0));
  c.setAAD('ff00', { encoding: 'hex' });
  const ct = Buffer.concat([c.update('payload'), c.final()]);
  const tag = c.getAuthTag();
  const d = crypto.createDecipheriv('aes-256-gcm', gk, iv);
  d.update(Buffer.alloc(0));
  d.setAAD(Buffer.from([0xff, 0]));
  d.setAuthTag(tag.toString('hex'), 'hex');
  return [tag.toString('hex'), Buffer.concat([d.update(ct), d.final()]).toString()];
});
results.gcm = gcm;

// ChaCha20 and ChaCha20-Poly1305.
const ck = bytes(32, 4);
const civ = bytes(12, 5);
const chacha = {};
// The 32-bit block counter wraps into the nonce's words, in one update or across several.
for (const [label, ivHex] of [['wrap', 'ffffffff' + '00'.repeat(12)], ['carry', 'ffffffff' + 'ff'.repeat(8) + '01000000'],
  ['carryAll', 'ff'.repeat(16)], ['middle', '05000000' + '03'.repeat(12)]]) {
  chacha[label] = outcome(() => {
    const iv = Buffer.from(ivHex, 'hex');
    const whole = crypto.createCipheriv('chacha20', ck, iv).update(bytes(150, 6));
    const c = crypto.createCipheriv('chacha20', ck, iv);
    const parts = [];
    for (let i = 0; i < 150; i += 37) parts.push(c.update(bytes(150, 6).subarray(i, i + 37)));
    parts.push(c.final());
    return [whole.toString('hex'), Buffer.concat(parts).equals(whole)];
  });
}
const seal = (f, opts) => {
  const c = crypto.createCipheriv('chacha20-poly1305', ck, civ, opts);
  const out = Buffer.concat([...f(c), c.final()]);
  return [out.toString('hex'), c.getAuthTag().toString('hex')];
};
const msg = bytes(40, 7);
chacha.aadBefore = outcome(() => seal((c) => { c.setAAD(bytes(5, 1)); return [c.update(msg)]; }));
chacha.aadTwice = outcome(() => seal((c) => { c.setAAD(bytes(3, 1)); c.setAAD(bytes(20, 2)); return [c.update(msg)]; }));
chacha.aadAfterData = outcome(() => seal((c) => { const a = c.update(msg.subarray(0, 7)); c.setAAD(bytes(5, 1)); return [a, c.update(msg.subarray(7))]; }));
chacha.aadAtEnd = outcome(() => seal((c) => { const a = c.update(msg); c.setAAD(bytes(17, 1)); return [a]; }));
chacha.aadAfterEmptyUpdate = outcome(() => seal((c) => { c.update(Buffer.alloc(0)); c.setAAD(bytes(5, 1)); return [c.update(msg)]; }));
chacha.noData = outcome(() => seal((c) => { c.setAAD(bytes(5, 1)); return []; }));
chacha.plaintextLength = outcome(() => seal((c) => { c.setAAD(bytes(5, 1), { plaintextLength: 40 }); return [c.update(msg)]; }));
for (const n of [1, 4, 12, 16]) chacha['tag' + n] = outcome(() => seal((c) => [c.update(msg)], { authTagLength: n }));
const open = (ct, tag, opts, aad) => {
  const d = crypto.createDecipheriv('chacha20-poly1305', ck, civ, opts);
  if (aad) d.setAAD(aad);
  if (tag) d.setAuthTag(tag);
  return Buffer.concat([d.update(ct), d.final()]);
};
const [sealedHex, tagHex] = seal((c) => { c.setAAD(bytes(5, 1)); return [c.update(msg)]; });
const sealed = Buffer.from(sealedHex, 'hex'), sealedTag = Buffer.from(tagHex, 'hex');
chacha.open = outcome(() => open(sealed, sealedTag, undefined, bytes(5, 1)).equals(msg));
chacha.openShortTag = outcome(() => open(sealed, sealedTag.subarray(0, 8), { authTagLength: 8 }, bytes(5, 1)).equals(msg));
chacha.openWrongAAD = outcome(() => open(sealed, sealedTag, undefined, bytes(5, 2)));
chacha.openBadTag = outcome(() => open(sealed, Buffer.alloc(16), undefined, bytes(5, 1)));
chacha.openNoTag = outcome(() => open(sealed, null, undefined, bytes(5, 1)));
chacha.openTagAfterData = outcome(() => {
  const d = crypto.createDecipheriv('chacha20-poly1305', ck, civ);
  d.setAAD(bytes(5, 1));
  const out = d.update(sealed);
  d.setAuthTag(sealedTag);
  return Buffer.concat([out, d.final()]).equals(msg);
});
chacha.setAuthTag8 = outcome(() => crypto.createDecipheriv('chacha20-poly1305', ck, civ).setAuthTag(Buffer.alloc(8)));
chacha.setAuthTag8With16 = outcome(() => crypto.createDecipheriv('chacha20-poly1305', ck, civ, { authTagLength: 16 }).setAuthTag(Buffer.alloc(8)));
chacha.setAuthTagTwice = outcome(() => { const d = crypto.createDecipheriv('chacha20-poly1305', ck, civ); d.setAuthTag(sealedTag); d.setAuthTag(sealedTag); });
chacha.tag0 = outcome(() => crypto.createCipheriv('chacha20-poly1305', ck, civ, { authTagLength: 0 }));
chacha.tag17 = outcome(() => crypto.createCipheriv('chacha20-poly1305', ck, civ, { authTagLength: 17 }));
chacha.badIvAndTag = outcome(() => crypto.createCipheriv('chacha20-poly1305', ck, bytes(5, 1), { authTagLength: 17 }));
chacha.badKeyAndTag = outcome(() => crypto.createCipheriv('chacha20-poly1305', bytes(5, 1), civ, { authTagLength: 17 }));
for (const n of [0, 8, 11, 13, 16]) chacha['polyIv' + n] = outcome(() => crypto.createCipheriv('chacha20-poly1305', ck, bytes(n, 1)));
for (const n of [0, 12, 17]) chacha['iv' + n] = outcome(() => crypto.createCipheriv('chacha20', ck, bytes(n, 1)));
chacha.nullIv = outcome(() => crypto.createCipheriv('chacha20', ck, null));
chacha.polyNullIv = outcome(() => crypto.createCipheriv('chacha20-poly1305', ck, null));
chacha.key16 = outcome(() => crypto.createCipheriv('chacha20-poly1305', bytes(16, 1), civ));
chacha.streamSetAAD = outcome(() => crypto.createCipheriv('chacha20', ck, bytes(16, 1)).setAAD(Buffer.alloc(1)));
chacha.streamGetAuthTag = outcome(() => { const c = crypto.createCipheriv('chacha20', ck, bytes(16, 1)); c.final(); return c.getAuthTag(); });
chacha.streamTagOption = outcome(() => crypto.createCipheriv('chacha20', ck, bytes(16, 1), { authTagLength: 5 }).final());
chacha.noPadding = outcome(() => { const c = crypto.createCipheriv('chacha20-poly1305', ck, civ); c.setAutoPadding(false); return Buffer.concat([c.update(msg), c.final()]); });
chacha.setAADAfterFinal = outcome(() => { const c = crypto.createCipheriv('chacha20-poly1305', ck, civ); c.final(); c.setAAD(Buffer.alloc(1)); });
results.chacha = chacha;

// Output encodings in any case, as Node takes them.
results.upperEncodings = ['BASE64', 'BASE64URL', 'Base64', 'UCS2', 'LATIN1', 'HEX'].map((e) => {
  const c = crypto.createCipheriv('aes-128-cbc', bytes(16, 1), bytes(16, 2));
  return c.update('hello world hello world!', 'utf8', e) + '|' + c.final(e);
});

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
  // Padding turned off after update() held back the last block.
  const ct = crypto.createCipheriv('aes-128-cbc', key, iv).setAutoPadding(false).update(bytes(32, 4));
  const late = crypto.createDecipheriv('aes-128-cbc', key, iv);
  r.push(outcome(() => late.update(ct)));
  late.setAutoPadding(false);
  r.push(outcome(() => late.final()));
  const late2 = crypto.createDecipheriv('aes-128-ecb', key, null);
  r.push(outcome(() => late2.update(Buffer.alloc(16))));
  late2.setAutoPadding(false);
  r.push(outcome(() => late2.final()));
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
  // Base64 pieces join into one encoding of the ciphertext.
  for (const n of [1, 15, 16, 17, 40]) {
    const b = crypto.createCipheriv('aes-128-cbc', key, iv);
    const text = b.update(bytes(n, 9), undefined, 'base64') + b.final('base64');
    const u = crypto.createCipheriv('aes-128-ctr', key, iv);
    r.push([text, u.update(bytes(n, 9), undefined, 'base64url') + u.final('base64url')]);
  }
  const c2 = crypto.createCipheriv('aes-128-cbc', key, iv);
  r.push(c2.update('616263', 'hex', 'hex') + c2.final('hex'));
  // A UTF-8 character split across updates comes out whole.
  const ctr = (dec) => (dec ? crypto.createDecipheriv : crypto.createCipheriv)('aes-128-ctr', key, iv);
  const ct = ctr(false).update('héllo wörld');
  const d = ctr(true);
  r.push([d.update(ct.subarray(0, 2), undefined, 'utf8'), d.update(ct.subarray(2), undefined, 'utf8'), d.final('utf8')]);
  const d3 = ctr(true);
  r.push([d3.update(ct.subarray(0, 2), undefined, 'utf8'), d3.update(ct.subarray(2), undefined, 'utf-8'), d3.final('UTF8')]);
  r.push(outcome(() => ctr(false).update('abc', 'hex')));
  const noArg = crypto.createCipheriv('aes-128-cbc', key, iv);
  noArg.setAutoPadding();
  r.push(Buffer.concat([noArg.update(bytes(16, 1)), noArg.final()]).length);
  const d2 = ctr(true);
  r.push([d2.update(ct.subarray(0, 2), undefined, 'utf-8'), d2.update(ct.subarray(2), undefined, 'utf-8'), d2.final('utf-8')]);
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
    chacha20: crypto.getCipherInfo('chacha20'),
    chachaPoly: crypto.getCipherInfo('ChaCha20-Poly1305'),
    chachaNids: [crypto.getCipherInfo(1018), crypto.getCipherInfo(1019)],
    chachaIvLengths: [12, 8, 16].map((n) => String(crypto.getCipherInfo('chacha20-poly1305', { ivLength: n }) && n)),
    ctr: crypto.getCipherInfo('aes-128-ctr'),
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
  gcmEmptyIvBadTag: outcome(() => crypto.createCipheriv('aes-128-gcm', k16, Buffer.alloc(0), { authTagLength: 5 })),
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
