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
	// util.js formats with the code it shares with console, passed in as inspectShared.
	shared, err := inspectjs.Load(rt)
	if err != nil {
		return err
	}
	setup, err := rt.RunScript(`(function (inspectShared) {
		globalThis.__util_module = (
`+utilJS+`
);
	})`, "util_setup.js")
	if err != nil {
		return err
	}
	_, err = setup.Call(nil, shared)
	return err
}
