// Package nodejs provides a Node.js-compatible JavaScript runtime for embedders.
//
// Use New to create a runtime with the full CLI-equivalent module set, or import
// individual subpackages (console, fs, process, …) and register only what you need
// via runtime.RegisterModule.
package nodejs

import (
	"fmt"

	"proto.zip/studio/orbital/pkg/nodejs/abort"
	"proto.zip/studio/orbital/pkg/nodejs/assert"
	"proto.zip/studio/orbital/pkg/nodejs/async_hooks"
	"proto.zip/studio/orbital/pkg/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/nodejs/child_process"
	"proto.zip/studio/orbital/pkg/nodejs/console"
	"proto.zip/studio/orbital/pkg/nodejs/crypto"
	"proto.zip/studio/orbital/pkg/nodejs/dgram"
	"proto.zip/studio/orbital/pkg/nodejs/diagnostics_channel"
	"proto.zip/studio/orbital/pkg/nodejs/dns"
	"proto.zip/studio/orbital/pkg/nodejs/domain"
	"proto.zip/studio/orbital/pkg/nodejs/esm"
	"proto.zip/studio/orbital/pkg/nodejs/events"
	"proto.zip/studio/orbital/pkg/nodejs/fetch"
	"proto.zip/studio/orbital/pkg/nodejs/fs"
	"proto.zip/studio/orbital/pkg/nodejs/http"
	"proto.zip/studio/orbital/pkg/nodejs/http2"
	"proto.zip/studio/orbital/pkg/nodejs/https"
	"proto.zip/studio/orbital/pkg/nodejs/module"
	"proto.zip/studio/orbital/pkg/nodejs/net"
	orbitalos "proto.zip/studio/orbital/pkg/nodejs/os"
	"proto.zip/studio/orbital/pkg/nodejs/path"
	"proto.zip/studio/orbital/pkg/nodejs/perf_hooks"
	"proto.zip/studio/orbital/pkg/nodejs/process"
	"proto.zip/studio/orbital/pkg/nodejs/punycode"
	"proto.zip/studio/orbital/pkg/nodejs/querystring"
	"proto.zip/studio/orbital/pkg/nodejs/readline"
	"proto.zip/studio/orbital/pkg/nodejs/repl"
	"proto.zip/studio/orbital/pkg/nodejs/stream"
	"proto.zip/studio/orbital/pkg/nodejs/string_decoder"
	"proto.zip/studio/orbital/pkg/nodejs/sys"
	"proto.zip/studio/orbital/pkg/nodejs/test"
	"proto.zip/studio/orbital/pkg/nodejs/timers"
	"proto.zip/studio/orbital/pkg/nodejs/tls"
	"proto.zip/studio/orbital/pkg/nodejs/tty"
	"proto.zip/studio/orbital/pkg/nodejs/url"
	"proto.zip/studio/orbital/pkg/nodejs/util"
	"proto.zip/studio/orbital/pkg/nodejs/webcrypto"
	"proto.zip/studio/orbital/pkg/nodejs/webstream"
	"proto.zip/studio/orbital/pkg/nodejs/worker_threads"
	"proto.zip/studio/orbital/pkg/nodejs/zlib"
	"proto.zip/studio/orbital/pkg/runtime"
)

// Instance is a Node.js-compatible runtime together with its ESM loader.
type Instance struct {
	Runtime *runtime.Runtime
	ESM     *esm.ESM
}

// New creates a runtime and registers the full Node.js compatibility module set
// (the same set used by the orbital CLI). cfg may be nil for runtime.DefaultConfig().
//
// New also configures worker_threads.RuntimeProvider and esm.LoaderRuntimeProvider
// so Worker and module.register hooks receive equally complete runtimes.
func New(cfg *runtime.Config) (*Instance, error) {
	if cfg == nil {
		cfg = runtime.DefaultConfig()
	}
	// Capture a shallow copy so later mutations of the caller's cfg do not affect
	// worker / loader-realm clones.
	cfgCopy := *cfg
	installProviders(&cfgCopy)

	return newInstance(&cfgCopy)
}

