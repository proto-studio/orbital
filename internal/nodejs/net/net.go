// Package net implements the Node.js net module for TCP/IPC networking.
package net

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed net.js
var netJS string

// Net provides TCP/IPC networking.
type Net struct {
	rt       *runtime.Runtime
	sockets  map[int64]runtime.TCPSocket
	servers  map[int64]runtime.TCPServer
	socketID int64
	serverID int64
	mu       sync.Mutex
}

// New creates a new Net module.
func New() *Net {
	return &Net{
		sockets: make(map[int64]runtime.TCPSocket),
		servers: make(map[int64]runtime.TCPServer),
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

	// Create internal object with native functions
	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8.FunctionCallback{
		"createSocket":     n.createSocketFunc,
		"connect":          n.connectFunc,
		"write":            n.writeFunc,
		"read":             n.readFunc,
		"close":            n.closeFunc,
		"setTimeout":       n.setTimeoutFunc,
		"setKeepAlive":     n.setKeepAliveFunc,
		"setNoDelay":       n.setNoDelayFunc,
		"getAddressInfo":   n.getAddressInfoFunc,
		"ref":              n.refFunc,
		"unref":            n.unrefFunc,
		"createServer":     n.createServerFunc,
		"listen":           n.listenFunc,
		"accept":           n.acceptFunc,
		"closeServer":      n.closeServerFunc,
		"closeSocket":      n.closeSocketFunc,
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

	if _, err := rt.RunScript(netJS, "net.js"); err != nil {
		return err
	}

	return nil
}

func (n *Net) createSocketFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()

	n.mu.Lock()
	id := atomic.AddInt64(&n.socketID, 1)
	socket := n.rt.SocketFactory().CreateTCPSocket()
	n.sockets[id] = socket
	n.mu.Unlock()

	return ctx.NewNumber(float64(id))
}

func (n *Net) connectFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}

	id := int64(args[0].Integer())
	host := args[1].String()
	port := int(args[2].Integer())
	callback := args[3]

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if !ok {
		n.callCallback(callback, "Socket not found", nil)
		return nil
	}

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		err := socket.Connect(context.Background(), host, port)

		n.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				n.callCallback(callback, err.Error(), nil)
			} else {
				n.callCallback(callback, "", nil)
			}
		})
	}()

	return nil
}

func (n *Net) writeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	data := args[1].String()
	callback := args[2]

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if !ok {
		n.callCallback(callback, "Socket not found", nil)
		return nil
	}

	// The JS side hands us a latin1 wire-string (buf.toString('binary')), where
	// each character is a single byte value 0-255. args[1].String() decodes it
	// through UTF-8, so bytes >= 0x80 arrive as multi-byte runes; latin1Bytes
	// reverses that by taking the low byte of each rune to recover the exact
	// wire bytes. Using []byte(data) here would re-UTF-8-encode them and corrupt
	// any non-ASCII payload (and desync Content-Length).
	wire := latin1Bytes(data)

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		_, err := socket.Write(wire)

		n.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				n.callCallback(callback, err.Error(), nil)
			} else {
				n.callCallback(callback, "", nil)
			}
		})
	}()

	return nil
}

func (n *Net) readFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[1]

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if !ok {
		n.callCallback(callback, "Socket not found", nil)
		return nil
	}

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		buf := make([]byte, 65536)
		bytesRead, err := socket.Read(buf)

		n.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			if err != nil {
				// Check for EOF
				if err.Error() == "EOF" || bytesRead == 0 {
					n.callCallback(callback, "", ctx.Null())
				} else {
					n.callCallback(callback, err.Error(), nil)
				}
			} else if bytesRead == 0 {
				n.callCallback(callback, "", ctx.Null())
			} else {
				// Deliver raw bytes as a latin1 (byte-per-codepoint) string so
				// arbitrary binary survives the crossing into V8. Passing the raw
				// bytes straight to NewString would run them through
				// String::NewFromUtf8, collapsing/replacing any multi-byte UTF-8
				// sequence (e.g. an HTTP body with non-ASCII characters would lose
				// bytes, breaking Content-Length accounting). The JS side recovers
				// the exact bytes with Buffer.from(data, 'binary').
				dataStr, _ := ctx.NewString(latin1String(buf[:bytesRead]))
				n.callCallback(callback, "", dataStr)
			}
		})
	}()

	return nil
}

