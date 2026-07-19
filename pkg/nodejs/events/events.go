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

	// The events module *is* the EventEmitter constructor (matching Node), so
	// require('events') resolves to it directly. The class carries an
	// `.EventEmitter` self-reference (set in events.js), so existing consumers
	// that read `__events_module.EventEmitter` keep working, and libraries that
	// do `class X extends require('events')` (e.g. undici) now work too.
	return rt.SetGlobal("__events_module", result)
}
