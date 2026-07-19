# Core package tests

Regression tests for npm packages that Orbital considers **core** — real-world
modules that must keep working on the Orbital native runtime. If one of these
breaks, we treat it as a runtime regression.

Rather than vendoring copies of tests, this suite is **manifest-driven**: each
core package is described in [`manifest.json`](./manifest.json). When the suite
runs it clones the upstream project from GitHub at a pinned ref, installs and
builds it with the host toolchain, and then runs the project's **own test
suite on Orbital**.

## Running

```bash
make test-core-packages            # all packages in the manifest
make test-core-packages PKG=mjml   # just one package

# extra flags pass through via CORE_PKG_ARGS, e.g. reuse a previous clone/build:
make test-core-packages CORE_PKG_ARGS="--offline --skip-install"
```

The Make target builds the native binary first. You can also invoke the runner
directly:

```bash
python3 scripts/run-core-package-tests.py --binary "$(pwd)/build/orbital"
python3 scripts/run-core-package-tests.py --package mjml --offline --skip-install
```

Cloned repositories live in `tests/core-packages/.cache/<name>/` (git-ignored).

## How it works

For each package the runner (`scripts/run-core-package-tests.py`):

1. Clones `repo` at `ref` into the cache dir (shallow when possible; existing
   checkouts are fetched and reset to `ref`).
2. Runs the `install` commands with the **host toolchain** (real node/npm) to
   fetch dependencies and build the project.
3. Runs the `test` commands with the **Orbital binary** exposed as the
   `$ORBITAL` environment variable, so the project's own suite executes on
   Orbital.

The runner exits non-zero if any package's install or test step fails, so it
works as a CI pass/fail gate.

## Manifest schema

```jsonc
{
  "packages": [
    {
      "name":        "mjml",                 // required, unique
      "description": "…",                     // optional
      "repo":        "https://…/mjml.git",    // required, git URL
      "ref":         "v4.15.3",               // optional (default branch if omitted)
      "subdir":      "packages/foo",          // optional cwd for all commands
      "env":         { "CI": "true" },        // optional extra env vars
      "install":     ["npm install", "…"],    // commands run with the host toolchain
      "test":        ["\"$ORBITAL\" test.js"] // commands run on Orbital ($ORBITAL set)
    }
  ]
}
```

Notes:

- Commands run through `/bin/bash` in the repo root (or `subdir`), so you can
  chain with `&&`, `cd` into a workspace package, etc.
- `$ORBITAL` is the absolute path to the built Orbital binary and is available
  to **all** commands; use it in `test` commands to run a suite on Orbital.
- Install steps use the normal host `node`/`npm` (needed to bootstrap npm
  itself); only the `test` steps are meant to run on Orbital.

## Adding a package

1. Add an entry to `manifest.json` with the repo, a pinned `ref`, the
   `install`/build commands, and a `test` command that runs the project's suite
   via `$ORBITAL`.
2. Run `make test-core-packages PKG=<name>` and iterate on the commands until
   the upstream suite passes on Orbital.

### Shared support files

`tests/core-packages/support/` holds helpers available to manifest commands via
the `$CORE_PKG_DIR` env var (the absolute path to this directory). Currently:

- `support/mocha-run.js` — runs spec files through the **real `mocha`** package
  installed in the project under test, using Mocha's public programmatic API
  (the same API `mocha`'s own bin uses). It is not a shim: describe/it/hooks,
  reporters, timeouts, and async handling are all real Mocha. Supports
  `--require <file>` (load a setup module first, like `mocha --require`) and
  `--invert-grep <re>` (run everything except tests whose title matches, for the
  documented specs a package's suite runs that Orbital can't emulate). Used by
  both the express and yargs own-suite gates. Use it with
  `"$ORBITAL" "$CORE_PKG_DIR/support/mocha-run.js" some.test.js …`.
- `support/mocha-self-run.js` — like `mocha-run.js`, but for testing **mocha
  itself**: it loads Mocha from the repo's own `lib/mocha.js` (a package can't
  resolve itself by name) and applies a setup file (default `test/setup.js`)
  before the specs, so Mocha's unit suite has its expected globals.
