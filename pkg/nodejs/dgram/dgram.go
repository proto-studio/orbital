// Package dgram implements the Node.js dgram module for UDP networking.
package dgram

import (
	_ "embed"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/nodejs/internal/netdial"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed dgram.js
var dgramJS string

// Dgram provides UDP socket functionality.
type Dgram struct {
	rt      *runtime.Runtime
	sockets map[int64]runtime.UDPSocket
	holds   map[int64]*holdState
	// slots give back each bound socket's place under Config.MaxOpenSockets.
	slots    map[int64]func()
	socketID int64
	mu       sync.Mutex
}

// holdState tracks whether one socket keeps the event loop alive.
type holdState struct {
	ref     bool
	waiting bool
	held    bool
	closed  bool
}

// New creates a new Dgram module.
func New() *Dgram {
	return &Dgram{
		sockets: make(map[int64]runtime.UDPSocket),
		holds:   make(map[int64]*holdState),
		slots:   make(map[int64]func()),
	}
}

// Name returns the module name.
func (d *Dgram) Name() string {
	return "dgram"
}

// Register sets up the dgram module.
func (d *Dgram) Register(rt *runtime.Runtime) error {
	d.rt = rt
	// Sockets still open when the runtime is disposed close with it, giving back
	// their places under Config.MaxOpenSockets, which a worker shares with its parent.
	rt.TrackResource(closeAll{d})
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create internal object with native functions
	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8.FunctionCallback{
		"createSocket":    d.createSocketFunc,
		"bind":            d.bindFunc,
		"send":            d.sendFunc,
		"receive":         d.receiveFunc,
		"close":           d.closeFunc,
		"address":         d.addressFunc,
		"setBroadcast":    d.setBroadcastFunc,
		"setMulticastTTL": d.setMulticastTTLFunc,
		"setOption":       d.setOptionFunc,
		"bufferSize":      d.bufferSizeFunc,
		"addMembership":   d.addMembershipFunc,
		"dropMembership":  d.dropMembershipFunc,
		"resolve":         d.resolveFunc,
		"ref":             d.refFunc,
		"unref":           d.unrefFunc,
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

// closeAll closes the sockets still open when the runtime is disposed.
type closeAll struct{ d *Dgram }

func (c closeAll) Close() error {
	c.d.mu.Lock()
	sockets := c.d.sockets
	slots := c.d.slots
	c.d.sockets = make(map[int64]runtime.UDPSocket)
	c.d.slots = make(map[int64]func())
	c.d.mu.Unlock()
	for _, socket := range sockets {
		if socket != nil {
			socket.Close()
		}
	}
	for _, release := range slots {
		release()
	}
	return nil
}

// takeSlot counts a socket against Config.MaxOpenSockets once, when it binds, explicitly
// or on its first send; closing it gives the slot back. Over the cap it fails with an
// "EMFILE: ..." error, which dgram.js reports as Node does.
func (d *Dgram) takeSlot(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.slots[id] != nil {
		return nil
	}
	release, err := d.rt.OpenSocket()
	if err != nil {
		return errors.New("EMFILE: too many open files")
	}
	d.slots[id] = release
	return nil
}

// dropSlotIfUnbound gives back the slot takeSlot took for a socket whose bind failed
// before it opened anything, explicitly or on its first send, so failed binds don't use
// up the cap.
func (d *Dgram) dropSlotIfUnbound(id int64, socket runtime.UDPSocket) {
	if socket.LocalAddr() != nil {
		return
	}
	d.mu.Lock()
	release := d.slots[id]
	delete(d.slots, id)
	d.mu.Unlock()
	if release != nil {
		release()
	}
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
	if err := d.takeSlot(id); err != nil {
		d.rt.EventLoop().EnqueueMicrotask(func() { d.callCallback(callback, err.Error()) })
		return nil
	}

	d.rt.EventLoop().AddPendingWork()
	go func() {
		defer d.rt.EventLoop().DonePendingWork()

		err := socket.Bind(nil, address, port)
		if err != nil {
			d.dropSlotIfUnbound(id, socket)
		}

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
	if len(args) < 6 {
		return nil
	}

	id := int64(args[0].Integer())
	msg := args[1].String()
	address := args[2].String()
	port := int(args[3].Integer())
	family := int(args[4].Integer())
	callback := args[5]

	d.mu.Lock()
	socket, ok := d.sockets[id]
	d.mu.Unlock()

	if !ok {
		d.callCallbackWithBytes(callback, "Socket not found", 0)
		return nil
	}
	// A socket that hasn't bound binds on its first send.
	if err := d.takeSlot(id); err != nil {
		d.rt.EventLoop().EnqueueMicrotask(func() { d.callCallbackWithBytes(callback, err.Error(), 0) })
		return nil
	}

	data, err := base64.StdEncoding.DecodeString(msg)
	if err != nil {
		d.dropSlotIfUnbound(id, socket)
		d.callCallbackWithBytes(callback, err.Error(), 0)
		return nil
	}

	d.rt.EventLoop().AddPendingWork()
	go func() {
		defer d.rt.EventLoop().DonePendingWork()

		n, err := d.send(socket, data, address, port, family)
		if err != nil {
			d.dropSlotIfUnbound(id, socket)
		}

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

// send resolves a destination name with the injected resolver, so a policy applies to the
// address the datagram actually goes to.
func (d *Dgram) send(socket runtime.UDPSocket, data []byte, address string, port, family int) (int, error) {
	ips, isName, err := netdial.Resolve(d.rt.ExecutionContext(), d.rt, address, family)
	if err != nil {
		return 0, err
	}
	if resolved, ok := socket.(runtime.ResolvedUDPSocket); ok && isName {
		return resolved.SendResolved(data, address, ips[0], port)
	}
	return socket.Send(data, ips[0], port)
}

// receive(id, callback) waits for a datagram. callback receives (errorText, base64Data,
// rinfo). A referenced socket keeps the event loop alive while it waits.
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

	d.setWaiting(id, true)
	go func() {
		buf := make([]byte, 65536)
		n, remoteAddr, remotePort, err := socket.Receive(buf)
		encoded := base64.StdEncoding.EncodeToString(buf[:n])

		d.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := d.rt.Context()
			defer ctx.PerformMicrotaskCheckpoint()

			if err != nil {
				d.callReceiveCallback(callback, err.Error(), nil, nil)
				return
			}

			rinfo, _ := ctx.NewObject()
			addrStr, _ := ctx.NewString(remoteAddr)
			rinfo.Set("address", addrStr)
			rinfo.Set("port", ctx.NewNumber(float64(remotePort)))
			family := "IPv4"
			if ip := net.ParseIP(remoteAddr); ip != nil && ip.To4() == nil {
				family = "IPv6"
			}
			familyStr, _ := ctx.NewString(family)
			rinfo.Set("family", familyStr)
			rinfo.Set("size", ctx.NewNumber(float64(n)))

			msgStr, _ := ctx.NewString(encoded)
			d.callReceiveCallback(callback, "", msgStr, rinfo)
		})
		d.setWaiting(id, false)
	}()

	return nil
}

// setWaiting records whether a receive is outstanding. Only a referenced socket's receive
// holds the event loop, as in Node.
func (d *Dgram) setWaiting(id int64, waiting bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.holds[id]
	if state == nil {
		if !waiting {
			return
		}
		state = &holdState{ref: true}
		d.holds[id] = state
	}
	state.waiting = waiting
	d.updateHoldLocked(id, state)
}

func (d *Dgram) updateHoldLocked(id int64, state *holdState) {
	want := state.waiting && state.ref
	if want != state.held {
		state.held = want
		if want {
			d.rt.EventLoop().AddPendingWork()
		} else {
			d.rt.EventLoop().DonePendingWork()
		}
	}
	if !state.waiting && state.closed {
		delete(d.holds, id)
	}
}

func (d *Dgram) setRef(id int64, ref bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.holds[id]
	if state == nil {
		state = &holdState{ref: ref}
		d.holds[id] = state
	}
	state.ref = ref
	d.updateHoldLocked(id, state)
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
	release := d.slots[id]
	delete(d.slots, id)
	d.mu.Unlock()

	if ok && socket != nil {
		socket.Close()
	}
	if release != nil {
		release()
	}

	d.mu.Lock()
	if state := d.holds[id]; state != nil {
		// Closing ends the wait, and nothing can receive on the socket after this.
		state.closed = true
		state.waiting = false
		d.updateHoldLocked(id, state)
	}
	d.mu.Unlock()

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

	host, port := addr.String(), 0
	if udp, ok := addr.(*net.UDPAddr); ok {
		host, port = udp.IP.String(), udp.Port
	}
	family := "IPv4"
	if strings.Contains(host, ":") {
		family = "IPv6"
	}

	obj, _ := ctx.NewObject()
	addrStr, _ := ctx.NewString(host)
	obj.Set("address", addrStr)
	obj.Set("port", ctx.NewNumber(float64(port)))
	familyStr, _ := ctx.NewString(family)
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

// setOption(id, name, ...args) sets a socket option and returns null, or an error code.
func (d *Dgram) setOptionFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Null()
	}
	d.mu.Lock()
	socket, ok := d.sockets[int64(args[0].Integer())]
	d.mu.Unlock()
	if !ok {
		s, _ := ctx.NewString("EBADF")
		return s
	}
	options, ok := socket.(runtime.UDPSocketOptions)
	if !ok {
		s, _ := ctx.NewString("ENOTSUP")
		return s
	}
	text := func(i int) string {
		if i < len(args) {
			return args[i].String()
		}
		return ""
	}
	number := func(i int) int {
		if i < len(args) {
			return int(args[i].Integer())
		}
		return 0
	}
	var err error
	switch args[1].String() {
	case "ttl":
		err = options.SetTTL(number(2))
	case "multicastLoopback":
		err = options.SetMulticastLoopback(number(2) != 0)
	case "multicastInterface":
		err = options.SetMulticastInterface(text(2))
	case "joinSource":
		err = options.JoinSourceGroup(text(2), text(3), text(4))
	case "leaveSource":
		err = options.LeaveSourceGroup(text(2), text(3), text(4))
	case "recvBuffer":
		err = options.SetReadBuffer(number(2))
	case "sendBuffer":
		err = options.SetWriteBuffer(number(2))
	}
	if err == nil {
		return ctx.Null()
	}
	s, _ := ctx.NewString(netdial.Describe(err, "setsockopt", "", 0).Code)
	return s
}

// bufferSize(id, name) returns the receive ("recvBuffer") or send ("sendBuffer") buffer size the
// operating system reports for the socket, an error code string, or undefined when the socket
// can't report it.
func (d *Dgram) bufferSizeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Undefined()
	}
	d.mu.Lock()
	socket, ok := d.sockets[int64(args[0].Integer())]
	d.mu.Unlock()
	if !ok {
		s, _ := ctx.NewString("EBADF")
		return s
	}
	sizes, ok := socket.(runtime.UDPSocketBufferSizes)
	if !ok {
		return ctx.Undefined()
	}
	var size int
	var err error
	if args[1].String() == "sendBuffer" {
		size, err = sizes.WriteBufferSize()
	} else {
		size, err = sizes.ReadBufferSize()
	}
	if errors.Is(err, errors.ErrUnsupported) {
		return ctx.Undefined()
	}
	if err != nil {
		s, _ := ctx.NewString(netdial.Describe(err, "getsockopt", "", 0).Code)
		return s
	}
	return ctx.NewNumber(float64(size))
}

