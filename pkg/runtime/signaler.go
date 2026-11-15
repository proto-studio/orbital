package runtime

import "syscall"

// ProcessSignaler sends signals to processes by pid, for process.kill(). Signals to a
// script's own child processes go through the ChildProcess its ProcessSpawner returned
// instead.
type ProcessSignaler interface {
	// Kill sends sig to the process with the given pid. Signal 0 sends nothing and only
	// checks that the process exists and may be signaled, as kill(2) does. Errors that are
	// a syscall.Errno, such as ESRCH or EPERM, reach the script with that code; any other
	// error reaches it as EPERM.
	Kill(pid int, sig syscall.Signal) error
}

// RealProcessSignaler sends real signals to any process the host process may signal, as
// Node does.
type RealProcessSignaler struct{}

// NewRealProcessSignaler creates a signaler that sends real signals.
func NewRealProcessSignaler() *RealProcessSignaler {
	return &RealProcessSignaler{}
}

// Kill sends sig to pid with kill(2).
func (RealProcessSignaler) Kill(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// NoOpProcessSignaler refuses every signal with EPERM.
type NoOpProcessSignaler struct{}

// NewNoOpProcessSignaler creates a signaler that refuses every signal.
func NewNoOpProcessSignaler() *NoOpProcessSignaler {
	return &NoOpProcessSignaler{}
}

// Kill returns EPERM.
func (NoOpProcessSignaler) Kill(pid int, sig syscall.Signal) error {
	return syscall.EPERM
}
