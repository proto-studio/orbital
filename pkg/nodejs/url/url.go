// Package url implements the Node.js url module.
package url

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed url.js
var urlJS string

// URL provides URL functionality.
type URL struct{}

// New creates a new URL module.
func New() *URL {
	return &URL{}
}

// Name returns the module name.
func (u *URL) Name() string {
	return "url"
}

// Register sets up the url module.
func (u *URL) Register(rt *runtime.Runtime) error {
	// Set up the module as a global
	setupCode := `
		(function() {
			const urlModule = ` + urlJS + `;
			globalThis.__url_module = urlModule;
			globalThis.URL = urlModule.URL;
			globalThis.URLSearchParams = urlModule.URLSearchParams;
		})();
	`
	_, err := rt.RunScript(setupCode, "url_setup.js")
	return err
}