// resolve(host, family, callback) resolves a destination with the injected resolver.
// callback receives (errorText, ip).
func (d *Dgram) resolveFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	host := args[0].String()
	family := int(args[1].Integer())
	callback := args[2]

	d.rt.EventLoop().AddPendingWork()
	go func() {
		defer d.rt.EventLoop().DonePendingWork()
		ips, _, err := netdial.Resolve(d.rt.ExecutionContext(), d.rt, host, family)
		d.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := d.rt.Context()
			if err != nil {
				desc := netdial.Describe(err, "getaddrinfo", host, 0)
				text, _ := ctx.NewString(desc.Code + ": " + desc.Message)
				d.rt.MakeCallback(callback, ctx.Undefined(), text, ctx.Null())
			} else {
				ip, _ := ctx.NewString(ips[0])
				d.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null(), ip)
			}
			ctx.PerformMicrotaskCheckpoint()
		})
	}()
	return nil
}

func (d *Dgram) refFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	if args := info.Args(); len(args) >= 1 {
		d.setRef(int64(args[0].Integer()), true)
	}
	return nil
}

func (d *Dgram) unrefFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	if args := info.Args(); len(args) >= 1 {
		d.setRef(int64(args[0].Integer()), false)
	}
	return nil
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

	d.rt.MakeCallback(callback, ctx.Undefined(), errVal)
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

	d.rt.MakeCallback(callback, ctx.Undefined(), errVal, ctx.NewNumber(float64(bytes)))
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

	d.rt.MakeCallback(callback, ctx.Undefined(), errVal, msg, rinfo)
}
