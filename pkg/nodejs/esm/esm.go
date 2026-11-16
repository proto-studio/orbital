// Package esm implements ES Module loading.
//
// V8 resolves imports through a synchronous callback (resolveModule). Rather
// than reimplement Node's resolution algorithm in Go, that callback delegates to
// a JS loader (esm-loader.js) which runs in the fully-initialized runtime and
// returns { url, source } for each requested specifier. See esm-loader.js.
package esm

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
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
	// lexer is cjs-module-lexer's parse function, compiled on first use.
	lexer *v8.Value
	// cjsExports caches the export names found for each CommonJS file.
	cjsExports map[string]*cjsNames
	// makeError is esm-loader.js's __esmModuleError, captured when the loader is
	// installed so code replacing the global cannot change the errors imports throw.
	makeError *v8.Value
	// rejected is esm-loader.js's __esmRejected, captured like makeError.
	rejected *v8.Value
	// keyPaths maps a module url carrying a query or fragment to its file.
	keyPaths map[string]string
	// linkHandled records that linkFailed's error went to an
	// 'uncaughtException' listener.
	linkHandled bool
	// linkErr is the last error object a failed resolution threw, which is what
	// a failed Instantiate threw.
	linkErr *v8.Value
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
		e.makeError, _ = global.Get("__esmModuleError")
		e.rejected, _ = global.Get("__esmRejected")
		e.loaderReady = true
		return nil
	}
	if _, err := e.rt.RunScript(esmLoaderJS, "esm-loader.js"); err != nil {
		return err
	}
	e.makeError, _ = global.Get("__esmModuleError")
	e.rejected, _ = global.Get("__esmRejected")
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

// RunModule compiles and runs an ES module from source, and runs the event loop until it
// has no work left. It doesn't emit 'beforeExit': that belongs to a program's entry point.
func (e *ESM) RunModule(source, filename string) (*v8.Value, error) {
	return e.runModule(source, filename, false)
}

// runModule runs an ES module. For the program's entry, the loop emits 'beforeExit' each
// time it runs out of work, as Node's does.
func (e *ESM) runModule(source, filename string, entry bool) (*v8.Value, error) {
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

	e.linkErr = nil
	if err := mod.Instantiate(e.resolveModule); err != nil {
		return nil, e.linkFailed(err)
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

	// Run any pending async operations (timers, microtasks, top-level await). For the
	// entry, 'beforeExit' is emitted when they run out.
	if entry {
		e.rt.RunEventLoop()
	} else {
		e.rt.EventLoop().Run()
	}
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
// JavaScript fails when the program exits: Instantiate when a builtin is required
// to build its wrapper, Evaluate for module code (an imported CommonJS module
// included). The
// caller must see the exit (or uncaught error), not the module error V8 reports.
func (e *ESM) endedOr(err error) error {
	if ended := e.rt.Ended(); ended != nil {
		return ended
	}
	return err
}

// linkFailed reports a module graph that failed to link because an import could
// not be resolved the way Node does: as an uncaught exception, printed with the
// error's stack, code and url (and seen by 'uncaughtException' listeners).
// Other link failures (and a program that already ended) return as they are.
func (e *ESM) linkFailed(err error) error {
	if ended := e.rt.Ended(); ended != nil {
		return ended
	}
	exc := e.linkErr
	e.linkErr = nil
	if exc == nil {
		return err
	}
	rejected := e.rejected
	if rejected == nil || !rejected.IsFunction() {
		return err
	}
	p, perr := rejected.Call(nil, exc)
	if perr != nil || p == nil || !p.IsPromise() {
		return err
	}
	e.rt.WatchModuleEvaluation(p)
	e.rt.Context().PerformMicrotaskCheckpoint()
	e.rt.EventLoop().Run()
	if ended := e.rt.Ended(); ended != nil {
		return ended
	}
	// An 'uncaughtException' listener handled it; Node carries on too.
	e.linkHandled = true
	return nil
}

// ErrPreloadFailed is returned by Preload when the module failed to resolve,
// link or run and an 'uncaughtException' listener handled the error. Node then runs no
// further preloads and not the entry point; the program ends normally.
var ErrPreloadFailed = errors.New("esm: an --import module failed and the error was handled")

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

	// As a file: URL, so the path's characters aren't read as URL syntax.
	entry := absPath
	if !winAbs(absPath) {
		entry = fileURL(absPath)
	}
	source, url, err := e.resolveAndLoad(entry, "")
	if err != nil {
		if ended := e.rt.Ended(); ended != nil {
			return nil, ended
		}
		// Like Node, a missing entry is an uncaught exception, which an
		// 'uncaughtException' listener from a preload can handle.
		e.linkErr = nil
		return nil, e.linkFailed(e.throwable(err))
	}

	return e.runModule(source, url, true)
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
		if ended := e.rt.Ended(); ended != nil {
			return ended
		}
		// Like Node, an --import that fails to resolve is an uncaught exception.
		e.linkErr = nil
		e.linkHandled = false
		thrown := e.throwable(err)
		if err := e.linkFailed(thrown); err != nil || !e.linkHandled {
			return err
		}
		return ErrPreloadFailed
	}
	e.linkHandled = false
	result, err := e.RunModule(source, url)
	if err != nil {
		return err
	}
	// A link error, or a throw or rejection while it ran, that a listener handled.
	if e.linkHandled || (result != nil && result.IsPromise() && result.PromiseState() == v8.PromiseRejected) {
		return ErrPreloadFailed
	}
	return nil
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
	source, url, err := e.resolveAndLoad(specifier, referrer)
	if err != nil {
		return "", "", e.throwable(err)
	}
	return source, url, nil
}

