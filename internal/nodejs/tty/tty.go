// Package tty implements the Node.js tty module (isatty + TTY streams).
package tty

import (
	_ "embed"
	"syscall"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed tty.js
var ttyJS string

// TTY provides the tty module.
type TTY struct {
	rt *runtime.Runtime
}

// New creates a new TTY module.
func New() *TTY {
	return &TTY{}
}

// Name returns the module name.
func (t *TTY) Name() string {
	return "tty"
}

// Register sets up the tty module.
func (t *TTY) Register(rt *runtime.Runtime) error {
	t.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	tmpl, err := iso.NewFunctionTemplate(t.isatty)
	if err != nil {
		return err
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("__tty_isatty", fn); err != nil {
		return err
	}

	if _, err := rt.RunScript(ttyJS, "tty.js"); err != nil {
		return err
	}
	return nil
}

// isatty(fd) reports whether the given file descriptor refers to a terminal.
func (t *TTY) isatty(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 || args[0] == nil {
		return ctx.NewBoolean(false)
	}
	fd := int(args[0].Integer())
	if fd < 0 {
		return ctx.NewBoolean(false)
	}
	// Inspect the fd via a raw fstat rather than os.NewFile: os.NewFile attaches
	// a finalizer that closes the descriptor when the *os.File is garbage
	// collected. Because we only borrow the fd (it belongs to the process's
	// stdio), that finalizer would close stdout/stderr out from under us the
	// next time the GC ran — silently dropping all further output. fstat needs
	// no ownership and no finalizer.
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return ctx.NewBoolean(false)
	}
	return ctx.NewBoolean(st.Mode&syscall.S_IFMT == syscall.S_IFCHR)
}
