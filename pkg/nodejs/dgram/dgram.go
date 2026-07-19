// Package dgram implements the Node.js dgram module for UDP networking.
package dgram

import (
	_ "embed"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed dgram.js
var dgramJS string

// Dgram provides UDP socket functionality.
type Dgram struct {
	rt       *runtime.Runtime
	sockets  map[int64]runtime.UDPSocket
	socketID int64
	mu       sync.Mutex
}

// New creates a new Dgram module.
func New() *Dgram {
	return &Dgram{
		sockets: make(map[int64]runtime.UDPSocket),
	}
}

// Name returns the module name.
func (d *Dgram) Name() string {
	return "dgram"
}

// Register sets up the dgram module.
func (d *Dgram) Register(rt *runtime.Runtime) error {
	d.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create internal object with native functions
	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8.FunctionCallback{
		"createSocket":          d.createSocketFunc,
		"bind":                  d.bindFunc,
		"send":                  d.sendFunc,
		"receive":               d.receiveFunc,
		"close":                 d.closeFunc,
		"address":               d.addressFunc,
		"setBroadcast":          d.setBroadcastFunc,
		"setMulticastTTL":       d.setMulticastTTLFunc,
		"setMulticastLoopback":  d.noopFunc,
		"setMulticastInterface": d.noopFunc,
		"addMembership":         d.addMembershipFunc,
		"dropMembership":        d.dropMembershipFunc,
		"setTTL":                d.noopFunc,
		"setRecvBufferSize":     d.noopFunc,
		"setSendBufferSize":     d.noopFunc,
		"getRecvBufferSize":     d.noopNumberFunc,
		"getSendBufferSize":     d.noopNumberFunc,
		"connect":               d.connectFunc,
		"disconnect":            d.noopFunc,
		"remoteAddress":         d.noopNullFunc,
		"ref":                   d.refFunc,
		"unref":                 d.unrefFunc,
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

	if err := rt.SetGlobal("__dgram_internal", internal); err != nil {
		return err
	}

	if _, err := rt.RunScript(dgramJS, "dgram.js"); err != nil {
		return err
	}

	return nil
}

func (d *Dgram) createSocketFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	socketType := "udp4"
	if len(args) > 0 {
		socketType = args[0].String()
	}

	d.mu.Lock()
	id := atomic.AddInt64(&d.socketID, 1)
	socket := d.rt.SocketFactory().CreateUDPSocket(socketType)
	d.sockets[id] = socket
	d.mu.Unlock()

	return ctx.NewNumber(float64(id))
}

func (d *Dgram) bindFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}

	id := int64(args[0].Integer())
	address := args[1].String()
	port := int(args[2].Integer())
	callback := args[3]

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if !ok {
		d.callCallback(callback, "Socket not found")
		return nil
	}

	d.rt.EventLoop().AddPendingWork()
	go func() {
		defer d.rt.EventLoop().DonePendingWork()

		err := socket.Bind(nil, address, port)

		d.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				d.callCallback(callback, err.Error())
			} else {
				d.callCallback(callback, "")
			}
		})
	}()

	return nil
}

func (d *Dgram) sendFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 5 {
		return nil
	}

	id := int64(args[0].Integer())
	msg := args[1].String()
	address := args[2].String()
	port := int(args[3].Integer())
	callback := args[4]

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if !ok {
		d.callCallbackWithBytes(callback, "Socket not found", 0)
		return nil
	}

	d.rt.EventLoop().AddPendingWork()
	go func() {
		defer d.rt.EventLoop().DonePendingWork()

		n, err := socket.Send([]byte(msg), address, port)

		d.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				d.callCallbackWithBytes(callback, err.Error(), 0)
			} else {
				d.callCallbackWithBytes(callback, "", n)
			}
		})
	}()

	return nil
}

