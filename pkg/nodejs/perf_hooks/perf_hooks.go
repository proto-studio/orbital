// Package perf_hooks implements the Node.js perf_hooks module.
package perf_hooks

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed perf_hooks.js
var perfHooksJS string

// PerfHooks provides performance measurement functionality.
type PerfHooks struct{}

// New creates a new PerfHooks module.
func New() *PerfHooks {
	return &PerfHooks{}
}

// Name returns the module name.
func (p *PerfHooks) Name() string {
	return "perf_hooks"
}

// Register sets up the perf_hooks module.
func (p *PerfHooks) Register(rt *runtime.Runtime) error {
	// Initialize perf_hooks
	if _, err := rt.RunScript(perfHooksJS, "perf_hooks.js"); err != nil {
		return err
	}

	return nil
}
