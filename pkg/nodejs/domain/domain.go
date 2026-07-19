// Package domain implements the deprecated Node.js domain module.
package domain

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed domain.js
var domainJS string

// Domain provides the domain module (deprecated).
type Domain struct{}

// New creates a new Domain module.
func New() *Domain {
	return &Domain{}
}

// Name returns the module name.
func (d *Domain) Name() string {
	return "domain"
}

// Register sets up the domain module.
func (d *Domain) Register(rt *runtime.Runtime) error {
	// Must come after events module
	if _, err := rt.RunScript(domainJS, "domain.js"); err != nil {
		return err
	}

	return nil
}