// Register attaches the default Node.js modules to an existing runtime.
// Prefer New unless you need a custom runtime construction path.
//
// Register does not install worker / loader providers; call New for that, or set
// worker_threads.RuntimeProvider and esm.LoaderRuntimeProvider yourself.
func Register(rt *runtime.Runtime) (*esm.ESM, error) {
	if rt == nil {
		return nil, fmt.Errorf("nodejs: runtime is nil")
	}
	loader := esm.New()
	if err := registerModules(rt, loader); err != nil {
		return nil, err
	}
	return loader, nil
}

// Modules returns the default Node.js module list in registration order.
// The returned slice includes a fresh ESM loader instance; CommonJS (module) is last.
// Callers who register a custom subset should preserve relative ordering for
// interdependent modules (e.g. webcrypto after crypto, module last).
func Modules() []runtime.Module {
	return defaultModules(esm.New())
}

func installProviders(cfg *runtime.Config) {
	worker_threads.RuntimeProvider = func() (*runtime.Runtime, error) {
		inst, err := newInstance(cfg)
		if err != nil {
			return nil, err
		}
		return inst.Runtime, nil
	}
	esm.LoaderRuntimeProvider = func() (*runtime.Runtime, error) {
		inst, err := newInstance(cfg)
		if err != nil {
			return nil, err
		}
		return inst.Runtime, nil
	}
}

func newInstance(cfg *runtime.Config) (*Instance, error) {
	rt, err := runtime.New(cfg)
	if err != nil {
		return nil, err
	}

	loader := esm.New()
	if err := registerModules(rt, loader); err != nil {
		rt.Dispose()
		return nil, err
	}

	return &Instance{Runtime: rt, ESM: loader}, nil
}

func registerModules(rt *runtime.Runtime, loader *esm.ESM) error {
	for _, mod := range defaultModules(loader) {
		if err := rt.RegisterModule(mod); err != nil {
			return fmt.Errorf("failed to register %s module: %w", mod.Name(), err)
		}
	}
	return nil
}

func defaultModules(loader *esm.ESM) []runtime.Module {
	// Order matters — see comments. CommonJS module system must be last.
	return []runtime.Module{
		abort.New(), // AbortController/AbortSignal globals - early for other modules
		console.New(),
		timers.New(),
		events.New(),
		process.New(),
		fs.New(),
		path.New(),
		buffer.New(),
		stream.New(),
		webstream.New(), // Web Streams API
		url.New(),
		orbitalos.New(),
		util.New(),
		crypto.New(),
		webcrypto.New(), // Web Crypto API (must come after crypto)
		net.New(),       // TCP/IPC sockets
		dgram.New(),     // UDP sockets
		tls.New(),       // TLS/SSL (must come after net)
		http.New(),
		https.New(), // Must come after http
		http2.New(), // HTTP/2 (must come after tls)
		string_decoder.New(),
		querystring.New(),
		assert.New(),
		zlib.New(),
		tty.New(),            // Terminal helpers (must come after process)
		async_hooks.New(),    // AsyncLocalStorage / AsyncResource (must come after timers/process)
		worker_threads.New(), // Isolate-backed workers (must come after events)
		dns.New(),
		readline.New(),
		fetch.New(),               // Web Fetch API
		perf_hooks.New(),          // Performance hooks
		punycode.New(),            // Punycode (deprecated)
		sys.New(),                 // Sys (deprecated, must come after util)
		diagnostics_channel.New(), // Diagnostics channel
		domain.New(),              // Domain (deprecated, must come after events)
		repl.New(),                // REPL module (must come after events and util)
		test.New(),                // Test runner (must come after events)
		child_process.New(),       // Child process spawning
		loader,                    // ES Module system
		module.New(),              // CommonJS module system - must be last
	}
}
