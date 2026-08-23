# Known limitations & deliberate shortcuts

Central index of features that are **partially implemented, approximated, or
intentionally skipped** inside otherwise-working modules. Module *presence* is
tracked separately in [`../modules.md`](../modules.md); this file tracks the gaps
*within* implemented modules so they can be picked up later.

Related, more detailed docs:

- [`async-context.md`](./async-context.md) — `async_hooks` / `AsyncLocalStorage`
  propagation across native `await` (needs `SetPromiseHook` glue).
- [`../tests/core-packages/README.md`](../tests/core-packages/README.md) —
  per-package test suites and the exact specs skipped for each (yargs, mocha,
  express, axios, …), with rationale.

Severity legend: **[gap]** missing capability · **[approx]** works but not
spec-exact · **[perf]** correctness OK, performance/architecture shortcut.

---

## ESM loader & hooks (`pkg/nodejs/esm/`)

- **[approx] Async loader-hook drive is bounded, not truly scheduled.**
  `awaitPromiseRT` pumps the isolated loader realm's microtask + event loop up to
  `maxHookTicks` (200000) iterations, then gives up with "hook promise did not
  settle" (`hooks.go`). This is a guard against a runaway hook, not a real
  cooperative scheduler; a legitimately long-running async hook could be cut off.
- **[gap] Import attributes are not supported.** `import x from './y.json' with
  { type: 'json' }` (and the older `assert { … }`) is not parsed; JSON is
  classified by extension instead. A fixture had to drop the `assert` clause.
- **[approx] One JS helper remains in the loader.** `esm-loader.js` still hosts
  `__esmFinishDynamicImport` (chains a module's evaluation promise to its
  namespace for `await import()`); everything else is Go.
- **[gap] `NODE_OPTIONS` parsing is a subset.** Only `--import` / `--require`
  (and their `=` forms) are honored (`cmd/orbital/main.go` `nodeOptionArgs`);
  other Node options in the env var are ignored.

## crypto — KeyObject API (`pkg/nodejs/crypto/keyobject.go`, `keyobject.js`)

- **[gap] RSA `publicExponent` option is ignored.** `generateKeyPair('rsa', …)`
  always uses Go's fixed exponent 65537; a requested non-65537 exponent is
  silently ignored (`doGenerateKeyPair`). jose only ever asks for 65537.
- **[perf] Key generation is synchronous.** The async `generateKeyPair(…, cb)`
  and `promisify` forms run the actual keygen inside a microtask on the main
  thread (`keyobject.js`), so RSA-2048/4096 generation blocks the event loop.
  Node offloads to the libuv threadpool. Needs a Go goroutine + event-loop
  completion to be truly async.
- **[gap] Post-quantum (ML-DSA) keys are unimplemented.** `generateKeyPair`
  rejects `ml-dsa-*`; Go's stdlib has no ML-DSA. (WebCrypto side correctly
  reports it as unsupported — see below.)
- **[gap] No node:crypto one-shot sign/verify/encrypt with KeyObject.** The
  KeyObject surface covers generate / import / export / equals only. `crypto.sign`,
  `crypto.verify`, `privateEncrypt`, `publicDecrypt`, `createSign/createVerify`,
  and `diffieHellman` for KeyObjects are not implemented. jose's default suite
  uses the WebCrypto path, so this was not needed there.
- **[approx] `KeyObject.equals` for asymmetric keys compares canonical JWK JSON**
  rather than DER (`keyEqualsFunc`). Functionally correct for equal keys; not the
  byte-for-byte SPKI comparison Node does.
- **[perf] `crypto.randomInt` has modulo bias.** `randomIntFunc` uses
  `n % rangeSize` (`crypto.go`); Node uses rejection sampling for a uniform
  distribution. Pre-existing, noted here for completeness.
- **[perf] `crypto.randomBytes(size, cb)` async form is synchronous.** It fills
  bytes immediately and calls back via a microtask (`crypto.go`), not the
  threadpool.

## WebCrypto — SubtleCrypto (`pkg/nodejs/webcrypto/webcrypto.js`)

