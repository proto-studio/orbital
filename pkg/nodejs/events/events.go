// Package events implements the Node.js events module (EventEmitter).
package events

import (
	"github.com/andrewcurioso/gnode/pkg/runtime"
)

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

	// Create EventEmitter as a JavaScript class using a script
	// This is simpler than trying to create a class entirely from Go
	eventEmitterCode := `
(function() {
	class EventEmitter {
		constructor() {
			this._events = {};
			this._maxListeners = 10;
		}

		on(event, listener) {
			return this.addListener(event, listener);
		}

		addListener(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			if (!this._events[event]) {
				this._events[event] = [];
			}
			this._events[event].push({ listener, once: false });
			
			// Check max listeners warning
			if (this._events[event].length > this._maxListeners && this._maxListeners !== 0) {
				console.warn('MaxListenersExceededWarning: Possible EventEmitter memory leak detected. ' +
					this._events[event].length + ' ' + event + ' listeners added. ' +
					'Use emitter.setMaxListeners() to increase limit');
			}
			
			return this;
		}

		once(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			if (!this._events[event]) {
				this._events[event] = [];
			}
			this._events[event].push({ listener, once: true });
			return this;
		}

		off(event, listener) {
			return this.removeListener(event, listener);
		}

		removeListener(event, listener) {
			if (!this._events[event]) {
				return this;
			}
			const idx = this._events[event].findIndex(e => e.listener === listener);
			if (idx !== -1) {
				this._events[event].splice(idx, 1);
			}
			return this;
		}

		removeAllListeners(event) {
			if (event === undefined) {
				this._events = {};
			} else {
				delete this._events[event];
			}
			return this;
		}

		emit(event, ...args) {
			if (!this._events[event]) {
				if (event === 'error') {
					const err = args[0];
					if (err instanceof Error) {
						throw err;
					}
					throw new Error('Unhandled error event');
				}
				return false;
			}

			const listeners = this._events[event].slice();
			for (const entry of listeners) {
				entry.listener.apply(this, args);
				if (entry.once) {
					this.removeListener(event, entry.listener);
				}
			}
			return true;
		}

		listeners(event) {
			if (!this._events[event]) {
				return [];
			}
			return this._events[event].map(e => e.listener);
		}

		rawListeners(event) {
			if (!this._events[event]) {
				return [];
			}
			return this._events[event].slice();
		}

		listenerCount(event) {
			if (!this._events[event]) {
				return 0;
			}
			return this._events[event].length;
		}

		prependListener(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			if (!this._events[event]) {
				this._events[event] = [];
			}
			this._events[event].unshift({ listener, once: false });
			return this;
		}

		prependOnceListener(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			if (!this._events[event]) {
				this._events[event] = [];
			}
			this._events[event].unshift({ listener, once: true });
			return this;
		}

		eventNames() {
			return Object.keys(this._events);
		}

		getMaxListeners() {
			return this._maxListeners;
		}

		setMaxListeners(n) {
			if (typeof n !== 'number' || n < 0 || Number.isNaN(n)) {
				throw new RangeError('The value of "n" is out of range');
			}
			this._maxListeners = n;
			return this;
		}

		static get defaultMaxListeners() {
			return 10;
		}

		static set defaultMaxListeners(n) {
			// No-op for now
		}
	}

	return EventEmitter;
})()
`

	result, err := ctx.RunScript(eventEmitterCode, "events.js")
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
