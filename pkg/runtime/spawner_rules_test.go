package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hostPath returns the resolved path of a program on the host's PATH.
func hostPath(t *testing.T, name string) string {
	t.Helper()
	found, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found: %v", name, err)
	}
	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// runOutput spawns command and returns its trimmed output, or the spawn error.
func runOutput(s ProcessSpawner, command string, args []string, options *SpawnOptions) (string, error) {
	proc, err := s.Spawn(context.Background(), command, args, options)
	if err != nil {
		return "", err
	}
	out, _ := io.ReadAll(proc.Stdout())
	proc.Wait()
	return strings.TrimSpace(string(out)), nil
}

// writeExecutable writes a shell script that prints output and makes it executable.
func writeExecutable(t *testing.T, path, output string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+output+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxedSpawnerComparesResolvedPaths(t *testing.T) {
	echo := hostPath(t, "echo")
	s := NewAllowListProcessSpawner(echo)
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}

	t.Run("full path", func(t *testing.T) {
		if out, err := runOutput(s, echo, []string{"ok"}, nil); err != nil || out != "ok" {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})

	t.Run("bare name found on PATH", func(t *testing.T) {
		if out, err := runOutput(s, "echo", []string{"ok"}, nil); err != nil || out != "ok" {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})

	t.Run("same name in another directory", func(t *testing.T) {
		dir := t.TempDir()
		other := filepath.Join(dir, "echo")
		writeExecutable(t, other, "other")
		if _, err := runOutput(s, other, nil, nil); !errors.Is(err, ErrCommandNotAllowed) {
			t.Fatalf("err = %v, want ErrCommandNotAllowed", err)
		}
		// A relative path is resolved against the working directory.
		if _, err := runOutput(s, "./echo", nil, &SpawnOptions{Cwd: dir}); !errors.Is(err, ErrCommandNotAllowed) {
			t.Fatalf("relative: err = %v, want ErrCommandNotAllowed", err)
		}
		if _, _, _, err := s.ExecFile(context.Background(), other, nil, nil); !errors.Is(err, ErrCommandNotAllowed) {
			t.Fatalf("ExecFile: err = %v, want ErrCommandNotAllowed", err)
		}
	})

	t.Run("symlink to an allowed program", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "say")
		if err := os.Symlink(echo, link); err != nil {
			t.Fatal(err)
		}
		if out, err := runOutput(s, link, []string{"ok"}, nil); err != nil || out != "ok" {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})

	t.Run("symlink named like an allowed program", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "echo")
		if err := os.Symlink(hostPath(t, "true"), link); err != nil {
			t.Fatal(err)
		}
		if _, err := runOutput(s, link, nil, nil); !errors.Is(err, ErrCommandNotAllowed) {
			t.Fatalf("err = %v, want ErrCommandNotAllowed", err)
		}
	})
}

// An allowed entry that is a symlink allows its target, under any name that resolves to it.
func TestSandboxedSpawnerResolvesEntries(t *testing.T) {
	echo := hostPath(t, "echo")
	link := filepath.Join(t.TempDir(), "say")
	if err := os.Symlink(echo, link); err != nil {
		t.Fatal(err)
	}
	s := NewAllowListProcessSpawner(link)
	if out, err := runOutput(s, echo, []string{"ok"}, nil); err != nil || out != "ok" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// The process sees the name it was given as argv[0], though the resolved path is what runs.
func TestSandboxedSpawnerKeepsArgv0(t *testing.T) {
	s := NewAllowListProcessSpawner(hostPath(t, "sh"))
	out, err := runOutput(s, "sh", []string{"-c", "echo $0"}, nil)
	if err != nil || out != "sh" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestSandboxedSpawnerBlockedPath(t *testing.T) {
	echo := hostPath(t, "echo")
	s := NewDenyListProcessSpawner(echo)
	for _, command := range []string{echo, "echo"} {
		if _, err := runOutput(s, command, nil, nil); !errors.Is(err, ErrCommandNotAllowed) {
			t.Fatalf("%s: err = %v, want ErrCommandNotAllowed", command, err)
		}
	}
	if out, err := runOutput(s, hostPath(t, "printf"), []string{"ok"}, nil); err != nil || out != "ok" {
		t.Fatalf("an unblocked program: out = %q, err = %v", out, err)
	}
}

// An entry that is not an absolute path to a file allows and blocks nothing it might be
// meant to, so the spawner refuses everything and says why.
func TestSandboxedSpawnerRejectsInvalidEntries(t *testing.T) {
	for _, cfg := range []*SandboxProcessConfig{
		{AllowedCommands: []string{"echo"}},
		{AllowedCommands: []string{filepath.Join(t.TempDir(), "missing")}},
		{AllowAll: true, BlockedCommands: []string{"rm"}},
	} {
		s := NewSandboxedProcessSpawner(cfg)
		if err := s.Err(); err == nil {
			t.Fatalf("%+v: Err() = nil", cfg)
		}
		if _, err := runOutput(s, hostPath(t, "echo"), nil, nil); !errors.Is(err, ErrCommandNotAllowed) {
			t.Fatalf("%+v: err = %v, want ErrCommandNotAllowed", cfg, err)
		}
	}
}
