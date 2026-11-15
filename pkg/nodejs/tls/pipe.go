package tls

import (
	"encoding/base64"

	"proto.zip/studio/orbital/pkg/v8"
)

// createPipe(onWrite, onEnd) returns the id of a socket that carries TLS over a JavaScript
// stream. onWrite(base64, seq) receives the bytes to send and onEnd(seq) is called when no
// more will come; the script reports each done with pipeWritten(id, seq, errorMessage).
// pipePush and pipeEnd hand the socket what the stream receives.
func (t *TLS) createPipeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 || !args[0].IsFunction() || !args[1].IsFunction() {
		return ctx.Throw("tls: invalid arguments")
	}
	onWrite, onEnd := args[0], args[1]
	var id int64
	id, pipe := t.net.NewPipe(func(data []byte, seq uint64) {
		t.deliver(func(ctx *v8.Context) {
			t.rt.MakeCallback(onWrite, ctx.Undefined(), jsString(ctx, base64.StdEncoding.EncodeToString(data)), ctx.NewNumber(float64(seq)))
		})
	}, func(seq uint64) {
		t.deliver(func(ctx *v8.Context) { t.rt.MakeCallback(onEnd, ctx.Undefined(), ctx.NewNumber(float64(seq))) })
	}, func() {
		t.mu.Lock()
		delete(t.pipes, id)
		t.mu.Unlock()
	})
	t.mu.Lock()
	t.pipes[id] = pipe
	t.mu.Unlock()
	return ctx.NewNumber(float64(id))
}

// pipePush(id, base64) passes bytes the stream received to the socket.
func (t *TLS) pipePushFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	t.mu.Lock()
	pipe := t.pipes[int64(args[0].Integer())]
	t.mu.Unlock()
	if pipe == nil {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		return info.Context().Throw("tls: invalid pipe data")
	}
	pipe.Push(data)
	return nil
}

// pipeEnd(id) tells the socket the stream has ended.
func (t *TLS) pipeEndFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	t.mu.Lock()
	pipe := t.pipes[int64(args[0].Integer())]
	t.mu.Unlock()
	if pipe != nil {
		pipe.End()
	}
	return nil
}

// pipeWritten(id, seq, errorMessage) reports that the stream took a write, or finished.
func (t *TLS) pipeWrittenFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	t.mu.Lock()
	pipe := t.pipes[int64(args[0].Integer())]
	t.mu.Unlock()
	if pipe != nil {
		pipe.Written(uint64(args[1].Integer()), args[2].String())
	}
	return nil
}
