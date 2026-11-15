package net

import (
	"errors"
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
	queued  int
	full    bool
	onDrain func()
	ended   bool
	closed  bool
	onWrite func(data []byte, seq uint64)
	onEnd   func(seq uint64)
	onClose func()
	// Writes and the end are numbered in the order they are handed to JavaScript, which
	// reports them done in the same order.
	issued    uint64
	done      uint64
	writeErr  error
	finishing bool

	// held keeps the event loop running while JavaScript has asked to read and the read
	// has bytes to process. A read waiting on an empty pipe, or bytes nobody has asked for,
	// hold nothing open, as with a JavaScript stream in Node.
	loop    *runtime.EventLoop
	held    bool
	readers int
}

// NewPipe registers a socket whose writes go to onWrite and whose reads return what Push
// supplies. onEnd is called when the writing side finishes and onClose once when the socket
// closes. Write returns at once; the socket's writer waits with WaitWritten until Written
// reports what was handed over done, so it never blocks the JavaScript thread itself.
func (n *Net) NewPipe(onWrite func(data []byte, seq uint64), onEnd func(seq uint64), onClose func()) (int64, *PipeSocket) {
	pipe := &PipeSocket{
		onWrite: onWrite, onEnd: onEnd, onClose: onClose,
		loop: n.rt.EventLoop(),
	}
	pipe.cond = sync.NewCond(&pipe.mu)
	id, conn := n.newConn()
	conn.mu.Lock()
	conn.noHold = true
	conn.beforeRead = pipe.reserve
	conn.afterRead = pipe.release
	conn.afterWrite = pipe.WaitWritten
	conn.mu.Unlock()
	conn.SetSocket(pipe)
	return id, pipe
}

// pipeHighWater is how much unread data a pipe queues before asking its stream to pause.
const pipeHighWater = 64 * 1024

// SetOnDrain sets what to call when a queue that was full has been read down, so the
// stream can be resumed.
func (p *PipeSocket) SetOnDrain(onDrain func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onDrain = onDrain
}

// Push makes data available to Read. It reports false once the queue is full: the stream
// should pause until the drain callback runs.
func (p *PipeSocket) Push(data []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.ended || len(data) == 0 {
		return true
	}
	p.chunks = append(p.chunks, append([]byte(nil), data...))
	p.queued += len(data)
	if p.readers > 0 {
		p.holdLocked(true)
	}
	p.cond.Broadcast()
	if p.queued >= pipeHighWater {
		p.full = true
	}
	return !p.full
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

// reserve is called on the JavaScript thread when a read is requested, so the loop stays up
// until the read has started.
func (p *PipeSocket) reserve() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readers++
	p.holdLocked(true)
}

// release is called once a requested read has returned, by which time the reader holds the
// loop itself until it has delivered what it read.
func (p *PipeSocket) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readers > 0 {
		p.readers--
	}
	p.holdLocked(false)
}

// End makes Read report the end of the stream once the data pushed so far is read. A read
// waiting for data holds the loop again, so the end is delivered.
func (p *PipeSocket) End() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = true
	if p.readers > 0 {
		p.holdLocked(true)
	}
	p.cond.Broadcast()
}

// Read waits for pushed data.
func (p *PipeSocket) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.chunks) == 0 && !p.ended && !p.closed {
		p.holdLocked(false)
		p.cond.Wait()
	}
	if p.closed {
		return 0, runtime.ErrSocketClosed
	}
	p.holdLocked(p.readers > 0)
	if len(p.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(buf, p.chunks[0])
	if n == len(p.chunks[0]) {
		p.chunks = p.chunks[1:]
	} else {
		p.chunks[0] = p.chunks[0][n:]
	}
	p.queued -= n
	if p.full && p.queued < pipeHighWater/2 {
		p.full = false
		if p.onDrain != nil {
			p.onDrain()
		}
	}
	return n, nil
}

// Written reports that the stream has taken the write, or finished, numbered seq. A
// non-empty message is the stream's error.
func (p *PipeSocket) Written(seq uint64, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if seq > p.done {
		p.done = seq
	}
	if message != "" && p.writeErr == nil {
		p.writeErr = errors.New(message)
	}
	p.cond.Broadcast()
}

// WaitWritten waits until the stream has taken everything handed to it so far, so the
// stream's backpressure reaches the writer. It must not run on the JavaScript thread.
func (p *PipeSocket) WaitWritten() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	target := p.issued
	for p.done < target && p.writeErr == nil && !p.closed {
		p.cond.Wait()
	}
	if p.writeErr != nil {
		return p.writeErr
	}
	if p.done < target {
		return runtime.ErrSocketClosed
	}
	return nil
}

// Write hands a copy of data to JavaScript.
func (p *PipeSocket) Write(data []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, runtime.ErrSocketClosed
	}
	if p.writeErr != nil {
		err := p.writeErr
		p.mu.Unlock()
		return 0, err
	}
	p.issued++
	seq := p.issued
	p.mu.Unlock()
	p.onWrite(append([]byte(nil), data...), seq)
	return len(data), nil
}

// CloseWrite ends the stream and waits until it has finished. It is only called off the
// JavaScript thread.
func (p *PipeSocket) CloseWrite() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return runtime.ErrSocketClosed
	}
	if p.finishing {
		p.mu.Unlock()
		return nil
	}
	p.finishing = true
	p.issued++
	seq := p.issued
	p.mu.Unlock()
	p.onEnd(seq)
	return p.WaitWritten()
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
