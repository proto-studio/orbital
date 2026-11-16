package v8_test

import (
	"errors"
	"testing"

	"proto.zip/studio/orbital/pkg/v8"
)

// A call that throws hands Go the value thrown when asked, which a Go callback can throw on
// unchanged; a plain Call doesn't keep it.
func TestCallErrorCarriesThrownValue(t *testing.T) {
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

	thrower, err := ctx.RunScript(`() => { throw { code: 'E_MINE' }; }`, "thrower.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	var plain *v8.JSError
	if _, err := thrower.Call(nil); !errors.As(err, &plain) || plain.Value != nil {
		t.Errorf("a plain Call kept the thrown value: %v", err)
	}
	tmpl, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		_, err := thrower.CallKeepingThrown(nil)
		var jsErr *v8.JSError
		if !errors.As(err, &jsErr) || jsErr.Value == nil {
			t.Errorf("the call's error has no thrown value: %v", err)
			return nil
		}
		info.Context().ThrowValue(jsErr.Value)
		jsErr.Value.Release()
		return nil
	})
	if err != nil {
		t.Fatalf("NewFunctionTemplate: %v", err)
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	check, err := ctx.RunScript(`(fn) => { try { fn(); return 'no throw'; } catch (e) { return e.code; } }`, "check.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	got, err := check.Call(nil, fn)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got.String() != "E_MINE" {
		t.Errorf("got %q, want the thrown object's code E_MINE", got.String())
	}
}
