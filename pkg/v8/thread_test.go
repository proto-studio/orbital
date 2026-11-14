package v8_test

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"proto.zip/studio/orbital/pkg/v8"
)

const preInitThreadEnv = "ORBITAL_V8_PRE_INIT_THREAD_CHILD"

// TestIsolateOnThreadOlderThanV8 runs an isolate on an OS thread that existed
// before V8 was initialized. With V8's memory protection keys on (x64 Linux
// with PKU hardware), such a thread can't read V8's code pages and the process
// dies with SEGV_PKUERR. It runs in a fresh process so no earlier test has
// initialized V8 yet.
func TestIsolateOnThreadOlderThanV8(t *testing.T) {
	if os.Getenv(preInitThreadEnv) == "1" {
		runOnThreadOlderThanV8(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestIsolateOnThreadOlderThanV8$", "-test.count=1")
	cmd.Env = append(os.Environ(), preInitThreadEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process failed: %v\n%s", err, out)
	}
}

func runOnThreadOlderThanV8(t *testing.T) {
	// Pin a goroutine to its OS thread before V8 exists in this process.
	ready := make(chan struct{})
	initialized := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		close(ready)
		<-initialized
		done <- useIsolate()
	}()
	<-ready

	// Initialize V8 elsewhere: the pinned thread is busy waiting, so this
	// goroutine runs on a different one.
	if err := v8.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	close(initialized)

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func useIsolate() error {
	iso, err := v8.NewIsolate()
	if err != nil {
		return err
	}
	defer iso.Dispose()
	ctx, err := iso.NewContext()
	if err != nil {
		return err
	}
	defer ctx.Dispose()
	// Run hot enough for V8 to compile machine code, then collect garbage so
	// the GC walks the code pages too.
	if _, err := ctx.RunScript(`
		function add(a, b) { return a + b; }
		let s = 0;
		for (let i = 0; i < 200000; i++) s = add(s, i);
		gc();
		s;
	`, "hot.js"); err != nil {
		return err
	}
	return iso.RequestGarbageCollectionForTesting(v8.FullGarbageCollection)
}
