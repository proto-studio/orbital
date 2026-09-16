package fetch

import (
	"testing"

	"proto.zip/studio/orbital/pkg/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	// Buffer polyfills TextEncoder/TextDecoder, which body decoding uses.
	if err := buffer.New().Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register buffer module: %v", err)
	}

	if err := New().Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register fetch module: %v", err)
	}

	return rt
}

func awaitString(t *testing.T, rt *runtime.Runtime, source string) string {
	t.Helper()
	result, err := rt.RunScript(source, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if result.IsPromise() {
		rt.Context().PerformMicrotaskCheckpoint()
		switch result.PromiseState() {
		case v8.PromiseFulfilled:
			result = result.PromiseResult()
		case v8.PromiseRejected:
			reason := result.PromiseResult()
			msg := "promise rejected"
			if reason != nil {
				msg = reason.String()
			}
			t.Fatalf("%s", msg)
		default:
			t.Fatal("promise still pending")
		}
	}
	return result.String()
}

func TestResponse_Text_Uint8Array(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	got := awaitString(t, rt, `new Response(new Uint8Array([104, 105])).text()`)
	if got != "hi" {
		t.Errorf("Response.text() of Uint8Array([104, 105]) should be %q, got %q", "hi", got)
	}
}

func TestResponse_Text_ArrayBuffer(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	got := awaitString(t, rt, `new Response(new Uint8Array([104, 105]).buffer).text()`)
	if got != "hi" {
		t.Errorf("Response.text() of ArrayBuffer should be %q, got %q", "hi", got)
	}
}

func TestResponse_Text_String(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	got := awaitString(t, rt, `new Response("hello").text()`)
	if got != "hello" {
		t.Errorf("Response.text() of string should be %q, got %q", "hello", got)
	}
}

func TestResponse_Text_Null(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	got := awaitString(t, rt, `new Response(null).text()`)
	if got != "" {
		t.Errorf("Response.text() of null should be empty, got %q", got)
	}
}

func TestRequest_Text_Uint8Array(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	got := awaitString(t, rt, `new Request("http://example.com", { method: "POST", body: new Uint8Array([104, 105]) }).text()`)
	if got != "hi" {
		t.Errorf("Request.text() of Uint8Array([104, 105]) should be %q, got %q", "hi", got)
	}
}

func TestResponse_ArrayBuffer_Uint8Array(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	got := awaitString(t, rt, `
		new Response(new Uint8Array([255, 254, 0, 104])).arrayBuffer()
			.then(buf => Array.from(new Uint8Array(buf)).join(','))
	`)
	if got != "255,254,0,104" {
		t.Errorf("Response.arrayBuffer() should return original bytes, got %q", got)
	}
}
