// Package esm implements ES Module loading.
//
// V8 resolves imports through a synchronous callback (resolveModule). Rather
// than reimplement Node's resolution algorithm in Go, that callback delegates to
// a JS loader (esm-loader.js) which runs in the fully-initialized runtime and
// returns { url, source } for each requested specifier. See esm-loader.js.
package esm

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed esm-loader.js
var esmLoaderJS string

// ESM provides ES Module functionality.
type ESM struct {
	rt          *runtime.Runtime
	loaderReady bool
	builtins    map[string]bool
	loader      *loaderRealm
	provider    func() (*runtime.Runtime, error)
}

// New creates an ESM module runtime without a loader realm provider, so
// module.register is unavailable.
func New() *ESM {
	return &ESM{}
}

// NewWithLoaderProvider creates an ESM module runtime whose isolated loader realm
// (module.register hooks) is built by provider. provider must return a runtime with
// the esm module registered, configured as this module's owner intends.
func NewWithLoaderProvider(provider func() (*runtime.Runtime, error)) *ESM {
	return &ESM{provider: provider}
}

// Name returns the module name.
func (e *ESM) Name() string {
	return "esm"
}

// Register sets up the ES Module runtime. The loader is installed right away so
// import() works from code that never loads an ES module itself, such as a
// CommonJS entry: until the dynamic-import callback is set, V8 rejects import()
// with "Not supported".
func (e *ESM) Register(rt *runtime.Runtime) error {
	e.rt = rt
	return e.ensureLoader()
}

// ensureLoader installs the JS helpers, the dynamic-import callback and the
// module.register bridge, once. None of them needs the CommonJS module system
// (registered after ESM) until a module is actually resolved.
func (e *ESM) ensureLoader() error {
	if e.loaderReady {
		return nil
	}
	ctx := e.rt.Context()
	global, err := ctx.Global()
	if err != nil {
		return err
	}
	if existing, _ := global.Get("__esmFinishDynamicImport"); existing != nil && existing.IsFunction() {
		e.loaderReady = true
		return nil
	}
	if _, err := e.rt.RunScript(esmLoaderJS, "esm-loader.js"); err != nil {
		return err
	}
	// Wire dynamic import() through the same JS resolver as static imports.
	ctx.SetDynamicImportResolver(func(specifier, referrer string) (string, string, error) {
		return e.resolveModule(specifier, referrer)
	})
	if err := e.installRegisterBridge(); err != nil {
		return err
	}
	e.loaderReady = true
	return nil
}

// installRegisterBridge exposes __esmRegister in the application realm so
// node:module's register() can register loader hooks (which run in the isolated
// loader realm; see hooks.go).
func (e *ESM) installRegisterBridge() error {
	ctx := e.rt.Context()
	global, err := ctx.Global()
	if err != nil {
		return err
	}
	tmpl, err := e.rt.Isolate().NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		c := info.Context()
		specifier := argString(info, 0)
		parentURL := argString(info, 1)
		if specifier == "" {
			return c.Throw("register(): specifier is required")
		}
		if err := e.registerHook(specifier, parentURL); err != nil {
			return c.Throw(err.Error())
		}
		return c.Undefined()
	})
	if err != nil {
		return err
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		return err
	}
	return global.Set("__esmRegister", fn)
}

// RunModule compiles and runs an ES module from source.
func (e *ESM) RunModule(source, filename string) (*v8.Value, error) {
	if e.rt == nil {
		return nil, errors.New("esm: runtime not initialized")
	}
	if err := e.ensureLoader(); err != nil {
		return nil, err
	}
	if ended := e.rt.Ended(); ended != nil {
		return nil, ended
	}

	ctx := e.rt.Context()

	mod, err := ctx.CompileModule(source, filename)
	if err != nil {
		return nil, err
	}

	if err := mod.Instantiate(e.resolveModule); err != nil {
		return nil, e.endedOr(err)
	}

	result, err := mod.Evaluate()
	if err != nil {
		return nil, e.endedOr(err)
	}

	// A module always evaluates to a promise (top-level-await semantics): it
	// fulfills once the module graph finishes evaluating and REJECTS if the
	// module (or anything it imports) threw. V8 hands that promise back here with
	// no Go-level error, and the module's open handles can keep the loop below
	// running indefinitely, so the rejection has to be observed from inside it.
	isPromise := result != nil && result.IsPromise()
	if isPromise {
		e.rt.WatchModuleEvaluation(result)
	}

	// Run any pending async operations (timers, microtasks, top-level await).
	e.rt.EventLoop().Run()
	if ended := e.rt.Ended(); ended != nil {
		return nil, ended
	}

	if isPromise {
		ctx.PerformMicrotaskCheckpoint()
		if result.PromiseState() == v8.PromisePending {
			return nil, errors.New("Top-level await promise never resolved before the event loop drained")
		}
	}

	return result, nil
}

// endedOr returns what ended the runtime in place of err, a failure from loading or
// running module code. process.exit() terminates execution, so any step that runs
// JavaScript fails when the program exits: Instantiate when an imported CommonJS
// module is required to build its wrapper, Evaluate for module code itself. The
// caller must see the exit (or uncaught error), not the module error V8 reports.
func (e *ESM) endedOr(err error) error {
	if ended := e.rt.Ended(); ended != nil {
		return ended
	}
	return err
}

