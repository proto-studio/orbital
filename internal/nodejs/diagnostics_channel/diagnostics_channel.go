// Package diagnostics_channel implements the diagnostics_channel module.
package diagnostics_channel

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed diagnostics_channel.js
var diagnosticsChannelJS string

// DiagnosticsChannel provides pub/sub diagnostics.
type DiagnosticsChannel struct{}

// New creates a new DiagnosticsChannel module.
func New() *DiagnosticsChannel {
	return &DiagnosticsChannel{}
}

// Name returns the module name.
func (d *DiagnosticsChannel) Name() string {
	return "diagnostics_channel"
}

// Register sets up the diagnostics_channel module.
func (d *DiagnosticsChannel) Register(rt *runtime.Runtime) error {
	if _, err := rt.RunScript(diagnosticsChannelJS, "diagnostics_channel.js"); err != nil {
		return err
	}

	return nil
}
