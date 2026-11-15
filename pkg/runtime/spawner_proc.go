// Package process defines interfaces for process spawning operations.
// This allows sandboxing of child process creation.
//
// Three spawner modes are available:
//
//  1. Disallow All - NewNoOpProcessSpawner()
//     All process spawning fails with EPERM error.
//
//  2. Allow List - NewAllowListProcessSpawner(commands)
//     Only specified commands can be spawned.
//
//  3. Allow All - NewRealProcessSpawner()
//     Same behavior as Node.js, no restrictions.
//
// For more complex scenarios, use NewSandboxedProcessSpawner with custom config.
package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

var (
	// ErrSpawnBlocked is returned when process spawning is blocked
	ErrSpawnBlocked = errors.New("EPERM: process spawning is disabled in sandbox mode")
	// ErrCommandNotAllowed is returned when a command is not in the allowlist
	ErrCommandNotAllowed = errors.New("EACCES: command not allowed")
)

// SpawnOptions contains options for spawning a process.
type SpawnOptions struct {
	Cwd      string
	Env      []string
	Uid      int
	Gid      int
	Shell    bool
	Detached bool
	Timeout  int // milliseconds, 0 = no timeout

	// Input is written to the stdin of Exec and ExecFile commands. Spawn ignores it,
	// because its caller writes to Stdin itself.
	Input []byte
}

// ChildProcess represents a spawned child process.
type ChildProcess interface {
	// Pid returns the process ID.
	Pid() int

	// Stdin returns the stdin writer.
	Stdin() io.WriteCloser

	// Stdout returns the stdout reader.
	Stdout() io.ReadCloser

	// Stderr returns the stderr reader.
	Stderr() io.ReadCloser

	// Wait waits for the process to exit and returns the exit code.
	Wait() (int, error)

	// Kill sends a signal to the process.
	Kill(signal syscall.Signal) error

	// Signal sends a signal to the process.
	Signal(signal syscall.Signal) error
}

// ProcessSpawner defines the interface for spawning processes.
type ProcessSpawner interface {
	// Spawn starts a new process.
	Spawn(ctx context.Context, command string, args []string, options *SpawnOptions) (ChildProcess, error)

	// Exec executes a command in a shell and returns output.
	Exec(ctx context.Context, command string, options *SpawnOptions) ([]byte, []byte, int, error)

	// ExecFile executes a file directly and returns output.
	ExecFile(ctx context.Context, file string, args []string, options *SpawnOptions) ([]byte, []byte, int, error)
}

// RealProcessSpawner implements ProcessSpawner using real OS processes.
type RealProcessSpawner struct{}

// NewRealProcessSpawner creates a spawner that creates real processes.
func NewRealProcessSpawner() *RealProcessSpawner {
	return &RealProcessSpawner{}
}

// Spawn starts a new process with the given command and arguments.
func (s *RealProcessSpawner) Spawn(ctx context.Context, command string, args []string, options *SpawnOptions) (ChildProcess, error) {
	if options == nil {
		options = &SpawnOptions{}
	}

	var cmd *exec.Cmd
	if options.Shell {
		// Run through shell
		shellCmd := command
		if len(args) > 0 {
			for _, arg := range args {
				shellCmd += " " + arg
			}
		}
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", shellCmd)
	} else {
		cmd = exec.CommandContext(ctx, command, args...)
	}

	if options.Cwd != "" {
		cmd.Dir = options.Cwd
	}

	// A nil Env means the host's environment. An empty one means an empty environment.
	if options.Env != nil {
		cmd.Env = options.Env
	} else {
		cmd.Env = os.Environ()
	}

	// Set up pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	// cmd.StdoutPipe and cmd.StderrPipe would close the read ends as soon as Wait sees
	// the process exit, losing output nobody has read yet. These stay open until the
	// caller closes them, so output can be read in full after the exit is reported.
	stdout, stdoutChild, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	stderr, stderrChild, err := os.Pipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		stdoutChild.Close()
		return nil, err
	}
	cmd.Stdout = stdoutChild
	cmd.Stderr = stderrChild

	err = cmd.Start()
	stdoutChild.Close()
	stderrChild.Close()
	if err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, err
	}

	return &RealChildProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}, nil
}