func (n *Net) closeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	n.mu.Lock()
	socket, ok := n.sockets[id]
	if ok {
		delete(n.sockets, id)
	}
	n.mu.Unlock()

	if ok && socket != nil {
		socket.Close()
	}

	return nil
}

func (n *Net) setTimeoutFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// Simplified - would need proper timeout handling
	return nil
}

func (n *Net) setKeepAliveFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	enable := args[1].Boolean()

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if ok && socket != nil {
		socket.SetKeepAlive(enable)
	}

	return nil
}

func (n *Net) setNoDelayFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	enable := args[1].Boolean()

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if ok && socket != nil {
		socket.SetNoDelay(enable)
	}

	return nil
}

func (n *Net) getAddressInfoFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Null()
	}

	id := int64(args[0].Integer())

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if !ok || socket == nil {
		return ctx.Null()
	}

	obj, _ := ctx.NewObject()

	if localAddr := socket.LocalAddr(); localAddr != nil {
		if tcpAddr, ok := localAddr.(*net.TCPAddr); ok {
			localAddrStr, _ := ctx.NewString(tcpAddr.IP.String())
			obj.Set("localAddress", localAddrStr)
			obj.Set("localPort", ctx.NewNumber(float64(tcpAddr.Port)))
		}
	}

	if remoteAddr := socket.RemoteAddr(); remoteAddr != nil {
		if tcpAddr, ok := remoteAddr.(*net.TCPAddr); ok {
			remoteAddrStr, _ := ctx.NewString(tcpAddr.IP.String())
			obj.Set("remoteAddress", remoteAddrStr)
			obj.Set("remotePort", ctx.NewNumber(float64(tcpAddr.Port)))
			family := "IPv4"
			if tcpAddr.IP.To4() == nil {
				family = "IPv6"
			}
			familyStr, _ := ctx.NewString(family)
			obj.Set("remoteFamily", familyStr)
		}
	}

	return obj
}

func (n *Net) refFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if ok && socket != nil {
		socket.Ref()
	}

	return nil
}

func (n *Net) unrefFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	n.mu.Lock()
	socket, ok := n.sockets[id]
	n.mu.Unlock()

	if ok && socket != nil {
		socket.Unref()
	}

	return nil
}

func (n *Net) createServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()

	n.mu.Lock()
	id := atomic.AddInt64(&n.serverID, 1)
	server := n.rt.SocketFactory().CreateTCPServer()
	n.servers[id] = server
	n.mu.Unlock()

	return ctx.NewNumber(float64(id))
}

func (n *Net) listenFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		s, _ := ctx.NewString("invalid listen arguments")
		return s
	}

	id := int64(args[0].Integer())
	host := args[1].String()
	port := int(args[2].Integer())
	// backlog is args[3] - not used in our implementation

	n.mu.Lock()
	server, ok := n.servers[id]
	n.mu.Unlock()

	if !ok {
		s, _ := ctx.NewString("Server not found")
		return s
	}

	// Bind synchronously. net.Listen binds the socket and assigns the (possibly
	// ephemeral) port immediately without blocking on connections, so the bound
	// address is valid as soon as this returns. This matches Node, where
	// server.address() is usable synchronously right after listen() and only the
	// 'listening' event is deferred. Packages like supertest depend on this: they
	// call app.listen(0) and then read server.address().port synchronously.
	if err := server.Listen(context.Background(), host, port); err != nil {
		s, _ := ctx.NewString(err.Error())
		return s
	}

	// null == success
	return ctx.Null()
}

