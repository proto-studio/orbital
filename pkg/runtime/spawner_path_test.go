package runtime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
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
