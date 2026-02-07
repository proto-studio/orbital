// Package events implements the Node.js events module (EventEmitter).
package events

import (
	_ "embed"

	"proto.zip/studio/orbital/pkg/runtime"
)

//go:embed events.js
var eventsJS string

// Events provides the events module.
type Events struct {
	rt *runtime.Runtime
}

// New creates a new Events module.
func New() *Events {
	return &Events{}
}

// Name returns the module name.
func (e *Events) Name() string {
	return "events"
}

// Register sets up the EventEmitter class.
func (e *Events) Register(rt *runtime.Runtime) error {
	e.rt = rt
	ctx := rt.Context()

	// Execute the embedded JavaScript to create the EventEmitter class
	result, err := ctx.RunScript(eventsJS, "events.js")
	if err != nil {
		return err
	}

	// Set EventEmitter as global
	if err := rt.SetGlobal("EventEmitter", result); err != nil {
		return err
	}

	// Create events module object
	eventsObj, err := ctx.NewObject()
	if err != nil {
		return err
	}
	if err := eventsObj.Set("EventEmitter", result); err != nil {
		return err
	}

	// Set events module as global (for require('events') simulation)
	return rt.SetGlobal("__events_module", eventsObj)
}
