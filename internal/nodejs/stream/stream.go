// Package stream implements the Node.js stream module.
package stream

import (
	_ "embed"

	"github.com/andrewcurioso/gnode/pkg/runtime"
)

//go:embed stream.js
var streamJS string

// Stream provides stream functionality.
type Stream struct{}

// New creates a new Stream module.
func New() *Stream {
	return &Stream{}
}

// Name returns the module name.
func (s *Stream) Name() string {
	return "stream"
}

// Register sets up the stream module.
func (s *Stream) Register(rt *runtime.Runtime) error {
	// Set up the module as a global
	setupCode := `
		(function() {
			const streamModule = ` + streamJS + `;
			globalThis.__stream_module = streamModule;
			globalThis.Stream = streamModule.Stream;
		})();
	`
	_, err := rt.RunScript(setupCode, "stream_setup.js")
	return err
}
