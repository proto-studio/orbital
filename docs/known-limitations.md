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

- **[approx] The application waits while an async loader hook runs.** `awaitPromiseRT`
  runs the isolated loader realm's event loop until a hook's promise settles, waiting on its
  timers and I/O as long as they take, as Node waits on its hooks thread. Node's main thread
  keeps running meanwhile (its timers fire while an `import()` waits on a hook); Orbital's
  waits. A hook promise that nothing in the loader realm can settle any more fails the import,
  where Node leaves it pending (and exits 13 if top-level `await` was waiting on it). (#107)
- **[gap] Import attributes are not checked.** `with { type: 'json' }` is parsed but ignored,
  and a module's type comes from its extension. Node throws `ERR_IMPORT_ASSERTION_TYPE_MISSING`
  for a JSON import with no attribute and `ERR_IMPORT_ASSERTION_TYPE_FAILED` for `type: 'json'`
  on a JavaScript module; Orbital loads both. The older `assert { … }` form, which Node 20
  accepts, is a `SyntaxError`. (#108)
- **[approx] A few JS helpers remain in the loader.** `esm-loader.js` still hosts
  `__esmFinishDynamicImport` (chains a module's evaluation promise to its
  namespace for `await import()`), `__esmModuleError` (builds the Node-shaped
  error a failed import throws) and `__esmRejected` (reports a link error as an
  uncaught exception); everything else is Go.
- **[gap] Most `NODE_OPTIONS` flags have no effect.** `NODE_OPTIONS` is split and checked as
  Node 20.18.1 does it (`cmd/orbital/node_options.go`): an option Node doesn't allow there, a
  bad negation or a missing value stops the program with exit code 9 and Node's message.
  Only `--require`/`-r`, `--import`, `--title`, `--input-type` and `--[no-]warnings` take
  effect, because those are the Node options the `orbital` command implements. Other options
  Node allows there (`--enable-source-maps`, `--max-old-space-size`, `--conditions`,
  `--unhandled-rejections`, …) are accepted and ignored, and option values (ports, sizes) are
  not validated. (#109)
- **[gap] A package's `"exports"` map is not enforced.** A subpath it doesn't list loads
  the file anyway; Node throws `ERR_PACKAGE_PATH_NOT_EXPORTED`. A missing module or package
  does fail with Node's `ERR_MODULE_NOT_FOUND`.
- **[approx] When an `import()`ed module runs relative to timers is not pinned down.** It runs
  after the importer's code, microtasks and `nextTick` callbacks, as in Node, but before a
  `setImmediate` or `setTimeout(…, 0)` the importer queued first. Node usually runs those
  first; the order depends on file I/O timing and varies between runs.

## crypto — KeyObject API (`pkg/nodejs/crypto/keyobject.go`, `keyobject.js`)

- **[gap] RSA `publicExponent` above 2^31-1 is refused.** Node accepts any odd
  uint32 exponent of 3 or more; Go's `crypto/rsa` can't use or export a key whose
  exponent is above 2147483647, so `generateKeyPair('rsa', …)` throws (or calls
  back with) an error for those instead (`generateRSAKey`). `crypto.subtle.generateKey`
  shares the generator, so the same exponents give an `OperationError` there.
- **[approx] `KeyObject.equals` for asymmetric keys compares canonical JWK JSON**
  rather than DER (`keyEqualsFunc`). Functionally correct for equal keys; not the
  byte-for-byte SPKI comparison Node does.

## WebCrypto — SubtleCrypto (`pkg/nodejs/webcrypto/webcrypto.js`)

- **[approx] `importKey` doesn't check key usages against the key type.** JWK members
  are checked as Node does (`kty`, `crv`, `alg`, `use`, `key_ops`, `ext`, coordinate
  lengths, on-curve points, private keys matching public ones), but usages are not:
  Node rejects, for example, a secret key imported with no usages, or a public key
  with `sign`, with a `SyntaxError`.
- **[approx] Only HKDF checks key usages in `deriveBits`/`deriveKey`.** HKDF rejects a
  base key without the `deriveBits` or `deriveKey` usage, or for another algorithm, with
  Node's `InvalidAccessError`; PBKDF2, ECDH and X25519 don't check yet.
- **[gap] ML-DSA and other non-allowlisted algorithms are rejected by design.**
  `SUPPORTED_IMPORT_ALGS` gates `importKey`; anything outside it throws
  `NotSupportedError` (matches Node). Expanding real algorithm support means
  adding both the allowlist entry and the native operation.
- **[approx] Key material crosses the Go boundary as base64 JWK/DER strings.**
  Correct, but an extra encode/parse per operation vs. holding native handles.

## buffer (`pkg/nodejs/buffer/buffer.js`)

- **[approx] `Buffer.byteLength(str, 'base64url')` is approximate.** Returns
  `(len*3)>>2` assuming no padding; exact decoded length can be 1–2 bytes less
  for some inputs. The decode path itself (`fromString`) is exact. (#117)

---

## Uncaught exceptions and rejections (`pkg/runtime/uncaught.go`, `uncaught.js`)

- **[approx] The printed error has no source-line header.** Node prints `file:line`, the source
  line and a caret above the stack, and its version below it. Orbital prints the stack. (#118)
- **[gap] Only the default `throw` rejection mode.** There is no `--unhandled-rejections` flag,
  and no `PromiseRejectionHandledWarning`. (#119)
- **[gap] `domain` does not follow async work.** Only a throw inside the synchronous `run()`
  reaches a domain's `'error'` listener. A throw from a timer started in one is uncaught. (#120)
- **[approx] A worker's `'error'` event carries text.** The parent gets an `Error` whose message
  is the worker's whole printed exception, not a copy of the thrown value. (#121)
- **[approx] `process.nextTick` runs after promise reactions.** Node runs the tick queue first.
  Orbital's ticks are event-loop work, so a promise reaction queued by the same code runs ahead
  of them. (#122)
- **[approx] A `_write` that throws before calling back becomes a stream error.** `doWrite` in
  `stream.js` gives it to the stream's `'error'` event, where Node lets it propagate to the caller
  of `write` and on to `uncaughtException`. (#123)
- **[approx] Native modules must use `Runtime.MakeCallback`.** A Go module that calls a function
  with `Value.Call` from the event loop and ignores the error still loses the exception.

---

## Network modules (`pkg/nodejs/net`, `tls`, `http`, `https`, `dns`, `dgram`)

- **Some TLS options are limited by Go's TLS.** TLS 1.3 cipher suites can't be chosen, so
  `TLS_` names in `ciphers` are accepted without effect, and the suite negotiated can differ
  from Node's. `honorCipherOrder`, `dhparam` and `sigalgs` have no effect, and `X448` in
  `ecdhCurve` is skipped. `ciphers` is read over the TLS 1.2 suites Go has, so a list that
  only names suites Go lacks, such as DHE ones, allows only TLS 1.3. Sessions are opaque
  buffers that only Orbital reads, a TLS 1.3 server issues one ticket where Node issues two,
  and `getSession()` is `undefined` until the first `'session'` event. Servers resume
  sessions only from tickets, so `'newSession'` and `'resumeSession'` never fire. CA
  certificates in a `pfx` are sent as the chain but not trusted for verifying peers.
- **A TLS handshake over a stream that isn't a socket keeps the process running** until it
  finishes, even if nothing else would. In Node only the stream decides that.
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
- **A response written in parts can finish a turn later than in Node.** For a request with
  no body, a handler that calls `res.write` and then `res.end` sees the request's `'end'`
  before the response's `'finish'`. Node completes those writes when the socket is uncorked,
  before the request ends.
- **`server.listen(handle)` takes only a handle with an `fd`,** not a `net.Server` or
  `net.Socket` object. (#128)
- **`net.connect` with an IPv6 zone** (`fe80::1%en0`) fails with `ENOTFOUND`, because a zone
  is free-form text a policy could be made to match on. `net.isIP` still accepts it.

## fs (`pkg/nodejs/fs`)

- **[perf] A few `fs` calls still run on the JavaScript thread.** `fs.realpath` checks the
  path off the thread but reports a failure synchronously, a `FileHandle`'s `readFile` and
  `writeFile` read and write synchronously, and `fs.createWriteStream` opens its file
  synchronously. The other callback and promise forms, and write streams' writes, run off
  the thread.
- **[gap] A custom `Filesystem` without `LinkFilesystem`** fails `symlink`, `readlink` and
  `link` with `ENOSYS`.
- **[approx] `fs.watch` and `fs.watchFile` poll.** `watch` checks every 100 ms and reports
  `'rename'` when an entry appears or goes and `'change'` when its stats change, so it can
  miss a change undone within one poll and doesn't report the events in Node's exact order.
  `watchFile` polls at its `interval`, as Node's does.
- **[approx] `fs.opendir` and `fs.openAsBlob` read when they open.** A `Dir` lists the
  directory when it's opened rather than as it's read, and `openAsBlob` reads the file into
  the `Blob` at once.
- **[approx] `fs.cp` doesn't copy sockets, FIFOs or devices specially,** and copies a
  symbolic link over an existing destination without Node's checks on what that link points
  to.
- **[approx] Some `fs.Stats` details depend on the filesystem.** On Linux `birthtime` is the
  change time. A custom `Filesystem` without `MetadataFilesystem` reports default `dev`,
  `ino`, `uid` and the like, and fails `truncate`, `chmod`, `chown` and `utimes` with
  `ENOSYS`; without `StatfsFilesystem`, `statfs` fails with `ENOSYS`.

## os and child_process (`pkg/nodejs/os`, `child_process`, `internal/sysconst`)

- **[gap] `os.constants` has no `dlopen` or `UV_UDP_REUSEADDR`.** It has `errno`, `signals`
  and `priority`, which match Node's values for the host. (#135)
- **[approx] `process.kill` is refused unless the embedder allows it.** It signals through
  `Config.ProcessSignaler`. The orbital CLI uses a real signaler, as Node behaves, except in
  `--sandbox` mode. A library embedder that sets none gets `EPERM` for every call.

## V8 embedding (`pkg/v8`)

- **[approx] V8's memory protection keys are turned off, on purpose.** On x64 Linux CPUs with
  PKU, V8 write-protects code pages with a key that only threads descended from the one that
  initialized V8 can use. Go runs isolates on any OS thread, so `v8go_init` passes
  `--no-memory-protection-keys` and code pages fall back to plain `mprotect` permissions. Other
  platforms are unaffected.

## util.inspect and console.log (`pkg/nodejs/internal/inspectjs`, `console`, `util`)

- **[gap] Self-references aren't labeled.** Node prints `<ref *1> { self: [Circular *1] }`.
  `util.inspect` prints a plain `[Circular]` without the `<ref *1>` marker, and `console.log`
  doesn't detect the cycle at all: it repeats the object until the depth limit and prints
  `[Object]`. (#138)
- **[gap] Symbol keys aren't listed.** `{ x: 2, [Symbol(k)]: 1 }` prints as `{ x: 2 }` in both.
  (#139)
- **[approx] `%i` of a number too large for an integer** clamps to the 64-bit limit
  (`9223372036854775807`). Node parses the number's text, so `2 ** 70` prints `1`. (#140)
- **[approx] `%d` of a numeric string** prints `NaN`. Node converts the string, so `'42'`
  prints `42`. (#140)
- **[approx] `util.format`'s `%d` of a BigInt drops the `n`,** so `42n` prints `42`. Node and
  `console.log` print `42n`. (#140)
- **[approx] String widths come from Node's no-ICU table.** Node 20 measures them with ICU,
  which counts more characters as wide, such as emoji outside the table's ranges (U+1F680).
  Arrays laid out in columns are padded differently when they hold them. (#137)

## Node features found missing during development

Surfaced while running real packages (jose, yargs, tsc, mocha, express, axios).
Items still missing are listed first; what was closed this cycle follows for
traceability.

### Still missing

- **[gap] Some ciphers are missing.** `createCipheriv` has AES (CBC, ECB, CTR, CFB,
  CFB8, OFB, GCM) and Triple DES (`des-ede3*`, `des-ede*`) from Go's standard library, and
  ChaCha20 and ChaCha20-Poly1305 from `golang.org/x/crypto` (`cipher.go`). CCM, OCB, XTS,
  CFB1, key wrap, Triple DES in CFB and OFB, ARIA, Camellia and SM4 throw
  `ERR_CRYPTO_UNKNOWN_CIPHER`, and `getCiphers()` doesn't list them. Neither library has
  most of these; Triple DES in CFB and OFB could be built from the standard library but
  isn't yet, and `x/crypto`'s XTS takes a sector number rather than OpenSSL's 16-byte
  tweak.
- **[perf] AES-GCM keeps the whole message until `final()`.** Output streams from
  `update()` as in Node, but the tag is computed by Go's AEAD over the full plaintext,
  so a GCM context holds it in memory.
- **[gap] `createECDH` and `diffieHellman` support P-256, P-384, P-521 and X25519.**
  Go's standard library has no other curves, so `secp256k1`, Brainpool and the rest
  are `ERR_CRYPTO_INVALID_CURVE`, and X448 and finite-field `dh` KeyObjects can't
  agree keys. `DiffieHellman#verifyError` checks primality, safety, size and the
  generator's range, not every condition OpenSSL's `DH_check` covers.
- **[gap] No unpadded RSA private-key operations.** `privateDecrypt` and
  `privateEncrypt` with `RSA_NO_PADDING` throw `ERR_CRYPTO_UNSUPPORTED_OPERATION`: Go's
  RSA has no raw private-key operation, and doing it with `math/big` wouldn't be
  constant time. The public-key forms work.
- **[approx] Signatures cover RSA (PKCS#1 v1.5 and PSS), ECDSA and Ed25519.** DSA,
  Ed448 and RSA with `RSA_X931_PADDING` aren't supported, and a `Sign` or `Verify`
  keeps the data written to it until `sign()` or `verify()`. Signing RSA-PSS with
  `saltLength: 0` throws `ERR_CRYPTO_UNSUPPORTED_OPERATION`: Go's RSA can't make an
  empty salt, and the private-key step done by hand wouldn't be constant time.
  Verifying such signatures works.
- **[gap] The `sm3` digest is unknown.** `pbkdf2` and `hkdf` take Node's other digests
  (MD5, SHA-1, SHA-2, SHA-3, SHAKE, RIPEMD-160, BLAKE2b-512, BLAKE2s-256 and MD5-SHA1)
  under Node's names and aliases, matched without regard to case (`digests.go`). Neither
  Go's standard library nor `golang.org/x/crypto` has SM3, so `sm3` throws
  `ERR_CRYPTO_INVALID_DIGEST`.
- **[approx] scrypt parameter errors don't carry over an earlier OpenSSL error.** Once
  one scrypt call fails the memory limit, Node adds that message to later
  `Invalid scrypt params` errors too; Orbital gives each error its own reason.
- **[approx] `getHashes()`, `getCurves()` and `getCiphers()` list only what Orbital
  implements** (`lists.go`): the digests in `digests.go`, the P-256/384/521 curves, and the
  ciphers in `cipher.go`. Node lists every name OpenSSL knows.
- **[approx] `X509Certificate` reads certificates with Go's `crypto/x509`.** Fields are
  printed as Node prints them from OpenSSL, but Go refuses some malformed certificates
  OpenSSL reads, and a certificate that fails to parse gives
  `ERR_OSSL_ASN1_WRONG_TAG` whatever OpenSSL's reason would be. `checkIssued` compares
  names, key identifiers and key usage but not the authority key identifier's issuer
  and serial.
- **[approx] No FIPS mode, secure heap or engines.** Orbital uses Go's crypto, so
  `getFips()` is always 0, `secureHeapUsed()` reports an empty heap, and `setEngine`
  finds no engine (`ERR_CRYPTO_ENGINE_UNKNOWN`). `setFips` and the deprecated `fips`
  property are not provided.

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
