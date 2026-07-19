// Package querystring implements the Node.js querystring module.
package querystring

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed querystring.js
var querystringJS string

// QueryString provides query string parsing functionality.
type QueryString struct {
	rt *runtime.Runtime
}

// New creates a new QueryString module.
func New() *QueryString {
	return &QueryString{}
}

// Name returns the module name.
func (q *QueryString) Name() string {
	return "querystring"
}

// Register sets up the querystring module.
func (q *QueryString) Register(rt *runtime.Runtime) error {
	q.rt = rt

	// Initialize querystring
	if _, err := rt.RunScript(querystringJS, "querystring.js"); err != nil {
		return err
	}

	return nil
}
