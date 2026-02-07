// Package tls implements the Node.js tls module for TLS/SSL networking.
package tls

import (
	"context"
	"crypto/tls"
	_ "embed"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/network"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
)

//go:embed tls.js
var tlsJS string

// TLS provides TLS/SSL functionality.
type TLS struct {
	rt       *runtime.Runtime
	sockets  map[int64]network.TCPSocket
	socketID int64
	mu       sync.Mutex
}

// New creates a new TLS module.
func New() *TLS {
	return &TLS{
		sockets: make(map[int64]network.TCPSocket),
	}
}

// Name returns the module name.
func (t *TLS) Name() string {
	return "tls"
}

// Register sets up the tls module.
func (t *TLS) Register(rt *runtime.Runtime) error {
	t.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create internal object with native functions
	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8go.FunctionCallback{
		"createTLSSocket":    t.createTLSSocketFunc,
		"connect":            t.connectFunc,
		"write":              t.writeFunc,
		"read":               t.readFunc,
		"close":              t.closeFunc,
		"getPeerCertificate": t.getPeerCertificateFunc,
		"getCipher":          t.getCipherFunc,
		"getProtocol":        t.getProtocolFunc,
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

	if err := rt.SetGlobal("__tls_internal", internal); err != nil {
		return err
	}

	// Must come after net module
	if _, err := rt.RunScript(tlsJS, "tls.js"); err != nil {
		return err
	}

	return nil
}

func (t *TLS) createTLSSocketFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()

	isServer := false
	if len(args) > 0 {
		isServer = args[0].Boolean()
	}

	// Create underlying TCP socket first
	tcpSocket := t.rt.SocketFactory().CreateTCPSocket()

	// Create TLS config
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // For demo - should be configurable
	}

	// Wrap with TLS
	tlsSocket := t.rt.SocketFactory().CreateTLSSocket(tcpSocket, tlsConfig, isServer)

	t.mu.Lock()
	id := atomic.AddInt64(&t.socketID, 1)
	t.sockets[id] = tlsSocket
	t.mu.Unlock()

	return ctx.NewNumber(float64(id))
}

func (t *TLS) connectFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 5 {
		return nil
	}

	id := int64(args[0].Integer())
	host := args[1].String()
	port := int(args[2].Integer())
	// servername is args[3] - used for SNI
	callback := args[4]

	t.mu.Lock()
	socket, ok := t.sockets[id]
	t.mu.Unlock()

	if !ok {
		t.callCallback(callback, "Socket not found", nil)
		return nil
	}

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		err := socket.Connect(context.Background(), host, port)

		t.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				t.callCallback(callback, err.Error(), nil)
			} else {
				t.callCallback(callback, "", nil)
			}
		})
	}()

	return nil
}

func (t *TLS) writeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	data := args[1].String()
	callback := args[2]

	t.mu.Lock()
	socket, ok := t.sockets[id]
	t.mu.Unlock()

	if !ok {
		t.callCallback(callback, "Socket not found", nil)
		return nil
	}

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		_, err := socket.Write([]byte(data))

		t.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				t.callCallback(callback, err.Error(), nil)
			} else {
				t.callCallback(callback, "", nil)
			}
		})
	}()

	return nil
}

func (t *TLS) readFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[1]

	t.mu.Lock()
	socket, ok := t.sockets[id]
	t.mu.Unlock()

	if !ok {
		t.callCallback(callback, "Socket not found", nil)
		return nil
	}

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		buf := make([]byte, 65536)
		n, err := socket.Read(buf)

		t.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			if err != nil {
				if err.Error() == "EOF" || n == 0 {
					t.callCallback(callback, "", ctx.Null())
				} else {
					t.callCallback(callback, err.Error(), nil)
				}
			} else if n == 0 {
				t.callCallback(callback, "", ctx.Null())
			} else {
				dataStr, _ := ctx.NewString(string(buf[:n]))
				t.callCallback(callback, "", dataStr)
			}
		})
	}()

	return nil
}

func (t *TLS) closeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	t.mu.Lock()
	socket, ok := t.sockets[id]
	if ok {
		delete(t.sockets, id)
	}
	t.mu.Unlock()

	if ok && socket != nil {
		socket.Close()
	}

	return nil
}

func (t *TLS) getPeerCertificateFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	// Return empty object - would need actual cert parsing
	obj, _ := ctx.NewObject()
	return obj
}

func (t *TLS) getCipherFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	obj, _ := ctx.NewObject()
	nameStr, _ := ctx.NewString("TLS_AES_256_GCM_SHA384")
	versionStr, _ := ctx.NewString("TLSv1.3")
	obj.Set("name", nameStr)
	obj.Set("standardName", nameStr)
	obj.Set("version", versionStr)
	return obj
}

func (t *TLS) getProtocolFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	val, _ := ctx.NewString("TLSv1.3")
	return val
}

func (t *TLS) callCallback(callback *v8go.Value, errMsg string, result *v8go.Value) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := t.rt.Context()
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
