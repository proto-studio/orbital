// Package https implements the Node.js https module.
package https

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed https.js
var httpsJS string

// HTTPS provides HTTPS functionality.
type HTTPS struct{}

// New creates a new HTTPS module.
func New() *HTTPS {
	return &HTTPS{}
}

// Name returns the module name.
func (h *HTTPS) Name() string {
	return "https"
}

// Register sets up the https module.
func (h *HTTPS) Register(rt *runtime.Runtime) error {
	// Initialize https (must come after http module)
	if _, err := rt.RunScript(httpsJS, "https.js"); err != nil {
		return err
	}

	return nil
}
