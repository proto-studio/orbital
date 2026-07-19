// Package readline implements the Node.js readline module.
package readline

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed readline.js
var readlineJS string

// Readline provides readline functionality.
type Readline struct{}

// New creates a new Readline module.
func New() *Readline {
	return &Readline{}
}

// Name returns the module name.
func (r *Readline) Name() string {
	return "readline"
}

// Register sets up the readline module.
func (r *Readline) Register(rt *runtime.Runtime) error {
	// Initialize readline
	if _, err := rt.RunScript(readlineJS, "readline.js"); err != nil {
		return err
	}

	return nil
}