- `support/tsc-run.sh` — drives the **real TypeScript compiler** on Orbital: the
  CLI (`lib/tsc.js` — the LKG build shipped in the repo) for `--version`, an
  emit + run of the generated JS, and diagnostic reporting; plus the programmatic
  API via `support/tsc-api.js`. Fixtures live in `support/tsc/`.
- `support/tsc-api.js` — exercises TypeScript's programmatic API
  (`transpileModule` + `createProgram` + the type checker) as build tools use it.
- `support/axios-smoke.js` — exercises **axios** against the **real network** to
  validate Orbital's outbound HTTP/HTTPS client stack (TLS, DNS, redirects,
  JSON): an HTTPS GET to `example.com`, a JSON HTTPS GET to `api.github.com`, a
  plain HTTP GET, a cross-scheme redirect (`http://` → `https://`), and a real
  `404`. Requires outbound network access.
- `support/yargs-build.sh` — compiles **yargs**' TypeScript sources to CommonJS
  with plain `tsc` (its rollup-based `build:cjs` is broken on modern host
  toolchains — see below) and reconstructs the CJS bootstrap the committed
  entries load.
- `support/yargs-suite.sh` — runs **yargs**' OWN mocha suite (the real
  `test/*.cjs` spec files, driven by actual Mocha via `support/mocha-run.js` +
  chai) against that build, loading `test/before.cjs` via `--require` exactly as
  yargs' `npm test` does. Orbital passes **782 of yargs' ~801 tests**; the 18
  skipped specs (`--invert-grep`) are documented in the script and below.
