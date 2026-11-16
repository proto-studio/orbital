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
// Async hooks are driven to completion synchronously from Go by running the
// loader realm's microtasks and event loop (awaitPromiseRT), waiting on its
// timers and I/O for as long as they take. That is our analog of Node awaiting
// the loader thread; because we own the loop, no real OS thread is required. A
// hook promise that nothing left in the loader realm can settle fails the import. The loader realm resolves its OWN graph (ts-blank-
// space, typescript, ...) with the plain default loader — hooks are never
// applied to the hooks' own dependencies, which structurally rules out the
// hook-loads-itself infinite loop.

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed loader-hooks.js
var loaderHooksJS string

// loaderRealm is the isolated second runtime that hosts registered hooks.
type loaderRealm struct {
	rt        *runtime.Runtime
	esm       *ESM // the loader realm's own esm instance (loads hook modules)
	hookCount int
}

// realmCloser disposes a loader realm when its owning runtime is disposed.
type realmCloser struct{ rt *runtime.Runtime }

func (c realmCloser) Close() error {
	c.rt.Dispose()
	return nil
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
	if e.provider == nil {
		return errors.New("esm: loader realm provider not configured")
	}
	lrt, err := e.provider()
	if err != nil {
		return err
	}
	// The loader realm belongs to this runtime and is disposed with it.
	realmID := e.rt.TrackResource(realmCloser{lrt})
	fail := func(err error) error {
		e.rt.UntrackResource(realmID)
		lrt.Dispose()
		return err
	}
	mod, ok := lrt.GetModule("esm")
	if !ok {
		return fail(errors.New("esm: loader realm is missing the esm module"))
	}
	le, ok := mod.(*ESM)
	if !ok {
		return fail(errors.New("esm: loader realm esm module has unexpected type"))
	}
	if err := le.ensureLoader(); err != nil {
		return fail(err)
	}

	// Prime the builtin-name set on the application realm now, so the loader
	// realm's __goResolve callback (which runs while the loader isolate is the
	// current one) never has to reach into the application isolate.
	e.loadBuiltins()

	if err := installV8Clone(lrt); err != nil {
		return fail(err)
	}
	if _, err := lrt.RunScript(loaderHooksJS, "loader-hooks.js"); err != nil {
		return fail(err)
	}
	if err := e.installLoaderBridges(lrt); err != nil {
		return fail(err)
	}

	e.loader = &loaderRealm{rt: lrt, esm: le}
	return nil
}

// installV8Clone installs __v8Clone in the loader realm, before loader-hooks.js
// captures it: a structured clone made with V8's serializer, as Node's transfer
// from its hooks thread is, so an Error is told by its brand rather than its
// prototype chain. A value that can't be cloned throws.
func installV8Clone(lrt *runtime.Runtime) error {
	ctx := lrt.Context()
	global, err := ctx.Global()
	if err != nil {
		return err
	}
	tmpl, err := lrt.Isolate().NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		c := info.Context()
		args := info.Args()
		if len(args) == 0 {
			return c.Undefined()
		}
		data, serr := c.SerializeValue(args[0], nil)
		if serr != nil {
			return c.Throw("DataCloneError: " + serr.Error())
		}
		out, derr := c.DeserializeValue(data, nil)
		if derr != nil {
			return c.Throw("DataCloneError: " + derr.Error())
		}
		return out
	})
	if err != nil {
		return err
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		return err
	}
	return global.Set("__v8Clone", fn)
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
		// The result crosses as JSON, which the JS side parses into own data
		// properties: setting them on an object here would run any setter a
		// hook has put on Object.prototype.
		res := map[string]any{}
		url, rerr := e.resolveToURL(specifier, parentURL)
		if rerr != nil {
			// Report Node's error (message, code and the missing module's url)
			// for the JS side to throw, so resolve hooks can catch it and retry
			// another candidate (ts-blank-space .js -> .ts).
			res["found"] = false
			res["message"] = rerr.Error()
			var me *moduleError
			if errors.As(rerr, &me) {
				res["code"] = me.code
				res["kind"] = me.kind()
				if me.input != "" {
					res["input"] = me.input
				}
				if me.module != "" {
					res["module"] = me.module
				}
				if me.url != "" {
					res["url"] = me.url
				}
			}
		} else {
			res["url"] = url
			res["found"] = true
		}
		data, merr := json.Marshal(res)
		if merr != nil {
			return c.Throw(merr.Error())
		}
		out, _ := c.NewString(string(data))
		return out
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
		// Like Node's default load, check the import's attributes against the
		// format the chain settled on (context.format), or the detected one.
		if argString(info, 3) != "" {
			checkFormat := argString(info, 1)
			if checkFormat == "" {
				checkFormat = format
			}
			attrs, typeReceived := parseAttrsJSON(argString(info, 2))
			if cerr := checkHookAttributes(e.displayURL(url), checkFormat, attrs, typeReceived); cerr != nil {
				me := cerr.(*moduleError)
				mv, _ := c.NewString(me.msg)
				cv, _ := c.NewString(me.code)
				obj.Set("message", mv)
				obj.Set("code", cv)
				return obj
			}
		}
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
		"globalThis.__addHook(__h, " + jsQuote(specifier) + ");\n" +
		"if (typeof __h.initialize === 'function') { globalThis.__hookInitResult = __h.initialize(); }\n"
	if _, err := le.RunModule(boot, "<register:"+specifier+">"); err != nil {
		return err
	}

	e.loader.hookCount++
	return nil
}

