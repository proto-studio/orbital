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
	"strings"
	"sync"
	"syscall"
	"time"

	"proto.zip/studio/orbital/pkg/nodejs/internal/netdial"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed net.js
var netJS string

const readBufferSize = 65536

// exitFlushTimeout limits how long process.exit() waits for writes in progress. A write to a
// healthy socket finishes at once, since it only has to reach the kernel. One that takes
// longer is blocked on a peer that is not reading, and Node would have lost that data too.
const exitFlushTimeout = 500 * time.Millisecond

var errUnixDenied = errors.New("EACCES: unix sockets are not available in this sandbox")

// Net provides TCP and Unix domain socket networking.
type Net struct {
	rt       *runtime.Runtime
	mu       sync.Mutex
	conns    map[int64]*Conn
	servers  map[int64]*server
	nextConn int64
	nextSrv  int64

	exitFlush *v8.Value // JavaScript's part of flushForExit
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
		"setExitFlush":     n.setExitFlushFunc,
		"writeAtExit":      n.writeAtExitFunc,
		"connect":          n.connectFunc,
		"connectUnix":      n.connectUnixFunc,
		"lookupAll":        n.lookupAllFunc,
		"connectAddress":   n.connectAddressFunc,
		"abortAttempt":     n.abortAttemptFunc,
		"timeoutError":     n.timeoutErrorFunc,
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
		"listenInherited":  n.listenInheritedFunc,
		"socketInherited":  n.socketInheritedFunc,
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

	rt.OnProcessExit(n.flushForExit)

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
	// noHold stops a waiting read from keeping the process running. A pipe's transport
	// holds the event loop itself, only while it has bytes to process, and hands that hold
	// to the read that returns them through afterRead.
	noHold     bool
	beforeRead func()
	afterRead  func()
	// afterWrite, if set, waits until a write the transport accepted has gone on.
	afterWrite func() error

	// Only the JavaScript thread adds to these, and flushForExit keeps it waiting
	// while they are read.
	writes     sync.WaitGroup
	exitWrites [][]byte
	// resolved is the host lookupAll resolved, whose addresses connectAddress dials.
	resolved *resolvedHost
	// attemptSeq numbers connection attempts, so one that was given up on cannot become
	// the socket's transport when it finishes late.
	attemptSeq    uint64
	attemptCancel context.CancelFunc
}

type resolvedHost struct {
	host   string
	ips    []string
	isName bool
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
	want := c.reading > 0 && c.ref && !c.closed && !c.noHold
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

	seq, attemptCtx := conn.beginAttempt()
	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		result, err := netdial.Connect(attemptCtx, n.rt, host, port, netdial.Options{
			Family:       opts.Family,
			LocalAddress: opts.LocalAddress,
			LocalPort:    opts.LocalPort,
			Prepare:      func(socket runtime.TCPSocket) { conn.setAttempt(seq, socket) },
		})

		var out connectResult
		if err != nil {
			desc := netdial.Describe(err, "connect", host, port)
			out.Error = &desc
		} else {
			if !conn.finishAttempt(seq, result.Socket) {
				return
			}
			out.IP = result.IP
			out.IsName = result.IsName
		}

		n.deliver(func(ctx *v8.Context) {
			n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, out))
		})
	}()
	return nil
}

// beginAttempt starts a connection attempt and returns its number and context, which
// abortAttempt cancels.
func (c *Conn) beginAttempt() (uint64, context.Context) {
	ctx, cancel := context.WithCancel(c.ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attemptSeq++
	c.attemptCancel = cancel
	return c.attemptSeq, ctx
}

// setAttempt records the socket an attempt is connecting, so it can be closed to give up.
func (c *Conn) setAttempt(seq uint64, socket runtime.TCPSocket) {
	c.mu.Lock()
	current := seq == c.attemptSeq && !c.closed
	if current {
		c.attempt = socket
	}
	c.mu.Unlock()
	if !current {
		socket.Close()
	}
}

// finishAttempt makes a connected socket the transport, unless the socket has closed or
// the attempt was given up on, in which case it closes it and reports false.
func (c *Conn) finishAttempt(seq uint64, socket runtime.TCPSocket) bool {
	c.mu.Lock()
	current := seq == c.attemptSeq && !c.closed
	if current {
		c.socket = socket
		c.attempt = nil
	}
	c.mu.Unlock()
	if !current {
		socket.Close()
	}
	return current
}

type lookupResult struct {
	Error     *netdial.NodeError `json:"error,omitempty"`
	Addresses []lookupAddress    `json:"addresses,omitempty"`
	IsName    bool               `json:"isName"`
}

type lookupAddress struct {
	Address string `json:"address"`
	Family  int    `json:"family"`
}

// lookupAll(id, host, family, callback) resolves host through the injected resolver, as
// connect does, and keeps the addresses for connectAddress. callback receives a JSON
// lookupResult listing them, so the script can choose the order to try them in.
func (n *Net) lookupAllFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	if !ok {
		return nil
	}
	host := args[1].String()
	family := int(args[2].Integer())
	callback := args[3]

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()
		ips, isName, err := netdial.Resolve(conn.ctx, n.rt, host, family)
		var out lookupResult
		if err != nil {
			desc := netdial.Describe(err, "connect", host, 0)
			out.Error = &desc
		} else {
			conn.mu.Lock()
			conn.resolved = &resolvedHost{host: host, ips: ips, isName: isName}
			conn.mu.Unlock()
			out.IsName = isName
			for _, ip := range ips {
				family := 4
				if strings.Contains(ip, ":") {
					family = 6
				}
				out.Addresses = append(out.Addresses, lookupAddress{Address: ip, Family: family})
			}
		}
		n.deliver(func(ctx *v8.Context) {
			n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, out))
		})
	}()
	return nil
}