// rejectionError converts a rejected module/promise reason (usually an Error
// object) into a Go error, preferring the JS stack trace so the CLI prints the
// same detail Node does for an uncaught module error.
func rejectionError(reason *v8.Value) error {
	if reason == nil {
		return &v8.JSError{Message: "module evaluation rejected"}
	}
	if reason.IsObject() {
		if st, _ := reason.Get("stack"); st != nil && !st.IsUndefined() {
			if s := st.String(); s != "" {
				return &v8.JSError{Message: s}
			}
		}
	}
	return &v8.JSError{Message: reason.String()}
}

// RunModuleFile loads and runs an ES module from a file. The entry is put
// through the same loader as any import so TypeScript stripping and CJS/ESM
// classification apply uniformly.
func (e *ESM) RunModuleFile(filename string) (*v8.Value, error) {
	absPath := filename
	if !filepath.IsAbs(filename) {
		cwd, _ := os.Getwd()
		absPath = filepath.Join(cwd, filename)
	}

	if err := e.ensureLoader(); err != nil {
		return nil, err
	}

	source, url, err := e.resolveAndLoad(absPath, "")
	if err != nil {
		return nil, e.endedOr(err)
	}

	return e.RunModule(source, url)
}

// Preload imports a module specifier (bare or path) before the entry runs,
// implementing Node's --import. The module is resolved relative to the current
// working directory and evaluated in the application realm; a module that calls
// module.register thereby installs its hooks before the entry is loaded.
func (e *ESM) Preload(specifier string) error {
	if e.rt == nil {
		return errors.New("esm: runtime not initialized")
	}
	if err := e.ensureLoader(); err != nil {
		return err
	}
	source, url, err := e.resolveAndLoad(specifier, "")
	if err != nil {
		return e.endedOr(err)
	}
	_, err = e.RunModule(source, url)
	return err
}

// GetModuleNamespace returns the namespace object of a module after evaluation.
func (e *ESM) GetModuleNamespace(source, filename string) (*v8.Value, error) {
	if err := e.ensureLoader(); err != nil {
		return nil, err
	}

	ctx := e.rt.Context()

	mod, err := ctx.CompileModule(source, filename)
	if err != nil {
		return nil, err
	}

	if err := mod.Instantiate(e.resolveModule); err != nil {
		return nil, e.endedOr(err)
	}

	if _, err := mod.Evaluate(); err != nil {
		return nil, e.endedOr(err)
	}

	e.rt.EventLoop().Run()
	if ended := e.rt.Ended(); ended != nil {
		return nil, ended
	}

	return mod.GetNamespace()
}

// resolveModule is called by V8 (via the C bridge) for every import. It runs the
// Go loader (default resolution + load + finalize, wrapped by any registered
// hooks) and returns the module source + its stable url (V8 caches modules by
// this url, so diamond imports share one instance).
func (e *ESM) resolveModule(specifier, referrer string) (string, string, error) {
	if err := e.ensureLoader(); err != nil {
		return "", "", err
	}
	return e.resolveAndLoad(specifier, referrer)
}

// resolveAndLoad produces the compilable ESM source + stable url for a
// specifier. When loader hooks are registered it runs them (in the isolated
// loader realm) around the Go defaults; otherwise it takes the pure-Go default
// path directly. See hooks.go.
func (e *ESM) resolveAndLoad(specifier, referrer string) (source, url string, err error) {
	if e.hooksActive() {
		return e.resolveAndLoadHooked(specifier, referrer)
	}
	return e.resolveAndLoadDefault(specifier, referrer)
}

// awaitPromiseRT drives rt's microtask queue (and event loop, for hooks that do
// real async work) until the given promise settles, then returns its value or a
// JS error for a rejection. maxTicks bounds the spin so a never-settling promise
// can't hang resolution — this is the guard against a runaway loader hook.
func awaitPromiseRT(rt *runtime.Runtime, p *v8.Value, maxTicks int) (*v8.Value, error) {
	if p == nil {
		return nil, errors.New("esm: nil promise")
	}
	if !p.IsPromise() {
		// A hook may return a plain value instead of a promise; pass it through.
		return p, nil
	}
	// The caller gets a rejection as an error, so it is not an unhandled one.
	rt.MarkPromiseHandled(p)
	ctx := rt.Context()
	for i := 0; i < maxTicks; i++ {
		switch p.PromiseState() {
		case v8.PromiseFulfilled:
			return p.PromiseResult(), nil
		case v8.PromiseRejected:
			reason := p.PromiseResult()
			msg := "promise rejected"
			if reason != nil {
				msg = reason.String()
			}
			return nil, &v8.JSError{Message: msg}
		}
		ctx.PerformMicrotaskCheckpoint()
		if p.PromiseState() == v8.PromisePending {
			rt.EventLoop().RunOnce()
		}
	}
	return nil, errors.New("esm: hook promise did not settle")
}
