// Package util implements the Node.js util module.
package util

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/nodejs/internal/inspectjs"
	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed util.js
var utilJS string

// Util provides utility functionality.
type Util struct{}

// New creates a new Util module.
func New() *Util {
	return &Util{}
}

// Name returns the module name.
func (u *Util) Name() string {
	return "util"
}

// Register sets up the util module.
func (u *Util) Register(rt *runtime.Runtime) error {
	setupCode := `
		(function() {
			// util.js formats with the code it shares with console, passed in as inspectShared.
			const utilModule = (function (inspectShared) { return (
` + utilJS + `
); })(` + inspectjs.JS + `);
			globalThis.__util_module = utilModule;
		})();
	`
	_, err := rt.RunScript(setupCode, "util_setup.js")
	return err
}