func (d *Dgram) receiveFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[1]

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if !ok {
		d.callReceiveCallback(callback, "Socket not found", nil, nil)
		return nil
	}

	d.rt.EventLoop().AddPendingWork()
	go func() {
		defer d.rt.EventLoop().DonePendingWork()

		buf := make([]byte, 65536)
		n, remoteAddr, remotePort, err := socket.Receive(buf)

		d.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()

			if err != nil {
				d.callReceiveCallback(callback, err.Error(), nil, nil)
				return
			}

			if n == 0 {
				d.callReceiveCallback(callback, "", ctx.Null(), nil)
				return
			}

			// Create rinfo object
			rinfo, _ := ctx.NewObject()
			addrStr, _ := ctx.NewString(remoteAddr)
			rinfo.Set("address", addrStr)
			rinfo.Set("port", ctx.NewNumber(float64(remotePort)))
			familyStr, _ := ctx.NewString("IPv4")
			rinfo.Set("family", familyStr)
			rinfo.Set("size", ctx.NewNumber(float64(n)))

			// Return message as binary string
			msgStr, _ := ctx.NewString(string(buf[:n]))
			d.callReceiveCallback(callback, "", msgStr, rinfo)
		})
	}()

	return nil
}

func (d *Dgram) closeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	d.mu.Lock()
	socket, ok := d.sockets[id]
	if ok {
		delete(d.sockets, id)
	}
	d.mu.Unlock()

	if ok && socket != nil {
		socket.Close()
	}

	return nil
}

func (d *Dgram) addressFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Null()
	}

	id := int64(args[0].Integer())

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if !ok || socket == nil {
		return ctx.Null()
	}

	addr := socket.LocalAddr()
	if addr == nil {
		return ctx.Null()
	}

	obj, _ := ctx.NewObject()
	addrStr, _ := ctx.NewString(addr.String())
	obj.Set("address", addrStr)
	obj.Set("port", ctx.NewNumber(0)) // Would need to parse from addr.String()
	familyStr, _ := ctx.NewString("IPv4")
	obj.Set("family", familyStr)

	return obj
}

func (d *Dgram) setBroadcastFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	enable := args[1].Boolean()

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if ok && socket != nil {
		socket.SetBroadcast(enable)
	}

	return nil
}

func (d *Dgram) setMulticastTTLFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	ttl := int(args[1].Integer())

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if ok && socket != nil {
		socket.SetMulticastTTL(ttl)
	}

	return nil
}

func (d *Dgram) addMembershipFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	multicastAddr := args[1].String()
	interfaceAddr := args[2].String()

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if ok && socket != nil {
		socket.AddMembership(multicastAddr, interfaceAddr)
	}

	return nil
}

func (d *Dgram) dropMembershipFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	multicastAddr := args[1].String()
	interfaceAddr := args[2].String()

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if ok && socket != nil {
		socket.DropMembership(multicastAddr, interfaceAddr)
	}

	return nil
}

func (d *Dgram) connectFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}

	// id := int64(args[0].Integer())
	// address := args[1].String()
	// port := int(args[2].Integer())
	callback := args[3]

	// UDP "connect" just sets default destination - simplified implementation
	d.callCallback(callback, "")

	return nil
}

func (d *Dgram) refFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if ok && socket != nil {
		socket.Ref()
	}

	return nil
}

func (d *Dgram) unrefFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if ok && socket != nil {
		socket.Unref()
	}

	return nil
}

func (d *Dgram) noopFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return nil
}

func (d *Dgram) noopNumberFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	return ctx.NewNumber(65536)
}

func (d *Dgram) noopNullFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	return ctx.Null()
}

func (d *Dgram) callCallback(callback *v8.Value, errMsg string) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := d.rt.Context()
	var errVal *v8.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	callback.Call(ctx.Undefined(), errVal)
}

func (d *Dgram) callCallbackWithBytes(callback *v8.Value, errMsg string, bytes int) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := d.rt.Context()
	var errVal *v8.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	callback.Call(ctx.Undefined(), errVal, ctx.NewNumber(float64(bytes)))
}

func (d *Dgram) callReceiveCallback(callback *v8.Value, errMsg string, msg *v8.Value, rinfo *v8.Value) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := d.rt.Context()
	var errVal *v8.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	if msg == nil {
		msg = ctx.Null()
	}
	if rinfo == nil {
		rinfo = ctx.Null()
	}

	callback.Call(ctx.Undefined(), errVal, msg, rinfo)
}
