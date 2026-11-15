// Example: wrapping a built-in ProcessSpawner
//
// Every child_process call a script makes reaches runtime.Config.ProcessSpawner. This
// example wraps the sandboxed spawner with a policy of its own: it logs each call, allows
// a few programs by name, and refuses shell command lines. See docs/child-processes.md.
//
// Run with: go run ./examples/process-spawner
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"proto.zip/studio/orbital/pkg/nodejs"
	"proto.zip/studio/orbital/pkg/runtime"
)

// policySpawner logs every call and passes the ones its policy allows to next.
type policySpawner struct {
	next    runtime.ProcessSpawner
	allowed map[string]bool
	log     *log.Logger
}

// refuse returns the error scripts see as EACCES.
func (p *policySpawner) refuse(reason string) error {
	p.log.Printf("  refused: %s", reason)
	return runtime.ErrCommandNotAllowed
}

// check applies the policy to a program and its arguments.
func (p *policySpawner) check(file string, options *runtime.SpawnOptions) error {
	// With Shell set, file is a whole command line for /bin/sh, not a program name.
	if options != nil && options.Shell {
		return p.refuse("shell command line")
	}
	if !p.allowed[file] {
		return p.refuse(file + " is not allowed")
	}
	return nil
}

// Spawn handles spawn, exec, execFile and their sync forms.
func (p *policySpawner) Spawn(ctx context.Context, file string, args []string, options *runtime.SpawnOptions) (runtime.ChildProcess, error) {
	p.log.Printf("Spawn %q %q shell=%v", file, args, options != nil && options.Shell)
	if err := p.check(file, options); err != nil {
		return nil, err
	}
	return p.next.Spawn(ctx, file, args, options)
}

// Exec receives a whole shell command line, so this policy refuses it.
func (p *policySpawner) Exec(ctx context.Context, command string, options *runtime.SpawnOptions) ([]byte, []byte, int, error) {
	p.log.Printf("Exec %q", command)
	return nil, nil, -1, p.refuse("shell command line")
}

// ExecFile runs a program directly, so it gets the same check as Spawn.
func (p *policySpawner) ExecFile(ctx context.Context, file string, args []string, options *runtime.SpawnOptions) ([]byte, []byte, int, error) {
	p.log.Printf("ExecFile %q %q", file, args)
	if err := p.check(file, nil); err != nil {
		return nil, nil, -1, err
	}
	return p.next.ExecFile(ctx, file, args, options)
}

func main() {
	// The built-in spawner underneath checks the executable each name resolves to on the
	// host's PATH, and limits how many children run at once.
	names := []string{"echo", "date"}
	var paths []string
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			log.Fatalf("%s: %v", name, err)
		}
		paths = append(paths, path)
	}
	next := runtime.NewSandboxedProcessSpawner(&runtime.SandboxProcessConfig{
		AllowedCommands: paths,
		MaxProcesses:    4,
	})
	if err := next.Err(); err != nil {
		log.Fatal(err)
	}

	spawner := &policySpawner{
		next:    next,
		allowed: map[string]bool{},
		log:     log.New(os.Stderr, "[spawner] ", 0),
	}
	for _, name := range names {
		spawner.allowed[name] = true
	}

	cfg := runtime.DefaultConfig()
	cfg.ProcessSpawner = spawner
	inst, err := nodejs.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create runtime: %v\n", err)
		os.Exit(1)
	}
	defer inst.Runtime.Dispose()

	script := strings.TrimSpace(`
const { execFileSync, execSync, spawnSync } = require('child_process');

function attempt(label, fn) {
  try {
    console.log(label, '->', String(fn()).trim());
  } catch (err) {
    console.log(label, '-> error', err.code);
  }
}

attempt('execFileSync echo', () => execFileSync('echo', ['hello']));
attempt('execFileSync rm', () => execFileSync('rm', ['-rf', 'some-dir']));
attempt('execSync', () => execSync('echo hello'));
attempt('spawnSync echo with shell', () => {
  const r = spawnSync('echo', ['hello'], { shell: true });
  if (r.error) throw r.error;
  return r.stdout;
});
`)
	if _, err := inst.Runtime.Run(script, "process-spawner.js"); err != nil {
		fmt.Fprintf(os.Stderr, "script: %v\n", err)
		os.Exit(1)
	}
}
