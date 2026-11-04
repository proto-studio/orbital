// Package net implements the Node.js net module for TCP and Unix domain sockets.
package net

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"

	"proto.zip/studio/orbital/pkg/nodejs/internal/netdial"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed net.js
var netJS string

const readBufferSize = 65536

var errUnixDenied = errors.New("EACCES: unix sockets are not available in this sandbox")

// Net provides TCP and Unix domain socket networking.
type Net struct {
	rt       *runtime.Runtime
	mu       sync.Mutex
	conns    map[int64]*Conn
	servers  map[int64]*server
	nextConn int64
	nextSrv  int64
}

// New creates a new Net module.
func New() *Net {
	return &Net{
		conns:   make(map[int64]*Conn),
		servers: make(map[int64]*server),
	}
}

// Name returns the module name.
func (n *Net) Name() string {
	return "net"
}

// Register sets up the net module.
func (n *Net) Register(rt *runtime.Runtime) error {
	n.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	funcs := map[string]v8.FunctionCallback{
		"createSocket":     n.createSocketFunc,
		"connect":          n.connectFunc,
		"connectUnix":      n.connectUnixFunc,
		"write":            n.writeFunc,
		"read":             n.readFunc,
		"shutdown":         n.shutdownFunc,
		"close":            n.closeFunc,
		"setKeepAlive":     n.setKeepAliveFunc,
		"setNoDelay":       n.setNoDelayFunc,
		"setLinger":        n.setLingerFunc,
		"getAddressInfo":   n.getAddressInfoFunc,
		"ref":              n.refFunc,
		"unref":            n.unrefFunc,
		"createServer":     n.createServerFunc,
		"listen":           n.listenFunc,
		"listenUnix":       n.listenUnixFunc,
		"accept":           n.acceptFunc,
		"closeServer":      n.closeServerFunc,
		"getServerAddress": n.getServerAddressFunc,
		"refServer":        n.refServerFunc,
		"unrefServer":      n.unrefServerFunc,
	}

	for name, fn := range funcs {
		tmpl, err := iso.NewFunctionTemplate(fn)
		if err != nil {
			return err
		}
		val, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := internal.Set(name, val); err != nil {
			return err
		}
	}

	if err := rt.SetGlobal("__net_internal", internal); err != nil {
		return err
	}

	_, err = rt.RunScript(netJS, "net.js")
	return err
}

// Conn is one socket as JavaScript sees it. Its transport can change while the socket
// stays the same object, which is how node:tls upgrades a connection.
type Conn struct {
	n      *Net
	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	socket     runtime.TCPSocket
	attempt    runtime.TCPSocket
	closed     bool
	ref        bool
	reading    int
	held       bool
	resourceID uint64
	readErr    error
	diverted   chan divertedRead
}

type divertedRead struct {
	data []byte
	err  error
}

func (n *Net) newConn() (int64, *Conn) {
	ctx, cancel := context.WithCancel(n.rt.ExecutionContext())
	conn := &Conn{n: n, ctx: ctx, cancel: cancel, ref: true}
	conn.resourceID = n.rt.TrackResource(conn)

	n.mu.Lock()
	defer n.mu.Unlock()
	n.nextConn++
	n.conns[n.nextConn] = conn
	return n.nextConn, conn
}

// Conn returns the socket with the given JavaScript id.
func (n *Net) Conn(id int64) (*Conn, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	conn, ok := n.conns[id]
	return conn, ok
}

// Context ends when the socket closes.
func (c *Conn) Context() context.Context { return c.ctx }

// Socket returns the socket's current transport.
func (c *Conn) Socket() runtime.TCPSocket {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.socket
}

// SetSocket replaces the socket's transport. It closes the replacement if the socket has
// already closed.
func (c *Conn) SetSocket(socket runtime.TCPSocket) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		socket.Close()
		return
	}
	c.socket = socket
	c.diverted = nil
	c.mu.Unlock()
}

