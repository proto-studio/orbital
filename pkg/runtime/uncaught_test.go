package runtime

import (
	"errors"
	"testing"
)

// A runtime with no process object has no handlers to run, so an exception ends it.
func TestMakeCallbackWithoutProcess(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()

	thrower, err := rt.RunScript(`(function (message) { throw new Error(message); })`, "thrower.js")
	if err != nil {
		t.Fatal(err)
	}
	ran, err := rt.RunScript(`globalThis.ran = false; (function () { globalThis.ran = true; })`, "ran.js")
	if err != nil {
		t.Fatal(err)
	}
	rt.EventLoop().EnqueueMicrotask(func() {
		message, _ := rt.Context().NewString("boom")
		rt.MakeCallback(thrower, nil, message)
		rt.MakeCallback(ran, nil)
	})

	_, err = rt.Run(`1`, "main.js")
	var uncaught *UncaughtError
	if !errors.As(err, &uncaught) {
		t.Fatalf("Run returned %v, want an UncaughtError", err)
	}
	if uncaught.ExitCode != 1 || uncaught.Origin != "uncaughtException" {
		t.Errorf("got %+v", uncaught)
	}
	if rt.UncaughtError() != uncaught {
		t.Error("UncaughtError does not return what Run did")
	}
	val, err := rt.GetGlobal("ran")
	if err != nil {
		t.Fatal(err)
	}
	if val.Boolean() {
		t.Error("a callback ran after the runtime ended")
	}
}

func TestRunStillReportsTerminationAsTermination(t *testing.T) {
	rt, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	rt.Kill("test")
	if _, err := rt.Run(`1`, "main.js"); err != nil {
		var uncaught *UncaughtError
		if errors.As(err, &uncaught) {
			t.Fatalf("a killed runtime reported an uncaught exception: %v", err)
		}
	}
}
