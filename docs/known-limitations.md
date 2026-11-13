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

## Uncaught exceptions and rejections (`pkg/runtime/uncaught.go`, `uncaught.js`)

- **[approx] The printed error has no source-line header.** Node prints `file:line`, the source
  line and a caret above the stack, and its version below it. Orbital prints the stack.
- **[gap] Only the default `throw` rejection mode.** There is no `--unhandled-rejections` flag,
  and no `PromiseRejectionHandledWarning`.
- **[gap] `domain` does not follow async work.** Only a throw inside the synchronous `run()`
  reaches a domain's `'error'` listener. A throw from a timer started in one is uncaught.
- **[approx] A worker's `'error'` event carries text.** The parent gets an `Error` whose message
  is the worker's whole printed exception, not a copy of the thrown value.
- **[approx] `process.nextTick` runs after promise reactions.** Node runs the tick queue first.
  Orbital's ticks are event-loop work, so a promise reaction queued by the same code runs ahead
  of them.
- **[approx] A `_write` that throws before calling back becomes a stream error.** `doWrite` in
  `stream.js` gives it to the stream's `'error'` event, where Node lets it propagate to the caller
  of `write` and on to `uncaughtException`.
- **[approx] Native modules must use `Runtime.MakeCallback`.** A Go module that calls a function
  with `Value.Call` from the event loop and ignores the error still loses the exception.

---

## Network modules (`pkg/nodejs/net`, `tls`, `http`, `https`, `dns`, `dgram`)

- **Hostname checks follow Go, not Node, unless a script passes `checkServerIdentity`.** A
  certificate with only a Common Name and no SAN fails where Node accepts it. Only a
  whole-label `*.` wildcard matches, not `f*.example.com`, and Go accepts `*.com` from a
  trusted CA where Node rejects it. `tls.checkServerIdentity` itself follows Node.
- **TLS options with no effect.** `ciphers`, `honorCipherOrder`, `ecdhCurve`, `dhparam` and
  `sigalgs` are ignored. `pfx` and encrypted keys are not supported and throw. Session
  resumption is not exposed: `getSession` and `getTLSTicket` return `undefined`, and no
  `session` event fires. `tls.rootCertificates` is empty.
- **`TLSSocket` can only wrap a `net.Socket`,** not an arbitrary stream.
- **The HTTP parser is stricter than Node in places.** All of these are refused, because
  another hop could read the same bytes differently:
  - a non-chunked final `Transfer-Encoding`, before the handler runs, where Node runs the
    handler first
  - bare LF or bare CR line endings, including in responses
  - a space before a header's colon, or a space inside its name, where Node accepts both on
    headers that don't decide framing
  - extra spaces in the start line, and HTTP/0.9 requests with no version
  - an empty `Transfer-Encoding`
  - a `Content-Length` above 2^53 - 1
  - a trailing tab on `Transfer-Encoding` in a response, where Node reads to EOF
  - chunk extensions with no name or value (`;=b`, `;a=`)
- **The HTTP parser follows the spec where Node is stricter on things that can't move a
  message's end.** Several chunk extensions on one line and escapes inside a quoted one are
  accepted. A `Connection: close` trailer is ignored rather than closing the connection.
- **A request followed by garbage in the same packet** gets `'aborted'` rather than `'end'`,
  because the rejection tears the connection down before `'end'` is delivered.
- **A 204 or 304 response never has a body,** as the HTTP spec says. Node reads one if it
  has a `Content-Length`.
- **`'finish'` on a response can come after the request's `'end'`,** because it waits for
  the write to complete on another thread.
- **Sockets cannot be made from file descriptors,** and `server.listen` cannot take a handle.
- **`net.connect` with an IPv6 zone** (`fe80::1%en0`) fails with `ENOTFOUND`, because a zone
  is free-form text a policy could be made to match on. `net.isIP` still accepts it.
- **Happy-eyeballs is sequential.** Addresses are tried in order, and `'lookup'` fires once
  rather than per address.
- **`dgram.getRecvBufferSize` and `getSendBufferSize`** report the size last set, or a default,
  rather than asking the OS.
- **`fs` callback calls run synchronously.** `fs.appendFile`, `open`, `write`, `read`, `mkdir`
  and the other callback forms, and `fs.createWriteStream`'s writes, do their work on the
  JavaScript thread and call back on the next tick. Only `readFile`, `writeFile` given a path,
  `stat`, `access` and read streams run off the thread.
- **`fs.rm` and `rmSync` look a path up with `stat`,** so a symbolic link to a directory counts
  as a directory.
- **Some `fs` calls are still missing,** including `accessSync`, `truncate`,
  `ftruncate`, `fsync`, `chmod`, `utimes`, `mkdtemp`, and the `fs.Stats` and `fs.Dirent` classes.

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