// BeginUpgrade returns a net.Conn over the socket's current transport for a protocol
// layered on top of it. A read already waiting on the transport is handed to the new
// layer instead of JavaScript, so no bytes are lost.
func (c *Conn) BeginUpgrade() (net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.socket == nil {
		return nil, runtime.ErrSocketClosed
	}
	conn := runtime.NewSocketConn(c.socket)
	if c.reading == 0 {
		return conn, nil
	}
	c.diverted = make(chan divertedRead, 1)
	return &divertedConn{Conn: conn, first: c.diverted}, nil
}

type divertedConn struct {
	net.Conn
	first   chan divertedRead
	pending []byte
	err     error
	drained bool
}

func (d *divertedConn) Read(b []byte) (int, error) {
	if !d.drained {
		result := <-d.first
		d.drained = true
		d.pending, d.err = result.data, result.err
	}
	if len(d.pending) > 0 {
		n := copy(b, d.pending)
		d.pending = d.pending[n:]
		return n, nil
	}
	if d.err != nil {
		err := d.err
		d.err = nil
		return 0, err
	}
	return d.Conn.Read(b)
}

// updateHold keeps the event loop running while a read is outstanding on a referenced
// socket. A paused or unreferenced socket does not keep the process alive, as in Node.
func (c *Conn) updateHoldLocked() {
	want := c.reading > 0 && c.ref && !c.closed
	if want == c.held {
		return
	}
	c.held = want
	if want {
		c.n.rt.EventLoop().AddPendingWork()
	} else {
		c.n.rt.EventLoop().DonePendingWork()
	}
}

// Close closes the socket. It implements io.Closer for the runtime's resource tracker.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.updateHoldLocked()
	socket := c.socket
	attempt := c.attempt
	c.socket = nil
	c.attempt = nil
	c.mu.Unlock()

	c.cancel()
	var err error
	if attempt != nil && attempt != socket {
		attempt.Close()
	}
	if socket != nil {
		err = socket.Close()
	}
	return err
}

func (n *Net) deliver(fn func(ctx *v8.Context)) {
	n.rt.EventLoop().EnqueueMicrotask(func() {
		ctx := n.rt.Context()
		fn(ctx)
		ctx.PerformMicrotaskCheckpoint()
	})
}

func jsString(ctx *v8.Context, s string) *v8.Value {
	v, err := ctx.NewString(s)
	if err != nil {
		return ctx.Null()
	}
	return v
}

func jsJSON(ctx *v8.Context, v any) *v8.Value {
	encoded, err := json.Marshal(v)
	if err != nil {
		return ctx.Null()
	}
	return jsString(ctx, string(encoded))
}

func (n *Net) createSocketFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	id, _ := n.newConn()
	return info.Context().NewNumber(float64(id))
}

type connectOptions struct {
	Family       int    `json:"family"`
	LocalAddress string `json:"localAddress"`
	LocalPort    int    `json:"localPort"`
}

type connectResult struct {
	Error  *netdial.NodeError `json:"error,omitempty"`
	IP     string             `json:"ip,omitempty"`
	IsName bool               `json:"isName"`
}

// connect(id, host, port, optionsJSON, callback) connects through the injected resolver
// and socket factory. callback receives a JSON connectResult.
func (n *Net) connectFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 5 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	if !ok {
		return nil
	}
	host := args[1].String()
	port := int(args[2].Integer())
	var opts connectOptions
	if err := json.Unmarshal([]byte(args[3].String()), &opts); err != nil {
		return info.Context().Throw("net: invalid connect options")
	}
	callback := args[4]

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		result, err := netdial.Connect(conn.ctx, n.rt, host, port, netdial.Options{
			Family:       opts.Family,
			LocalAddress: opts.LocalAddress,
			LocalPort:    opts.LocalPort,
			Prepare: func(socket runtime.TCPSocket) {
				conn.mu.Lock()
				conn.attempt = socket
				conn.mu.Unlock()
			},
		})

		var out connectResult
		if err != nil {
			desc := netdial.Describe(err, "connect", host, port)
			out.Error = &desc
		} else {
			conn.mu.Lock()
			closed := conn.closed
			if !closed {
				conn.socket = result.Socket
			}
			conn.attempt = nil
			conn.mu.Unlock()
			if closed {
				result.Socket.Close()
				return
			}
			out.IP = result.IP
			out.IsName = result.IsName
		}

		n.deliver(func(ctx *v8.Context) {
			callback.Call(ctx.Undefined(), jsJSON(ctx, out))
		})
	}()
	return nil
}

