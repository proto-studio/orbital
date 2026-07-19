package esm

// Loader hooks (module.register / --import), implemented with an ISOLATED
// loader realm.
//
// Node runs module-customization hooks on a separate loader thread with its own
// environment: the hooks cannot share mutable globals or the module cache with
// the application, and communicate only via serializable data. Orbital mirrors
// that isolation with a second, fully-provisioned Runtime (its own V8 context,
// globals, event loop, and per-context module cache). The application realm's
// Go resolver marshals only strings (specifier/referrer in, url/source/format
// out) to and from that realm — never live objects.
//
// Async hooks are driven to completion synchronously from Go by pumping the
// loader realm's microtask queue + event loop (awaitPromiseRT). That is our
// analog of Node awaiting the loader thread; because we own the loop, no real
// OS thread is required. The drive is bounded (maxHookTicks) so a runaway hook
// cannot hang resolution. The loader realm resolves its OWN graph (ts-blank-
// space, typescript, ...) with the plain default loader — hooks are never
// applied to the hooks' own dependencies, which structurally rules out the
// hook-loads-itself infinite loop.

import (
	_ "embed"
	"errors"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed loader-hooks.js
var loaderHooksJS string

// LoaderRuntimeProvider builds a fully-configured runtime for the isolated
// loader realm. It is set by nodejs.New (or the embedder) to the same builder
// used for the application runtime (and for worker threads), so the loader
// realm has a real require + builtins and can load hook modules and their
// dependencies.
var LoaderRuntimeProvider func() (*runtime.Runtime, error)

// maxHookTicks bounds how long Go will pump the loader realm waiting for a hook
// promise to settle before giving up (guards against a runaway loader hook).
const maxHookTicks = 200000

// loaderRealm is the isolated second runtime that hosts registered hooks.
type loaderRealm struct {
	rt        *runtime.Runtime
	esm       *ESM // the loader realm's own esm instance (loads hook modules)
	hookCount int
}

// hooksActive reports whether any resolve/load hooks are registered.
func (e *ESM) hooksActive() bool {
	return e.loader != nil && e.loader.hookCount > 0
}

// ensureLoaderRealm lazily creates the isolated loader realm and installs the
// orchestrator + Go default-loader bridges.
func (e *ESM) ensureLoaderRealm() error {
	if e.loader != nil {
		return nil
	}
	if LoaderRuntimeProvider == nil {
		return errors.New("esm: loader realm provider not configured")
	}
	lrt, err := LoaderRuntimeProvider()
	if err != nil {
		return err
	}
	mod, ok := lrt.GetModule("esm")
	if !ok {
		return errors.New("esm: loader realm is missing the esm module")
	}
	le, ok := mod.(*ESM)
	if !ok {
		return errors.New("esm: loader realm esm module has unexpected type")
	}
	if err := le.ensureLoader(); err != nil {
		return err
	}

	// Prime the builtin-name set on the application realm now, so the loader
	// realm's __goResolve callback (which runs while the loader isolate is the
	// current one) never has to reach into the application isolate.
	e.loadBuiltins()

	if _, err := lrt.RunScript(loaderHooksJS, "loader-hooks.js"); err != nil {
		return err
	}
	if err := e.installLoaderBridges(lrt); err != nil {
		return err
	}

	e.loader = &loaderRealm{rt: lrt, esm: le}
	return nil
}

// installLoaderBridges installs __goResolve / __goLoad in the loader realm.
// These are the terminal (default) resolve/load: they run the pure-Go default
// loader against the APPLICATION realm's module graph and return only strings.
func (e *ESM) installLoaderBridges(lrt *runtime.Runtime) error {
	iso := lrt.Isolate()
	ctx := lrt.Context()
	global, err := ctx.Global()
	if err != nil {
		return err
	}

	resolveTmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		c := info.Context()
		specifier := argString(info, 0)
		parentURL := argString(info, 1)
		obj, _ := c.NewObject()
		url, rerr := e.resolveToURL(specifier, parentURL)
		if rerr != nil {
			// Report the attempted path so the JS side can attach it to the
			// Node ERR_MODULE_NOT_FOUND error's `url`, letting resolve hooks
			// rewrite the extension and retry (ts-blank-space .js -> .ts).
			av, _ := c.NewString(e.attemptedPath(specifier, parentURL))
			obj.Set("url", av)
			obj.Set("found", c.False())
			return obj
		}
		uv, _ := c.NewString(url)
		obj.Set("url", uv)
		obj.Set("found", c.True())
		return obj
	})
	if err != nil {
		return err
	}
	resolveFn, err := resolveTmpl.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := global.Set("__goResolve", resolveFn); err != nil {
		return err
	}

	loadTmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		c := info.Context()
		url := argString(info, 0)
		src, format, lerr := e.loadURL(url)
		if lerr != nil {
			return c.Throw("Cannot load module '" + url + "': " + lerr.Error())
		}
		obj, _ := c.NewObject()
		sv, _ := c.NewString(src)
		fv, _ := c.NewString(format)
		obj.Set("source", sv)
		obj.Set("format", fv)
		return obj
	})
	if err != nil {
		return err
	}
	loadFn, err := loadTmpl.GetFunction(ctx)
	if err != nil {
		return err
	}
	return global.Set("__goLoad", loadFn)
}

