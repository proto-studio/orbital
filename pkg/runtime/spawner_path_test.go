package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// writeTool writes an executable script named name into dir that prints output.
func writeTool(t *testing.T, dir, name, output string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho "+output+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func spawnOutput(t *testing.T, spawner ProcessSpawner, command string, options *SpawnOptions) string {
	t.Helper()
	proc, err := spawner.Spawn(context.Background(), command, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(proc.Stdout())
	proc.Wait()
	return strings.TrimSpace(string(out))
}

// As in Node, a bare command is looked up on the PATH the child is given.
func TestRealSpawnerUsesEnvPath(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, dir, "orbital-only-here", "found")
	env := []string{"PATH=" + dir}
	if got := spawnOutput(t, NewRealProcessSpawner(), "orbital-only-here", &SpawnOptions{Env: env}); got != "found" {
		t.Fatalf("got %q, want found", got)
	}
	if _, err := NewRealProcessSpawner().Spawn(context.Background(), "orbital-only-here", nil, nil); err == nil {
		t.Fatal("found the command without its PATH")
	}
}

// SandboxedProcessSpawner looks bare names up on the host's PATH, where its entries are,
// so a PATH from the script does not change which program an allowed name runs.
func TestSandboxedSpawnerIgnoresScriptPath(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, dir, "echo", "from-script-dir")
	spawner := NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowedCommands: []string{hostPath(t, "echo")}})
	proc, err := spawner.Spawn(context.Background(), "echo", []string{"host"}, &SpawnOptions{Env: []string{"PATH=" + dir}})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(proc.Stdout())
	proc.Wait()
	if got := strings.TrimSpace(string(out)); got != "host" {
		t.Fatalf("got %q, want the host's echo to print host", got)
	}
}

// Argv0 is what the process sees as its name.
func TestSpawnArgv0(t *testing.T) {
	proc, err := NewRealProcessSpawner().Spawn(context.Background(), "sh", []string{"-c", "echo $0"}, &SpawnOptions{Argv0: "custom0"})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(proc.Stdout())
	proc.Wait()
	if s := strings.TrimSpace(string(out)); s != "custom0" {
		t.Fatalf("argv[0] = %q, want custom0", s)
	}
}

// As in Node, a PATH entry this process may not execute is skipped, and an empty PATH
// searches the working directory.
func TestRealSpawnerPathSearch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may execute any file with an execute bit")
	}
	first, second := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(first, "orbital-path-tool"), []byte("#!/bin/sh\necho first\n"), 0o010); err != nil {
		t.Fatal(err)
	}
	writeTool(t, second, "orbital-path-tool", "second")
	env := []string{"PATH=" + first + string(os.PathListSeparator) + second}
	if got := spawnOutput(t, NewRealProcessSpawner(), "orbital-path-tool", &SpawnOptions{Env: env}); got != "second" {
		t.Fatalf("got %q, want second", got)
	}
	if got := spawnOutput(t, NewRealProcessSpawner(), "orbital-path-tool", &SpawnOptions{Cwd: second, Env: []string{"PATH="}}); got != "second" {
		t.Fatalf("empty PATH: got %q, want second", got)
	}
}

// A file found on PATH but not executable is EACCES, not ENOENT, as in Node.
func TestRealSpawnerPathNotExecutable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may execute any file with an execute bit")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orbital-path-tool"), []byte("#!/bin/sh\necho x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewRealProcessSpawner().Spawn(context.Background(), "orbital-path-tool", nil, &SpawnOptions{Env: []string{"PATH=" + dir}})
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("err = %v, want EACCES", err)
	}
}