// unixFactory returns the factory for Unix domain sockets if this runtime offers them.
// A socket path reaches local services through the filesystem, so a runtime whose
// filesystem is sandboxed, or whose factory does not opt in, does not offer them.
func (n *Net) unixFactory() (runtime.UnixSocketFactory, error) {
	factory := runtime.UnixSocketsFor(n.rt.SocketFactory())
	if factory == nil || !runtime.FilesystemIsUnrestricted(n.rt.Filesystem()) {
		return nil, errUnixDenied
	}
	return factory, nil
}

// connectUnix(id, path, callback) connects to a Unix domain socket.
func (n *Net) connectUnixFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	if !ok {
		return nil
	}
	path := args[1].String()
	callback := args[2]

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		factory, err := n.unixFactory()
		var socket runtime.TCPSocket
		if err == nil {
			socket = factory.CreateUnixSocket()
			conn.mu.Lock()
			conn.attempt = socket
			conn.mu.Unlock()
			err = socket.Connect(conn.ctx, path, 0)
		}

		var out connectResult
		if err != nil {
			desc := netdial.Describe(err, "connect", path, 0)
			out.Error = &desc
		} else {
			conn.mu.Lock()
			closed := conn.closed
			if !closed {
				conn.socket = socket
			}
			conn.attempt = nil
			conn.mu.Unlock()
			if closed {
				socket.Close()
				return
			}
		}
		n.deliver(func(ctx *v8.Context) {
			callback.Call(ctx.Undefined(), jsJSON(ctx, out))
		})
	}()
	return nil
}

// write(id, base64Data, callback) writes bytes. They cross the boundary as base64 because
// a JavaScript string cannot carry them unchanged. callback receives a JSON error or null.
func (n *Net) writeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	callback := args[2]
	if !ok {
		return n.failSocketClosed(callback, "write")
	}
	data, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		return info.Context().Throw("net: invalid write data")
	}
	socket := conn.Socket()
	if socket == nil {
		return n.failSocketClosed(callback, "write")
	}

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()
		_, err := socket.Write(data)
		n.deliver(func(ctx *v8.Context) {
			if err != nil {
				desc := netdial.Describe(err, "write", "", 0)
				callback.Call(ctx.Undefined(), jsJSON(ctx, desc))
				return
			}
			callback.Call(ctx.Undefined(), ctx.Null())
		})
	}()
	return nil
}

func (n *Net) failSocketClosed(callback *v8.Value, syscallName string) *v8.Value {
	n.deliver(func(ctx *v8.Context) {
		desc := netdial.NodeError{Message: "Socket is closed", Code: "ERR_SOCKET_CLOSED", Syscall: syscallName}
		callback.Call(ctx.Undefined(), jsJSON(ctx, desc))
	})
	return nil
}

