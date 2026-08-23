# Async context (`async_hooks` / `AsyncLocalStorage`)

Orbital implements `async_hooks` as a real core module (not a user-space
polyfill). `AsyncLocalStorage`, `AsyncResource`, and `createHook` live in the
runtime's embedded core and context is propagated by the runtime itself.

## What propagates today

Context set with `AsyncLocalStorage.run()` / `.enterWith()` flows across the
async boundaries the Go event loop owns, because the runtime captures the active
context when a callback is scheduled and restores it when the callback runs:

- `setTimeout` / `setInterval`
- `setImmediate`
- `queueMicrotask`
- `process.nextTick`
- `AsyncResource.runInAsyncScope()` / `AsyncResource.bind()`

This covers the way libraries actually consume async context (they bind
callbacks to an `AsyncResource`, e.g. undici's request handlers).

## Known gap: native `await` / `Promise.prototype.then` continuations

Context does **not** yet propagate automatically across a bare `await` or a
`.then()` continuation, e.g.:

```js
const als = new AsyncLocalStorage();
als.run({ id: 1 }, async () => {
  await something();
  als.getStore(); // may be undefined after the await
});
```

### Why

Promise continuations run inside V8's own microtask queue, which the Go event
loop never sees. The only correct way to hook them is V8's
`Isolate::SetPromiseHook` (or `ContinuationPreservedEmbedderData`). We
deliberately do **not** monkey-patch `Promise.prototype.then` to fake this — that
is a polyfill, it is incorrect for `await`, and it degrades performance for all
promises.

### The fix (tracked)

Expose `v8::Isolate::SetPromiseHook` through the cgo glue
(`pkg/v8/csrc/v8go.cc` + a Go wrapper) and drive `init/before/after/resolve`
from it, exactly like Node's embedder does. The V8 API is already present in the
pinned headers (`v8-isolate.h`, `v8-promise.h`).

The reason this is not done in the same change: the glue
(`libv8go_glue.a`) must be compiled with V8's **hardened Chromium libc++**
(`std::__Cr::` inline namespace, pointer-compression + sandbox layout) so its
symbols match `libv8_monolith.a`. That requires V8's own compile flags
(`scripts/build-glue.py` reads them from a `gn`-generated
`compile_commands.json`) and a matching clang. Only `libv8go_glue.a` needs
rebuilding — run `make v8-glue` (see CONTRIBUTING.md); `libv8_monolith.a` is
untouched. Headers come from `deps/v8/include` or the CI `v8-headers` artifact,
not a full V8 source checkout.

Until that glue rebuild ships, use `AsyncResource`/`bind` (or the event-loop
primitives above) to carry context across async work.
