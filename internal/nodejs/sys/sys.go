// Package sys implements the Node.js sys module (DEPRECATED - alias for util).
package sys

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed sys.js
var sysJS string

// Sys provides the deprecated sys functionality (alias for util).
type Sys struct{}

// New creates a new Sys module.
func New() *Sys {
	return &Sys{}
}

// Name returns the module name.
func (s *Sys) Name() string {
	return "sys"
}

// Register sets up the sys module.
func (s *Sys) Register(rt *runtime.Runtime) error {
	// Initialize sys (must come after util module)
	if _, err := rt.RunScript(sysJS, "sys.js"); err != nil {
		return err
	}

	return nil
}