// read(id, callback) waits for data. callback receives (jsonError, base64Data); both null
// mean the end of the stream. JavaScript only asks when it wants more, which is how a
// paused or full socket applies backpressure to the peer.
func (n *Net) readFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	callback := args[1]
	if !ok {
		return nil
	}

	conn.mu.Lock()
	socket := conn.socket
	if socket == nil || conn.closed {
		conn.mu.Unlock()
		return nil
	}
	conn.reading++
	conn.updateHoldLocked()
	pending := conn.readErr
	conn.readErr = nil
	conn.mu.Unlock()

	go func() {
		buf := make([]byte, readBufferSize)
		n2, err := 0, pending
		if err == nil {
			n2, err = socket.Read(buf)
		}

		conn.mu.Lock()
		if conn.diverted != nil {
			diverted := conn.diverted
			conn.diverted = nil
			conn.reading--
			conn.updateHoldLocked()
			conn.mu.Unlock()
			diverted <- divertedRead{data: buf[:n2], err: err}
			return
		}
		if conn.closed {
			conn.reading--
			conn.updateHoldLocked()
			conn.mu.Unlock()
			return
		}
		// Data can arrive with an error. The data goes first and the next read reports it.
		if n2 > 0 && err != nil {
			conn.readErr = err
			err = nil
		}
		conn.mu.Unlock()

		var encoded string
		if n2 > 0 {
			encoded = base64.StdEncoding.EncodeToString(buf[:n2])
		}
		n.deliver(func(ctx *v8.Context) {
			switch {
			case n2 > 0:
				callback.Call(ctx.Undefined(), ctx.Null(), jsString(ctx, encoded))
			case err == nil, errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
				errors.Is(err, net.ErrClosed), errors.Is(err, runtime.ErrSocketClosed):
				callback.Call(ctx.Undefined(), ctx.Null(), ctx.Null())
			default:
				desc := netdial.Describe(err, "read", "", 0)
				callback.Call(ctx.Undefined(), jsJSON(ctx, desc), ctx.Null())
			}
		})

		conn.mu.Lock()
		conn.reading--
		conn.updateHoldLocked()
		conn.mu.Unlock()
	}()
	return nil
}

// shutdown(id, callback) finishes the sending side and leaves the receiving side open. A
// socket from a factory that cannot half-close is closed outright, which still delivers
// what was written but ends the read side too. The peer being gone already is not an
// error, as in Node.
func (n *Net) shutdownFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	callback := args[1]
	var socket runtime.TCPSocket
	if ok {
		socket = conn.Socket()
	}
	if socket == nil {
		n.deliver(func(ctx *v8.Context) { callback.Call(ctx.Undefined()) })
		return nil
	}

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()
		if errors.Is(runtime.CloseWrite(socket), errors.ErrUnsupported) {
			conn.Close()
		}
		n.deliver(func(ctx *v8.Context) { callback.Call(ctx.Undefined()) })
	}()
	return nil
}

// close(id) closes the socket without blocking JavaScript. crypto/tls can take seconds to
// give up on a stalled peer, and the event loop stays alive until the close has gone out.
func (n *Net) closeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	id := int64(args[0].Integer())

	n.mu.Lock()
	conn, ok := n.conns[id]
	delete(n.conns, id)
	n.mu.Unlock()
	if !ok {
		return nil
	}

	n.rt.UntrackResource(conn.resourceID)
	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()
		conn.Close()
	}()
	return nil
}

func (n *Net) setKeepAliveFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) >= 2 {
		if conn, ok := n.Conn(int64(args[0].Integer())); ok {
			if socket := conn.Socket(); socket != nil {
				socket.SetKeepAlive(args[1].Boolean())
			}
		}
	}
	return nil
}

func (n *Net) setNoDelayFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) >= 2 {
		if conn, ok := n.Conn(int64(args[0].Integer())); ok {
			if socket := conn.Socket(); socket != nil {
				socket.SetNoDelay(args[1].Boolean())
			}
		}
	}
	return nil
}

func (n *Net) setLingerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) >= 2 {
		if conn, ok := n.Conn(int64(args[0].Integer())); ok {
			if linger, ok := conn.Socket().(runtime.LingerSetter); ok {
				linger.SetLinger(int(args[1].Integer()))
			}
		}
	}
	return nil
}

