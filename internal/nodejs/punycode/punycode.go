// Package punycode implements the Node.js punycode module (DEPRECATED).
package punycode

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed punycode.js
var punycodeJS string

// Punycode provides punycode functionality.
type Punycode struct{}

// New creates a new Punycode module.
func New() *Punycode {
	return &Punycode{}
}

// Name returns the module name.
func (p *Punycode) Name() string {
	return "punycode"
}

// Register sets up the punycode module.
func (p *Punycode) Register(rt *runtime.Runtime) error {
	// Initialize punycode
	if _, err := rt.RunScript(punycodeJS, "punycode.js"); err != nil {
		return err
	}

	return nil
}