// registerHook implements module.register(specifier, parentURL). It imports the
// hooks module into the isolated loader realm and adds it to the hook chain.
func (e *ESM) registerHook(specifier, parentURL string) error {
	if err := e.ensureLoaderRealm(); err != nil {
		return err
	}
	le := e.loader.esm

	// Resolve the hooks module to a concrete url using the loader realm's own
	// default resolver (its graph, its filesystem view).
	hookURL, err := le.resolveToURL(specifier, parentURL)
	if err != nil {
		return errors.New("esm: cannot resolve hook module '" + specifier + "': " + err.Error())
	}

	// Import the hooks module into the loader realm and register it. Any
	// initialize() export is invoked; the event loop drained by RunModule
	// settles it (and the module's own async top-level work, e.g. loading
	// typescript).
	boot := "import * as __h from " + jsQuote(hookURL) + ";\n" +
		"globalThis.__addHook(__h);\n" +
		"if (typeof __h.initialize === 'function') { globalThis.__hookInitResult = __h.initialize(); }\n"
	if _, err := le.RunModule(boot, "<register:"+specifier+">"); err != nil {
		return err
	}

	e.loader.hookCount++
	return nil
}

// resolveAndLoadHooked runs the registered hook chain (in the isolated loader
// realm) around the Go defaults, then finalizes in the application realm.
func (e *ESM) resolveAndLoadHooked(specifier, referrer string) (source, url string, err error) {
	lrt := e.loader.rt

	// resolve chain -> { url, format }
	resP, err := e.callLoaderRealm("__runResolve", specifier, referrer)
	if err != nil {
		return "", "", err
	}
	resolved, err := awaitPromiseRT(lrt, resP, maxHookTicks)
	if err != nil {
		return "", "", err
	}
	url = objString(resolved, "url")
	if url == "" {
		return "", "", errors.New("esm: resolve hook returned no url for '" + specifier + "'")
	}
	// Hooks may return a file:// URL; normalize to a plain path so it matches the
	// default loader's keys (consistent V8 module cache + referrer dirs).
	url = stripFileURL(url)
	preFormat := objString(resolved, "format")

	// load chain -> { source, format }
	loadP, err := e.callLoaderRealm("__runLoad", url, preFormat)
	if err != nil {
		return "", "", err
	}
	loaded, err := awaitPromiseRT(lrt, loadP, maxHookTicks)
	if err != nil {
		return "", "", err
	}
	src := objString(loaded, "source")
	format := objString(loaded, "format")

	// Finalize in the APPLICATION realm: CJS/JSON interop wrappers must run
	// require() against the application's module graph, not the loader realm's.
	final, err := e.finalize(url, src, format)
	if err != nil {
		return "", "", err
	}
	return final, url, nil
}

// callLoaderRealm invokes a global function in the loader realm with string args
// and returns its (usually promise) result.
func (e *ESM) callLoaderRealm(fn string, args ...string) (*v8.Value, error) {
	ctx := e.loader.rt.Context()
	global, err := ctx.Global()
	if err != nil {
		return nil, err
	}
	fnVal, err := global.Get(fn)
	if err != nil {
		return nil, err
	}
	if fnVal == nil || !fnVal.IsFunction() {
		return nil, errors.New("esm: loader realm " + fn + " is not available")
	}
	argVals := make([]*v8.Value, len(args))
	for i, a := range args {
		sv, err := ctx.NewString(a)
		if err != nil {
			return nil, err
		}
		argVals[i] = sv
	}
	return fnVal.Call(nil, argVals...)
}

func argString(info *v8.FunctionCallbackInfo, idx int) string {
	if idx >= info.Length() {
		return ""
	}
	v := info.Arg(idx)
	if v == nil || v.IsUndefined() {
		return ""
	}
	return v.String()
}

func objString(obj *v8.Value, key string) string {
	if obj == nil || !obj.IsObject() {
		return ""
	}
	v, _ := obj.Get(key)
	if v == nil || v.IsUndefined() {
		return ""
	}
	return v.String()
}
