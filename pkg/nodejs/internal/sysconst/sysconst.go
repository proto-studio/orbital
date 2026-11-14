// Package sysconst holds the host's errno and signal numbers under the names Node uses, so
// os.constants, fs errors and child_process agree with each other and with Node on the same
// platform. The values come from the syscall package's per-platform files, so each build
// carries its own platform's numbers.
package sysconst

import "syscall"

// Constant is a named number, such as ELOOP or SIGUSR1.
type Constant struct {
	Name  string
	Value int
}

// errnoNames lists the errno names Node defines, in Node's order. A platform has those it
// defines; see errnoValues.
var errnoNames = []string{
	"E2BIG", "EACCES", "EADDRINUSE", "EADDRNOTAVAIL", "EAFNOSUPPORT", "EAGAIN", "EALREADY",
	"EBADF", "EBADMSG", "EBUSY", "ECANCELED", "ECHILD", "ECONNABORTED", "ECONNREFUSED",
	"ECONNRESET", "EDEADLK", "EDESTADDRREQ", "EDOM", "EDQUOT", "EEXIST", "EFAULT", "EFBIG",
	"EHOSTUNREACH", "EIDRM", "EILSEQ", "EINPROGRESS", "EINTR", "EINVAL", "EIO", "EISCONN",
	"EISDIR", "ELOOP", "EMFILE", "EMLINK", "EMSGSIZE", "EMULTIHOP", "ENAMETOOLONG", "ENETDOWN",
	"ENETRESET", "ENETUNREACH", "ENFILE", "ENOBUFS", "ENODATA", "ENODEV", "ENOENT", "ENOEXEC",
	"ENOLCK", "ENOLINK", "ENOMEM", "ENOMSG", "ENOPROTOOPT", "ENOSPC", "ENOSR", "ENOSTR",
	"ENOSYS", "ENOTCONN", "ENOTDIR", "ENOTEMPTY", "ENOTSOCK", "ENOTSUP", "ENOTTY", "ENXIO",
	"EOPNOTSUPP", "EOVERFLOW", "EPERM", "EPIPE", "EPROTO", "EPROTONOSUPPORT", "EPROTOTYPE",
	"ERANGE", "EROFS", "ESPIPE", "ESRCH", "ESTALE", "ETIME", "ETIMEDOUT", "ETXTBSY",
	"EWOULDBLOCK", "EXDEV",
}

// signalNames lists the signal names Node defines, in Node's order. A platform has those it
// defines; see signalValues.
var signalNames = []string{
	"SIGHUP", "SIGINT", "SIGQUIT", "SIGILL", "SIGTRAP", "SIGABRT", "SIGIOT", "SIGBUS",
	"SIGFPE", "SIGKILL", "SIGUSR1", "SIGSEGV", "SIGUSR2", "SIGPIPE", "SIGALRM", "SIGTERM",
	"SIGCHLD", "SIGSTKFLT", "SIGCONT", "SIGSTOP", "SIGTSTP", "SIGBREAK", "SIGTTIN", "SIGTTOU",
	"SIGURG", "SIGXCPU", "SIGXFSZ", "SIGVTALRM", "SIGPROF", "SIGWINCH", "SIGIO", "SIGPOLL",
	"SIGLOST", "SIGPWR", "SIGINFO", "SIGSYS", "SIGUNUSED",
}

var (
	errnos  = collect(errnoNames, func(name string) (int, bool) { e, ok := errnoValues[name]; return int(e), ok })
	signals = collect(signalNames, func(name string) (int, bool) { s, ok := signalValues[name]; return int(s), ok })
)

func collect(names []string, value func(string) (int, bool)) []Constant {
	out := make([]Constant, 0, len(names))
	for _, name := range names {
		if v, ok := value(name); ok {
			out = append(out, Constant{name, v})
		}
	}
	return out
}

// Errnos returns the platform's errno constants as os.constants.errno has them, in order.
// The caller must not modify the result.
func Errnos() []Constant { return errnos }

// Signals returns the platform's signal constants as os.constants.signals has them, in
// order. The caller must not modify the result.
func Signals() []Constant { return signals }

// ErrnoName returns the code Node gives errno, such as "ENOENT". Where two names share a
// number, the first wins, as libuv reports EAGAIN rather than EWOULDBLOCK.
func ErrnoName(errno syscall.Errno) (string, bool) {
	for _, c := range errnos {
		if c.Value == int(errno) {
			return c.Name, true
		}
	}
	return "", false
}

// Signal returns the platform's number for a signal name such as "SIGTERM".
func Signal(name string) (syscall.Signal, bool) {
	s, ok := signalValues[name]
	return s, ok
}