- `support/react-ssr-setup.sh` — installs the **published** `react`/`react-dom`
  bundles (pinned to the checkout's tag) into an isolated `.ssr` dir. React's
  from-source build and Jest suite can't run on Orbital, so we test the exact CJS
  artifacts npm ships rather than the monorepo source.
- `support/react-ssr-smoke.js` — drives **react-dom/server** on Orbital:
  `renderToString` + `renderToStaticMarkup` (with HTML-escaping checks) and
  streaming `renderToPipeableStream` piped into a real `http.createServer`
  response, then fetched back over the HTTP client. Uses `React.createElement`
  (no JSX build) with function components, context, `useId`/`useMemo`, and keyed
  lists.
- `support/jose-setup.sh` — installs the **published** `jose` package (pinned to
  the checkout's tag) into an isolated `.smoke` dir and copies the smoke script in
  (jose is ESM-only, so a bare `import 'jose'` must resolve from the script's own
  directory). jose's own multi-runtime test harness can't run on Orbital, so we
  test the exact ESM artifacts npm ships.
- `support/jose-smoke.mjs` — drives **jose** on Orbital across the full JOSE
  surface (JWS/JWT/JWE/JWK, 45 checks) to exercise Orbital's native Go WebCrypto
  (`pkg/nodejs/webcrypto/subtle.go`): RSA/ECDSA/EdDSA signatures, AES-GCM/CBC
  content encryption, AES-KW/RSA-OAEP/ECDH-ES/PBES2 key management, JWK
  import/export + thumbprints, and tamper rejection.

> Mocha now runs natively on Orbital, so there is no longer a `node:test`-backed
> shim — packages with Mocha suites exercise real Mocha.

### Example: mjml

The `mjml` entry pins the current release (`v5.4.0`) and runs `mjml-core`'s own
unit tests (Mocha BDD + `chai`) on Orbital using the project's **real installed
Mocha** via `support/mocha-run.js` (all 20 tests pass).

Notes / gotchas encountered:

- **lerna:** mjml still declares `lerna@^3.22.1` with a `postinstall: lerna
  bootstrap` step, even at `v5.4.0`. (A migration to lerna v9 was merged and
  then reverted upstream, so lerna 3 remains current.) `lerna bootstrap` fails
  on modern Node, so we install with `--ignore-scripts` and build `mjml-core`
  directly with Babel — this sidesteps lerna entirely and is version-independent.
- **cheerio/undici support:** `skeleton.test.js` exercises mjml's full render
  pipeline, which pulls in `cheerio@1.0.0` and (transitively) `undici`. Loading
  that chain surfaced several real gaps in Orbital's core modules, now fixed:
  - `require('events')` returned a wrapper object instead of the `EventEmitter`
    constructor, so `class X extends require('node:events')` (undici's
    `Dispatcher`) threw "Class extends value is not a constructor". The events
    module now *is* the constructor, matching Node.
  - Added real core implementations of `async_hooks`
    (`AsyncLocalStorage`/`AsyncResource`/`createHook` with context propagation
    across the event loop — see `docs/async-context.md`), `worker_threads`
    (isolate-backed `Worker` + `MessageChannel`/`MessagePort` with JSON message
    passing), the `util/types` subpath, `process.stdout`/`stderr` writable
    streams, and `require('console')` with a full `Console` class.

  All `mjml-core` unit tests, including `skeleton.test.js`, now pass on Orbital.

### Example: mocha

The `mocha` entry pins `v10.8.2` and runs Mocha's **own unit specs**
(`test/unit/*.spec.js`) on Orbital via `support/mocha-self-run.js` (real Mocha
loaded from the repo + its `test/setup.js` bootstrap). 19 of the 23 unit spec
files run green (**321 passing, 1 pending**).

Four specs are currently excluded and tracked as known Orbital gaps:

- `runnable.spec.js`, `timeout.spec.js` — driven by sinon fake timers; they
  wedge the process rather than hitting Mocha's own timeout.
- `mocha.spec.js` — relies on `rewiremock`-based module mocking of `Suite`.
- `throw.spec.js` — exercises `process` `uncaughtException` timing that Orbital
  does not yet reproduce, so those cases time out.

### Example: typescript (tsc)

The `typescript` entry pins `v5.9.3`. The repo ships its **LKG (last-known-good)
build** in `lib/`, so no compile step is needed — `support/tsc-run.sh` runs the
real compiler on Orbital directly from `lib/tsc.js` and checks: `tsc --version`,
compiling a strict TS fixture to JS and **running the emitted JS on Orbital**,
that a type error is reported (`TS2322`, non-zero exit), and the programmatic
API (`transpileModule` + `createProgram` + checker) via `support/tsc-api.js`.

Real Orbital gaps this surfaced (now fixed):

- **`fs.Stats` predicate methods.** `fs.statSync(...)` returned a plain object
  with boolean props but no `isFile()`/`isDirectory()` **methods** (only the
  promises path had them), so `tsc` threw `stat.isFile is not a function`. The fs
  layer now attaches the full Stats method set across the sync/callback/promise
  APIs (`pkg/nodejs/fs/fs_setup.js`), and `readdirSync(..., {
  withFileTypes: true })` returns `Dirent`-like objects with the same predicates.
- **`fs.realpathSync` / `fs.lstatSync`** were missing entirely; added (built on
  the existing primitives) so the compiler host can canonicalize/stat paths.
- **Synchronous fd API.** `tsc` emits output via
  `openSync`/`writeSync`/`closeSync` (not `writeFileSync`). Orbital had none of
  these, so emit failed with `openSync is not a function`. They are now backed by
  a real file-descriptor table in Go (`pkg/nodejs/fs/fs.go`), along with
  `readSync`/`fstatSync`. This also added `Context.Throw` to the V8 binding so
  these calls raise proper JS exceptions.

### Example: axios

The `axios` entry pins `v1.18.1`. `dist/` is not committed, so the install step
builds it from source with rollup (`npm run build`) to produce
`dist/node/axios.cjs`; `support/axios-smoke.js` then drives axios against the
**real network** — the whole reason axios is a core package is to validate
Orbital's outbound HTTP/HTTPS client end-to-end (TLS handshake, DNS, redirects,
streaming, JSON) with a real-world consumer. It performs an HTTPS GET to
`example.com` (200), a JSON HTTPS GET to `api.github.com` (200 + JSON), a plain
HTTP GET (port 80), a cross-scheme redirect (`http://cloudflare.com` → `https`),
and asserts a real `404` rejects with an `AxiosError` carrying the response.
This step requires outbound network access.

Real Orbital gaps this surfaced (now fixed):

- **`https.get`/`https.request` with an options object** (e.g.
  `https.get({ hostname, path })`) built an `http://host:443` URL because the
  scheme was only forced when a `protocol` was already present. The `https`
  module now pins the scheme to `https:` and defaults the port to `443` for the
  options form (`pkg/nodejs/https/https.js`). (axios itself already worked
  because it passes a full URL; this fixes the common raw-`https` call pattern.)
- **`http.createServer` was a non-functional placeholder** (it even referenced an
  undefined `runtime` global and threw). While chasing axios, `http.createServer`
  was replaced with a real **HTTP/1.1 server** built on Orbital's existing `net`
  TCP server (`pkg/nodejs/http/http.go`): request-line/header parsing,
  `Content-Length` and chunked request bodies, a streaming `IncomingMessage`, and
  a `ServerResponse` with `writeHead`/`setHeader`/`write`/`end`, correct
  `Content-Length`, and keep-alive/close handling.

> A local `/etc/hosts` override that points a well-known domain (e.g.
> `example.com`) at `127.0.0.1` will make network assertions against that domain
> fail; the test deliberately uses domains (`api.github.com`, `httpforever.com`,
> `cloudflare.com`) that aren't typically overridden.

### Example: yargs

The `yargs` entry pins `v17.7.2`. yargs is authored in TypeScript and its
published `build/index.cjs` is produced by rollup — but that step
(`@rollup/plugin-typescript` driving a newer host TypeScript) dies with
`TypeError: path.charCodeAt is not a function`, a dev-tooling version drift with
nothing to do with Orbital. Plain `tsc` compiles the same sources to CommonJS
fine, so `support/yargs-build.sh` installs with `--ignore-scripts` (to skip the
broken `prepare`), compiles with a CommonJS tsconfig that includes the two files
the repo reserves for rollup (`lib/cjs.ts`, `lib/platform-shims/cjs.ts`),
reconstructs the tiny `build/index.cjs` bootstrap, and mirrors the locale JSON
where the compiled i18n shim looks for it.

`support/yargs-suite.sh` then runs yargs' **own** mocha suite against that build —
the real `test/*.cjs` spec files (the same ones `npm test` runs:
`c8 mocha ./test/*.cjs --require ./test/before.cjs --check-leaks`), driven through
actual Mocha via `support/mocha-run.js` + chai, loading `test/before.cjs` via
`--require` just as yargs does (`c8`/coverage and `--check-leaks` are dropped —
they are tooling wrappers, not assertions). Orbital passes **782 of yargs' ~801
tests** (1 pending).

Real Orbital gaps this surfaced (now fixed):

- **`fs.readFileSync` swallowed read failures.** yargs resolves the POSIX `C`
  locale, and `y18n` does `JSON.parse(fs.readFileSync(file))` inside a `try`,
  treating a thrown `ENOENT` as "no locale file". Orbital's `readFileSync`
  returned `undefined` on any failure instead of throwing, so a missing file
  became `JSON.parse(undefined)` → `SyntaxError` and crashed. `readFileSync` now
  raises a proper Node-style error (`code: 'ENOENT'`, `errno`, `syscall`, `path`)
  when a read fails, matching Node and the catchable behavior libraries expect
  (`pkg/nodejs/fs/fs_setup.js`).
- **`util.format()` with no arguments returned `'undefined'`** instead of `''`.
  yargs' logger emits a blank separator line as `logger.error()` (zero args), and
  the suite captures it via `util.format(...msg)`; the stray `'undefined'` line
  broke ~37 `usage`/`validation` assertions. `format()`/`formatWithOptions()` now
  return `''` when called with no format arguments, matching Node
  (`pkg/nodejs/util/util.js`).
- **`require.cache` was a `Map`, not a plain object.** Node exposes it (and
  `Module._cache`) as a null-prototype object keyed by resolved filename, so
  `delete require.cache[require.resolve(id)]` and `id in require.cache` work —
  yargs' `clearRequireCache` and countless hot-reload/test patterns rely on this.
  Against a Map both are silent no-ops (the real cache was never busted). The
  cache is now a real object and cache-busting forces re-execution
  (`pkg/nodejs/module/module.js`).
- **`path.normalize` stripped trailing slashes.** `normalize('/tmp/')` returned
  `'/tmp'`; Node preserves the separator (`'/tmp/'`). Now fixed
  (`pkg/nodejs/path/path.go`), which keeps directory paths directory-shaped
  for yargs' `normalize` option, static file servers, etc.
- **Missing modules didn't produce a Node-shaped error.** `require.resolve` for an
  unresolvable request returned `null` (Node throws), and `require` of a missing
  module threw `Cannot find module: x` with no `.code`. Libraries branch on
  `err.code === 'MODULE_NOT_FOUND'` to treat an optional dependency as absent
  (yargs' config `extends` does exactly this). `require.resolve` now throws and
  both paths raise `Cannot find module 'x'` with `code: 'MODULE_NOT_FOUND'`
  (`pkg/nodejs/module/module.js`).

The 18 skipped specs (see `support/yargs-suite.sh` for the exact list) depend on
capabilities Orbital does not emulate — none are yargs bugs:

- **`commandDir` (7):** yargs resolves the command directory relative to the
  caller's file via `get-caller-file`, which reads a fixed stack-frame position;
  under Mocha's runner on Orbital that frame differs, so the fixtures don't load.
- **`$0` executable name (5):** these expect `$0` to be `node` because Node's real
  test run is `node …/mocha`; on Orbital `argv[0]` is the `orbital` binary.
- **host environment (3):** OS-locale detection and the Node.js-version guard.
- **non-module command object (1):** the thrown message embeds `util.inspect` of
  the object across multiple lines; Orbital's `util.inspect` does not yet wrap by
  `breakLength`, so it stays single-line.
- **cached-help timing (2):** async `process.exit`/`emit` ordering inside yargs'
  own process-global-mutation test harness.

### Example: express

The `express` entry pins `v5.2.1` and runs Express's **own** mocha test suite —
the real `test/*.js` spec files (the same ones `npm test` runs), driven through
actual Mocha via `support/mocha-run.js` and `supertest`. There are no
hand-written test drivers: booting the real framework and hammering it with real
HTTP requests is a broad integration test of Orbital's HTTP server
(`http.createServer` on the `net` TCP server) and client, the event loop,
streams, and the module system.

`support/express-suite.sh` runs the spec files that pass 100% on Orbital and
documents (in its header) the files that are excluded. Orbital currently passes
**1,060 of Express's ~1,127 tests** (the gate runs the 61 files that are 100%
green — 782 tests). The 8 excluded files are mostly a test-harness artifact:
`send`'s dotfile protection 404s because the checkout lives under `.cache`
(`res.sendFile`, `res.download`, most of `express.static`). The rest are a few
genuine edges — Go's URL parser rejecting malformed percent-escapes
(`app.router`, some `express.static`), the IPv4-mapped IPv6 address form
(`req.ip`), and one-offs in `app.listen`/`app.options`/`app.use`.

Real Orbital gaps this surfaced (now fixed):

- **V8 lacked Unicode property escapes (`\p{...}`) and `Intl`.** Express 5's
  router (`path-to-regexp@8`) has `/^[$_\p{ID_Start}]$/u` at module top level,
  which is a `SyntaxError` on a non-ICU V8 and cannot be shimmed in JS. V8 is now
  built with `v8_enable_i18n_support=true` **and** `icu_use_data_file=false` (ICU
  data embedded in the monolith, so no external `icudtl.dat` and the glue's
  `InitializeICUDefaultLocation("")` works). See `scripts/build-v8.sh` and the
  `Makefile` `v8-build` target.
- **Async I/O never woke a sleeping event loop.** When a timer was pending, the
  loop waited out the *entire* timer duration even after an I/O completion
  (accept/read/connect) signaled it — so inbound HTTP, `net`, and the HTTP client
  callbacks stalled until the next timer fired. The wait now returns immediately
  on an early wakeup (`pkg/runtime/eventloop.go`). This was the single biggest
  blocker for any server workload.
- **`server.address()` was null right after `listen()`.** `net.Server.listen`
  bound asynchronously, but Node (and `supertest`) read `server.address().port`
  synchronously after `listen(0)`. Binding is now synchronous (only the
  `listening` event is deferred), and `address().family` reports `IPv4`/`IPv6`
  (`pkg/nodejs/net/net.{go,js}`).
- **`require('stream')` wasn't a callable constructor.** Node's stream module
  *is* the legacy `Stream` constructor; packages do `util.inherits(X, Stream)`
  (`send`) and `Stream.call(this)` (`superagent`). The export is now a callable
  ES5 constructor with the stream classes attached (`pkg/nodejs/stream/stream.js`).
- **`require('process')` didn't resolve.** Node exposes `process` as a
  requireable builtin (superagent's `http2wrapper` uses it); added to the module
  registry (`pkg/nodejs/module/module.js`).
- **The HTTP client rejected non-string header values and never auto-flowed
  responses.** superagent sets `Content-Length` as a number (the native client
  unmarshals headers into `map[string]string`) and reads responses via
  `res.on('data')` without `resume()` (Node auto-flows on a `data` listener).
  Header values are now coerced to strings and the response `IncomingMessage`
  auto-flows when a `data` listener is attached (`pkg/nodejs/http/http.go`).
- **Sockets didn't expose `readable`/`writable`.** `on-finished` (used by
  body-parser via raw-body) treats a request as already finished when
  `!req.socket.readable`, so `express.json`/`urlencoded`/etc. skipped reading the
  body and left `req.body` undefined. Public getters were added
  (`pkg/nodejs/net/net.js`).
- **Streaming file I/O and compression were missing.** `send` (behind
  `res.sendFile`/`express.static`) needs async `fs.stat`/`fs.access` and
  `fs.createReadStream` with byte-range support, and body-parser's gzip/deflate
  paths need streaming zlib. Both are now native: async fd stat/read/close plus a
  chunk reader in Go (`pkg/nodejs/fs/fs.go`) with a thin `Readable` glue, and
  streaming gunzip/inflate/unzip over `io.Pipe` (`pkg/nodejs/zlib/zlib.go`)
  exposed as `stream.Transform`s. `fs.Stats` now also carries `Date` timestamps
  and the numeric fields (`ino`, `dev`, …) that `etag` requires.
- **Byte integrity across the JS/Go boundary.** Request/response bodies, crypto
  digests, and hashes went through V8 strings, which mangled non-ASCII bytes
  (double-UTF-8-encoding). A consistent latin1 wire-string convention now carries
  raw bytes across the boundary for the `http`/`net`/`crypto` layers, so multibyte
  bodies and `etag` hashes match Node byte-for-byte.
- **`req.host`/`hostname`/`subdomains` saw the loopback address.** Go's `net/http`
  ignores a `Host` entry in the header map (it uses `req.Host`), so a client's
  custom `Host` header was dropped and the server saw `127.0.0.1:port`. The client
  now copies a `Host` header onto `httpReq.Host` (`pkg/runtime/client_net.go`).
- **Multi-value response headers and 204/304 bodies.** The client now collapses
  duplicate header lines the way Node's parser does (set-cookie stays an array,
  cookie joins with `"; "`, most others with `", "`), and the server no longer
  emits a `Content-Length` for 1xx/204/304 responses
  (`pkg/nodejs/http/http.go`).
