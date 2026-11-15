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
// The entry is sh as PATH finds it, which may be a link to a shell of another name.
func TestSandboxedSpawnerKeepsArgv0(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	s := NewAllowListProcessSpawner(sh)
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

// A relative command and a relative working directory resolve to the path that runs.
func TestSandboxedSpawnerRelativeCwd(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	for _, dir := range []string{"sub", filepath.Join("sub", "sub")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeExecutable(t, filepath.Join(dir, "tool"), dir)
	}
	allowed := filepath.Join(base, "sub", "tool")

	s := NewAllowListProcessSpawner(allowed)
	out, _, _, err := s.ExecFile(context.Background(), "./tool", nil, &SpawnOptions{Cwd: "sub"})
	if err != nil || strings.TrimSpace(string(out)) != "sub" {
		t.Fatalf("allowed: out = %q, err = %v", out, err)
	}

	denied := NewDenyListProcessSpawner(allowed)
	if _, _, _, err := denied.ExecFile(context.Background(), "./tool", nil, &SpawnOptions{Cwd: "sub"}); !errors.Is(err, ErrCommandNotAllowed) {
		t.Fatalf("blocked: err = %v, want ErrCommandNotAllowed", err)
	}
}

// A program can act on the name it runs under, so an entry allows it only under the
// entry's name. Run under another name, it gets the entry's path as argv[0].
func TestSandboxedSpawnerKeepsEntryName(t *testing.T) {
	sh := hostPath(t, "sh")
	dir := t.TempDir()
	approved := filepath.Join(dir, "approved")
	other := filepath.Join(dir, "other")
	for _, link := range []string{approved, other} {
		if err := os.Symlink(sh, link); err != nil {
			t.Fatal(err)
		}
	}
	s := NewAllowListProcessSpawner(approved)
	args := []string{"-c", "echo $0"}

	if out, err := runOutput(s, approved, args, nil); err != nil || out != approved {
		t.Fatalf("by the entry: out = %q, err = %v", out, err)
	}
	for _, command := range []string{other, sh} {
		if out, err := runOutput(s, command, args, nil); err != nil || out != approved {
			t.Fatalf("%s: out = %q, err = %v, want argv[0] %q", command, out, err, approved)
		}
		out, _, _, err := s.ExecFile(context.Background(), command, args, nil)
		if got := strings.TrimSpace(string(out)); err != nil || got != approved {
			t.Fatalf("ExecFile %s: out = %q, err = %v", command, got, err)
		}
	}
	if out, err := runOutput(s, approved, args, &SpawnOptions{Argv0: "renamed"}); err != nil || out != approved {
		t.Fatalf("with Argv0: out = %q, err = %v", out, err)
	}
}

// In denylist mode every name may run, so argv[0] stays as given.
func TestSandboxedSpawnerAllowAllKeepsName(t *testing.T) {
	approved := filepath.Join(t.TempDir(), "approved")
	if err := os.Symlink(hostPath(t, "sh"), approved); err != nil {
		t.Fatal(err)
	}
	s := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowAll: true, AllowedCommands: []string{approved}})
	if out, err := runOutput(s, "sh", []string{"-c", "echo $0"}, nil); err != nil || out != "sh" {
		t.Fatalf("out = %q, err = %v, want sh", out, err)
	}
}
