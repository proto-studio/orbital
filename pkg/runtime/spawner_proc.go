// Package process defines interfaces for process spawning operations.
// This allows sandboxing of child process creation.
//
// Three spawner modes are available:
//
//  1. Disallow All - NewNoOpProcessSpawner()
//     All process spawning fails with EPERM error.
//
//  2. Allow List - NewAllowListProcessSpawner(paths)
//     Only the executables at the given absolute paths can be spawned.
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
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	// ErrSpawnBlocked is returned when process spawning is blocked
	ErrSpawnBlocked = errors.New("EPERM: process spawning is disabled in sandbox mode")
	// ErrCommandNotAllowed is returned when a command is not in the allowlist
	ErrCommandNotAllowed = errors.New("EACCES: command not allowed")
	// ErrTooManyProcesses is returned when MaxProcesses children are already running
	ErrTooManyProcesses = errors.New("EMFILE: too many child processes")
)

// StdioMode says what one file descriptor of a spawned process is connected to.
type StdioMode int

const (
	// StdioPipe connects the descriptor to a pipe its parent can use.
	StdioPipe StdioMode = iota
	// StdioIgnore connects descriptors 0-2 to the null device and leaves others closed.
	StdioIgnore
	// StdioFile gives the process a file its parent already has open.
	StdioFile
)

// StdioOption configures one file descriptor of a spawned process.
type StdioOption struct {
	Mode StdioMode
	File *os.File // for StdioFile
}

// SpawnOptions contains options for spawning a process.
type SpawnOptions struct {
	Cwd      string
	Env      []string
	Uid      int
	Gid      int
	Shell    bool
	Detached bool
	Timeout  int // milliseconds, 0 = no timeout

	// Argv0 is the name the process sees as argv[0], if not the command. Spawn and
	// ExecFile only.
	Argv0 string

	// Stdio configures the process's file descriptors, indexed by number. Spawn honors
	// it; Exec and ExecFile do not. Descriptors 0-2 default to pipes, and any others
	// are left closed.
	Stdio []StdioOption

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

	// Wait waits for the process to exit and returns the exit code. A process ended by a
	// signal has no exit code, so this returns -1 and ExitSignal returns the signal.
	Wait() (int, error)

	// ExitSignal returns the signal that ended the process, or 0 if none did. It is
	// only meaningful after Wait has returned.
	ExitSignal() syscall.Signal

	// Pipe returns the parent's end of a pipe at descriptor 3 or above, or nil.
	Pipe(fd int) io.ReadWriteCloser

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
		// As in Node, a bare name is looked up on the PATH the child is given, if any.
		path := command
		if pathList, ok := envValue(options.Env, "PATH"); ok && !strings.Contains(command, "/") {
			resolved, err := lookPathIn(command, pathList, options.Cwd)
			if err != nil {
				return nil, err
			}
			path = resolved
		}
		cmd = exec.CommandContext(ctx, path, args...)
		cmd.Args[0] = command
		if options.Argv0 != "" {
			cmd.Args[0] = options.Argv0
		}
	}

	if options.Cwd != "" {
		cmd.Dir = options.Cwd
	}

	// Node makes a detached child the leader of a new session and process group.
	if options.Detached {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	// A nil Env means the host's environment. An empty one means an empty environment.
	if options.Env != nil {
		cmd.Env = options.Env
	} else {
		cmd.Env = os.Environ()
	}

	return startProcess(cmd, options.Stdio)
}

// envValue returns the value of name in an environment list, and whether it is set.
func envValue(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], name+"="); ok {
			return value, true
		}
	}
	return "", false
}

