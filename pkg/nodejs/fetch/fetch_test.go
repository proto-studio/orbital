package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/nodejs/abort"
	"proto.zip/studio/orbital/pkg/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/nodejs/timers"
	"proto.zip/studio/orbital/pkg/nodejs/webstream"
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
	for i := 0; i < 20 && result.IsPromise() && result.PromiseState() == v8.PromisePending; i++ {
		rt.Context().PerformMicrotaskCheckpoint()
	}
	if result.IsPromise() {
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

func setupStreamingRuntime(t *testing.T, cfg *runtime.Config) *runtime.Runtime {
	t.Helper()
	rt, err := runtime.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	for _, mod := range []runtime.Module{
		abort.New(),
		buffer.New(),
		webstream.New(),
		timers.New(),
		New(),
	} {
		if err := mod.Register(rt); err != nil {
			rt.Dispose()
			t.Fatalf("Failed to register %s: %v", mod.Name(), err)
		}
	}
	return rt
}

func runJS(t *testing.T, rt *runtime.Runtime, source string) string {
	t.Helper()
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			rt.Kill("test timeout")
		}
	}()
	defer close(done)

	result, err := rt.Run(source, "test.js")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	for i := 0; i < 20 && result != nil && result.IsPromise() && result.PromiseState() == v8.PromisePending; i++ {
		rt.Context().PerformMicrotaskCheckpoint()
	}
	if result != nil && result.IsPromise() {
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
	if result == nil {
		t.Fatal("nil result")
	}
	return result.String()
}

// staticClient is a buffered HTTPClient, the compatibility fallback.
type staticClient struct {
	status  int
	headers map[string][]string
	body    []byte
}

func (s staticClient) Do(context.Context, *runtime.Request) (*runtime.Response, error) {
	status := s.status
	if status == 0 {
		status = 200
	}
	return &runtime.Response{
		StatusCode: status,
		Headers:    s.headers,
		Body:       append([]byte(nil), s.body...),
	}, nil
}

func (s staticClient) Get(ctx context.Context, url string) (*runtime.Response, error) {
	return s.Do(ctx, &runtime.Request{Method: http.MethodGet, URL: url})
}

func (s staticClient) Post(ctx context.Context, url string, contentType string, body []byte) (*runtime.Response, error) {
	return s.Do(ctx, &runtime.Request{Method: http.MethodPost, URL: url, Body: body})
}

// streamClient returns headers immediately and writes the body on a pipe.
type streamClient struct {
	write func(*io.PipeWriter)
}

func (s streamClient) Do(context.Context, *runtime.Request) (*runtime.Response, error) {
	pr, pw := io.Pipe()
	go s.write(pw)
	return &runtime.Response{
		StatusCode: http.StatusOK,
		Headers:    map[string][]string{"content-type": {"text/event-stream"}},
		BodyReader: pr,
	}, nil
}

func (s streamClient) Get(ctx context.Context, url string) (*runtime.Response, error) {
	return s.Do(ctx, &runtime.Request{Method: http.MethodGet, URL: url})
}

func (s streamClient) Post(ctx context.Context, url string, contentType string, body []byte) (*runtime.Response, error) {
	return s.Do(ctx, &runtime.Request{Method: http.MethodPost, URL: url, Body: body})
}

func TestFetch_BufferedClientStillWorks(t *testing.T) {
	rt := setupStreamingRuntime(t, &runtime.Config{
		HTTPClient: staticClient{
			headers: map[string][]string{"content-type": {"application/json"}},
			body:    []byte(`{"ok":true}`),
		},
	})
	defer rt.Dispose()

	got := runJS(t, rt, `(async () => {
		const res = await fetch("http://example.test/item");
		const text = await res.text();
		const res2 = await fetch("http://example.test/item");
		const data = await res2.json();
		const res3 = await fetch("http://example.test/item");
		const reader = res3.body.getReader();
		const first = await reader.read();
		const second = await reader.read();
		const chunk = new TextDecoder().decode(first.value);
		return text + "|" + data.ok + "|" + chunk + "|" + first.done + "|" + second.done;
	})()`)
	if got != `{"ok":true}|true|{"ok":true}|false|true` {
		t.Fatalf("got %q", got)
	}
}

func TestFetch_StreamResolvesBeforeBodyEnds(t *testing.T) {
	released := make(chan struct{})
	var once sync.Once
	rt := setupStreamingRuntime(t, &runtime.Config{
		HTTPClient: streamClient{write: func(pw *io.PipeWriter) {
			_, _ = pw.Write([]byte("data: hello\n\n"))
			// Second write blocks until the reader is closed.
			_, _ = pw.Write([]byte("more"))
			once.Do(func() { close(released) })
			_ = pw.Close()
		}},
	})
	defer rt.Dispose()

	start := time.Now()
	got := runJS(t, rt, `(async () => {
		const res = await fetch("http://example.test/events", {
			headers: { Accept: "text/event-stream" }
		});
		if (!res.body || typeof res.body.getReader !== "function") {
			throw new Error("response.body is not a ReadableStream");
		}
		const reader = res.body.getReader();
		const { value, done } = await reader.read();
		if (done) throw new Error("stream ended before the first chunk");
		const text = new TextDecoder().decode(value);
		reader.releaseLock();
		await res.body.cancel();
		return text;
	})()`)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("fetch blocked on the open body (%s)", time.Since(start))
	}
	if got != "data: hello\n\n" {
		t.Fatalf("chunk %q", got)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the stream did not close the body reader")
	}
}

