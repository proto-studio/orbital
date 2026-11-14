package sysconst

import "syscall"

// errnoValues maps each errno name Node defines on this platform to its number.
var errnoValues = map[string]syscall.Errno{
	"E2BIG": syscall.E2BIG, "EACCES": syscall.EACCES, "EADDRINUSE": syscall.EADDRINUSE,
	"EADDRNOTAVAIL": syscall.EADDRNOTAVAIL, "EAFNOSUPPORT": syscall.EAFNOSUPPORT,
	"EAGAIN": syscall.EAGAIN, "EALREADY": syscall.EALREADY, "EBADF": syscall.EBADF,
	"EBADMSG": syscall.EBADMSG, "EBUSY": syscall.EBUSY, "ECANCELED": syscall.ECANCELED,
	"ECHILD": syscall.ECHILD, "ECONNABORTED": syscall.ECONNABORTED,
	"ECONNREFUSED": syscall.ECONNREFUSED, "ECONNRESET": syscall.ECONNRESET,
	"EDEADLK": syscall.EDEADLK, "EDESTADDRREQ": syscall.EDESTADDRREQ, "EDOM": syscall.EDOM,
	"EDQUOT": syscall.EDQUOT, "EEXIST": syscall.EEXIST, "EFAULT": syscall.EFAULT,
	"EFBIG": syscall.EFBIG, "EHOSTUNREACH": syscall.EHOSTUNREACH, "EIDRM": syscall.EIDRM,
	"EILSEQ": syscall.EILSEQ, "EINPROGRESS": syscall.EINPROGRESS, "EINTR": syscall.EINTR,
	"EINVAL": syscall.EINVAL, "EIO": syscall.EIO, "EISCONN": syscall.EISCONN,
	"EISDIR": syscall.EISDIR, "ELOOP": syscall.ELOOP, "EMFILE": syscall.EMFILE,
	"EMLINK": syscall.EMLINK, "EMSGSIZE": syscall.EMSGSIZE, "EMULTIHOP": syscall.EMULTIHOP,
	"ENAMETOOLONG": syscall.ENAMETOOLONG, "ENETDOWN": syscall.ENETDOWN,
	"ENETRESET": syscall.ENETRESET, "ENETUNREACH": syscall.ENETUNREACH,
	"ENFILE": syscall.ENFILE, "ENOBUFS": syscall.ENOBUFS, "ENODATA": syscall.ENODATA,
	"ENODEV": syscall.ENODEV, "ENOENT": syscall.ENOENT, "ENOEXEC": syscall.ENOEXEC,
	"ENOLCK": syscall.ENOLCK, "ENOLINK": syscall.ENOLINK, "ENOMEM": syscall.ENOMEM,
	"ENOMSG": syscall.ENOMSG, "ENOPROTOOPT": syscall.ENOPROTOOPT, "ENOSPC": syscall.ENOSPC,
	"ENOSR": syscall.ENOSR, "ENOSTR": syscall.ENOSTR, "ENOSYS": syscall.ENOSYS,
	"ENOTCONN": syscall.ENOTCONN, "ENOTDIR": syscall.ENOTDIR, "ENOTEMPTY": syscall.ENOTEMPTY,
	"ENOTSOCK": syscall.ENOTSOCK, "ENOTSUP": syscall.ENOTSUP, "ENOTTY": syscall.ENOTTY,
	"ENXIO": syscall.ENXIO, "EOPNOTSUPP": syscall.EOPNOTSUPP, "EOVERFLOW": syscall.EOVERFLOW,
	"EPERM": syscall.EPERM, "EPIPE": syscall.EPIPE, "EPROTO": syscall.EPROTO,
	"EPROTONOSUPPORT": syscall.EPROTONOSUPPORT, "EPROTOTYPE": syscall.EPROTOTYPE,
	"ERANGE": syscall.ERANGE, "EROFS": syscall.EROFS, "ESPIPE": syscall.ESPIPE,
	"ESRCH": syscall.ESRCH, "ESTALE": syscall.ESTALE, "ETIME": syscall.ETIME,
	"ETIMEDOUT": syscall.ETIMEDOUT, "ETXTBSY": syscall.ETXTBSY,
	"EWOULDBLOCK": syscall.EWOULDBLOCK, "EXDEV": syscall.EXDEV,
}

// signalValues maps each signal name Node defines on this platform to its number.
var signalValues = map[string]syscall.Signal{
	"SIGHUP": syscall.SIGHUP, "SIGINT": syscall.SIGINT, "SIGQUIT": syscall.SIGQUIT,
	"SIGILL": syscall.SIGILL, "SIGTRAP": syscall.SIGTRAP, "SIGABRT": syscall.SIGABRT,
	"SIGIOT": syscall.SIGIOT, "SIGBUS": syscall.SIGBUS, "SIGFPE": syscall.SIGFPE,
	"SIGKILL": syscall.SIGKILL, "SIGUSR1": syscall.SIGUSR1, "SIGSEGV": syscall.SIGSEGV,
	"SIGUSR2": syscall.SIGUSR2, "SIGPIPE": syscall.SIGPIPE, "SIGALRM": syscall.SIGALRM,
	"SIGTERM": syscall.SIGTERM, "SIGCHLD": syscall.SIGCHLD, "SIGCONT": syscall.SIGCONT,
	"SIGSTOP": syscall.SIGSTOP, "SIGTSTP": syscall.SIGTSTP, "SIGTTIN": syscall.SIGTTIN,
	"SIGTTOU": syscall.SIGTTOU, "SIGURG": syscall.SIGURG, "SIGXCPU": syscall.SIGXCPU,
	"SIGXFSZ": syscall.SIGXFSZ, "SIGVTALRM": syscall.SIGVTALRM, "SIGPROF": syscall.SIGPROF,
	"SIGWINCH": syscall.SIGWINCH, "SIGIO": syscall.SIGIO, "SIGINFO": syscall.SIGINFO,
	"SIGSYS": syscall.SIGSYS,
}