type addressInfo struct {
	LocalAddress  string `json:"localAddress,omitempty"`
	LocalPort     int    `json:"localPort,omitempty"`
	LocalFamily   string `json:"localFamily,omitempty"`
	RemoteAddress string `json:"remoteAddress,omitempty"`
	RemotePort    int    `json:"remotePort,omitempty"`
	RemoteFamily  string `json:"remoteFamily,omitempty"`
}

func familyOf(ip net.IP) string {
	if ip.To4() != nil {
		return "IPv4"
	}
	return "IPv6"
}

func describeAddresses(socket runtime.TCPSocket) addressInfo {
	var out addressInfo
	if addr, ok := socket.LocalAddr().(*net.TCPAddr); ok {
		out.LocalAddress = addr.IP.String()
		out.LocalPort = addr.Port
		out.LocalFamily = familyOf(addr.IP)
	}
	if addr, ok := socket.RemoteAddr().(*net.TCPAddr); ok {
		out.RemoteAddress = addr.IP.String()
		out.RemotePort = addr.Port
		out.RemoteFamily = familyOf(addr.IP)
	}
	return out
}

func (n *Net) getAddressInfoFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Null()
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	if !ok {
		return ctx.Null()
	}
	socket := conn.Socket()
	if socket == nil {
		return ctx.Null()
	}
	return jsJSON(ctx, describeAddresses(socket))
}

func (n *Net) setConnRef(info *v8.FunctionCallbackInfo, ref bool) *v8.Value {
	args := info.Args()
	if len(args) >= 1 {
		if conn, ok := n.Conn(int64(args[0].Integer())); ok {
			conn.mu.Lock()
			conn.ref = ref
			conn.updateHoldLocked()
			conn.mu.Unlock()
		}
	}
	return nil
}

func (n *Net) refFunc(info *v8.FunctionCallbackInfo) *v8.Value   { return n.setConnRef(info, true) }
func (n *Net) unrefFunc(info *v8.FunctionCallbackInfo) *v8.Value { return n.setConnRef(info, false) }

// server is one listening socket.
type server struct {
	n          *Net
	mu         sync.Mutex
	srv        runtime.TCPServer
	accepting  int
	ref        bool
	held       bool
	closed     bool
	resourceID uint64
}

func (s *server) updateHoldLocked() {
	want := s.accepting > 0 && s.ref && !s.closed
	if want == s.held {
		return
	}
	s.held = want
	if want {
		s.n.rt.EventLoop().AddPendingWork()
	} else {
		s.n.rt.EventLoop().DonePendingWork()
	}
}

// Close stops the server. It implements io.Closer for the runtime's resource tracker.
func (s *server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.updateHoldLocked()
	srv := s.srv
	s.mu.Unlock()
	if srv != nil {
		return srv.Close()
	}
	return nil
}

func (n *Net) createServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	s := &server{n: n, ref: true}
	s.resourceID = n.rt.TrackResource(s)
	n.mu.Lock()
	n.nextSrv++
	id := n.nextSrv
	n.servers[id] = s
	n.mu.Unlock()
	return info.Context().NewNumber(float64(id))
}

func (n *Net) server(id int64) (*server, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, ok := n.servers[id]
	return s, ok
}

// listen(id, host, port) binds synchronously and returns null or a JSON error. Binding
// does not block on connections, and it makes server.address() usable straight away, as
// in Node, where only the 'listening' event is deferred.
func (n *Net) listenFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Throw("net: invalid listen arguments")
	}
	s, ok := n.server(int64(args[0].Integer()))
	if !ok {
		return ctx.Throw("net: server not found")
	}
	host := args[1].String()
	port := int(args[2].Integer())

	srv := n.rt.SocketFactory().CreateTCPServer()
	if err := srv.Listen(n.rt.ExecutionContext(), host, port); err != nil {
		return jsJSON(ctx, netdial.Describe(err, "listen", host, port))
	}
	s.mu.Lock()
	s.srv = srv
	s.mu.Unlock()
	return ctx.Null()
}