// connectAddress(id, index, port, optionsJSON, callback) connects to the index-th address
// lookupAll resolved. Only addresses resolved here can be chosen. callback receives a JSON
// connectResult, or nothing if the attempt was given up on with abortAttempt.
func (n *Net) connectAddressFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 5 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	if !ok {
		return nil
	}
	index := int(args[1].Integer())
	port := int(args[2].Integer())
	var opts connectOptions
	if err := json.Unmarshal([]byte(args[3].String()), &opts); err != nil {
		return info.Context().Throw("net: invalid connect options")
	}
	callback := args[4]

	conn.mu.Lock()
	resolved := conn.resolved
	conn.mu.Unlock()
	if resolved == nil || index < 0 || index >= len(resolved.ips) {
		return info.Context().Throw("net: no such resolved address")
	}
	ip := resolved.ips[index]

	seq, attemptCtx := conn.beginAttempt()
	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()
		socket, err := netdial.DialOne(attemptCtx, n.rt, resolved.host, ip, resolved.isName, port, netdial.Options{
			LocalAddress: opts.LocalAddress,
			LocalPort:    opts.LocalPort,
			Prepare:      func(socket runtime.TCPSocket) { conn.setAttempt(seq, socket) },
		})
		var out connectResult
		if err != nil {
			desc := netdial.Describe(err, "connect", ip, port)
			out.Error = &desc
		} else {
			if !conn.finishAttempt(seq, socket) {
				return
			}
			out.IP = ip
			out.IsName = resolved.isName
		}
		n.deliver(func(ctx *v8.Context) {
			n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, out))
		})
	}()
	return nil
}

// abortAttempt(id) gives up on the socket's connection attempt in progress.
func (n *Net) abortAttemptFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	conn, ok := n.Conn(int64(args[0].Integer()))
	if !ok {
		return nil
	}
	conn.mu.Lock()
	conn.attemptSeq++
	attempt, cancel := conn.attempt, conn.attemptCancel
	conn.attempt, conn.attemptCancel = nil, nil
	conn.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// A socket may hold its lock while it dials, so it is closed off the JavaScript thread.
	if attempt != nil {
		go attempt.Close()
	}
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
			n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, out))
		})
	}()
	return nil
}

// write(id, base64Data, callback) writes bytes. They cross the boundary as base64 because
// a JavaScript string cannot carry them unchanged. It returns true when the socket took all
// the bytes at once, and callback is not called; otherwise callback later receives a JSON
// error or null.
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

	conn.mu.Lock()
	afterWrite := conn.afterWrite
	conn.mu.Unlock()

	// Like libuv, write at once what the socket takes without waiting. When that is
	// everything, the write is done and JavaScript completes it in this turn, as Node does.
	written, err := runtime.TryWrite(socket, data)
	if errors.Is(err, errors.ErrUnsupported) {
		written, err = 0, nil
	}
	if err == nil && written == len(data) && afterWrite == nil {
		return info.Context().NewBoolean(true)
	}
	data = data[written:]

	n.rt.EventLoop().AddPendingWork()
	conn.writes.Add(1)
	go func() {
		defer n.rt.EventLoop().DonePendingWork()
		if err == nil && len(data) > 0 {
			_, err = socket.Write(data)
		}
		conn.writes.Done()
		if err == nil && afterWrite != nil {
			err = afterWrite()
		}
		n.deliver(func(ctx *v8.Context) {
			if err != nil {
				desc := netdial.Describe(err, "write", "", 0)
				n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, desc))
				return
			}
			n.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null())
		})
	}()
	return nil
}

// setExitFlush(fn) registers what JavaScript does when the process exits.
func (n *Net) setExitFlushFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	if args := info.Args(); len(args) >= 1 {
		n.exitFlush = args[0]
	}
	return nil
}

// writeAtExit(id, base64Data) queues bytes to follow a socket's write in progress. It is
// only called from flushForExit.
func (n *Net) writeAtExitFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		return info.Context().Throw("net: invalid write data")
	}
	if conn, ok := n.Conn(int64(args[0].Integer())); ok {
		conn.exitWrites = append(conn.exitWrites, data)
	}
	return nil
}

