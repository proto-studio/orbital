package runtime

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// InheritedListenerFactory is an optional extension of SocketFactory for listening sockets
// the process already has, such as ones passed by systemd socket activation or a
// supervisor, which scripts name by file descriptor as in server.listen({ fd }).
type InheritedListenerFactory interface {
	// ListenInherited returns a server accepting on the listening socket fd. A factory
	// refuses any descriptor it was not told a script may use.
	ListenInherited(fd int) (TCPServer, error)
}

// InheritedSocketFactory is an optional extension of SocketFactory for connected sockets and
// pipes the process already has, which scripts wrap with new net.Socket({ fd }).
type InheritedSocketFactory interface {
	// SocketInherited returns a socket reading and writing fd. A factory refuses any
	// descriptor it was not told a script may use.
	SocketInherited(fd int) (TCPSocket, error)
}

// ErrNotInherited reports a file descriptor that is not one the embedder made available.
// Node reports EINVAL for a descriptor it can't listen on.
var ErrNotInherited = fmt.Errorf("inherited descriptor: %w", syscall.EINVAL)

// ErrFileDescriptor reports a descriptor of a kind net.Socket can't wrap, with the type
// Node names in ERR_INVALID_FD_TYPE.
type ErrFileDescriptor struct{ Type string }

func (e *ErrFileDescriptor) Error() string {
	return "ERR_INVALID_FD_TYPE: Unsupported fd type: " + e.Type
}

// AllowInheritedDescriptors lets scripts use the given descriptors, with server.listen and
// new net.Socket. Nothing else in the process can be reached by descriptor.
func (f *RealSocketFactory) AllowInheritedDescriptors(fds ...int) {
	f.inheritedFDs = append(f.inheritedFDs, fds...)
}

// AllowAllInheritedDescriptors lets scripts use any descriptor the process has open, as
// Node does. It suits a command-line runtime, not an embedder hosting untrusted scripts.
func (f *RealSocketFactory) AllowAllInheritedDescriptors() {
	f.allInherited = true
}

func (f *RealSocketFactory) allowsFD(fd int) bool {
	if fd < 0 {
		return false
	}
	if f.allInherited {
		return true
	}
	for _, a := range f.inheritedFDs {
		if a == fd {
			return true
		}
	}
	return false
}

// dupInherited returns a close-on-exec copy of fd. The descriptor stays its owner's: only
// the copy is wrapped and closed here.
func dupInherited(fd int) (int, error) {
	// ForkLock keeps a concurrent fork from inheriting the copy before it is close-on-exec.
	syscall.ForkLock.RLock()
	dup, err := syscall.Dup(fd)
	if err == nil {
		syscall.CloseOnExec(dup)
	}
	syscall.ForkLock.RUnlock()
	return dup, err
}

// ListenInherited listens on fd if the factory allows it.
func (f *RealSocketFactory) ListenInherited(fd int) (TCPServer, error) {
	if !f.allowsFD(fd) {
		return nil, ErrNotInherited
	}
	dup, err := dupInherited(fd)
	if err != nil {
		return nil, ErrNotInherited
	}
	file := os.NewFile(uintptr(dup), "inherited")
	defer file.Close()
	// A connected socket would pass as a listener and then fail every accept.
	if !isListening(dup) {
		return nil, ErrNotInherited
	}
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, ErrNotInherited
	}
	network := ""
	switch listener.Addr().(type) {
	case *net.TCPAddr:
	case *net.UnixAddr:
		network = "unix"
	default:
		listener.Close()
		return nil, ErrNotInherited
	}
	return &RealTCPServer{listener: listener, network: network}, nil
}

// SocketInherited wraps fd, a connected socket or a pipe, if the factory allows it.
func (f *RealSocketFactory) SocketInherited(fd int) (TCPSocket, error) {
	if !f.allowsFD(fd) {
		return nil, &ErrFileDescriptor{Type: "UNKNOWN"}
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return nil, &ErrFileDescriptor{Type: "UNKNOWN"}
	}
	kind := st.Mode & syscall.S_IFMT
	switch kind {
	case syscall.S_IFSOCK, syscall.S_IFIFO, syscall.S_IFCHR:
	case syscall.S_IFREG:
		return nil, &ErrFileDescriptor{Type: "FILE"}
	default:
		return nil, &ErrFileDescriptor{Type: "UNKNOWN"}
	}
	dup, err := dupInherited(fd)
	if err != nil {
		return nil, &ErrFileDescriptor{Type: "UNKNOWN"}
	}
	if kind != syscall.S_IFSOCK {
		// Nonblocking, os.NewFile puts the pipe in Go's poller, so closing it interrupts a
		// read or write in progress. As in Node, this applies to the descriptor's other
		// copies too, since they share the setting.
		if err := syscall.SetNonblock(dup, true); err != nil {
			syscall.Close(dup)
			return nil, &ErrFileDescriptor{Type: "UNKNOWN"}
		}
		return &RealTCPSocket{conn: &fileConn{file: os.NewFile(uintptr(dup), "inherited")}}, nil
	}
	file := os.NewFile(uintptr(dup), "inherited")
	defer file.Close()
	if isListening(dup) {
		return nil, &ErrFileDescriptor{Type: "UNKNOWN"}
	}
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, &ErrFileDescriptor{Type: "UNKNOWN"}
	}
	return &RealTCPSocket{conn: conn}, nil
}

// isListening reports whether fd is a listening socket. Where SO_ACCEPTCONN can't be read,
// as on macOS, a socket with a peer is not one, and otherwise listen is called, which fails
// for a connected socket and leaves a listening one listening.
func isListening(fd int) bool {
	listening, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_ACCEPTCONN)
	if err == nil {
		return listening != 0
	}
	if _, err := syscall.Getpeername(fd); err == nil {
		return false
	}
	return syscall.Listen(fd, syscall.SOMAXCONN) == nil
}

// fileConn presents a pipe or terminal as a connection with no addresses.
type fileConn struct{ file *os.File }

func (c *fileConn) Read(b []byte) (int, error)    { return c.file.Read(b) }
func (c *fileConn) Write(b []byte) (int, error)   { return c.file.Write(b) }
func (c *fileConn) Close() error                  { return c.file.Close() }
func (c *fileConn) LocalAddr() net.Addr           { return nil }
func (c *fileConn) RemoteAddr() net.Addr          { return nil }
func (c *fileConn) SetDeadline(t time.Time) error { return ignoreNoDeadline(c.file.SetDeadline(t)) }
func (c *fileConn) SetReadDeadline(t time.Time) error {
	return ignoreNoDeadline(c.file.SetReadDeadline(t))
}
func (c *fileConn) SetWriteDeadline(t time.Time) error {
	return ignoreNoDeadline(c.file.SetWriteDeadline(t))
}

func ignoreNoDeadline(err error) error {
	if errors.Is(err, os.ErrNoDeadline) {
		return nil
	}
	return err
}