// listenUnix(id, path) binds a Unix domain socket and returns null or a JSON error.
func (n *Net) listenUnixFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("net: invalid listen arguments")
	}
	s, ok := n.server(int64(args[0].Integer()))
	if !ok {
		return ctx.Throw("net: server not found")
	}
	path := args[1].String()

	factory, err := n.unixFactory()
	if err != nil {
		return jsJSON(ctx, netdial.Describe(err, "listen", path, 0))
	}
	srv := factory.CreateUnixServer()
	if err := srv.Listen(n.rt.ExecutionContext(), path, 0); err != nil {
		return jsJSON(ctx, netdial.Describe(err, "listen", path, 0))
	}
	s.mu.Lock()
	s.srv = srv
	s.mu.Unlock()
	return ctx.Null()
}

// accept(id, callback) waits for one connection. callback receives (jsonError, socketId,
// jsonAddresses), and nothing at all once the server has closed.
func (n *Net) acceptFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	s, ok := n.server(int64(args[0].Integer()))
	callback := args[1]
	if !ok {
		return nil
	}

	s.mu.Lock()
	srv := s.srv
	if srv == nil || s.closed {
		s.mu.Unlock()
		return nil
	}
	s.accepting++
	s.updateHoldLocked()
	s.mu.Unlock()

	go func() {
		socket, err := srv.Accept(n.rt.ExecutionContext())

		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			if err == nil {
				socket.Close()
			}
			s.mu.Lock()
			s.accepting--
			s.updateHoldLocked()
			s.mu.Unlock()
			return
		}

		n.deliver(func(ctx *v8.Context) {
			if err != nil {
				desc := netdial.Describe(err, "accept", "", 0)
				callback.Call(ctx.Undefined(), jsJSON(ctx, desc), ctx.Null(), ctx.Null())
				return
			}
			id, conn := n.newConn()
			conn.mu.Lock()
			conn.socket = socket
			conn.mu.Unlock()
			callback.Call(ctx.Undefined(), ctx.Null(), ctx.NewNumber(float64(id)), jsJSON(ctx, describeAddresses(socket)))
		})

		s.mu.Lock()
		s.accepting--
		s.updateHoldLocked()
		s.mu.Unlock()
	}()
	return nil
}

func (n *Net) closeServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	id := int64(args[0].Integer())
	n.mu.Lock()
	s, ok := n.servers[id]
	delete(n.servers, id)
	n.mu.Unlock()
	if ok {
		n.rt.UntrackResource(s.resourceID)
		s.Close()
	}
	return nil
}

// getServerAddress(id) returns the bound address as JSON: an object for TCP, or a string
// path for a Unix domain socket.
func (n *Net) getServerAddressFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Null()
	}
	s, ok := n.server(int64(args[0].Integer()))
	if !ok {
		return ctx.Null()
	}
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return ctx.Null()
	}
	switch addr := srv.Addr().(type) {
	case *net.TCPAddr:
		return jsJSON(ctx, map[string]any{"address": addr.IP.String(), "family": familyOf(addr.IP), "port": addr.Port})
	case *net.UnixAddr:
		return jsJSON(ctx, addr.Name)
	}
	return ctx.Null()
}

func (n *Net) setServerRef(info *v8.FunctionCallbackInfo, ref bool) *v8.Value {
	args := info.Args()
	if len(args) >= 1 {
		if s, ok := n.server(int64(args[0].Integer())); ok {
			s.mu.Lock()
			s.ref = ref
			s.updateHoldLocked()
			s.mu.Unlock()
		}
	}
	return nil
}

func (n *Net) refServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return n.setServerRef(info, true)
}

func (n *Net) unrefServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return n.setServerRef(info, false)
}
