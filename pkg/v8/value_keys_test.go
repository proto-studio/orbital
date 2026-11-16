package v8_test

import (
	"testing"

	"proto.zip/studio/orbital/pkg/v8"
)

// GetOwnNonIndexKeys lists own enumerable keys other than array indices, strings then symbols,
// leaving out inherited and non-enumerable ones. Keys that aren't valid indices stay.
func TestGetOwnNonIndexKeys(t *testing.T) {
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

	arr, err := ctx.RunScript(`
		const a = [1, 2];
		Object.setPrototypeOf(a, Object.assign(Object.create(Array.prototype), { inherited: 1 }));
		a.x = 1;
		a[Symbol('s')] = 2;
		a['4294967295'] = 3;
		Object.defineProperty(a, 'hidden', { value: 4 });
		a;
	`, "keys.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	keys, err := arr.GetOwnNonIndexKeys()
	if err != nil || keys == nil {
		t.Fatalf("GetOwnNonIndexKeys: %v, %v", keys, err)
	}
	describe, err := ctx.RunScript(`(keys) => keys.map(String).join(',')`, "describe.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	got, err := describe.Call(nil, keys)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if want := "x,4294967295,Symbol(s)"; got.String() != want {
		t.Errorf("got %q, want %q", got.String(), want)
	}
}

// OwnNonIndexKeysFunction answers the same keys from JavaScript, and a released value is gone.
func TestOwnNonIndexKeysFunction(t *testing.T) {
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

	fn, err := ctx.OwnNonIndexKeysFunction()
	if err != nil {
		t.Fatalf("OwnNonIndexKeysFunction: %v", err)
	}
	check, err := ctx.RunScript(`(keys) => [keys(Object.assign([1, 2], { x: 1 })).join(), String(keys(5))].join('|')`, "check.js")
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	got, err := check.Call(nil, fn)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if want := "x|undefined"; got.String() != want {
		t.Errorf("got %q, want %q", got.String(), want)
	}
	got.Release()
	got.Release()
	if got.IsString() {
		t.Error("a released value still reads as a string")
	}
}
