package crypto

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"

	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed randombytes.js
var randomBytesJS string

// Work for the callback forms of the crypto functions runs on the runtime's
// thread pool, as Node runs it on the libuv threadpool, so timers and I/O
// keep going while it does. The result comes back to the JS thread through
// the event loop's queue, and the loop stays alive until it has.

// beforeOffThreadWork, when set by a test, runs on the pool before the work
// does.
var beforeOffThreadWork func()

// runOffThread runs work on the runtime's thread pool, then calls callback
// on the JS thread with the values deliver builds from work's result.
func (c *Crypto) runOffThread(callback *v8.Value, work func() func(ctx *v8.Context) []*v8.Value) {
	loop := c.rt.EventLoop()
	ctx := c.rt.Context()
	loop.AddPendingWork()
	c.rt.RunInThreadPool(func() {
		defer loop.DonePendingWork()
		if beforeOffThreadWork != nil {
			beforeOffThreadWork()
		}
		deliver := work()
		loop.EnqueueMicrotask(func() {
			c.rt.MakeCallback(callback, nil, deliver(ctx)...)
		})
	})
}

// errorArgs is the callback arguments for a failure: the message, for the JS
// wrapper to turn into an Error.
func errorArgs(ctx *v8.Context, err error) []*v8.Value {
	msg, _ := ctx.NewString(err.Error())
	return []*v8.Value{msg}
}

// randomBytesAsyncFunc backs randomBytes(size, callback). The JS wrapper has
// validated size; callback gets (undefined, base64) or (message).
func (c *Crypto) randomBytesAsyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 || !args[1].IsFunction() {
		return ctx.Throw("randomBytes: size and callback are required")
	}
	size := args[0].Integer()
	if size < 0 || size > maxNativeBytes {
		return ctx.Throw(outOfRange("size", 0, maxNativeBytes, size))
	}
	c.runOffThread(args[1], func() func(*v8.Context) []*v8.Value {
		buf := make([]byte, size)
		if _, err := rand.Read(buf); err != nil {
			return func(ctx *v8.Context) []*v8.Value { return errorArgs(ctx, err) }
		}
		encoded := base64.StdEncoding.EncodeToString(buf)
		return func(ctx *v8.Context) []*v8.Value {
			data, _ := ctx.NewString(encoded)
			return []*v8.Value{ctx.Undefined(), data}
		}
	})
	return ctx.Undefined()
}

// generateKeyPairAsyncFunc backs generateKeyPair(type, options, callback).
// callback gets (undefined, publicHandle, privateHandle) or (message).
func (c *Crypto) generateKeyPairAsyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 || !args[2].IsFunction() {
		return ctx.Throw("generateKeyPair: type, options and callback are required")
	}
	kind := args[0].String()
	optsJSON := ""
	if !args[1].IsUndefined() && !args[1].IsNull() {
		optsJSON = args[1].String()
	}
	c.runOffThread(args[2], func() func(*v8.Context) []*v8.Value {
		pubID, privID, err := c.doGenerateKeyPair(kind, optsJSON)
		if err != nil {
			return func(ctx *v8.Context) []*v8.Value { return errorArgs(ctx, err) }
		}
		return func(ctx *v8.Context) []*v8.Value {
			return []*v8.Value{ctx.Undefined(), ctx.NewNumber(float64(pubID)), ctx.NewNumber(float64(privID))}
		}
	})
	return ctx.Undefined()
}