// thrownError is a loader failure together with the JavaScript error V8 throws
// for it (or rejects import() with).
type thrownError struct {
	err error
	exc *v8.Value
}

func (t *thrownError) Error() string        { return t.err.Error() }
func (t *thrownError) Unwrap() error        { return t.err }
func (t *thrownError) Exception() *v8.Value { return t.exc }

// throwable attaches to err the error object the importing code sees: for a
// moduleError, an Error with Node's message, code and url. Other failures keep
// the generic error V8's resolve callback reports.
func (e *ESM) throwable(err error) error {
	var me *moduleError
	if !errors.As(err, &me) || e.rt.Ended() != nil {
		return err
	}
	mk := e.makeError
	if mk == nil || !mk.IsFunction() {
		return err
	}
	ctx := e.rt.Context()
	args := make([]*v8.Value, 3, 4)
	for i, s := range []string{me.msg, me.code, me.url} {
		v, serr := ctx.NewString(s)
		if serr != nil {
			return err
		}
		args[i] = v
	}
	kind, kerr := ctx.NewString(me.kind())
	if kerr != nil {
		return err
	}
	args = append(args, kind)
	exc, cerr := mk.Call(nil, args...)
	if cerr != nil || exc == nil {
		return err
	}
	e.linkErr = exc
	return &thrownError{err: err, exc: exc}
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

// awaitPromiseRT waits for a promise from the loader realm rt (a hook's result)
// and returns its value, or a JS error for a rejection. It runs rt's microtasks
// and event loop until the promise settles, waiting on timers and I/O as long
// as they take, as Node waits on its hooks thread. It gives up only when rt
// has nothing left that could settle the promise, or when stop (the importing
// runtime's execution context) ends: killing or timing out the application
// kills the loader realm's wait with it.
func awaitPromiseRT(rt *runtime.Runtime, p *v8.Value, stop context.Context) (*v8.Value, error) {
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
	settled := func() bool {
		ctx.PerformMicrotaskCheckpoint()
		return p.PromiseState() != v8.PromisePending
	}
	if !settled() {
		finished := make(chan struct{})
		go func() {
			select {
			case <-stop.Done():
				rt.Kill("the importing runtime stopped")
			case <-finished:
			}
		}()
		rt.EventLoop().RunUntil(settled)
		close(finished)
		if err := stop.Err(); err != nil {
			return nil, fmt.Errorf("esm: import stopped while a loader hook ran: %w", err)
		}
	}
	switch p.PromiseState() {
	case v8.PromiseFulfilled:
		return p.PromiseResult(), nil
	case v8.PromiseRejected:
		reason := p.PromiseResult()
		if me := codedError(reason); me != nil {
			return nil, me
		}
		msg := "promise rejected"
		if reason != nil {
			msg = reason.String()
		}
		return nil, &v8.JSError{Message: msg}
	}
	if ended := rt.Ended(); ended != nil {
		return nil, ended
	}
	return nil, errors.New("esm: hook promise never settled: the loader hooks have no pending work left")
}

// codedError carries a hook's rejection with a Node error code (such as the
// default resolver's ERR_MODULE_NOT_FOUND) back to the application realm with its
// message, code and url, so the importer sees the error Node would give it.
func codedError(reason *v8.Value) *moduleError {
	if reason == nil || !reason.IsObject() {
		return nil
	}
	code, name := objString(reason, "code"), objString(reason, "name")
	if code == "" && name != "URIError" {
		return nil
	}
	return &moduleError{
		msg:       objString(reason, "message"),
		code:      code,
		url:       objString(reason, "url"),
		typeError: name == "TypeError",
		uriError:  name == "URIError",
	}
}