// lookPathIn finds an executable named file in the directories of pathList, as
// exec.LookPath does with the process's own PATH. As in Node, where the child searches
// after changing to its working directory, a relative directory is relative to cwd. The
// result is absolute, so nothing searches for it again.
func lookPathIn(file, pathList, cwd string) (string, error) {
	if !filepath.IsAbs(cwd) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		cwd = filepath.Join(wd, cwd)
	}
	dirs := filepath.SplitList(pathList)
	if len(dirs) == 0 {
		// An empty PATH is one empty entry, the current directory, as the shell reads it.
		dirs = []string{""}
	}
	for _, dir := range dirs {
		if dir == "" {
			dir = "."
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate := filepath.Join(dir, file)
		// As exec.LookPath does, skip what this process may not execute.
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if unix.Faccessat(unix.AT_FDCWD, candidate, unix.X_OK, unix.AT_EACCESS) == nil {
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

// startProcess connects a command's file descriptors as stdio asks and starts it.
func startProcess(cmd *exec.Cmd, stdio []StdioOption) (*RealChildProcess, error) {
	option := func(fd int) StdioOption {
		if fd < len(stdio) {
			return stdio[fd]
		}
		return StdioOption{Mode: StdioPipe}
	}

	proc := &RealChildProcess{cmd: cmd, pipes: make(map[int]io.ReadWriteCloser)}
	var parentEnds []io.Closer // closed if the process cannot start
	var childEnds []*os.File   // always closed once the process has its copies
	fail := func(err error) (*RealChildProcess, error) {
		for _, end := range parentEnds {
			end.Close()
		}
		for _, end := range childEnds {
			end.Close()
		}
		return nil, err
	}

	switch in := option(0); in.Mode {
	case StdioPipe:
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fail(err)
		}
		proc.stdin = stdin
		parentEnds = append(parentEnds, stdin)
	case StdioFile:
		cmd.Stdin = in.File
	}

	// cmd.StdoutPipe and cmd.StderrPipe would close the read ends as soon as Wait sees
	// the process exit, losing output nobody has read yet. These stay open until the
	// caller closes them, so output can be read in full after the exit is reported.
	outputs := []struct {
		fd     int
		target *io.Writer
		reader *io.ReadCloser
	}{{1, &cmd.Stdout, &proc.stdout}, {2, &cmd.Stderr, &proc.stderr}}
	for _, output := range outputs {
		switch out := option(output.fd); out.Mode {
		case StdioPipe:
			read, write, err := os.Pipe()
			if err != nil {
				return fail(err)
			}
			parentEnds = append(parentEnds, read)
			childEnds = append(childEnds, write)
			*output.target = write
			*output.reader = read
		case StdioFile:
			*output.target = out.File
		}
	}

	for fd := 3; fd < len(stdio); fd++ {
		var file *os.File // a nil entry leaves the descriptor closed in the child
		switch stdio[fd].Mode {
		case StdioPipe:
			parent, child, err := socketPair()
			if err != nil {
				return fail(err)
			}
			parentEnds = append(parentEnds, parent)
			childEnds = append(childEnds, child)
			proc.pipes[fd] = parent
			file = child
		case StdioFile:
			file = stdio[fd].File
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, file)
	}

	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	for _, end := range childEnds {
		end.Close()
	}
	return proc, nil
}

// socketPair returns the two ends of a connection for a descriptor above stdio. A socket
// rather than a pipe lets data flow both ways, as it does in Node. The parent's end also
// supports CloseWrite.
func socketPair() (net.Conn, *os.File, error) {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, err
	}

	parentFile := os.NewFile(uintptr(fds[0]), "stdio-pipe")
	defer parentFile.Close()
	child := os.NewFile(uintptr(fds[1]), "stdio-pipe")
	parent, err := net.FileConn(parentFile)
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	return parent, child, nil
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
	if options.Argv0 != "" {
		cmd.Args[0] = options.Argv0
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
	pipes  map[int]io.ReadWriteCloser
	signal syscall.Signal
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

// Pipe returns the parent's end of the pipe at a descriptor above stdio, or nil.
func (p *RealChildProcess) Pipe(fd int) io.ReadWriteCloser {
	if pipe, ok := p.pipes[fd]; ok {
		return pipe
	}
	return nil
}

// ExitSignal returns the signal that ended the process, or 0 if none did.
func (p *RealChildProcess) ExitSignal() syscall.Signal { return p.signal }

// Wait waits for the process to exit and returns the exit code.
func (p *RealChildProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	if state := p.cmd.ProcessState; state != nil {
		if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			p.signal = status.Signal()
		}
	}
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
//
// Commands are compared by the executable they run, not by name. AllowedCommands and
// BlockedCommands hold absolute paths, which are resolved through symlinks when the rules
// are first used. Each command a script runs is resolved the same way: a bare name on the
// host's PATH, a relative path against the working directory, then through symlinks. Only
// an exact match counts, and the resolved path is what runs.
type SandboxedProcessSpawner struct {
	// AllowedCommands lists the absolute paths of the executables that may run, in
	// allowlist mode. If empty and AllowAll is false, all commands are blocked.
	AllowedCommands []string
	// BlockedCommands lists the absolute paths of executables that may not run. It applies
	// in both modes.
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

	rulesOnce sync.Once
	allowed   map[string][]string // resolved AllowedCommands, to the entries naming each
	blocked   map[string]bool     // resolved BlockedCommands
	rulesErr  error
}

// SandboxProcessConfig configures the sandboxed process spawner.
type SandboxProcessConfig struct {
	// AllowedCommands lists the absolute paths of the executables that may run, such as
	// "/bin/ls". A symlink is resolved to its target, which is what the entry allows. A
	// bare name a script runs is looked up on the host's PATH and allowed if it resolves
	// to an entry.
	AllowedCommands []string
	// BlockedCommands lists the absolute paths of executables that may not run, resolved
	// in the same way. It is checked first, so it also applies when AllowAll is true.
	BlockedCommands []string
	// AllowAll allows all commands (use with BlockedCommands for denylist mode).
	AllowAll bool
	// MaxProcesses limits concurrent child processes, including Exec and ExecFile while they
	// run (0 = unlimited).
	MaxProcesses int
	// AllowShellCommands allows shell command lines even in allowlist mode: exec(), and
	// spawn with shell: true. When false, these are refused unless AllowAll is true. Either
	// way they are refused if /bin/sh is blocked.
	AllowShellCommands bool
}

// NewSandboxedProcessSpawner creates a sandboxed process spawner with full config.
//
// Every entry of AllowedCommands and BlockedCommands must be an absolute path to an
// existing file. If one isn't, the spawner refuses every command with an error that names
// the entry, and Err reports it.
func NewSandboxedProcessSpawner(cfg *SandboxProcessConfig) *SandboxedProcessSpawner {
	if cfg == nil {
		cfg = &SandboxProcessConfig{}
	}
	s := &SandboxedProcessSpawner{
		AllowedCommands:    cfg.AllowedCommands,
		BlockedCommands:    cfg.BlockedCommands,
		AllowAll:           cfg.AllowAll,
		MaxProcesses:       cfg.MaxProcesses,
		AllowShellCommands: cfg.AllowShellCommands,
		real:               NewRealProcessSpawner(),
	}
	s.rules()
	return s
}

// NewAllowListProcessSpawner creates a spawner that only allows the executables at the
// given absolute paths. See NewSandboxedProcessSpawner.
//
// Example:
//
//	spawner := runtime.NewAllowListProcessSpawner("/bin/ls", "/bin/cat", "/usr/bin/grep")
func NewAllowListProcessSpawner(commands ...string) *SandboxedProcessSpawner {
	return NewSandboxedProcessSpawner(&SandboxProcessConfig{AllowedCommands: commands})
}

// NewDenyListProcessSpawner creates a spawner that allows all commands except the
// executables at the given absolute paths. See NewSandboxedProcessSpawner.
//
// Example:
//
//	spawner := runtime.NewDenyListProcessSpawner("/bin/rm", "/usr/bin/sudo", "/bin/chmod")
func NewDenyListProcessSpawner(blockedCommands ...string) *SandboxedProcessSpawner {
	return NewSandboxedProcessSpawner(&SandboxProcessConfig{
		BlockedCommands:    blockedCommands,
		AllowAll:           true,
		AllowShellCommands: true,
	})
}

// Err reports an entry of AllowedCommands or BlockedCommands that is not an absolute path
// to an existing file. While there is one, every command is refused.
func (s *SandboxedProcessSpawner) Err() error {
	_, _, err := s.rules()
	return err
}

// rules resolves AllowedCommands and BlockedCommands once.
func (s *SandboxedProcessSpawner) rules() (allowed map[string][]string, blocked map[string]bool, err error) {
	s.rulesOnce.Do(func() {
		s.allowed, s.rulesErr = resolveEntries("AllowedCommands", s.AllowedCommands)
		if s.rulesErr != nil {
			return
		}
		var blockedEntries map[string][]string
		blockedEntries, s.rulesErr = resolveEntries("BlockedCommands", s.BlockedCommands)
		s.blocked = make(map[string]bool, len(blockedEntries))
		for path := range blockedEntries {
			s.blocked[path] = true
		}
	})
	return s.allowed, s.blocked, s.rulesErr
}

// resolveEntries resolves a list of absolute executable paths through symlinks. It maps
// each resolved path to the entries that name it, in order.
func resolveEntries(field string, entries []string) (map[string][]string, error) {
	resolved := make(map[string][]string, len(entries))
	for _, entry := range entries {
		if !filepath.IsAbs(entry) {
			return nil, fmt.Errorf("%w: %s entry %q is not an absolute path", ErrCommandNotAllowed, field, entry)
		}
		path, err := filepath.EvalSymlinks(entry)
		if err != nil {
			return nil, fmt.Errorf("%w: %s entry %q: %v", ErrCommandNotAllowed, field, entry, err)
		}
		resolved[path] = append(resolved[path], entry)
	}
	return resolved, nil
}

// resolveCommand finds the executable command names, as the OS would run it: a bare name
// on the host's PATH, a relative path against cwd (or the host's working directory), and
// then through symlinks.
func resolveCommand(command, cwd string) (string, error) {
	path := command
	if !strings.Contains(command, "/") {
		found, err := exec.LookPath(command)
		if err != nil {
			return "", err
		}
		path = found
	}
	if !filepath.IsAbs(path) {
		// The process starts in cwd, which is itself relative to the host's working
		// directory if it is relative.
		if !filepath.IsAbs(cwd) {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			cwd = filepath.Join(wd, cwd)
		}
		path = filepath.Join(cwd, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", &exec.Error{Name: command, Err: err}
	}
	return resolved, nil
}

// pathAllowed reports whether the rules let the executable at a resolved path run.
func (s *SandboxedProcessSpawner) pathAllowed(resolved string) error {
	allowed, blocked, err := s.rules()
	if err != nil {
		return err
	}
	if blocked[resolved] {
		return ErrCommandNotAllowed
	}
	if s.AllowAll || len(allowed[resolved]) > 0 {
		return nil
	}
	return ErrCommandNotAllowed
}

// checkCommand resolves command and checks it. It returns the path to run, and options
// that keep the name the script gave as argv[0].
func (s *SandboxedProcessSpawner) checkCommand(command string, options *SpawnOptions) (string, *SpawnOptions, error) {
	if err := s.Err(); err != nil {
		return "", nil, err
	}
	opts := SpawnOptions{}
	if options != nil {
		opts = *options
	}
	resolved, err := resolveCommand(command, opts.Cwd)
	if err != nil {
		return "", nil, err
	}
	if err := s.pathAllowed(resolved); err != nil {
		return "", nil, err
	}
	// Some programs act on the name they are run by, as a multicall binary does, so in
	// allowlist mode an entry allows the program under its own name. A name that no entry
	// for this program has is replaced with the first entry.
	argv0 := opts.Argv0
	if argv0 == "" {
		argv0 = command
	}
	allowed, _, _ := s.rules()
	if entries := allowed[resolved]; !s.AllowAll && len(entries) > 0 {
		named := false
		for _, entry := range entries {
			if filepath.Base(entry) == filepath.Base(argv0) {
				named = true
				break
			}
		}
		if !named {
			argv0 = entries[0]
		}
	}
	opts.Argv0 = argv0
	return resolved, &opts, nil
}

// Spawn starts a new process if the command is allowed by the sandbox rules.
func (s *SandboxedProcessSpawner) Spawn(ctx context.Context, command string, args []string, options *SpawnOptions) (ChildProcess, error) {
	// A shell runs the whole line, command and arguments joined, so the program that runs
	// is the shell. Such a spawn needs the rules that allow a shell.
	if options != nil && options.Shell {
		if err := s.shellAllowed(); err != nil {
			return nil, err
		}
	} else {
		resolved, opts, err := s.checkCommand(command, options)
		if err != nil {
			return nil, err
		}
		command, options = resolved, opts
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
	if err := s.shellAllowed(); err != nil {
		return nil, nil, -1, err
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
	resolved, opts, err := s.checkCommand(file, options)
	if err != nil {
		return nil, nil, -1, err
	}
	if err := s.acquire(); err != nil {
		return nil, nil, -1, err
	}
	defer s.release()

	return s.real.ExecFile(ctx, resolved, args, opts)
}

// shellAllowed reports whether the rules let a shell run a command line. The real spawner
// runs command lines with /bin/sh.
func (s *SandboxedProcessSpawner) shellAllowed() error {
	if err := s.Err(); err != nil {
		return err
	}
	// A command line is too complex to check, so it takes an explicit allowance.
	if !s.AllowAll && !s.AllowShellCommands {
		return ErrCommandNotAllowed
	}
	// The shell itself may be blocked.
	_, blocked, _ := s.rules()
	shell, err := filepath.EvalSymlinks("/bin/sh")
	if err != nil {
		return err
	}
	if blocked[shell] {
		return ErrCommandNotAllowed
	}
	return nil
}

// acquire counts a process against MaxProcesses, or fails if the limit is reached. Exec and
// ExecFile count too, for as long as they run.
func (s *SandboxedProcessSpawner) acquire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MaxProcesses > 0 && s.activeCount >= s.MaxProcesses {
		return ErrTooManyProcesses
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
