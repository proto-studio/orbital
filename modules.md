# Node.js Core (Built-in) Modules — Checklist

Tracks every module in the current Node.js distribution (v26 `module.builtinModules`).
`[x]` = implemented and resolvable, `[ ]` = not yet implemented.

> **Note:** `[x]` means the module is present and resolvable — it does **not**
> guarantee every API within it is complete. Intra-module gaps, approximations,
> and deliberate shortcuts are catalogued in
> [`docs/known-limitations.md`](docs/known-limitations.md).

## System & Process
- [x] process
- [x] os
- [x] path
- [ ] path/posix — POSIX-specific `path` specifier
- [ ] path/win32 — Windows-specific `path` specifier
- [x] fs
- [x] fs/promises
- [x] child_process
- [x] worker_threads — Isolate-backed workers; JSON message passing over Go channels
- [x] async_hooks — AsyncLocalStorage/AsyncResource/createHook; event-loop context propagation (await-boundary needs SetPromiseHook glue, see docs/async-context.md)
- [ ] cluster — Requires multi-process architecture with IPC (significant complexity)
- [x] perf_hooks
- [x] diagnostics_channel
- [ ] v8 — Requires V8-specific APIs (partial support possible)
- [ ] vm — Requires V8 script compilation/context APIs
- [ ] wasi — WebAssembly System Interface
- [ ] inspector — V8 inspector protocol
- [ ] inspector/promises — Promise-based inspector API

## Networking & IPC
- [x] http — client plus a real HTTP/1.1 server (createServer) built on the net TCP server
- [x] https
- [x] http2
- [x] net
- [x] tls
- [x] dgram
- [x] dns
- [x] dns/promises

## Module System & Loaders
- [x] module
- [x] url

## Crypto & Compression
- [x] crypto — hash/hmac/random + KeyObject (generate/import/export). No node:crypto one-shot sign/verify/cipher for KeyObjects yet; see known-limitations.md
- [x] crypto/webcrypto — SubtleCrypto; deriveBits is PBKDF2/ECDH/X25519 only, ML-DSA rejected; see known-limitations.md
- [x] zlib

## Streams & Buffers
- [x] stream
- [x] stream/promises
- [x] stream/web
- [ ] stream/consumers — Consumer helpers (arrayBuffer/blob/buffer/json/text)
- [x] buffer
- [x] string_decoder

## Timers & Scheduling
- [x] timers
- [x] timers/promises

## Testing & Diagnostics
- [x] assert
- [x] assert/strict
- [x] node:test
- [ ] node:test/reporters — Optional test reporters (can be added later)
- [ ] trace_events — Requires V8 tracing APIs

## Utilities
- [x] util
- [x] util/types
- [x] events
- [x] querystring
- [x] readline
- [x] readline/promises
- [x] repl
- [x] console
- [x] tty — isatty + TTY read/write streams

## Web / WHATWG APIs (Node-backed)
- [x] fetch
- [x] Headers
- [x] Request
- [x] Response
- [x] FormData
- [x] URL
- [x] URLSearchParams
- [x] TextEncoder
- [x] TextDecoder
- [x] AbortController

## Deprecated (still present)
- [x] domain
- [x] sys
- [x] punycode
- [ ] constants — Legacy aggregate constants module (superseded by per-module constants)

## Experimental / prefix-only (node: required)
- [ ] node:sqlite — Built-in SQLite database (experimental)
- [ ] node:sea — Single Executable Applications
- [ ] node:ffi — Foreign Function Interface (experimental)
