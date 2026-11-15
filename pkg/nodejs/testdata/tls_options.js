// Runs unchanged in Node and in Orbital. tls_options.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestTLSOptions_MatchesNode'
//
// The fixtures in tls_options/ are one P-256 key and its self-signed certificate for
// localhost, in the forms Node accepts: plain and encrypted PEM keys, and PKCS#12 files.
const tls = require('tls');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const dir = globalThis.FIXTURES || path.join(__dirname, 'tls_options');
const f = (name) => fs.readFileSync(path.join(dir, name));
const cert = f('cert.pem');
const key = f('key.pem');
const results = {};

function describe(e) {
  return { code: e.code === undefined ? null : e.code, message: e.message, library: e.library || null, reason: e.reason || null };
}

function contexts() {
  const cases = {
    pfx: { pfx: f('server.pfx'), passphrase: 'sekrit' },
    pfxObject: { pfx: [{ buf: f('server.pfx'), passphrase: 'sekrit' }] },
    pfxObjectOwnPassphrase: { pfx: [{ buf: f('server.pfx'), passphrase: 'sekrit' }], passphrase: 'wrong' },
    pfxWrongPassphrase: { pfx: f('server.pfx'), passphrase: 'wrong' },
    pfxNoPassphrase: { pfx: f('server.pfx') },
    pfxEmptyPassword: { pfx: f('server-nopass.pfx') },
    pfxSHA1MAC: { pfx: f('server-sha1mac.pfx'), passphrase: 'sekrit' },
    pfxLegacy: { pfx: f('server-legacy.pfx'), passphrase: 'sekrit' },
    pfxGarbage: { pfx: Buffer.from('nope') },
    pkcs8: { cert, key: f('key-pkcs8-encrypted.pem'), passphrase: 'sekrit' },
    pkcs8Wrong: { cert, key: f('key-pkcs8-encrypted.pem'), passphrase: 'wrong' },
    pkcs8Missing: { cert, key: f('key-pkcs8-encrypted.pem') },
    legacyPem: { cert, key: f('key-legacy-encrypted.pem'), passphrase: 'sekrit' },
    legacyPemWrong: { cert, key: f('key-legacy-encrypted.pem'), passphrase: 'wrong' },
    keyObject: { cert, key: [{ pem: f('key-pkcs8-encrypted.pem'), passphrase: 'sekrit' }] },
    ecdhCurveUnknown: { ecdhCurve: 'nope' },
    ecdhCurveRepeated: { ecdhCurve: 'P-256:prime256v1' },
    ecdhCurveList: { ecdhCurve: 'X25519:P-256:secp384r1' },
    ecdhCurveX448: { ecdhCurve: 'X448' },
    ciphersUnknown: { ciphers: 'nope' },
    ciphersTLS13Only: { ciphers: 'TLS_AES_128_GCM_SHA256' },
    ciphersSomeKnown: { ciphers: 'nope:ECDHE-RSA-AES128-GCM-SHA256' },
    ciphersKeywords: { ciphers: 'HIGH:!aNULL:!MD5' },
    ciphersExcludeGroup: { ciphers: 'HIGH:!AESGCM' },
    ciphersKeywordAndName: { ciphers: 'DEFAULT:ECDHE-RSA-AES128-GCM-SHA256' },
    ciphersJoined: { ciphers: 'ECDHE+AESGCM' },
    ciphersMedium: { ciphers: 'MEDIUM' },
    ciphersNothingLeft: { ciphers: '!ALL' },
    ciphersAllExcluded: { ciphers: 'AESGCM:!ECDHE:!kRSA' },
  };
  const out = {};
  for (const [name, options] of Object.entries(cases)) {
    try {
      tls.createSecureContext(options);
      out[name] = 'ok';
    } catch (e) {
      out[name] = describe(e);
    }
  }
  try {
    tls.connect({ port: 1, pfx: f('server.pfx'), passphrase: 'wrong' }).on('error', () => {});
    out.connectThrows = false;
  } catch (e) {
    out.connectThrows = describe(e);
  }
  return out;
}

function listen(options) {
  return new Promise((resolve) => {
    const server = tls.createServer(options, (s) => s.end('hi'));
    server.on('tlsClientError', () => {});
    server.listen(0, '127.0.0.1', () => resolve(server));
  });
}

function connect(port, options, familyOnly) {
  return new Promise((resolve) => {
    const c = tls.connect({ port, host: '127.0.0.1', servername: 'localhost', ca: cert, ...options }, () => {
      const protocol = c.getProtocol();
      // crypto/tls picks its own TLS 1.3 suite, so only TLS 1.2 ciphers are compared. Where
      // the list leaves a choice, which of them is picked is up to the TLS library too.
      let cipher = protocol === 'TLSv1.2' ? c.getCipher().name : null;
      if (cipher && familyOnly) cipher = /GCM/.test(cipher) ? 'some GCM cipher' : 'some non-GCM cipher';
      resolve({ protocol, cipher, authorized: c.authorized });
      c.destroy();
    });
    c.on('error', () => resolve({ error: true }));
  });
}