func TestFetch_RealClientStreamsAndCancelCloses(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/none":
			w.WriteHeader(http.StatusNoContent)
			return
		case "/empty":
			w.WriteHeader(http.StatusOK)
			return
		case "/bytes":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{'h', 'i', 0xff})
			return
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: hello\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
				close(released)
			case <-time.After(5 * time.Second):
				t.Errorf("client did not disconnect")
			}
		}
	}))
	defer srv.Close()

	rt := setupStreamingRuntime(t, nil)
	defer rt.Dispose()

	start := time.Now()
	got := runJS(t, rt, `(async () => {
		const base = `+"`"+srv.URL+"`"+`;
		const res = await fetch(base + "/events", { headers: { Accept: "text/event-stream" } });
		const reader = res.body.getReader();
		const { value, done } = await reader.read();
		if (done) throw new Error("expected a chunk");
		const text = new TextDecoder().decode(value);
		await reader.cancel();

		const none = await fetch(base + "/none");
		const head = await fetch(base + "/empty", { method: "HEAD" });
		const empty = await fetch(base + "/empty");
		const emptyReader = empty.body.getReader();
		const emptyRead = await emptyReader.read();
		const emptyText = await (await fetch(base + "/empty")).text();

		const bytes = new Uint8Array(await (await fetch(base + "/bytes")).arrayBuffer());
		const doc = await fetch(base + "/json");
		let streamed = "";
		for await (const chunk of doc.body) {
			streamed += new TextDecoder().decode(chunk);
		}
		const again = await (await fetch(base + "/json")).json();
		return [
			text,
			none.body === null,
			head.body === null,
			empty.body !== null,
			emptyRead.done,
			JSON.stringify(emptyText),
			Array.from(bytes).join(","),
			streamed,
			again.ok
		].join("|");
	})()`)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("fetch blocked on the open body (%s)", time.Since(start))
	}
	want := "data: hello\n\n|true|true|true|true|\"\"|104,105,255|{\"ok\":true}|true"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("reader.cancel() did not close the connection")
	}
}

func TestFetch_AbortCancelsRequestAndBody(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var startOnce sync.Once
	var cancelOnce sync.Once
	rt := setupStreamingRuntime(t, &runtime.Config{
		HTTPClient: &blockingClient{started: started, cancelled: cancelled, startOnce: &startOnce, cancelOnce: &cancelOnce},
	})
	defer rt.Dispose()

	got := runJS(t, rt, `(async () => {
		const ac = new AbortController();
		const pending = fetch("http://example.test/block", { signal: ac.signal });
		ac.abort();
		try {
			await pending;
			return "resolved";
		} catch (e) {
			return e.name + ":" + e.message;
		}
	})()`)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("abort did not cancel the HTTP client context")
	}
	if got != "AbortError:This operation was aborted" {
		t.Fatalf("got %q", got)
	}

	// Abort after headers errors the pending body read and closes the reader.
	bodyClosed := make(chan struct{})
	var bodyOnce sync.Once
	rt2 := setupStreamingRuntime(t, &runtime.Config{
		HTTPClient: streamClient{write: func(pw *io.PipeWriter) {
			_, _ = pw.Write([]byte("data: one\n\n"))
			_, _ = pw.Write([]byte("more"))
			bodyOnce.Do(func() { close(bodyClosed) })
			_ = pw.Close()
		}},
	})
	defer rt2.Dispose()

	got = runJS(t, rt2, `(async () => {
		const ac = new AbortController();
		const res = await fetch("http://example.test/events", { signal: ac.signal });
		const reader = res.body.getReader();
		const first = await reader.read();
		const pending = reader.read();
		ac.abort();
		let name = "none";
		try {
			await pending;
		} catch (e) {
			name = e.name;
		}
		return new TextDecoder().decode(first.value) + "|" + name;
	})()`)
	if got != "data: one\n\n|AbortError" {
		t.Fatalf("got %q", got)
	}
	select {
	case <-bodyClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("abort did not close the response body")
	}
}

func TestFetch_DeadlineReleasesBody(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: hello\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
			close(released)
		case <-time.After(5 * time.Second):
			t.Errorf("deadline did not release the body")
		}
	}))
	defer srv.Close()

	// The deadline starts when the runtime is created, before modules register,
	// so leave room for setup and still release a body the script never cancels.
	rt := setupStreamingRuntime(t, &runtime.Config{Timeout: 2 * time.Second})
	defer rt.Dispose()

	got := runJS(t, rt, `(async () => {
		const res = await fetch("`+srv.URL+`");
		const reader = res.body.getReader();
		const { value } = await reader.read();
		return new TextDecoder().decode(value);
	})()`)
	if got != "data: hello\n\n" {
		t.Fatalf("chunk %q", got)
	}
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("invoke deadline did not release the response body")
	}
}

// blockingClient waits in Do until ctx is cancelled.
type blockingClient struct {
	started    chan struct{}
	cancelled  chan struct{}
	startOnce  *sync.Once
	cancelOnce *sync.Once
}

func (c *blockingClient) Do(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-ctx.Done()
	c.cancelOnce.Do(func() { close(c.cancelled) })
	return nil, ctx.Err()
}

func (c *blockingClient) Get(ctx context.Context, url string) (*runtime.Response, error) {
	return c.Do(ctx, &runtime.Request{Method: http.MethodGet, URL: url})
}

func (c *blockingClient) Post(ctx context.Context, url string, contentType string, body []byte) (*runtime.Response, error) {
	return c.Do(ctx, &runtime.Request{Method: http.MethodPost, URL: url, Body: body})
}
