// jose (panva) encryption/signing smoke test for the Orbital runtime.
//
// jose v6 is ESM-only and implemented entirely on top of the Web Crypto API
// (globalThis.crypto.subtle). This drives the REAL published jose package end to
// end to validate Orbital's native Go WebCrypto backend across the full JOSE
// surface: JWS signing (HMAC/RSA/RSA-PSS/ECDSA/EdDSA), JWE encryption
// (AES-GCM/AES-CBC content encryption; dir/AES-KW/AES-GCMKW/RSA-OAEP/ECDH-ES/
// PBES2 key management), JWT, JWK import/export + thumbprints, and tamper
// rejection.
//
// jose's own test suite is a bespoke multi-runtime harness (not Mocha/Jest) that
// does not run on Orbital, so — like axios/react — this exercises the exact CJS/
// ESM artifacts npm ships. It is run from an isolated install dir so the bare
// `import 'jose'` specifier resolves (see support/jose-setup.sh).
//
// NB: an unhandled rejection in a top-level await silently exits 0 on Orbital,
// so every check is wrapped and a non-zero exit code is forced on any failure.
import * as jose from 'jose';

const enc = new TextEncoder();
const dec = new TextDecoder();

let passed = 0;
let failed = 0;
const failures = [];

async function check(name, fn) {
  try {
    await fn();
    passed++;
    console.log('  ok   ' + name);
  } catch (e) {
    failed++;
    const msg = (e && e.message) || String(e);
    failures.push(name + ' :: ' + msg);
    console.log('  FAIL ' + name + ' :: ' + msg);
  }
}

function assert(cond, msg) {
  if (!cond) throw new Error(msg || 'assertion failed');
}

const PLAINTEXT = 'The quick brown fox jumps over the lazy dog. 🦊';

