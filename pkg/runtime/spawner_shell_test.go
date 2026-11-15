package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A shell runs the whole line, so allowing its first word must not allow the rest.
func TestSandboxedSpawnWithShellNeedsShellRules(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	line := []string{"hi;", "touch", marker}

	allowlist := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowedCommands: []string{hostPath(t, "echo")}})
	if _, err := allowlist.Spawn(context.Background(), "echo", line, &SpawnOptions{Shell: true}); !errors.Is(err, ErrCommandNotAllowed) {
		t.Fatalf("shell spawn of an allowed name: err = %v, want ErrCommandNotAllowed", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the rest of the shell line ran")
	}

	// Without a shell the same call is allowed, and the arguments stay arguments.
	proc, err := allowlist.Spawn(context.Background(), "echo", line, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(proc.Stdout())
	proc.Wait()
	if !strings.HasPrefix(string(out), "hi; touch") {
		t.Fatalf("echo printed %q", out)
	}

	// AllowShellCommands is what permits a shell line, as it does for Exec.
	shells := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowedCommands: []string{hostPath(t, "echo")}, AllowShellCommands: true})
	proc, err = shells.Spawn(context.Background(), "echo", []string{"ok"}, &SpawnOptions{Shell: true})
	if err != nil {
		t.Fatal(err)
	}
	proc.Wait()

	// In denylist mode a denied shell stays denied.
	denied := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowAll: true, BlockedCommands: []string{"/bin/sh"}})
	if _, err := denied.Spawn(context.Background(), "echo", []string{"x"}, &SpawnOptions{Shell: true}); !errors.Is(err, ErrCommandNotAllowed) {
		t.Fatalf("denylisted shell: err = %v", err)
	}
}

// Exec and ExecFile count against MaxProcesses while they run.
func TestSandboxedExecCountsTowardMaxProcesses(t *testing.T) {
	s := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowAll: true, MaxProcesses: 1})
	proc, err := s.Spawn(context.Background(), "sleep", []string{"1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Exec(context.Background(), "true", nil); err == nil {
		t.Fatal("Exec ran past MaxProcesses")
	}
	if _, _, _, err := s.ExecFile(context.Background(), "true", nil, nil); err == nil {
		t.Fatal("ExecFile ran past MaxProcesses")
	}
	proc.Kill(9)
	proc.Wait()
	if _, _, _, err := s.ExecFile(context.Background(), "true", nil, nil); err != nil {
		t.Fatalf("ExecFile after the limit freed up: %v", err)
	}
}

// ExecFile runs its file without a shell, so the file's own rule applies.
func TestSandboxedExecFileChecksTheFile(t *testing.T) {
	allowlist := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowedCommands: []string{hostPath(t, "echo")}})
	out, _, _, err := allowlist.ExecFile(context.Background(), "echo", []string{"ok"}, &SpawnOptions{Shell: true})
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("allowed file: out = %q, err = %v", out, err)
	}

	denied := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowAll: true, BlockedCommands: []string{hostPath(t, "echo")}})
	if _, _, _, err := denied.ExecFile(context.Background(), "echo", []string{"x"}, &SpawnOptions{Shell: true}); !errors.Is(err, ErrCommandNotAllowed) {
		t.Fatalf("blocked file: err = %v", err)
	}
}
