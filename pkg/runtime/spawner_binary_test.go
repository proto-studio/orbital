package runtime

import (
	"bytes"
	"context"
	"io"
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
