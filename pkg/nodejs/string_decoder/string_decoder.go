// Package string_decoder implements the Node.js string_decoder module.
package string_decoder

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed string_decoder.js
var stringDecoderJS string

// StringDecoder provides string decoding functionality.
type StringDecoder struct {
	rt *runtime.Runtime
}

// New creates a new StringDecoder module.
func New() *StringDecoder {
	return &StringDecoder{}
}

// Name returns the module name.
func (s *StringDecoder) Name() string {
	return "string_decoder"
}

// Register sets up the string_decoder module.
func (s *StringDecoder) Register(rt *runtime.Runtime) error {
	s.rt = rt

	// Initialize string_decoder
	if _, err := rt.RunScript(stringDecoderJS, "string_decoder.js"); err != nil {
		return err
	}

	return nil
}
