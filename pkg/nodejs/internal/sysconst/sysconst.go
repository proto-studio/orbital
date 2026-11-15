// Package sysconst holds the host's errno, signal and dlopen numbers under the names Node uses,
// so os.constants, fs errors and child_process agree with each other and with Node on the same
// platform. Errno and signal values come from the syscall package's per-platform files, so each
// build carries its own platform's numbers; dlopen's, which syscall doesn't have, from the
// platform's <dlfcn.h>.
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

// dlopenNames lists the dlopen flags Node defines, in Node's order. A platform has those it
// defines; see dlopenValues.
var dlopenNames = []string{"RTLD_LAZY", "RTLD_NOW", "RTLD_GLOBAL", "RTLD_LOCAL", "RTLD_DEEPBIND"}

var (
	dlopen  = collect(dlopenNames, func(name string) (int, bool) { v, ok := dlopenValues[name]; return v, ok })
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

// Dlopen returns the platform's dlopen flags as os.constants.dlopen has them, in order. The
// caller must not modify the result.
func Dlopen() []Constant { return dlopen }

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