async function connections() {
  const cases = {
    pfx: [{ pfx: f('server.pfx'), passphrase: 'sekrit' }, {}],
    pkcs8: [{ cert, key: f('key-pkcs8-encrypted.pem'), passphrase: 'sekrit' }, {}],
    legacyPem: [{ cert, key: [{ pem: f('key-legacy-encrypted.pem'), passphrase: 'sekrit' }] }, {}],
    clientPfx: [{ cert, key, requestCert: true, ca: cert }, { pfx: f('server.pfx'), passphrase: 'sekrit' }],
    serverCipherTLS12: [{ cert, key, ciphers: 'ECDHE-ECDSA-AES256-GCM-SHA384' }, { maxVersion: 'TLSv1.2' }],
    clientCipherTLS12: [{ cert, key }, { ciphers: 'ECDHE-ECDSA-CHACHA20-POLY1305', maxVersion: 'TLSv1.2' }],
    tls13Only: [{ cert, key }, { ciphers: 'TLS_AES_128_GCM_SHA256' }],
    tls13OnlyAgainstTLS12: [{ cert, key, maxVersion: 'TLSv1.2' }, { ciphers: 'TLS_AES_128_GCM_SHA256' }],
    noCommonTLS12Cipher: [{ cert, key, ciphers: 'ECDHE-ECDSA-AES256-GCM-SHA384', maxVersion: 'TLSv1.2' },
      { ciphers: 'ECDHE-ECDSA-AES128-GCM-SHA256' }],
    curve: [{ cert, key, ecdhCurve: 'P-384' }, { ecdhCurve: 'P-384:P-256' }],
    noCommonCurve: [{ cert, key, ecdhCurve: 'P-384', maxVersion: 'TLSv1.2' }, { ecdhCurve: 'P-256' }],
    excludeGroup: [{ cert, key, ciphers: 'HIGH:!AESGCM', maxVersion: 'TLSv1.2' }, {}, true],
    keywordAndName: [{ cert, key, ciphers: 'DEFAULT:ECDHE-RSA-AES128-GCM-SHA256', maxVersion: 'TLSv1.2' }, {}, true],
    tls13OnlyAtMostTLS12: [{ cert, key }, { ciphers: 'TLS_AES_128_GCM_SHA256', maxVersion: 'TLSv1.2' }],
  };
  const out = {};
  for (const [name, [serverOptions, clientOptions, familyOnly]] of Object.entries(cases)) {
    const server = await listen(serverOptions);
    out[name] = await connect(server.address().port, clientOptions, familyOnly);
    server.close();
  }
  return out;
}

async function sessions(maxVersion) {
  const server = await listen({ cert, key, maxVersion });
  const port = server.address().port;
  const once = (session) => new Promise((resolve) => {
    const out = { events: [] };
    const c = tls.connect({ port, host: '127.0.0.1', servername: 'localhost', ca: cert, session }, () => {
      out.events.push('secureConnect');
      out.reused = c.isSessionReused();
    });
    // Whether a resumed connection gets a new ticket is up to the server's TLS library.
    if (!session) c.once('session', (s) => {
      out.events.push('session');
      out.session = s;
      out.isBuffer = Buffer.isBuffer(s);
      out.ticketIsBuffer = Buffer.isBuffer(c.getTLSTicket());
      out.getSessionMatches = Buffer.isBuffer(c.getSession());
    });
    c.resume();
    c.on('close', () => resolve(out));
  });
  const first = await once();
  const second = await once(first.session);
  server.close();
  delete first.session;
  delete second.session;
  // OpenSSL sends two tickets and crypto/tls one, so only the first is compared.
  first.events = first.events.slice(0, 2);
  return { first, second, keys: server.getTicketKeys().length };
}

function ticketKeys() {
  const server = tls.createServer({ cert, key });
  const out = { length: server.getTicketKeys().length, random: !server.getTicketKeys().equals(Buffer.alloc(48)) };
  try {
    server.setTicketKeys(Buffer.alloc(3));
  } catch (e) {
    out.short = e.code;
  }
  const keys = crypto.randomBytes(48);
  server.setTicketKeys(keys);
  out.set = server.getTicketKeys().equals(keys);
  out.option = tls.createServer({ cert, key, ticketKeys: keys }).getTicketKeys().equals(keys);
  const view = new DataView(keys.buffer, keys.byteOffset, keys.byteLength);
  out.dataView = tls.createServer({ cert, key, ticketKeys: view }).getTicketKeys().equals(keys);
  const wide = new Float64Array(6);
  new Uint8Array(wide.buffer).set(keys);
  server.setTicketKeys(wide);
  out.wideArray = server.getTicketKeys().equals(keys);
  return out;
}

function rootCertificates() {
  const roots = tls.rootCertificates;
  return {
    length: roots.length,
    frozen: Object.isFrozen(roots),
    same: roots === tls.rootCertificates,
    sha256: crypto.createHash('sha256').update(roots.join('\n')).digest('hex'),
  };
}

async function main() {
  results.contexts = contexts();
  results.connections = await connections();
  results.sessionsTLS13 = await sessions('TLSv1.3');
  results.sessionsTLS12 = await sessions('TLSv1.2');
  results.ticketKeys = ticketKeys();
  results.rootCertificates = rootCertificates();
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