func (n *Net) acceptFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	serverID := int64(args[0].Integer())
	callback := args[1]

	n.mu.Lock()
	server, ok := n.servers[serverID]
	n.mu.Unlock()

	if !ok {
		n.callCallbackWithArgs(callback, "Server not found")
		return nil
	}

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		socket, err := server.Accept(context.Background())

		n.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			if err != nil {
				n.callCallbackWithArgs(callback, err.Error())
				return
			}

			// Create socket ID and store
			n.mu.Lock()
			socketID := atomic.AddInt64(&n.socketID, 1)
			n.sockets[socketID] = socket
			n.mu.Unlock()

			// Create address info object
			addrInfo, _ := ctx.NewObject()

			if localAddr := socket.LocalAddr(); localAddr != nil {
				if tcpAddr, ok := localAddr.(*net.TCPAddr); ok {
					localAddrStr, _ := ctx.NewString(tcpAddr.IP.String())
					addrInfo.Set("localAddress", localAddrStr)
					addrInfo.Set("localPort", ctx.NewNumber(float64(tcpAddr.Port)))
				}
			}

			if remoteAddr := socket.RemoteAddr(); remoteAddr != nil {
				if tcpAddr, ok := remoteAddr.(*net.TCPAddr); ok {
					remoteAddrStr, _ := ctx.NewString(tcpAddr.IP.String())
					addrInfo.Set("remoteAddress", remoteAddrStr)
					addrInfo.Set("remotePort", ctx.NewNumber(float64(tcpAddr.Port)))
					family := "IPv4"
					if tcpAddr.IP.To4() == nil {
						family = "IPv6"
					}
					familyStr, _ := ctx.NewString(family)
					addrInfo.Set("remoteFamily", familyStr)
				}
			}

			n.callCallbackWithArgs(callback, "", ctx.NewNumber(float64(socketID)), addrInfo)
		})
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
	server, ok := n.servers[id]
	if ok {
		delete(n.servers, id)
	}
	n.mu.Unlock()

	if ok && server != nil {
		server.Close()
	}

	return nil
}

func (n *Net) closeSocketFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return n.closeFunc(info)
}

func (n *Net) getServerAddressFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Null()
	}

	id := int64(args[0].Integer())

	n.mu.Lock()
	server, ok := n.servers[id]
	n.mu.Unlock()

	if !ok || server == nil {
		return ctx.Null()
	}

	addr := server.Addr()
	if addr == nil {
		return ctx.Null()
	}

	obj, _ := ctx.NewObject()
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		family := "IPv4"
		if tcpAddr.IP.To4() == nil {
			family = "IPv6"
		}
		addrStr, _ := ctx.NewString(tcpAddr.IP.String())
		familyStr, _ := ctx.NewString(family)
		obj.Set("address", addrStr)
		obj.Set("port", ctx.NewNumber(float64(tcpAddr.Port)))
		obj.Set("family", familyStr)
	}

	return obj
}

func (n *Net) refServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	n.mu.Lock()
	server, ok := n.servers[id]
	n.mu.Unlock()

	if ok && server != nil {
		server.Ref()
	}

	return nil
}

func (n *Net) unrefServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	n.mu.Lock()
	server, ok := n.servers[id]
	n.mu.Unlock()

	if ok && server != nil {
		server.Unref()
	}

	return nil
}

func (n *Net) callCallback(callback *v8.Value, errMsg string, result *v8.Value) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := n.rt.Context()
	var errVal *v8.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	if result == nil {
		result = ctx.Undefined()
	}

	callback.Call(ctx.Undefined(), errVal, result)
}

func (n *Net) callCallbackWithArgs(callback *v8.Value, errMsg string, args ...*v8.Value) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := n.rt.Context()
	var errVal *v8.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	allArgs := make([]*v8.Value, 0, len(args)+1)
	allArgs = append(allArgs, errVal)
	allArgs = append(allArgs, args...)

	callback.Call(ctx.Undefined(), allArgs...)
}

// formatPort converts an integer port to a string.
func formatPort(port int) string {
	return fmt.Sprintf("%d", port)
}

// latin1String maps each raw byte to the Unicode code point of the same value,
// producing a valid UTF-8 Go string that survives the crossing into V8 without
// the lossy replacement NewString would apply to invalid UTF-8. The JS side
// recovers the exact bytes with Buffer.from(s, 'binary'/'latin1'). This is the
// same byte-safe convention the fs and http layers use.
func latin1String(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// latin1Bytes is the inverse of latin1String: it recovers the raw bytes from a
// latin1 wire-string that arrived from V8 (decoded as UTF-8 runes) by taking the
// low byte of each code point.
func latin1Bytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return out
}
