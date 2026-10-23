package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRealHTTPClient_DoReturnsBeforeBodyEnds(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: hello\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
			close(released)
		case <-time.After(5 * time.Second):
			t.Errorf("client did not close the streamed body")
		}
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := NewRealHTTPClient().Do(context.Background(), &Request{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Do waited for the response body to finish")
	}
	if resp.BodyReader == nil {
		t.Fatal("expected a streaming BodyReader")
	}
	if len(resp.Body) != 0 {
		t.Fatalf("streaming response should leave Body empty, got %d bytes", len(resp.Body))
	}
	buf := make([]byte, 64)
	n, err := resp.BodyReader.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "data: hello\n\n" {
		t.Fatalf("chunk %q", got)
	}
	if err := resp.BodyReader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the body did not release the server")
	}
}

func TestRealHTTPClient_HeaderTimeoutDoesNotCutBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := NewRealHTTPClientWithTimeout(time.Second).Do(context.Background(), &Request{
		Method: http.MethodGet,
		URL:    srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("Do waited for the body (%s)", time.Since(start))
	}
	body, err := resp.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "late" {
		t.Fatalf("body %q", body)
	}
}

func TestRealHTTPClient_HeaderTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	start := time.Now()
	_, err := NewRealHTTPClientWithTimeout(200*time.Millisecond).Do(context.Background(), &Request{
		Method: http.MethodGet,
		URL:    srv.URL,
	})
	if err == nil {
		t.Fatal("expected a header timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("header timeout took too long: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestRealHTTPClient_RequestTimeoutCoversBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	resp, err := NewRealHTTPClient().Do(context.Background(), &Request{
		Method:  http.MethodGet,
		URL:     srv.URL,
		Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resp.Bytes(); err == nil {
		t.Fatal("expected the body read to hit Request.Timeout")
	}
}

func TestRealHTTPClient_GetBuffersBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	resp, err := NewRealHTTPClient().Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.BodyReader != nil {
		t.Fatal("Get should buffer the body")
	}
	if string(resp.Body) != "hello" {
		t.Fatalf("body %q", resp.Body)
	}
}

func TestSandboxedHTTPClient_StreamRespectsMaxResponseSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	client := NewSandboxedHTTPClient(&SandboxHTTPConfig{
		AllowedHosts:      []string{"127.0.0.1"},
		AllowRealRequests: true,
		MaxResponseSize:   4,
	})
	resp, err := client.Do(context.Background(), &Request{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resp.Bytes()
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("error = %v, want response too large", err)
	}

	exact := NewSandboxedHTTPClient(&SandboxHTTPConfig{
		AllowedHosts:      []string{"127.0.0.1"},
		AllowRealRequests: true,
		MaxResponseSize:   5,
	})
	resp, err = exact.Do(context.Background(), &Request{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	body, err := resp.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body %q", body)
	}
}
