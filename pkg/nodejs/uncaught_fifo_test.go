//go:build unix

package nodejs

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A frame naming a FIFO isn't read, which could block the report: the header shows the
// line from the source V8 compiled.
func TestUncaughtSourceSkipsFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := runUncaught(t, fmt.Sprintf("eval(%q);", "throw new Error('fifo')\n//# sourceURL="+fifo))
		done <- err
	}()
	select {
	case err := <-done:
		var uncaught *runtime.UncaughtError
		want := fifo + ":1\nthrow new Error('fifo')\n^\n"
		if !errors.As(err, &uncaught) || uncaught.Source != want {
			t.Fatalf("Run returned %v (source %q), want the uncaught error with source %q", err, uncaughtSource(err), want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the fatal report blocked reading a FIFO")
	}
}

func uncaughtSource(err error) string {
	var uncaught *runtime.UncaughtError
	if errors.As(err, &uncaught) {
		return uncaught.Source
	}
	return ""
}
