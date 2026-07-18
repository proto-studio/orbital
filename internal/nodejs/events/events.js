(function() {
	class EventEmitter {
		constructor() {
			this._events = {};
			this._maxListeners = undefined;
		}

		// Lazily initialize the listener store. Node does this in every method so
		// that subclasses created via util.inherits() (which never call the
		// EventEmitter constructor) still work — mocha's Suite is one such case.
		_store() {
			if (this._events === undefined || this._events === null) {
				this._events = {};
			}
			return this._events;
		}

		on(event, listener) {
			return this.addListener(event, listener);
		}

		addListener(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			const events = this._store();
			// Node emits a 'newListener' event before adding, which some libraries
			// (including mocha) rely on.
			if (events['newListener']) {
				this.emit('newListener', event, listener);
			}
			if (!events[event]) {
				events[event] = [];
			}
			events[event].push({ listener, once: false });

			const max = this.getMaxListeners();
			if (events[event].length > max && max !== 0) {
				console.warn('MaxListenersExceededWarning: Possible EventEmitter memory leak detected. ' +
					events[event].length + ' ' + event + ' listeners added. ' +
					'Use emitter.setMaxListeners() to increase limit');
			}

			return this;
		}

		once(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			const events = this._store();
			if (!events[event]) {
				events[event] = [];
			}
			events[event].push({ listener, once: true });
			return this;
		}

		off(event, listener) {
			return this.removeListener(event, listener);
		}

		removeListener(event, listener) {
			const events = this._store();
			if (!events[event]) {
				return this;
			}
			const idx = events[event].findIndex(e => e.listener === listener);
			if (idx !== -1) {
				events[event].splice(idx, 1);
				if (events['removeListener']) {
					this.emit('removeListener', event, listener);
				}
			}
			return this;
		}

		removeAllListeners(event) {
			if (event === undefined) {
				this._events = {};
			} else if (this._events) {
				delete this._events[event];
			}
			return this;
		}

		emit(event, ...args) {
			const events = this._store();
			if (!events[event] || events[event].length === 0) {
				if (event === 'error') {
					const err = args[0];
					if (err instanceof Error) {
						throw err;
					}
					const e = new Error('Unhandled error event');
					throw e;
				}
				return false;
			}

			const listeners = events[event].slice();
			for (const entry of listeners) {
				if (entry.once) {
					this.removeListener(event, entry.listener);
				}
				entry.listener.apply(this, args);
			}
			return true;
		}

		listeners(event) {
			const events = this._store();
			if (!events[event]) {
				return [];
			}
			return events[event].map(e => e.listener);
		}

		rawListeners(event) {
			const events = this._store();
			if (!events[event]) {
				return [];
			}
			return events[event].slice();
		}

		listenerCount(event) {
			const events = this._store();
			if (!events[event]) {
				return 0;
			}
			return events[event].length;
		}

		prependListener(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			const events = this._store();
			if (!events[event]) {
				events[event] = [];
			}
			events[event].unshift({ listener, once: false });
			return this;
		}

		prependOnceListener(event, listener) {
			if (typeof listener !== 'function') {
				throw new TypeError('The "listener" argument must be of type Function');
			}
			const events = this._store();
			if (!events[event]) {
				events[event] = [];
			}
			events[event].unshift({ listener, once: true });
			return this;
		}

		eventNames() {
			return Object.keys(this._store());
		}

		getMaxListeners() {
			if (this._maxListeners === undefined || this._maxListeners === null) {
				return EventEmitter.defaultMaxListeners;
			}
			return this._maxListeners;
		}

		setMaxListeners(n) {
			if (typeof n !== 'number' || n < 0 || Number.isNaN(n)) {
				throw new RangeError('The value of "n" is out of range');
			}
			this._maxListeners = n;
			return this;
		}

		addEventListener(event, listener) {
			return this.addListener(event, listener);
		}

		removeEventListener(event, listener) {
			return this.removeListener(event, listener);
		}

	}

	EventEmitter.defaultMaxListeners = 10;

	// In Node.js the events module *is* the EventEmitter constructor, with the
	// class also exposed as a named property. This lets both usage styles work:
	//   const EventEmitter = require('events');        // extend it as a class
	//   const { EventEmitter } = require('events');    // destructure it
	// Libraries such as undici rely on `class X extends require('node:events')`.
	EventEmitter.EventEmitter = EventEmitter;

	// Static helper: EventEmitter.once(emitter, name) -> Promise<any[]>
	EventEmitter.once = function(emitter, name) {
		return new Promise((resolve, reject) => {
			function cleanup() {
				if (typeof emitter.removeListener === 'function') {
					emitter.removeListener(name, onEvent);
					if (errorListener) {
						emitter.removeListener('error', errorListener);
					}
				}
			}
			function onEvent(...args) {
				cleanup();
				resolve(args);
			}
			let errorListener = null;
			if (name !== 'error') {
				errorListener = function(err) {
					cleanup();
					reject(err);
				};
				emitter.once('error', errorListener);
			}
			emitter.once(name, onEvent);
		});
	};

	return EventEmitter;
})()
