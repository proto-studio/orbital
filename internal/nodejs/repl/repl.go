// Package repl implements the Node.js REPL module.
package repl

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed repl.js
var replJS string

// REPL provides the REPL module.
type REPL struct{}

// New creates a new REPL module.
func New() *REPL {
	return &REPL{}
}

// Name returns the module name.
func (r *REPL) Name() string {
	return "repl"
}

// Register sets up the repl module.
func (r *REPL) Register(rt *runtime.Runtime) error {
	// Must come after events and util modules
	if _, err := rt.RunScript(replJS, "repl.js"); err != nil {
		return err
	}

	return nil
}