// flushForExit runs on the JavaScript thread as process.exit() ends the process. Data
// that write() has accepted reaches the kernel first, as it would have in Node.
func (n *Net) flushForExit() {
	if n.exitFlush != nil {
		// The process is ending, so an error here has nowhere to go.
		_, _ = n.exitFlush.Call(nil)
	}

	n.mu.Lock()
	conns := make([]*Conn, 0, len(n.conns))
	for _, conn := range n.conns {
		conns = append(conns, conn)
	}
	n.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, conn := range conns {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn.writes.Wait()
				if socket := conn.Socket(); socket != nil {
					for _, data := range conn.exitWrites {
						if _, err := socket.Write(data); err != nil {
							return
						}
					}
				}
			}()
		}
		wg.Wait()
	}()

	timer := time.NewTimer(exitFlushTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (n *Net) failSocketClosed(callback *v8.Value, syscallName string) *v8.Value {
	n.deliver(func(ctx *v8.Context) {
		desc := netdial.NodeError{Message: "Socket is closed", Code: "ERR_SOCKET_CLOSED", Syscall: syscallName}
		n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, desc))
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
	beforeRead := conn.beforeRead
	conn.mu.Unlock()
	if beforeRead != nil {
		beforeRead()
	}

	go func() {
		buf := make([]byte, readBufferSize)
		n2, err := 0, pending
		if err == nil {
			n2, err = socket.Read(buf)
		}
		conn.mu.Lock()
		noHold, afterRead := conn.noHold, conn.afterRead
		conn.mu.Unlock()
		if noHold {
			// Keep the loop running until what was read is delivered.
			n.rt.EventLoop().AddPendingWork()
			defer n.rt.EventLoop().DonePendingWork()
		}
		if afterRead != nil {
			afterRead()
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
				n.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null(), jsString(ctx, encoded))
			case err == nil, errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
				errors.Is(err, net.ErrClosed), errors.Is(err, runtime.ErrSocketClosed):
				n.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null(), ctx.Null())
			default:
				desc := netdial.Describe(err, "read", "", 0)
				n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, desc), ctx.Null())
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
		n.deliver(func(ctx *v8.Context) { n.rt.MakeCallback(callback, ctx.Undefined()) })
		return nil
	}

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()
		if errors.Is(runtime.CloseWrite(socket), errors.ErrUnsupported) {
			conn.Close()
		}
		n.deliver(func(ctx *v8.Context) { n.rt.MakeCallback(callback, ctx.Undefined()) })
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
// listenInherited(serverId, fd) listens on a socket the process inherited, if the runtime's
// socket factory offers it. It returns null or a JSON error.
func (n *Net) listenInheritedFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("net: invalid listen arguments")
	}
	s, ok := n.server(int64(args[0].Integer()))
	if !ok {
		return ctx.Throw("net: server not found")
	}
	fd := int(args[1].Integer())
	err := runtime.ErrNotInherited
	var srv runtime.TCPServer
	if factory, ok := n.rt.SocketFactory().(runtime.InheritedListenerFactory); ok {
		srv, err = factory.ListenInherited(fd)
	}
	if err != nil {
		return jsJSON(ctx, netdial.Describe(err, "listen", "", 0))
	}
	s.mu.Lock()
	s.srv = srv
	s.mu.Unlock()
	return ctx.Null()
}

// socketInherited(fd) returns the id of a socket over a descriptor the process has, if the
// runtime's socket factory offers it, or a JSON error.
func (n *Net) socketInheritedFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("net: invalid arguments")
	}
	fd := int(args[0].Integer())
	var socket runtime.TCPSocket
	var err error = &runtime.ErrFileDescriptor{Type: "UNKNOWN"}
	if factory, ok := n.rt.SocketFactory().(runtime.InheritedSocketFactory); ok {
		socket, err = factory.SocketInherited(fd)
	}
	if err != nil {
		return jsJSON(ctx, netdial.Describe(err, "open", "", 0))
	}
	id, conn := n.newConn()
	conn.SetSocket(socket)
	return ctx.NewNumber(float64(id))
}

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
				n.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, desc), ctx.Null(), ctx.Null())
				return
			}
			id, conn := n.newConn()
			conn.mu.Lock()
			conn.socket = socket
			conn.mu.Unlock()
			n.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null(), ctx.NewNumber(float64(id)), jsJSON(ctx, describeAddresses(socket)))
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

// timeoutError(ip, port) returns, as JSON, the error Node records for an attempt it gave up
// on: ETIMEDOUT with this platform's errno.
func (n *Net) timeoutErrorFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	ip, port := args[0].String(), int(args[1].Integer())
	err := &netdial.AddressError{Err: syscall.ETIMEDOUT, Address: ip, Port: port}
	return jsJSON(info.Context(), netdial.Describe(err, "connect", ip, port))
}
