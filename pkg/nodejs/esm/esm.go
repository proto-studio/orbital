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
	"encoding/json"
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
	// hookError is esm-loader.js's __esmHookError, captured like makeError.
	hookError *v8.Value
	// keyPaths maps a module url carrying a query or fragment to its file.
	keyPaths map[string]string
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
		e.hookError, _ = global.Get("__esmHookError")
		e.loaderReady = true
		return nil
	}
	if _, err := e.rt.RunScript(esmLoaderJS, "esm-loader.js"); err != nil {
		return err
	}
	e.makeError, _ = global.Get("__esmModuleError")
	e.rejected, _ = global.Get("__esmRejected")
	e.hookError, _ = global.Get("__esmHookError")
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
		return nil, e.linkFailed(err, entry)
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
			return nil, ErrUnsettledTopLevelAwait
		}
	}

	return result, nil
}

// ErrUnsettledTopLevelAwait is returned when a module's top-level await never
// settles and nothing is left to run. Node then runs the 'exit' listeners and
// exits with code 13.
var ErrUnsettledTopLevelAwait = errors.New("top-level await promise never resolved before the event loop drained")

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
func (e *ESM) linkFailed(err error, entry bool) error {
	if ended := e.rt.Ended(); ended != nil {
		return ended
	}
	exc := e.linkErr
	e.linkErr = nil
	if exc == nil {
		// Not a resolution failure: the exception linking threw (a SyntaxError
		// in a dependency, a missing export).
		var thrown v8.ExceptionError
		if !errors.As(err, &thrown) || thrown.Exception() == nil {
			return err
		}
		exc = thrown.Exception()
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
	// For the program's entry the loop emits 'beforeExit' as it runs out, as
	// Node's does after a handled failure.
	if entry {
		e.rt.RunEventLoop()
	} else {
		e.rt.EventLoop().Run()
	}
	if ended := e.rt.Ended(); ended != nil {
		return ended
	}
	// An 'uncaughtException' listener handled it; Node carries on too.
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
		return nil, e.linkFailed(e.throwable(err), true)
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
	// Like Node, which runs import() on each --import: resolution, link,
	// syntax and evaluation failures all reject it with the real error, and a
	// rejection is reported as an uncaught exception. The script has no name,
	// so the module resolves from the working directory.
	p, err := e.rt.RunScript("import("+jsQuote(specifier)+")", "")
	if err != nil {
		return e.endedOr(err)
	}
	if p == nil || !p.IsPromise() {
		return errors.New("esm: --import " + specifier + " did not start")
	}
	e.rt.WatchModuleEvaluation(p)
	e.rt.Context().PerformMicrotaskCheckpoint()
	// While the import is pending, an emptied loop emits 'beforeExit', as
	// Node's does while startup waits on it: a listener can settle the await.
	e.rt.RunEventLoopWhile(func() bool { return p.PromiseState() == v8.PromisePending })
	if ended := e.rt.Ended(); ended != nil {
		return ended
	}
	switch p.PromiseState() {
	case v8.PromiseRejected:
		// An 'uncaughtException' listener handled it.
		return ErrPreloadFailed
	case v8.PromisePending:
		// Its top-level await never settled: Node exits 13.
		return ErrUnsettledTopLevelAwait
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
	ctx := e.rt.Context()
	if me.hookValue != nil {
		exc, derr := ctx.DeserializeValue(me.hookValue, nil)
		if derr != nil || exc == nil {
			return err
		}
		e.linkErr = exc
		return &thrownError{err: err, exc: exc}
	}
	if me.hookPayload != "" {
		// An error a loader hook threw, rebuilt as it was.
		fn := e.hookError
		if fn == nil || !fn.IsFunction() {
			return err
		}
		arg, serr := ctx.NewString(me.hookPayload)
		if serr != nil {
			return err
		}
		args := []*v8.Value{arg}
		if me.hookProps != nil {
			if props, derr := ctx.DeserializeValue(me.hookProps, nil); derr == nil {
				args = append(args, props)
			}
		}
		exc, cerr := fn.Call(nil, args...)
		if cerr != nil || exc == nil {
			return err
		}
		e.linkErr = exc
		return &thrownError{err: err, exc: exc}
	}
	mk := e.makeError
	if mk == nil || !mk.IsFunction() {
		return err
	}
	args := make([]*v8.Value, 3, 6)
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
	input, ierr := ctx.NewString(me.input)
	if ierr != nil {
		return err
	}
	module, merr := ctx.NewString(me.module)
	if merr != nil {
		return err
	}
	args = append(args, kind, input, module)
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
		if me := describeRejection(rt, reason); me != nil {
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

// describeRejection carries a hook chain's rejection from the loader realm rt
// to the application realm as its class, message, stack and own enumerable
// properties, as Node's transfer from its hooks thread keeps them. code is kept
// for the loader's own bookkeeping.
func describeRejection(rt *runtime.Runtime, reason *v8.Value) *moduleError {
	global, err := rt.Context().Global()
	if err != nil {
		return nil
	}
	fn, _ := global.Get("__describeRejection")
	if fn == nil || !fn.IsFunction() {
		return nil
	}
	if reason == nil {
		reason = rt.Context().Undefined()
	}
	res, err := fn.Call(nil, reason)
	if err != nil || res == nil || !res.IsString() || res.String() == "" {
		return nil
	}
	payload := res.String()
	var d struct {
		Clone   bool           `json:"clone"`
		Name    string         `json:"name"`
		Message string         `json:"message"`
		Props   map[string]any `json:"props"`
	}
	if json.Unmarshal([]byte(payload), &d) != nil {
		return nil
	}
	if d.Clone {
		// A value that isn't an error: structured-cloned across, as Node does,
		// from the clone the loader realm took (so its getters ran once).
		value := reason
		if cloneFn, _ := global.Get("__rejectionClone"); cloneFn != nil && cloneFn.IsFunction() {
			if c, cerr := cloneFn.Call(nil, reason); cerr == nil && c != nil {
				value = c
			}
		}
		data, serr := rt.Context().SerializeValue(value, nil)
		if serr != nil {
			return nil
		}
		return &moduleError{msg: "a loader hook threw " + reason.String(), hookValue: data}
	}
	code, _ := d.Props["code"].(string)
	me := &moduleError{msg: d.Name + ": " + d.Message, code: code, hookPayload: payload}
	// The properties themselves cross through V8's serializer, keeping what
	// JSON loses; the JSON copy stays as a fallback.
	if propsFn, _ := global.Get("__rejectionProps"); propsFn != nil && propsFn.IsFunction() {
		if props, perr := propsFn.Call(nil, reason); perr == nil && props != nil {
			if data, serr := rt.Context().SerializeValue(props, nil); serr == nil {
				me.hookProps = data
			}
		}
	}
	return me
}
