package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

var binaryPayload = []byte{0x41, 0, 0x42, 0xff, 0, 0x43}

const emitBinaryPayload = `printf '\101\000\102\377\000\103'`

// Output has to stay readable after Wait: a process commonly exits before anything has
// read what it wrote, and JavaScript reports the exit while its streams are still open.
func TestRealSpawnerOutputSurvivesWait(t *testing.T) {
	proc, err := NewRealProcessSpawner().Spawn(context.Background(), "/bin/sh",
		[]string{"-c", emitBinaryPayload + "; " + emitBinaryPayload + " >&2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Stdout().Close()
	defer proc.Stderr().Close()
	proc.Stdin().Close()

	if code, err := proc.Wait(); err != nil || code != 0 {
		t.Fatalf("Wait() = %d, %v", code, err)
	}
	for name, reader := range map[string]io.Reader{"stdout": proc.Stdout(), "stderr": proc.Stderr()} {
		got, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, binaryPayload) {
			t.Errorf("%s = %x, want %x", name, got, binaryPayload)
		}
	}
}

func TestRealSpawnerInput(t *testing.T) {
	spawner := NewRealProcessSpawner()
	options := &SpawnOptions{Input: binaryPayload}

	stdout, _, code, err := spawner.Exec(context.Background(), "cat", options)
	if err != nil || code != 0 || !bytes.Equal(stdout, binaryPayload) {
		t.Errorf("Exec = %x, %d, %v, want %x", stdout, code, err, binaryPayload)
	}
	stdout, _, code, err = spawner.ExecFile(context.Background(), "cat", nil, options)
	if err != nil || code != 0 || !bytes.Equal(stdout, binaryPayload) {
		t.Errorf("ExecFile = %x, %d, %v, want %x", stdout, code, err, binaryPayload)
	}
}

func TestRealChildProcess_ExitSignal(t *testing.T) {
	proc, err := NewRealProcessSpawner().Spawn(context.Background(), "/bin/sh", []string{"-c", "kill -TERM $$"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, err := proc.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if code != -1 || proc.ExitSignal() != syscall.SIGTERM {
		t.Fatalf("exit = %d, %v; want -1, SIGTERM", code, proc.ExitSignal())
	}

	proc, err = NewRealProcessSpawner().Spawn(context.Background(), "/bin/sh", []string{"-c", "exit 3"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := proc.Wait(); code != 3 || proc.ExitSignal() != 0 {
		t.Fatalf("exit = %d, %v; want 3, no signal", code, proc.ExitSignal())
	}
}

func TestRealChildProcess_Stdio(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	proc, err := NewRealProcessSpawner().Spawn(context.Background(), "/bin/sh",
		[]string{"-c", "echo to-file; echo discarded >&2; echo to-pipe >&3; if (echo x >&4) 2>/dev/null; then exit 9; fi"},
		&SpawnOptions{Stdio: []StdioOption{
			{Mode: StdioIgnore},
			{Mode: StdioFile, File: file},
			{Mode: StdioIgnore},
			{Mode: StdioPipe},
			{Mode: StdioIgnore},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if proc.Stdin() != nil || proc.Stdout() != nil || proc.Stderr() != nil {
		t.Fatal("a descriptor that is not a pipe has no stream")
	}
	if proc.Pipe(4) != nil {
		t.Fatal("descriptor 4 is not a pipe")
	}
	got, err := io.ReadAll(proc.Pipe(3))
	if err != nil {
		t.Fatal(err)
	}
	proc.Pipe(3).Close()
	if string(got) != "to-pipe\n" {
		t.Fatalf("pipe = %q", got)
	}
	if code, err := proc.Wait(); code != 0 || err != nil {
		t.Fatalf("exit = %d, %v", code, err)
	}
	contents, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "to-file\n" {
		t.Fatalf("file = %q", contents)
	}
}
