package nodejs

import (
	"errors"
	"fmt"
	"net"
	"os"
	goruntime "runtime"
	"syscall"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// inheritedListener opens a listening socket and returns a descriptor for it that stays
// open for the test, as one a supervisor passes to a process would.
func inheritedListener(t *testing.T) (fd int, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	file, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	t.Cleanup(func() { file.Close() })
	return int(file.Fd()), ln.Addr().(*net.TCPAddr).Port
}

const inheritedScript = `
const net = require('net');
const server = net.createServer((socket) => socket.end('hello'));
server.on('error', (e) => done({ error: e.code, message: e.message, address: e.address }));
server.listen(%s, () => {
  const address = server.address();
  net.connect(address.port, '127.0.0.1').on('data', (d) => {
    server.close();
    done({ listening: address.port === %d, reply: String(d) });
  });
});`

// server.listen({ fd }) works on a listening socket the embedder made available, through
// the same policy as any listen, and on nothing else.
func TestNet_ListenOnInheritedSocket(t *testing.T) {
	t.Run("an allowed descriptor", func(t *testing.T) {
		fd, port := inheritedListener(t)
		factory := runtime.NewRealSocketFactory()
		factory.AllowInheritedListeners(fd)
		got := runJS(t, &runtime.Config{SocketFactory: factory}, fmt.Sprintf(inheritedScript, fmt.Sprintf("{ fd: %d }", fd), port))
		wantJSON(t, got, map[string]any{"listening": true, "reply": "hello"})
	})
	t.Run("as a handle", func(t *testing.T) {
		fd, port := inheritedListener(t)
		factory := runtime.NewRealSocketFactory()
		factory.AllowInheritedListeners(fd)
		got := runJS(t, &runtime.Config{SocketFactory: factory}, fmt.Sprintf(inheritedScript, fmt.Sprintf("{ handle: { fd: %d } }", fd), port))
		wantJSON(t, got, map[string]any{"listening": true, "reply": "hello"})
	})
	t.Run("through a policy that allows it", func(t *testing.T) {
		fd, port := inheritedListener(t)
		factory := runtime.NewRealSocketFactory()
		factory.AllowInheritedListeners(fd)
		filtered := runtime.NewFilteredSocketFactory(factory, runtime.NewAllowAllPolicy())
		got := runJS(t, &runtime.Config{SocketFactory: filtered}, fmt.Sprintf(inheritedScript, fmt.Sprintf("{ fd: %d }", fd), port))
		wantJSON(t, got, map[string]any{"listening": true, "reply": "hello"})
	})
	t.Run("a policy that refuses it", func(t *testing.T) {
		fd, port := inheritedListener(t)
		factory := runtime.NewRealSocketFactory()
		factory.AllowInheritedListeners(fd)
		filtered := runtime.NewFilteredSocketFactory(factory, runtime.NewDenyAllPolicy())
		got := runJS(t, &runtime.Config{SocketFactory: filtered}, fmt.Sprintf(inheritedScript, fmt.Sprintf("{ fd: %d }", fd), port))
		if got["error"] == nil || got["listening"] != nil {
			t.Fatalf("result = %v", got)
		}
	})
	t.Run("a descriptor that wasn't allowed", func(t *testing.T) {
		fd, port := inheritedListener(t)
		for _, factory := range []runtime.SocketFactory{
			nil,
			runtime.NewRealSocketFactory(),
			runtime.NewFilteredSocketFactory(runtime.NewRealSocketFactory(), runtime.NewAllowAllPolicy()),
		} {
			got := runJS(t, &runtime.Config{SocketFactory: factory}, fmt.Sprintf(inheritedScript, fmt.Sprintf("{ fd: %d }", fd), port))
			// As Node reports a descriptor it can't listen on.
			wantJSON(t, got, map[string]any{"error": "EINVAL", "message": "listen EINVAL: invalid argument", "address": nil})
		}
	})
}

// A descriptor that isn't a TCP listener is refused.
func TestRealSocketFactory_ListenInheritedOnlyTCP(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	factory := runtime.NewRealSocketFactory()
	factory.AllowInheritedListeners(int(r.Fd()))
	if _, err := factory.ListenInherited(int(r.Fd())); err == nil {
		t.Fatal("listened on a pipe")
	}
	if _, err := factory.ListenInherited(int(w.Fd()) + 100); !errors.Is(err, runtime.ErrNotInherited) {
		t.Fatalf("err = %v", err)
	}
}

// A connected socket's descriptor is refused even if allowed, and listening leaves the
// embedder's descriptor open: only a copy is used.
func TestRealSocketFactory_ListenInheritedChecksAndKeepsTheDescriptor(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	connected, err := client.(*net.TCPConn).File()
	if err != nil {
		t.Fatal(err)
	}
	defer connected.Close()

	fd, _ := inheritedListener(t)
	func() {
		factory := runtime.NewRealSocketFactory()
		factory.AllowInheritedListeners(int(connected.Fd()), fd)
		if _, err := factory.ListenInherited(int(connected.Fd())); !errors.Is(err, runtime.ErrNotInherited) {
			t.Fatalf("connected socket: err = %v", err)
		}
		for i := 0; i < 2; i++ {
			server, err := factory.ListenInherited(fd)
			if err != nil {
				t.Fatalf("listen %d: %v", i, err)
			}
			server.Close()
		}
	}()
	// Nothing the factory made may close the embedder's descriptor once it is collected.
	goruntime.GC()
	goruntime.GC()
	if _, err := syscall.Getsockname(fd); err != nil {
		t.Fatalf("the original descriptor was closed: %v", err)
	}
}

// An invalid descriptor throws before the server is changed, so it can listen afterwards
// and the rejected call's callback never runs.
func TestNet_ListenWithBadDescriptorLeavesServerAlone(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const net = require('net');
const server = net.createServer();
const calls = [];
let error;
try { server.listen({ fd: -1 }, () => calls.push('rejected')); } catch (e) { error = e.code; }
server.listen(0, '127.0.0.1', () => {
  calls.push('listening');
  server.close(() => setImmediate(() => done({ error, calls })));
});`)
	wantJSON(t, got, map[string]any{"error": "ERR_INVALID_ARG_VALUE", "calls": []any{"listening"}})
}
