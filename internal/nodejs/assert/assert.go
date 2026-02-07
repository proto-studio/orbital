// Package assert implements the Node.js assert module.
package assert

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed assert.js
var assertJS string

// Assert provides assertion testing functionality.
type Assert struct {
	rt *runtime.Runtime
}

// New creates a new Assert module.
func New() *Assert {
	return &Assert{}
}

// Name returns the module name.
func (a *Assert) Name() string {
	return "assert"
}

// Register sets up the assert module.
func (a *Assert) Register(rt *runtime.Runtime) error {
	a.rt = rt

	// Initialize assert
	if _, err := rt.RunScript(assertJS, "assert.js"); err != nil {
		return err
	}

	return nil
}