// Exec executes a command in a shell and returns stdout, stderr, and exit code.
func (s *RealProcessSpawner) Exec(ctx context.Context, command string, options *SpawnOptions) ([]byte, []byte, int, error) {
	if options == nil {
		options = &SpawnOptions{}
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)

	if options.Cwd != "" {
		cmd.Dir = options.Cwd
	}

	// A nil Env means the host's environment. An empty one means an empty environment.
	if options.Env != nil {
		cmd.Env = options.Env
	} else {
		cmd.Env = os.Environ()
	}

	if len(options.Input) > 0 {
		cmd.Stdin = bytes.NewReader(options.Input)
	}

	var stdout, stderr []byte
	var stdoutBuf, stderrBuf safeBuffer

	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stdout = stdoutBuf.Bytes()
	stderr = stderrBuf.Bytes()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return stdout, stderr, -1, err
		}
	}

	return stdout, stderr, exitCode, nil
}

// ExecFile executes a file directly and returns stdout, stderr, and exit code.
func (s *RealProcessSpawner) ExecFile(ctx context.Context, file string, args []string, options *SpawnOptions) ([]byte, []byte, int, error) {
	if options == nil {
		options = &SpawnOptions{}
	}

	cmd := exec.CommandContext(ctx, file, args...)

	if options.Cwd != "" {
		cmd.Dir = options.Cwd
	}

	// A nil Env means the host's environment. An empty one means an empty environment.
	if options.Env != nil {
		cmd.Env = options.Env
	} else {
		cmd.Env = os.Environ()
	}

	if len(options.Input) > 0 {
		cmd.Stdin = bytes.NewReader(options.Input)
	}

	var stdoutBuf, stderrBuf safeBuffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stdout := stdoutBuf.Bytes()
	stderr := stderrBuf.Bytes()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return stdout, stderr, -1, err
		}
	}

	return stdout, stderr, exitCode, nil
}

// RealChildProcess wraps an os/exec.Cmd.
type RealChildProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// Pid returns the process ID, or -1 if the process hasn't started.
func (p *RealChildProcess) Pid() int {
	if p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return -1
}

// Stdin returns the stdin writer for the process.
func (p *RealChildProcess) Stdin() io.WriteCloser { return p.stdin }

// Stdout returns the stdout reader for the process.
func (p *RealChildProcess) Stdout() io.ReadCloser { return p.stdout }

// Stderr returns the stderr reader for the process.
func (p *RealChildProcess) Stderr() io.ReadCloser { return p.stderr }

