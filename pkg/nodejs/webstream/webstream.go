// Package webstream implements the Web Streams API (stream/web).
package webstream

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed webstream.js
var webstreamJS string

// WebStream provides the Web Streams API.
type WebStream struct{}

// New creates a new WebStream module.
func New() *WebStream {
	return &WebStream{}
}

// Name returns the module name.
func (w *WebStream) Name() string {
	return "webstream"
}

// Register sets up the stream/web module.
func (w *WebStream) Register(rt *runtime.Runtime) error {
	if _, err := rt.RunScript(webstreamJS, "webstream.js"); err != nil {
		return err
	}

	return nil
}
