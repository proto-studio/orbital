// Package webcrypto implements the Web Crypto API (crypto/webcrypto).
package webcrypto

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed webcrypto.js
var webcryptoJS string

// WebCrypto provides the Web Crypto API.
type WebCrypto struct{}

// New creates a new WebCrypto module.
func New() *WebCrypto {
	return &WebCrypto{}
}

// Name returns the module name.
func (w *WebCrypto) Name() string {
	return "webcrypto"
}

// Register sets up the webcrypto module.
func (w *WebCrypto) Register(rt *runtime.Runtime) error {
	// Initialize webcrypto (must come after crypto module)
	if _, err := rt.RunScript(webcryptoJS, "webcrypto.js"); err != nil {
		return err
	}

	return nil
}
