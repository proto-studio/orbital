// Package async_hooks implements a minimal Node.js async_hooks module.
package async_hooks

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed async_hooks.js
var asyncHooksJS string

// AsyncHooks provides AsyncLocalStorage and AsyncResource.
type AsyncHooks struct{}

// New creates a new AsyncHooks module.
func New() *AsyncHooks {
	return &AsyncHooks{}
}

// Name returns the module name.
func (a *AsyncHooks) Name() string {
	return "async_hooks"
}

// Register sets up the async_hooks module.
func (a *AsyncHooks) Register(rt *runtime.Runtime) error {
	if _, err := rt.RunScript(asyncHooksJS, "async_hooks.js"); err != nil {
		return err
	}

	return nil
}
