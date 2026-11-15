package net

import (
	"io"
	"sync"

	"proto.zip/studio/orbital/pkg/runtime"
)

// PipeSocket is a transport whose bytes come from and go to JavaScript, so a protocol such
// as TLS can run over any duplex stream rather than a network socket.
type PipeSocket struct {
	runtime.NoOpTCPSocket

	mu      sync.Mutex
	cond    *sync.Cond
	chunks  [][]byte
	ended   bool
	closed  bool
	onWrite func([]byte)
	onEnd   func()
	onClose func()

	// held keeps the event loop running from when bytes arrive until the reader comes back
	// for more with nothing left, so the bytes are processed before the process can end.
	// Otherwise, as with a JavaScript stream in Node, an idle pipe holds nothing open.
	loop *runtime.EventLoop
	held bool
}

// NewPipe registers a socket whose writes go to onWrite and whose reads return what Push
// supplies. onEnd runs when the writing side finishes, and onClose once when the socket closes.
func (n *Net) NewPipe(onWrite func([]byte), onEnd, onClose func()) (int64, *PipeSocket) {
	pipe := &PipeSocket{onWrite: onWrite, onEnd: onEnd, onClose: onClose, loop: n.rt.EventLoop()}
	pipe.cond = sync.NewCond(&pipe.mu)
	id, conn := n.newConn()
	conn.mu.Lock()
	conn.noHold = true
	conn.mu.Unlock()
	conn.SetSocket(pipe)
	return id, pipe
}

// Push makes data available to Read.
func (p *PipeSocket) Push(data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.ended || len(data) == 0 {
		return
	}
	p.chunks = append(p.chunks, append([]byte(nil), data...))
	p.holdLocked(true)
	p.cond.Broadcast()
}

func (p *PipeSocket) holdLocked(hold bool) {
	if hold == p.held {
		return
	}
	p.held = hold
	if hold {
		p.loop.AddPendingWork()
	} else {
		p.loop.DonePendingWork()
	}
}

// End makes Read report the end of the stream once the data pushed so far is read.
func (p *PipeSocket) End() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = true
	p.cond.Broadcast()
}

// Read waits for pushed data.
func (p *PipeSocket) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.chunks) == 0 {
		p.holdLocked(false)
	}
	for len(p.chunks) == 0 && !p.ended && !p.closed {
		p.cond.Wait()
	}
	if p.closed {
		return 0, runtime.ErrSocketClosed
	}
	if len(p.chunks) == 0 {
		p.holdLocked(false)
		return 0, io.EOF
	}
	n := copy(buf, p.chunks[0])
	if n == len(p.chunks[0]) {
		p.chunks = p.chunks[1:]
	} else {
		p.chunks[0] = p.chunks[0][n:]
	}
	return n, nil
}

// Write hands a copy of data to JavaScript.
func (p *PipeSocket) Write(data []byte) (int, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, runtime.ErrSocketClosed
	}
	p.onWrite(append([]byte(nil), data...))
	return len(data), nil
}

// CloseWrite finishes the writing side.
func (p *PipeSocket) CloseWrite() error {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return runtime.ErrSocketClosed
	}
	p.onEnd()
	return nil
}

// Close stops reads and writes.
func (p *PipeSocket) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.holdLocked(false)
	p.cond.Broadcast()
	p.mu.Unlock()
	p.onClose()
	return nil
}