// Wait waits for the process to exit and returns the exit code.
func (p *RealChildProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// Kill sends a signal to terminate the process.
func (p *RealChildProcess) Kill(signal syscall.Signal) error {
	return p.Signal(signal)
}

// Signal sends a signal to the process.
func (p *RealChildProcess) Signal(signal syscall.Signal) error {
	if p.cmd.Process == nil {
		return errors.New("process not started")
	}
	return p.cmd.Process.Signal(signal)
}

// safeBuffer is a thread-safe buffer.
type safeBuffer struct {
	buf []byte
	mu  sync.Mutex
}

// Write appends data to the buffer in a thread-safe manner.
func (b *safeBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// Bytes returns the buffer contents in a thread-safe manner.
func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf
}

// SandboxedProcessSpawner implements ProcessSpawner with restrictions.
type SandboxedProcessSpawner struct {
	// AllowedCommands is a list of allowed command names/paths/patterns.
	// If empty and AllowAll is false, all commands are blocked.
	AllowedCommands []string
	// BlockedCommands is a list of blocked command names/paths/patterns.
	BlockedCommands []string
	// AllowAll allows all commands (use with BlockedCommands for denylist mode).
	AllowAll bool
	// MaxProcesses limits concurrent child processes, including Exec and ExecFile while they
	// run (0 = unlimited).
	MaxProcesses int
	// AllowShellCommands allows shell command lines (exec(), or a spawn with shell: true) in
	// allowlist mode.
	AllowShellCommands bool
	// underlying spawner
	real        *RealProcessSpawner
	activeCount int
	mu          sync.Mutex
}

// SandboxProcessConfig configures the sandboxed process spawner.
type SandboxProcessConfig struct {
	// AllowedCommands is a list of allowed command names, paths, or glob patterns.
	// Supports: exact match ("ls"), glob patterns ("/usr/bin/*"), prefix ("npm:*")
	AllowedCommands []string
	// BlockedCommands is a list of blocked command names, paths, or glob patterns.
	// Applied after AllowedCommands check when AllowAll is true.
	BlockedCommands []string
	// AllowAll allows all commands (use with BlockedCommands for denylist mode).
	AllowAll bool
	// MaxProcesses limits concurrent child processes, including Exec and ExecFile while they
	// run (0 = unlimited).
	MaxProcesses int
	// AllowShellCommands allows shell command lines even in allowlist mode: exec(), and
	// spawn with shell: true. When false, these are refused unless AllowAll is true and
	// /bin/sh is not blocked.
	AllowShellCommands bool
}

// NewSandboxedProcessSpawner creates a sandboxed process spawner with full config.
func NewSandboxedProcessSpawner(cfg *SandboxProcessConfig) *SandboxedProcessSpawner {
	if cfg == nil {
		cfg = &SandboxProcessConfig{}
	}
	return &SandboxedProcessSpawner{
		AllowedCommands:    cfg.AllowedCommands,
		BlockedCommands:    cfg.BlockedCommands,
		AllowAll:           cfg.AllowAll,
		MaxProcesses:       cfg.MaxProcesses,
		AllowShellCommands: cfg.AllowShellCommands,
		real:               NewRealProcessSpawner(),
	}
}

// NewAllowListProcessSpawner creates a spawner that only allows specific commands.
// Commands can be exact names ("ls"), full paths ("/usr/bin/node"), or glob patterns ("/usr/bin/*").
//
// Example:
//
//	spawner := process.NewAllowListProcessSpawner("ls", "cat", "grep", "/usr/bin/node")
func NewAllowListProcessSpawner(commands ...string) *SandboxedProcessSpawner {
	return &SandboxedProcessSpawner{
		AllowedCommands:    commands,
		AllowAll:           false,
		AllowShellCommands: false,
		real:               NewRealProcessSpawner(),
	}
}

// NewDenyListProcessSpawner creates a spawner that allows all commands except those blocked.
// Commands can be exact names, full paths, or glob patterns.
//
// Example:
//
//	spawner := process.NewDenyListProcessSpawner("rm", "sudo", "chmod")
func NewDenyListProcessSpawner(blockedCommands ...string) *SandboxedProcessSpawner {
	return &SandboxedProcessSpawner{
		BlockedCommands:    blockedCommands,
		AllowAll:           true,
		AllowShellCommands: true,
		real:               NewRealProcessSpawner(),
	}
}

// matchPattern checks if a command matches a pattern.
// Supports exact match, glob patterns (*, ?), and path matching.
func matchPattern(pattern, command string) bool {
	// Exact match
	if pattern == command {
		return true
	}

	// Try glob match
	if strings.ContainsAny(pattern, "*?[") {
		matched, err := filepath.Match(pattern, command)
		if err == nil && matched {
			return true
		}
		// Also try matching just the base name
		matched, err = filepath.Match(pattern, filepath.Base(command))
		if err == nil && matched {
			return true
		}
	}

	// Match base name against pattern (e.g., "node" matches "/usr/bin/node")
	if filepath.Base(command) == pattern {
		return true
	}

	return false
}

// isCommandAllowed checks if a command is permitted by the allowlist/blocklist rules.
func (s *SandboxedProcessSpawner) isCommandAllowed(command string) bool {
	// Check blocked list first
	for _, blocked := range s.BlockedCommands {
		if matchPattern(blocked, command) {
			return false
		}
	}

	// If AllowAll, permit unless blocked
	if s.AllowAll {
		return true
	}

	// Check allowed list
	for _, allowed := range s.AllowedCommands {
		if matchPattern(allowed, command) {
			return true
		}
	}

	return false
}

// Spawn starts a new process if the command is allowed by the sandbox rules.
func (s *SandboxedProcessSpawner) Spawn(ctx context.Context, command string, args []string, options *SpawnOptions) (ChildProcess, error) {
	// A shell runs the whole line, command and arguments joined, so the command's name
	// says nothing about what runs. Such a spawn needs the rules that allow a shell.
	if options != nil && options.Shell {
		if !s.shellAllowed() {
			return nil, ErrCommandNotAllowed
		}
	} else if !s.isCommandAllowed(command) {
		return nil, ErrCommandNotAllowed
	}

	if err := s.acquire(); err != nil {
		return nil, err
	}

	proc, err := s.real.Spawn(ctx, command, args, options)
	if err != nil {
		s.release()
		return nil, err
	}

	return &sandboxedChildProcess{
		ChildProcess: proc,
		spawner:      s,
	}, nil
}

// Exec executes a shell command if shell commands are allowed.
func (s *SandboxedProcessSpawner) Exec(ctx context.Context, command string, options *SpawnOptions) ([]byte, []byte, int, error) {
	if !s.shellAllowed() {
		return nil, nil, -1, ErrCommandNotAllowed
	}
	if err := s.acquire(); err != nil {
		return nil, nil, -1, err
	}
	defer s.release()

	return s.real.Exec(ctx, command, options)
}

// ExecFile executes a file if it is allowed by the sandbox rules.
func (s *SandboxedProcessSpawner) ExecFile(ctx context.Context, file string, args []string, options *SpawnOptions) ([]byte, []byte, int, error) {
	// ExecFile runs file directly, without a shell, so the file is what gets checked.
	if !s.isCommandAllowed(file) {
		return nil, nil, -1, ErrCommandNotAllowed
	}
	if err := s.acquire(); err != nil {
		return nil, nil, -1, err
	}
	defer s.release()

	return s.real.ExecFile(ctx, file, args, options)
}

// shellAllowed reports whether the rules let a shell run a command line.
func (s *SandboxedProcessSpawner) shellAllowed() bool {
	// A command line is too complex to check, so it takes an explicit allowance.
	if !s.AllowAll && !s.AllowShellCommands {
		return false
	}
	// In denylist mode, the shell itself may be denied.
	return !s.AllowAll || s.isCommandAllowed("/bin/sh")
}

// acquire counts a process against MaxProcesses, or fails if the limit is reached. Exec and
// ExecFile count too, for as long as they run.
func (s *SandboxedProcessSpawner) acquire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MaxProcesses > 0 && s.activeCount >= s.MaxProcesses {
		return errors.New("EMFILE: too many child processes")
	}
	s.activeCount++
	return nil
}