- **`url.parse` mishandled backslashes and invalid host chars.** For special
  schemes the WHATWG rules treat `\` as `/`, a single leading slash is never a
  host, and a hostname is truncated at the first invalid label — all required so
  `res.location` can't be tricked into a redirect that bypasses allow-lists
  (`pkg/nodejs/url/url.js`).

The runner (`support/mocha-run.js`) also force-exits after the run (like
`mocha --exit`) so suites that leave listening sockets open don't hang the
process.

### Example: react (SSR)

The `react` entry pins `facebook/react` at `v19.2.7`. Unlike Express/Mocha, React
does **not** run its own suite here: it's Jest-based and requires a from-source
build (flow + rollup + a bespoke bundler) that can't run on Orbital. So — like
axios — this installs the **published** `react`/`react-dom` bundles (the
exact prebuilt CJS artifacts npm ships to production, pinned to the same version
as the checkout's tag) into an isolated `.ssr` dir (`support/react-ssr-setup.sh`)
and drives them on Orbital with `support/react-ssr-smoke.js`.

The smoke test builds a component tree with `React.createElement` (no JSX build
step) — function components, `createContext`/`useContext`, `useId`, `useMemo`, and
keyed lists — and exercises all three server APIs: `renderToString`,
`renderToStaticMarkup` (asserting dangerous markup is HTML-escaped), and streaming
`renderToPipeableStream` piped into a real `http.createServer` response that is
then fetched back over the HTTP client. This is a broad exercise of Orbital's
module system (React switches production/development bundles on
`process.env.NODE_ENV` and pulls `./cjs/*` via CommonJS), the JS engine globals
React leans on, and — for streaming — the Node stream + HTTP stack.

Real Orbital gaps this surfaced (now fixed):

- **`queueMicrotask` was not a global.** React's SSR renderer defers work with the
  WHATWG `queueMicrotask(cb)` global, which Orbital didn't expose. It's now backed
  by the event loop's microtask queue (`pkg/nodejs/timers/timers.go`).
- **`TextEncoder.prototype.encodeInto` was missing.** The streaming renderer
  UTF-8-encodes chunks straight into a preallocated `Uint8Array` via `encodeInto`,
  which Orbital's `TextEncoder` polyfill lacked (it only had `encode`). A
  spec-compliant `encodeInto` (whole-code-point writes, `{ read, written }`
  result) was added (`pkg/nodejs/buffer/buffer.js`).

### Example: jose (encryption / signing)

The `jose` entry pins `panva/jose` at `v6.2.3`. jose v6 is **ESM-only** and
implemented **entirely on top of the Web Crypto API** (`globalThis.crypto.subtle`),
which made it the package that finally forced Orbital's WebCrypto to become real.
Like axios/react it doesn't run its own suite (that's a bespoke multi-runtime
harness that can't run on Orbital); instead `support/jose-setup.sh` installs the
published package into an isolated `.smoke` dir and `support/jose-smoke.mjs` (an
ESM script, run from that dir so the bare `import 'jose'` resolves) drives **45
checks** on Orbital:

- **JWS** — HMAC (HS256/384/512), RSA (RS256/512), RSA-PSS (PS256/512), ECDSA
  (ES256/384/512) and EdDSA (Ed25519), in compact, flattened and general
  serializations.
- **JWT** — signed claims with issuer/audience/expiry validation, plus rejection
  of expired tokens and wrong issuers.
- **JWE** — every content-encryption algorithm (A128/192/256GCM and
  A128/192/256CBC-HS) combined with every key-management mode: `dir`,
  AES-KW (A128/192/256KW), A256GCMKW, RSA-OAEP/-256, ECDH-ES and ECDH-ES+A256KW
  over both P-256 and X25519, and password-based PBES2-HS256/512. Includes
  flattened/general serializations and `EncryptJWT`/`jwtDecrypt`.
- **JWK** — import/export round-trips for RSA/EC/OKP/oct keys and RFC 7638
  thumbprints.
- **Tamper rejection** — flipped JWS signatures, wrong verification keys, and
  mutated JWE ciphertext all fail closed.

Every token produced by the smoke was additionally verified **byte-for-byte
interoperable with Node.js jose in both directions** during development.

Before this, Orbital's `crypto.subtle` was a JS stub that only implemented
`digest` and HMAC. It is now a **native Go-backed `SubtleCrypto`**
(`pkg/nodejs/webcrypto/subtle.go`): key generation, sign/verify,
encrypt/decrypt and `deriveBits` are implemented with Go's standard library —
`crypto/rsa` (PKCS1v15, PSS, OAEP), `crypto/ecdsa` (raw r‖s signatures),
`crypto/ecdh` (P-256/384/521 + X25519 ECDH), `crypto/ed25519`, `crypto/aes` with
GCM/CBC, a RFC 3394 AES-KW implementation, and `crypto/pbkdf2`. Keys cross the
Go/JS boundary as JWK JSON and payloads as base64; `webcrypto.js` is now just the
WHATWG-shaped surface (CryptoKey/JWK plumbing, wrap/unwrap) that delegates to it.

Real Orbital gaps this surfaced (now fixed):

- **`structuredClone` was not a global.** jose deep-clones JWT claim sets with the
  WHATWG `structuredClone` global (Node ≥ 17), which Orbital didn't expose. A
  structured-clone implementation (dates, regexps, Map/Set, typed arrays, cyclic
  refs) was added (`pkg/nodejs/buffer/buffer.js`).
- **A binary-unsafe hash path.** Hash inputs crossed the crypto module's boundary
  as NUL-terminated C strings, so any data containing a zero byte was **truncated
  at the first NUL** — invisible for ASCII (etags) but corrupting for binary. It
  broke ECDH-ES cross-runtime (its Concat KDF hashes the raw shared secret, which
  routinely contains zero bytes). `crypto.subtle.digest` now runs through native Go
  over a base64 boundary, so binary digests are exact.

### A note on the "silent exit" under GC (root cause + fix)

Getting Mocha to load surfaced a bug that looked mysterious but had a mundane
cause. Under memory pressure (reproducible with `GOGC=1`), a program that did
`require('mocha')` would stop producing output partway through and exit `0` — the
classic "silent exit". Extensive V8/cgo investigation (GC-during-reentrant-
execution, `Script::Run` returning empty, `GOEXPERIMENT=cgocheck2`, etc.) was a
red herring: **execution never actually stopped**. A filesystem marker written
*after* the failing point proved the script ran to completion; only the *stdout
output* was being lost.

The real cause was in the `tty` module. `tty.isatty(fd)` wrapped the descriptor
with `os.NewFile(uintptr(fd), "")` to `Stat` it. `os.NewFile` attaches a
finalizer that **closes the descriptor when the returned `*os.File` is garbage
collected**. Since `isatty` only borrows the process's stdio fds (it does not own
them), the next GC after Mocha probed `isatty(1)` ran that finalizer and closed
fd 1. All subsequent `process.stdout` writes then failed with `EBADF` and were
silently dropped (`write()` errors are ignored). It only reproduced under
aggressive GC because that's what makes the finalizer run promptly.

The fix (`pkg/nodejs/tty/tty.go`) inspects the descriptor with a raw
`syscall.Fstat` instead — no `*os.File`, no finalizer, no ownership — so borrowed
fds are never closed. With that in place Go's GC runs completely normally; the
runtime does **not** disable, throttle, or otherwise manipulate the collector.

Two hardening changes came out of the same investigation and remain in place:

- `pkg/v8` now returns `ErrExecutionTerminated` when a V8 execution yields an
  empty result with no pending exception, instead of a silent `(nil, nil)` — so a
  genuinely terminated/interrupted script is observable rather than a silent early
  return.
- The main goroutine is pinned to the main OS thread (`runtime.LockOSThread`) so
  the primary V8 isolate is always entered from a single, stable native stack.
