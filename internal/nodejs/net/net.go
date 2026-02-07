// Package net implements the Node.js net module for TCP/IPC networking.
package net

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/network"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
)

//go:embed net.js
var netJS string

// Net provides TCP/IPC networking.
type Net struct {
	rt       *runtime.Runtime
	sockets  map[int64]network.TCPSocket
	servers  map[int64]network.TCPServer
	socketID int64
	serverID int64
	mu       sync.Mutex
}

// New creates a new Net module.
func New() *Net {
	return &Net{
		sockets: make(map[int64]network.TCPSocket),
		servers: make(map[int64]network.TCPServer),
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
	funcs := map[string]v8go.FunctionCallback{
		"createSocket":       n.createSocketFunc,
		"connect":            n.connectFunc,
		"write":              n.writeFunc,
		"read":               n.readFunc,
		"close":              n.closeFunc,
		"setTimeout":         n.setTimeoutFunc,
		"setKeepAlive":       n.setKeepAliveFunc,
		"setNoDelay":         n.setNoDelayFunc,
		"getAddressInfo":     n.getAddressInfoFunc,
		"ref":                n.refFunc,
		"unref":              n.unrefFunc,
		"createServer":       n.createServerFunc,
		"listen":             n.listenFunc,
		"accept":             n.acceptFunc,
		"closeServer":        n.closeServerFunc,
		"closeSocket":        n.closeSocketFunc,
		"getServerAddress":   n.getServerAddressFunc,
		"refServer":          n.refServerFunc,
		"unrefServer":        n.unrefServerFunc,
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

func (n *Net) createSocketFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()

	n.mu.Lock()
	id := atomic.AddInt64(&n.socketID, 1)
	socket := n.rt.SocketFactory().CreateTCPSocket()
	n.sockets[id] = socket
	n.mu.Unlock()

	return ctx.NewNumber(float64(id))
}

func (n *Net) connectFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) writeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		_, err := socket.Write([]byte(data))

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

func (n *Net) readFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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
				// Return data as string (binary encoding)
				dataStr, _ := ctx.NewString(string(buf[:bytesRead]))
				n.callCallback(callback, "", dataStr)
			}
		})
	}()

	return nil
}

func (n *Net) closeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) setTimeoutFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// Simplified - would need proper timeout handling
	return nil
}

func (n *Net) setKeepAliveFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) setNoDelayFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) getAddressInfoFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) refFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) unrefFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) createServerFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()

	n.mu.Lock()
	id := atomic.AddInt64(&n.serverID, 1)
	server := n.rt.SocketFactory().CreateTCPServer()
	n.servers[id] = server
	n.mu.Unlock()

	return ctx.NewNumber(float64(id))
}

func (n *Net) listenFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 5 {
		return nil
	}

	id := int64(args[0].Integer())
	host := args[1].String()
	port := int(args[2].Integer())
	// backlog is args[3] - not used in our implementation
	callback := args[4]

	n.mu.Lock()
	server, ok := n.servers[id]
	n.mu.Unlock()

	if !ok {
		n.callCallback(callback, "Server not found", nil)
		return nil
	}

	n.rt.EventLoop().AddPendingWork()
	go func() {
		defer n.rt.EventLoop().DonePendingWork()

		err := server.Listen(context.Background(), host, port)

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

func (n *Net) acceptFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) closeServerFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) closeSocketFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	return n.closeFunc(info)
}

func (n *Net) getServerAddressFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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
		addrStr, _ := ctx.NewString(tcpAddr.IP.String())
		portStr, _ := ctx.NewString(strconv.Itoa(tcpAddr.Port))
		obj.Set("address", addrStr)
		obj.Set("port", ctx.NewNumber(float64(tcpAddr.Port)))
		obj.Set("family", portStr)
	}

	return obj
}

func (n *Net) refServerFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) unrefServerFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

func (n *Net) callCallback(callback *v8go.Value, errMsg string, result *v8go.Value) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := n.rt.Context()
	var errVal *v8go.Value
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

func (n *Net) callCallbackWithArgs(callback *v8go.Value, errMsg string, args ...*v8go.Value) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := n.rt.Context()
	var errVal *v8go.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	allArgs := make([]*v8go.Value, 0, len(args)+1)
	allArgs = append(allArgs, errVal)
	allArgs = append(allArgs, args...)

	callback.Call(ctx.Undefined(), allArgs...)
}

// formatPort converts an integer port to a string.
func formatPort(port int) string {
	return fmt.Sprintf("%d", port)
}
