package v8_test

import (
	"runtime"
	"testing"

	"proto.zip/studio/orbital/pkg/v8"
)

// ReleaseAfter releases the values made while it runs, an inner call only its own, and leaves
// values made before it.
func TestReleaseAfter(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	before, _ := ctx.NewString("before")
	var outer, inner *v8.Value
	ctx.ReleaseAfter(func() {
		outer, _ = ctx.NewString("outer")
		ctx.ReleaseAfter(func() {
			inner, _ = ctx.NewString("inner")
		})
		if inner.IsString() {
			t.Error("the inner value outlived its scope")
		}
		if !outer.IsString() || outer.String() != "outer" {
			t.Error("the inner scope released the outer value")
		}
	})
	if outer.IsString() {
		t.Error("the outer value outlived its scope")
	}
	if !before.IsString() || before.String() != "before" {
		t.Error("a value made before the scope was released")
	}
}

// A Go callback sees the context it was called in, the same one each call, so values it makes
// fall in that context's release scopes.
func TestCallbackValuesFallInReleaseScope(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	var made []*v8.Value
	tmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		info.Context().ReleaseAfter(func() {
			v, _ := ctx.NewString("made")
			made = append(made, v)
		})
		return nil
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := fn.Call(nil); err != nil {
			t.Fatalf("Call: %v", err)
		}
	}
	for i, v := range made {
		if v.IsString() {
			t.Errorf("value %d made with the outer context outlived the callback's scope", i)
		}
	}
}

// A Go callback that JavaScript calls while a release scope is open, and that keeps a value it
// was given, like a timer keeping its function, still has it once the scope ends.
func TestReleaseAfterSparesNestedCallbacks(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	var kept, made *v8.Value
	tmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		kept = info.Arg(0)
		made, _ = ctx.NewString("made")
		return nil
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	keep, err := tmpl.GetFunction(ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	run, err := ctx.RunScript(`(keep) => { keep(() => 'still here'); }`, "run.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	ctx.ReleaseAfter(func() {
		if _, err := run.Call(nil, keep); err != nil {
			t.Fatalf("Call: %v", err)
		}
	})
	if kept == nil || !kept.IsFunction() {
		t.Fatal("the callback's argument was released with the outer scope")
	}
	got, err := kept.Call(nil)
	if err != nil || got.String() != "still here" {
		t.Errorf("calling the kept function: %v, %v", got, err)
	}
	if !made.IsString() {
		t.Error("a value the callback made was released with the outer scope")
	}
}

// A value a Go callback keeps holds on to the Context it was called in, so the context stays
// usable after every other reference to that Context is gone.
func TestCallbackValueKeepsContext(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()

	var kept *v8.Value
	func() {
		ctx, err := iso.NewContext()
		if err != nil {
			t.Fatalf("NewContext: %v", err)
		}
		tmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
			kept = info.Arg(0)
			return nil
		})
		if err != nil {
			t.Fatalf("NewFunctionTemplate: %v", err)
		}
		keep, err := tmpl.GetFunction(ctx)
		if err != nil {
			t.Fatalf("GetFunction: %v", err)
		}
		run, err := ctx.RunScript(`(keep) => keep(() => 'alive')`, "run.js")
		if err != nil {
			t.Fatalf("RunScript: %v", err)
		}
		if _, err := run.Call(nil, keep); err != nil {
			t.Fatalf("Call: %v", err)
		}
	}()
	for i := 0; i < 5; i++ {
		runtime.GC()
	}
	got, err := kept.Call(nil)
	if err != nil || got.String() != "alive" {
		t.Errorf("calling the kept function: %v, %v", got, err)
	}
}

// A callback V8 runs in a context after its Go Context is disposed, here a function made in
// that context and called from another one, still runs, with a context of its own, which the
// values it keeps go on using after it returns.
func TestCallbackAfterContextDisposed(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()
	first, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	second, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer second.Dispose()

	ran := 0
	var kept *v8.Value
	tmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		ran++
		kept = info.Arg(0)
		s, _ := info.Context().NewString("made")
		if s == nil || s.String() != "made" {
			t.Error("the callback couldn't make a value in its context")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	fn, err := tmpl.GetFunction(first)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	global, err := second.Global()
	if err != nil {
		t.Fatalf("Global: %v", err)
	}
	if err := global.Set("stored", fn); err != nil {
		t.Fatalf("Set: %v", err)
	}
	first.Dispose()
	if _, err := second.RunScript(`stored({ later: () => 'still here' })`, "late.js"); err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if ran != 1 {
		t.Fatalf("the callback ran %d times, want 1", ran)
	}
	for i := 0; i < 5; i++ {
		runtime.GC()
	}
	later, err := kept.Get("later")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := later.Call(nil)
	if err != nil || got.String() != "still here" {
		t.Errorf("using what the callback kept: %v, %v", got, err)
	}
}

// Code that a Go callback runs still gets its context's dynamic import() and rejection
// reports, which find the context's own wrapper: the callback's wrapper is a separate one.
func TestCallbackContextKeepsImportAndRejections(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()
	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	ctx.SetDynamicImportResolver(func(specifier, referrer string) (string, string, error) {
		return "export default 'imported ' + " + "'" + specifier + "';", specifier, nil
	})
	reports, err := ctx.RunScript(`globalThis.reports = []; (event, promise, value) => { reports.push(event + ':' + (value && value.message)); }`, "reports.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if !ctx.SetPromiseRejectHandler(reports) {
		t.Fatal("SetPromiseRejectHandler failed")
	}
	tmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		// Calls back into JavaScript from the callback's own Context.
		if _, err := info.Arg(0).Call(nil); err != nil {
			t.Errorf("calling back: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	global, err := ctx.Global()
	if err != nil {
		t.Fatalf("Global: %v", err)
	}
	if err := global.Set("callGo", fn); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := ctx.RunScript(`
		globalThis.imported = 'pending';
		callGo(() => {
			import('mod').then((m) => { imported = m.default; }, (e) => { imported = 'failed: ' + e; });
			Promise.reject(new Error('unhandled'));
		});
	`, "main.js"); err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	for i := 0; i < 3; i++ {
		ctx.PerformMicrotaskCheckpoint()
	}
	got, err := ctx.RunScript(`imported + ' | ' + reports.join(',')`, "check.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if want := "imported mod | 0:unhandled"; got.String() != want {
		t.Errorf("got %q, want %q", got.String(), want)
	}
}