async function main() {
  // --- base64url ------------------------------------------------------------
  await check('base64url encode/decode round-trip', async () => {
    const b = enc.encode('héllo, wörld · 日本語');
    const s = jose.base64url.encode(b);
    assert(!/[+/=]/.test(s), 'must be url-safe unpadded');
    assert(dec.decode(jose.base64url.decode(s)) === 'héllo, wörld · 日本語', 'round-trip');
  });

  // --- JWS: HMAC ------------------------------------------------------------
  for (const alg of ['HS256', 'HS384', 'HS512']) {
    await check('JWS ' + alg + ' sign/verify', async () => {
      const key = await jose.generateSecret(alg);
      const jws = await new jose.CompactSign(enc.encode(PLAINTEXT))
        .setProtectedHeader({ alg }).sign(key);
      const { payload } = await jose.compactVerify(jws, key);
      assert(dec.decode(payload) === PLAINTEXT, 'payload mismatch');
    });
  }

  // --- JWS: asymmetric ------------------------------------------------------
  const sigAlgs = ['RS256', 'RS512', 'PS256', 'PS512', 'ES256', 'ES384', 'ES512', 'EdDSA'];
  for (const alg of sigAlgs) {
    await check('JWS ' + alg + ' sign/verify', async () => {
      const { publicKey, privateKey } = await jose.generateKeyPair(alg);
      const jws = await new jose.CompactSign(enc.encode(PLAINTEXT))
        .setProtectedHeader({ alg }).sign(privateKey);
      const { payload } = await jose.compactVerify(jws, publicKey);
      assert(dec.decode(payload) === PLAINTEXT, 'payload mismatch');
    });
  }

  // --- JWS serializations ---------------------------------------------------
  await check('JWS flattened sign/verify', async () => {
    const key = await jose.generateSecret('HS256');
    const jws = await new jose.FlattenedSign(enc.encode(PLAINTEXT))
      .setProtectedHeader({ alg: 'HS256' }).sign(key);
    assert(jws.signature && jws.payload, 'flattened shape');
    const { payload } = await jose.flattenedVerify(jws, key);
    assert(dec.decode(payload) === PLAINTEXT, 'payload mismatch');
  });
  await check('JWS general sign/verify', async () => {
    const key = await jose.generateSecret('HS256');
    const jws = await new jose.GeneralSign(enc.encode(PLAINTEXT))
      .addSignature(key).setProtectedHeader({ alg: 'HS256' }).sign();
    assert(Array.isArray(jws.signatures), 'general shape');
    const { payload } = await jose.generalVerify(jws, key);
    assert(dec.decode(payload) === PLAINTEXT, 'payload mismatch');
  });

  // --- JWT ------------------------------------------------------------------
  await check('JWT sign/verify with claims', async () => {
    const key = await jose.generateSecret('HS256');
    const jwt = await new jose.SignJWT({ role: 'admin' })
      .setProtectedHeader({ alg: 'HS256' })
      .setIssuer('urn:orbital')
      .setAudience('urn:test')
      .setIssuedAt()
      .setExpirationTime('2h')
      .sign(key);
    const { payload } = await jose.jwtVerify(jwt, key, { issuer: 'urn:orbital', audience: 'urn:test' });
    assert(payload.role === 'admin', 'claim mismatch');
  });
  await check('JWT expired token rejected', async () => {
    const key = await jose.generateSecret('HS256');
    const jwt = await new jose.SignJWT({})
      .setProtectedHeader({ alg: 'HS256' })
      .setExpirationTime('-1h')
      .sign(key);
    let threw = false;
    try { await jose.jwtVerify(jwt, key); } catch (e) { threw = e.code === 'ERR_JWT_EXPIRED'; }
    assert(threw, 'expired token was accepted');
  });
  await check('JWT wrong issuer rejected', async () => {
    const key = await jose.generateSecret('HS256');
    const jwt = await new jose.SignJWT({}).setProtectedHeader({ alg: 'HS256' })
      .setIssuer('urn:orbital').sign(key);
    let threw = false;
    try { await jose.jwtVerify(jwt, key, { issuer: 'urn:evil' }); } catch { threw = true; }
    assert(threw, 'wrong issuer was accepted');
  });

  // --- JWE: dir (direct content encryption) ---------------------------------
  const encs = ['A128GCM', 'A192GCM', 'A256GCM', 'A128CBC-HS256', 'A192CBC-HS384', 'A256CBC-HS512'];
  for (const encAlg of encs) {
    await check('JWE dir ' + encAlg + ' encrypt/decrypt', async () => {
      const key = await jose.generateSecret(encAlg);
      const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
        .setProtectedHeader({ alg: 'dir', enc: encAlg }).encrypt(key);
      const { plaintext } = await jose.compactDecrypt(jwe, key);
      assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
    });
  }

  // --- JWE: AES key wrap ----------------------------------------------------
  for (const alg of ['A128KW', 'A192KW', 'A256KW', 'A256GCMKW']) {
    await check('JWE ' + alg + ' (+A256GCM) encrypt/decrypt', async () => {
      const key = await jose.generateSecret(alg);
      const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
        .setProtectedHeader({ alg, enc: 'A256GCM' }).encrypt(key);
      const { plaintext } = await jose.compactDecrypt(jwe, key);
      assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
    });
  }

  // --- JWE: RSA-OAEP --------------------------------------------------------
  for (const alg of ['RSA-OAEP', 'RSA-OAEP-256']) {
    await check('JWE ' + alg + ' (+A256GCM) encrypt/decrypt', async () => {
      const { publicKey, privateKey } = await jose.generateKeyPair(alg);
      const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
        .setProtectedHeader({ alg, enc: 'A256GCM' }).encrypt(publicKey);
      const { plaintext } = await jose.compactDecrypt(jwe, privateKey);
      assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
    });
  }

  // --- JWE: ECDH-ES ---------------------------------------------------------
  await check('JWE ECDH-ES P-256 (+A256GCM) encrypt/decrypt', async () => {
    const { publicKey, privateKey } = await jose.generateKeyPair('ECDH-ES');
    const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
      .setProtectedHeader({ alg: 'ECDH-ES', enc: 'A256GCM' }).encrypt(publicKey);
    const { plaintext } = await jose.compactDecrypt(jwe, privateKey);
    assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
  });
  await check('JWE ECDH-ES+A256KW P-256 (+A256GCM) encrypt/decrypt', async () => {
    const { publicKey, privateKey } = await jose.generateKeyPair('ECDH-ES+A256KW');
    const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
      .setProtectedHeader({ alg: 'ECDH-ES+A256KW', enc: 'A256GCM' }).encrypt(publicKey);
    const { plaintext } = await jose.compactDecrypt(jwe, privateKey);
    assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
  });
  await check('JWE ECDH-ES X25519 (+A256GCM) encrypt/decrypt', async () => {
    const { publicKey, privateKey } = await jose.generateKeyPair('ECDH-ES', { crv: 'X25519' });
    const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
      .setProtectedHeader({ alg: 'ECDH-ES', enc: 'A256GCM' }).encrypt(publicKey);
    const { plaintext } = await jose.compactDecrypt(jwe, privateKey);
    assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
  });

  // --- JWE: PBES2 (password-based; opt-in allowlist) ------------------------
  for (const alg of ['PBES2-HS256+A128KW', 'PBES2-HS512+A256KW']) {
    await check('JWE ' + alg + ' (+A256GCM) encrypt/decrypt', async () => {
      const password = enc.encode('correct horse battery staple');
      const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
        .setProtectedHeader({ alg, enc: 'A256GCM' }).encrypt(password);
      const { plaintext } = await jose.compactDecrypt(jwe, password, {
        keyManagementAlgorithms: [alg],
        contentEncryptionAlgorithms: ['A256GCM'],
      });
      assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
    });
  }

  // --- JWE serializations + JWT encryption ----------------------------------
  await check('JWE flattened encrypt/decrypt', async () => {
    const key = await jose.generateSecret('A256GCM');
    const jwe = await new jose.FlattenedEncrypt(enc.encode(PLAINTEXT))
      .setProtectedHeader({ alg: 'dir', enc: 'A256GCM' }).encrypt(key);
    assert(jwe.ciphertext && jwe.iv && jwe.tag, 'flattened shape');
    const { plaintext } = await jose.flattenedDecrypt(jwe, key);
    assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
  });
  await check('JWE general encrypt/decrypt', async () => {
    const key = await jose.generateSecret('A256KW');
    const jwe = await new jose.GeneralEncrypt(enc.encode(PLAINTEXT))
      .setProtectedHeader({ enc: 'A256GCM' })
      .addRecipient(key).setUnprotectedHeader({ alg: 'A256KW' })
      .encrypt();
    assert(Array.isArray(jwe.recipients), 'general shape');
    const { plaintext } = await jose.generalDecrypt(jwe, key);
    assert(dec.decode(plaintext) === PLAINTEXT, 'plaintext mismatch');
  });
  await check('EncryptJWT / jwtDecrypt (dir A256GCM)', async () => {
    const key = await jose.generateSecret('A256GCM');
    const jwt = await new jose.EncryptJWT({ sub: 'alice' })
      .setProtectedHeader({ alg: 'dir', enc: 'A256GCM' })
      .setIssuedAt().setExpirationTime('1h').encrypt(key);
    const { payload } = await jose.jwtDecrypt(jwt, key);
    assert(payload.sub === 'alice', 'claim mismatch');
  });

  // --- JWK import/export + thumbprints --------------------------------------
  await check('JWK export/import RSA (RS256) round-trip', async () => {
    const { privateKey } = await jose.generateKeyPair('RS256', { extractable: true });
    const jwk = await jose.exportJWK(privateKey);
    assert(jwk.kty === 'RSA' && jwk.d, 'expected private RSA JWK');
    const reimported = await jose.importJWK(jwk, 'RS256');
    const jws = await new jose.CompactSign(enc.encode('x'))
      .setProtectedHeader({ alg: 'RS256' }).sign(reimported);
    assert(jws.split('.').length === 3, 're-signed with imported key');
  });
  await check('JWK export/import EC (P-256) round-trip', async () => {
    const { publicKey, privateKey } = await jose.generateKeyPair('ES256', { extractable: true });
    const pubJwk = await jose.exportJWK(publicKey);
    const prvJwk = await jose.exportJWK(privateKey);
    assert(pubJwk.kty === 'EC' && pubJwk.crv === 'P-256', 'EC JWK shape');
    const sk = await jose.importJWK(prvJwk, 'ES256');
    const pk = await jose.importJWK(pubJwk, 'ES256');
    const jws = await new jose.CompactSign(enc.encode('x')).setProtectedHeader({ alg: 'ES256' }).sign(sk);
    const { payload } = await jose.compactVerify(jws, pk);
    assert(dec.decode(payload) === 'x', 'verify after import');
  });
  await check('JWK export/import OKP (Ed25519) round-trip', async () => {
    const { publicKey, privateKey } = await jose.generateKeyPair('EdDSA', { extractable: true });
    const prvJwk = await jose.exportJWK(privateKey);
    const pubJwk = await jose.exportJWK(publicKey);
    assert(prvJwk.kty === 'OKP' && prvJwk.crv === 'Ed25519', 'OKP JWK shape');
    const sk = await jose.importJWK(prvJwk, 'EdDSA');
    const pk = await jose.importJWK(pubJwk, 'EdDSA');
    const jws = await new jose.CompactSign(enc.encode('x')).setProtectedHeader({ alg: 'EdDSA' }).sign(sk);
    const { payload } = await jose.compactVerify(jws, pk);
    assert(dec.decode(payload) === 'x', 'verify after import');
  });
  await check('JWK export/import oct round-trip', async () => {
    const key = await jose.generateSecret('HS256', { extractable: true });
    const jwk = await jose.exportJWK(key);
    assert(jwk.kty === 'oct' && jwk.k, 'oct JWK shape');
    const reimported = await jose.importJWK(jwk, 'HS256');
    const jws = await new jose.CompactSign(enc.encode('x')).setProtectedHeader({ alg: 'HS256' }).sign(reimported);
    const { payload } = await jose.compactVerify(jws, reimported);
    assert(dec.decode(payload) === 'x', 'verify after import');
  });
  await check('JWK thumbprint (EC + RSA, RFC 7638)', async () => {
    const { publicKey: ec } = await jose.generateKeyPair('ES256', { extractable: true });
    const { publicKey: rsa } = await jose.generateKeyPair('RS256', { extractable: true });
    const ecTp = await jose.calculateJwkThumbprint(await jose.exportJWK(ec));
    const rsaTp = await jose.calculateJwkThumbprint(await jose.exportJWK(rsa));
    assert(ecTp.length === 43 && rsaTp.length === 43, 'sha-256 thumbprint length');
    assert(ecTp !== rsaTp, 'distinct thumbprints');
  });

  // --- Tamper / wrong-key rejection -----------------------------------------
  await check('JWS tampered signature rejected', async () => {
    const key = await jose.generateSecret('HS256');
    const jws = await new jose.CompactSign(enc.encode(PLAINTEXT)).setProtectedHeader({ alg: 'HS256' }).sign(key);
    const parts = jws.split('.');
    const sig = jose.base64url.decode(parts[2]); sig[0] ^= 0xff;
    parts[2] = jose.base64url.encode(sig);
    let threw = false;
    try { await jose.compactVerify(parts.join('.'), key); } catch { threw = true; }
    assert(threw, 'tampered signature accepted');
  });
  await check('JWS wrong verification key rejected', async () => {
    const key = await jose.generateSecret('HS256');
    const other = await jose.generateSecret('HS256');
    const jws = await new jose.CompactSign(enc.encode(PLAINTEXT)).setProtectedHeader({ alg: 'HS256' }).sign(key);
    let threw = false;
    try { await jose.compactVerify(jws, other); } catch { threw = true; }
    assert(threw, 'wrong key accepted');
  });
  await check('JWE tampered ciphertext rejected', async () => {
    const key = await jose.generateSecret('A256GCM');
    const jwe = await new jose.CompactEncrypt(enc.encode(PLAINTEXT))
      .setProtectedHeader({ alg: 'dir', enc: 'A256GCM' }).encrypt(key);
    const parts = jwe.split('.');
    const ct = jose.base64url.decode(parts[3]); ct[0] ^= 0xff;
    parts[3] = jose.base64url.encode(ct);
    let threw = false;
    try { await jose.compactDecrypt(parts.join('.'), key); } catch { threw = true; }
    assert(threw, 'tampered ciphertext accepted');
  });

  console.log('');
  console.log(`jose smoke: ${passed} passed, ${failed} failed`);
  if (failed > 0) {
    console.log('Failures:');
    for (const f of failures) console.log('  - ' + f);
    process.exit(1);
  }
}

main().catch((e) => {
  console.error('jose smoke crashed:', (e && e.stack) || e);
  process.exit(1);
});
