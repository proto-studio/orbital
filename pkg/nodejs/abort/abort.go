// Package abort implements AbortController and AbortSignal.
package abort

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed abort.js
var abortJS string

// Abort provides AbortController and AbortSignal globals.
type Abort struct{}

// New creates a new Abort module.
func New() *Abort {
	return &Abort{}
}

// Name returns the module name.
func (a *Abort) Name() string {
	return "abort"
}

// Register sets up AbortController and AbortSignal as globals.
func (a *Abort) Register(rt *runtime.Runtime) error {
	// Initialize abort controller/signal
	if _, err := rt.RunScript(abortJS, "abort.js"); err != nil {
		return err
	}

	return nil
}
