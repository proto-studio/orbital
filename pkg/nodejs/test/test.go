// Package test implements the Node.js test runner (node:test).
package test

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed test.js
var testJS string

// Test provides the test runner module.
type Test struct{}

// New creates a new Test module.
func New() *Test {
	return &Test{}
}

// Name returns the module name.
func (t *Test) Name() string {
	return "test"
}

// Register sets up the test module.
func (t *Test) Register(rt *runtime.Runtime) error {
	// Must come after events module
	if _, err := rt.RunScript(testJS, "test.js"); err != nil {
		return err
	}

	return nil
}
