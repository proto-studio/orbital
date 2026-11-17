package runtime

import (
	"errors"
	"testing"
)

func TestStartIsolate_CountsAgainstMaxWorkers(t *testing.T) {
	parent, err := New(&Config{MaxWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Dispose()
	child, err := New(&Config{MaxWorkers: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Dispose()
	child.ShareIsolateLimit(parent)

	release, err := parent.StartIsolate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := child.StartIsolate(); !errors.Is(err, ErrWorkerLimit) {
		t.Fatalf("child = %v, want ErrWorkerLimit from the parent's cap", err)
	}
	release()
	release()
	if _, err := child.StartIsolate(); err != nil {
		t.Fatalf("after a release: %v", err)
	}
	if _, err := parent.StartIsolate(); !errors.Is(err, ErrWorkerLimit) {
		t.Fatalf("past the cap after a double release: %v", err)
	}

	none, err := New(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer none.Dispose()
	if _, err := none.StartIsolate(); !errors.Is(err, ErrWorkerLimit) {
		t.Fatalf("MaxWorkers 0: %v, want ErrWorkerLimit", err)
	}
}
