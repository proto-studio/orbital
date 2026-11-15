package net

import (
	"context"
	goruntime "runtime"
	"sync/atomic"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

type closeCounter struct {
	runtime.NoOpTCPSocket
	closed atomic.Int32
}

func (s *closeCounter) Close() error {
	s.closed.Add(1)
	return nil
}

// An attempt that connects just as JavaScript gives up on it doesn't stay the transport.
func TestAbortClosesATransportPromotedByThatAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Conn{ctx: ctx, cancel: cancel}
	seq, _ := c.beginAttempt()
	socket := &closeCounter{}
	if !c.finishAttempt(seq, socket) {
		t.Fatal("the current attempt was not promoted")
	}
	c.abortAttempt()
	if c.Socket() != nil {
		t.Fatal("the transport survived its attempt being given up on")
	}
	for i := 0; i < 1000 && socket.closed.Load() == 0; i++ {
		goruntime.Gosched()
	}
	if socket.closed.Load() != 1 {
		t.Fatalf("closed %d times", socket.closed.Load())
	}

	// The next attempt becomes the transport as usual.
	seq, _ = c.beginAttempt()
	next := &closeCounter{}
	if !c.finishAttempt(seq, next) || c.Socket() != next {
		t.Fatal("the next attempt did not become the transport")
	}
}