- **[gap] `deriveBits`/`deriveKey` support PBKDF2, ECDH, X25519 only.** `HKDF`
  is accepted by `importKey` (it's in the import allowlist) but `deriveBits`
  throws `NotSupportedError` for it — an inconsistency to resolve when HKDF
  derivation is implemented natively.
- **[approx] JWK validation is presence-only.** `assertValidJWK` checks that the
  required members exist for the `kty` (e.g. RSA needs `n`,`e`); it does not do
  full structural validation (coordinate lengths, on-curve checks, base64url
  charset). Enough to reject the structurally-empty keys libraries probe with.
- **[gap] ML-DSA and other non-allowlisted algorithms are rejected by design.**
  `SUPPORTED_IMPORT_ALGS` gates `importKey`; anything outside it throws
  `NotSupportedError` (matches Node). Expanding real algorithm support means
  adding both the allowlist entry and the native operation.
- **[approx] Key material crosses the Go boundary as base64 JWK/DER strings.**
  Correct, but an extra encode/parse per operation vs. holding native handles.

## buffer (`pkg/nodejs/buffer/buffer.js`)

- **[approx] `Buffer.byteLength(str, 'base64url')` is approximate.** Returns
  `(len*3)>>2` assuming no padding; exact decoded length can be 1–2 bytes less
  for some inputs. The decode path itself (`fromString`) is exact.

## util (`pkg/nodejs/util/util.js`)

- **[approx] `util.inspect` line-wrapping differs from Node.** Orbital keeps
  more output on one line; Node breaks at `breakLength`. This causes a yargs spec
  (multiline object output) to be skipped — see the yargs section of the
  core-packages README.

---

## Node features found missing during development

Surfaced while running real packages (jose, yargs, tsc, mocha, express, axios).
Items still missing are listed first; what was closed this cycle follows for
traceability.

### Still missing

- **[gap] `node:crypto` symmetric & signature operations.** The module exposes
  hash / hmac / random + the KeyObject API, but **not**: `createCipheriv` /
  `createDecipheriv`, `createSign` / `createVerify` and one-shot `sign` /
  `verify`, `createDiffieHellman` / `createECDH`, and symmetric `generateKey` /
  `generateKeySync`. jose exercises these through WebCrypto instead, so its suite
  passes, but native `node:crypto` consumers of them will not work.
- **[gap] `node:crypto` KDFs.** `pbkdf2` / `pbkdf2Sync`, `scrypt` / `scryptSync`,
  and `hkdf` / `hkdfSync` are absent from `node:crypto`. (WebCrypto has a native
  PBKDF2 for `deriveBits`; it is not surfaced on `node:crypto`.)
- **[gap] `node:crypto` introspection is partial.** `getHashes()` returns only
  `md5, sha1, sha256, sha384, sha512` (`crypto.go`); Node lists far more. There
  is no `getCiphers()` / `getCurves()` / `X509Certificate`.
- **[gap] `SubtleCrypto` HKDF derivation** — see the WebCrypto section above.

### Closed during this cycle

These were missing and were implemented while getting jose/yargs onto their
native suites; kept here as a changelog pointer.

- ESM: dynamic `import()`, top-level `await`, `import.meta.url`,
  `module.register()`, `--import` (+ `NODE_OPTIONS`), resolve/load loader hooks,
  and **surfacing of module top-level throws** (a rejected evaluation promise was
  previously swallowed — see `esm.go` `RunModule`).
- `util.parseArgs`; `util.format()` with no args now returns `''` (was
  `'undefined'`).
- `require.cache` is a plain object (was a `Map`, so `delete`/`in` failed);
  `require.resolve` throws `MODULE_NOT_FOUND` with `.code` (was returning `null`).
- `path.normalize` preserves a trailing slash.
- `crypto.generateKeyPair(Sync)`, `createSecretKey` / `createPrivateKey` /
  `createPublicKey`, and the `KeyObject` class (JWK/PEM/DER).
- `SubtleCrypto.importKey` / `exportKey` for `spki` / `pkcs8`, plus algorithm
  allowlisting and JWK member validation.
- `Buffer` `base64url` encoding.
- `fs.Stats` predicate methods across sync/callback/promise APIs,
  `fs.realpathSync` / `lstatSync`, and the synchronous fd API
  (`openSync`/`writeSync`/`readSync`/`fstatSync`/`closeSync`) — see the tsc
  section of the core-packages README.

---

## How to pick one up

1. Find the code path cited above.
2. Check whether a native Go implementation exists in stdlib (crypto, encoding)
   — prefer Go over JS for crypto/async per project convention.
3. If it needs new V8 glue (e.g. `SetPromiseHook`), note that `libv8go_glue.a`
   is rebuilt with V8's toolchain (see `async-context.md` for the toolchain
   caveat), not by plain `go build`.
4. Add/enable the corresponding test (often an already-skipped spec in the
   core-packages suites).