// release ends a count taken by acquire.
func (s *SandboxedProcessSpawner) release() {
	s.mu.Lock()
	s.activeCount--
	s.mu.Unlock()
}

// sandboxedChildProcess wraps a ChildProcess to track active process count.
type sandboxedChildProcess struct {
	ChildProcess
	spawner *SandboxedProcessSpawner
}

// Wait waits for the process to exit and decrements the active process count.
func (p *sandboxedChildProcess) Wait() (int, error) {
	code, err := p.ChildProcess.Wait()
	p.spawner.release()
	return code, err
}

// NoOpProcessSpawner blocks all process spawning.
type NoOpProcessSpawner struct{}

// NewNoOpProcessSpawner creates a spawner that blocks all operations.
func NewNoOpProcessSpawner() *NoOpProcessSpawner {
	return &NoOpProcessSpawner{}
}

// Spawn always returns ErrSpawnBlocked.
func (s *NoOpProcessSpawner) Spawn(ctx context.Context, command string, args []string, options *SpawnOptions) (ChildProcess, error) {
	return nil, ErrSpawnBlocked
}

// Exec always returns ErrSpawnBlocked.
func (s *NoOpProcessSpawner) Exec(ctx context.Context, command string, options *SpawnOptions) ([]byte, []byte, int, error) {
	return nil, nil, -1, ErrSpawnBlocked
}

// ExecFile always returns ErrSpawnBlocked.
func (s *NoOpProcessSpawner) ExecFile(ctx context.Context, file string, args []string, options *SpawnOptions) ([]byte, []byte, int, error) {
	return nil, nil, -1, ErrSpawnBlocked
}
