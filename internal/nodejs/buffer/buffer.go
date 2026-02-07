// Package buffer implements the Node.js Buffer class.
package buffer

import (
	_ "embed"

	"github.com/andrewcurioso/gnode/pkg/runtime"
)

//go:embed buffer.js
var bufferJS string

// Buffer provides the Buffer global class.
type Buffer struct {
	rt *runtime.Runtime
}

// New creates a new Buffer module.
func New() *Buffer {
	return &Buffer{}
}

// Name returns the module name.
func (b *Buffer) Name() string {
	return "buffer"
}

// Register sets up the Buffer global class.
func (b *Buffer) Register(rt *runtime.Runtime) error {
	b.rt = rt
	ctx := rt.Context()

	// Execute the embedded JavaScript to create the Buffer class
	result, err := ctx.RunScript(bufferJS, "buffer.js")
	if err != nil {
		return err
	}

	// Set Buffer as global
	if err := rt.SetGlobal("Buffer", result); err != nil {
		return err
	}

	// Create buffer module object for require('buffer')
	moduleCode := `
({
	Buffer: Buffer,
	kMaxLength: Buffer.kMaxLength,
	INSPECT_MAX_BYTES: 50,
	constants: {
		MAX_LENGTH: Buffer.kMaxLength,
		MAX_STRING_LENGTH: 536870888
	}
})
`
	moduleResult, err := ctx.RunScript(moduleCode, "buffer_module.js")
	if err != nil {
		return err
	}

	return rt.SetGlobal("__buffer_module", moduleResult)
}
