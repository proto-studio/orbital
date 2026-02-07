package process

import (
	"context"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestRealProcessSpawner_Echo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	spawner := NewRealProcessSpawner()
	ctx := context.Background()

	proc, err := spawner.Spawn(ctx, "echo", []string{"hello", "world"}, nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Wait for completion
	exitCode, err := proc.Wait()
	if err != nil {
		t.Errorf("Process wait failed: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("Exit code should be 0, got %d", exitCode)
	}

	// Check PID
	if proc.Pid() == 0 {
		t.Error("PID should not be 0")
	}
}

func TestRealProcessSpawner_Stdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	spawner := NewRealProcessSpawner()
	ctx := context.Background()

	proc, err := spawner.Spawn(ctx, "echo", []string{"test output"}, nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	stdout := proc.Stdout()
	buf := make([]byte, 100)
	n, _ := stdout.Read(buf)
	output := string(buf[:n])

	proc.Wait()

	if output != "test output\n" {
		t.Errorf("Stdout mismatch: got %q, want %q", output, "test output\n")
	}
}

func TestRealProcessSpawner_Stdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	spawner := NewRealProcessSpawner()
	ctx := context.Background()

	proc, err := spawner.Spawn(ctx, "cat", []string{}, nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	stdin := proc.Stdin()
	stdin.Write([]byte("hello from stdin"))
	stdin.Close()

	stdout := proc.Stdout()
	buf := make([]byte, 100)
	n, _ := stdout.Read(buf)
	output := string(buf[:n])

	proc.Wait()

	if output != "hello from stdin" {
		t.Errorf("Stdin/Stdout mismatch: got %q", output)
	}
}

func TestRealProcessSpawner_Kill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	spawner := NewRealProcessSpawner()
	ctx := context.Background()

	proc, err := spawner.Spawn(ctx, "sleep", []string{"60"}, nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Kill it
	if err := proc.Kill(syscall.SIGKILL); err != nil {
		t.Errorf("Kill failed: %v", err)
	}

	// Wait should return quickly
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Process should have been killed")
	}
}

func TestRealProcessSpawner_Exec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	spawner := NewRealProcessSpawner()
	ctx := context.Background()

	stdout, stderr, exitCode, err := spawner.Exec(ctx, "echo hello", nil)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	if exitCode != 0 {
		t.Errorf("Exit code should be 0, got %d", exitCode)
	}

	if string(stdout) != "hello\n" {
		t.Errorf("Stdout mismatch: got %q", stdout)
	}

	if len(stderr) != 0 {
		t.Errorf("Stderr should be empty, got %q", stderr)
	}
}

func TestNoOpProcessSpawner(t *testing.T) {
	spawner := NewNoOpProcessSpawner()
	ctx := context.Background()

	_, err := spawner.Spawn(ctx, "echo", []string{"test"}, nil)
	if err == nil {
		t.Error("NoOp spawner should always return error")
	}

	_, _, _, err = spawner.Exec(ctx, "echo test", nil)
	if err == nil {
		t.Error("NoOp Exec should return error")
	}
}

func TestSpawnOptions(t *testing.T) {
	opts := &SpawnOptions{
		Cwd:      "/tmp",
		Shell:    true,
		Detached: false,
		Timeout:  30000,
	}

	if opts.Cwd != "/tmp" {
		t.Errorf("Cwd mismatch: got %q", opts.Cwd)
	}
	if !opts.Shell {
		t.Error("Shell should be true")
	}
	if opts.Timeout != 30000 {
		t.Errorf("Timeout mismatch: got %d", opts.Timeout)
	}
}
