package zlib

import (
	"errors"
	"testing"
)

func TestLimitedBuffer_StopsAtTheLimit(t *testing.T) {
	b := &limitedBuffer{limit: 10}
	if n, err := b.Write(make([]byte, 6)); n != 6 || err != nil {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := b.Write(make([]byte, 4)); n != 4 || err != nil {
		t.Fatalf("write up to the limit = %d, %v", n, err)
	}
	if _, err := b.Write([]byte{0}); !errors.Is(err, errTooLarge) {
		t.Fatalf("write past the limit = %v, want errTooLarge", err)
	}
	if b.buf.Len() != 10 {
		t.Fatalf("kept %d bytes, want 10", b.buf.Len())
	}
}

// The natives keep a script-supplied limit within what one result can carry, since
// scripts can call them directly.
func TestMaxOutput_IsWhatOneStringCarries(t *testing.T) {
	if maxOutput != (1<<29-24)/4*3 {
		t.Fatalf("maxOutput = %d", maxOutput)
	}
}