// resolveAndLoadHooked runs the registered hook chain (in the isolated loader
// realm) around the Go defaults, then finalizes in the application realm.
func (e *ESM) resolveAndLoadHooked(specifier, referrer string, attrs []v8.ImportAttribute) (source, url string, err error) {
	lrt := e.loader.rt

	// resolve chain -> { url, format, importAttributes }
	// Hooks see the importer as Node shows it: its file: URL (with any query),
	// without the type a typed import adds to its cache key.
	parentURL := referrer
	if referrer != "" {
		parentURL = e.displayURL(referrer)
	}
	resP, err := e.callLoaderRealm("__runResolve", specifier, parentURL, attrsJSON(attrs))
	if err != nil {
		return "", "", err
	}
	resolved, err := awaitPromiseRT(lrt, resP, e.rt.ExecutionContext())
	if err != nil {
		return "", "", err
	}
	resolvedAttrs := objString(resolved, "importAttributes")
	// The loader realm keeps the resolved attributes for the load; any return
	// before the load starts releases them.
	loading := false
	defer func() {
		if !loading {
			e.dropResolvedAttrs(resolvedAttrs)
		}
	}()
	url = objString(resolved, "url")
	if url == "" {
		return "", "", errors.New("esm: resolve hook returned no url for '" + specifier + "'")
	}
	// Hooks may return a file:// URL; normalize to a plain path so it matches the
	// default loader's keys (consistent V8 module cache + referrer dirs).
	url, err = e.hookURLKey(url)
	if err != nil {
		return "", "", err
	}
	preFormat := objString(resolved, "format")
	// Like Node's module map, a type that isn't a string is refused before any
	// load hook runs, except undefined, which there means the implicit type (the
	// default load still refuses it).
	resolvedList, typeReceived := parseAttrsJSON(resolvedAttrs)
	keyList := keyAttrs(resolvedList, resolvedAttrs)
	if typeReceived == "undefined" {
		keyList = withoutType(resolvedList)
	} else if typeReceived != "" {
		return "", "", attrTypeError("ERR_INVALID_ARG_TYPE",
			`The "type" argument must be of type string. Received `+typeReceived)
	}

	// The default load checks the attributes, except for an entry point and a
	// (url, type) pair already checked: Node reuses that first module job, and
	// its outcome, without loading again.
	check, key := "", ""
	if attrs != nil {
		key = attrCheckKey(url, keyList)
		if prior, seen := e.attrChecks[key]; seen {
			if prior != nil {
				return "", "", prior
			}
		} else {
			check = "1"
		}
	}

	// load chain -> { source, format }
	loading = true
	loadP, err := e.callLoaderRealm("__runLoad", url, preFormat, resolvedAttrs, check)
	if err != nil {
		return "", "", err
	}
	loaded, err := awaitPromiseRT(lrt, loadP, e.rt.ExecutionContext())
	if check != "" {
		var me *moduleError
		if err == nil {
			e.recordAttrCheck(key, nil)
		} else if errors.As(err, &me) && (strings.HasPrefix(me.code, "ERR_IMPORT_") || me.code == "ERR_INVALID_ARG_TYPE") {
			e.recordAttrCheck(key, me)
		}
	}
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
	if attrs == nil {
		return final, url, nil
	}
	return final, e.typedKey(url, format, keyList), nil
}

// dropResolvedAttrs releases the attributes the loader realm keeps for a load
// that won't happen.
func (e *ESM) dropResolvedAttrs(resolvedAttrs string) {
	_, _ = e.callLoaderRealm("__dropAttrs", resolvedAttrs)
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
