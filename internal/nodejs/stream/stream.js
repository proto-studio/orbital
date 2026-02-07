(function() {
	'use strict';

	// EventEmitter base (simplified version for streams)
	class EventEmitter {
		constructor() {
			this._events = {};
			this._maxListeners = 10;
		}

		on(event, listener) {
			if (!this._events[event]) {
				this._events[event] = [];
			}
			this._events[event].push(listener);
			return this;
		}

		addListener(event, listener) {
			return this.on(event, listener);
		}

		once(event, listener) {
			const wrapper = (...args) => {
				this.removeListener(event, wrapper);
				listener.apply(this, args);
			};
			wrapper._originalListener = listener;
			return this.on(event, wrapper);
		}

		removeListener(event, listener) {
			if (!this._events[event]) return this;
			this._events[event] = this._events[event].filter(l => 
				l !== listener && l._originalListener !== listener
			);
			return this;
		}

		off(event, listener) {
			return this.removeListener(event, listener);
		}

		emit(event, ...args) {
			if (!this._events[event]) return false;
			const listeners = this._events[event].slice();
			for (const listener of listeners) {
				listener.apply(this, args);
			}
			return true;
		}

		removeAllListeners(event) {
			if (event) {
				delete this._events[event];
			} else {
				this._events = {};
			}
			return this;
		}

		listeners(event) {
			return this._events[event] ? this._events[event].slice() : [];
		}

		listenerCount(event) {
			return this._events[event] ? this._events[event].length : 0;
		}

		setMaxListeners(n) {
			this._maxListeners = n;
			return this;
		}

		getMaxListeners() {
			return this._maxListeners;
		}
	}

	// Stream base class
	class Stream extends EventEmitter {
		constructor(options) {
			super();
			this._options = options || {};
		}

		pipe(destination, options) {
			const source = this;
			options = options || {};

			function ondata(chunk) {
				if (destination.writable !== false) {
					if (destination.write(chunk) === false && source.pause) {
						source.pause();
					}
				}
			}

			source.on('data', ondata);

			function ondrain() {
				if (source.readable && source.resume) {
					source.resume();
				}
			}

			destination.on('drain', ondrain);

			function onend() {
				if (options.end !== false) {
					destination.end();
				}
			}

			source.on('end', onend);

			function onerror(err) {
				cleanup();
				if (this.listenerCount('error') === 0) {
					throw err;
				}
			}

			source.on('error', onerror);
			destination.on('error', onerror);

			function cleanup() {
				source.removeListener('data', ondata);
				destination.removeListener('drain', ondrain);
				source.removeListener('end', onend);
				source.removeListener('error', onerror);
				destination.removeListener('error', onerror);
				source.removeListener('close', cleanup);
				destination.removeListener('close', cleanup);
			}

			source.on('close', cleanup);
			destination.on('close', cleanup);

			destination.emit('pipe', source);

			return destination;
		}

		unpipe(destination) {
			// Simplified unpipe
			this.removeAllListeners('data');
			return this;
		}
	}

	// Readable stream
	class Readable extends Stream {
		constructor(options) {
			super(options);
			this.readable = true;
			this._readableState = {
				buffer: [],
				flowing: null,
				ended: false,
				endEmitted: false,
				reading: false,
				highWaterMark: (options && options.highWaterMark) || 16384,
				objectMode: (options && options.objectMode) || false,
				destroyed: false,
				paused: true
			};

			if (options && typeof options.read === 'function') {
				this._read = options.read;
			}
		}

		_read(size) {
			// Override in subclass or pass in options
		}

		read(size) {
			const state = this._readableState;

			if (state.buffer.length === 0) {
				if (state.ended) {
					return null;
				}
				return null;
			}

			let chunk;
			if (state.objectMode) {
				chunk = state.buffer.shift();
			} else {
				if (!size || size >= state.buffer.length) {
					chunk = state.buffer.join('');
					state.buffer = [];
				} else {
					const full = state.buffer.join('');
					chunk = full.slice(0, size);
					state.buffer = [full.slice(size)];
				}
			}

			if (state.buffer.length === 0 && state.ended) {
				if (!state.endEmitted) {
					state.endEmitted = true;
					this.emit('end');
				}
			}

			return chunk;
		}

		push(chunk) {
			const state = this._readableState;

			if (chunk === null) {
				state.ended = true;
				if (state.buffer.length === 0) {
					state.endEmitted = true;
					this.emit('end');
				}
				return false;
			}

			state.buffer.push(chunk);

			if (state.flowing) {
				this.emit('data', chunk);
			}

			return state.buffer.length < state.highWaterMark;
		}

		unshift(chunk) {
			this._readableState.buffer.unshift(chunk);
		}

		resume() {
			const state = this._readableState;
			if (!state.flowing) {
				state.flowing = true;
				state.paused = false;
				// Emit buffered data
				while (state.buffer.length > 0) {
					const chunk = state.buffer.shift();
					this.emit('data', chunk);
					if (state.paused) break;
				}
				if (state.ended && state.buffer.length === 0 && !state.endEmitted) {
					state.endEmitted = true;
					this.emit('end');
				}
			}
			return this;
		}

		pause() {
			const state = this._readableState;
			if (state.flowing !== false) {
				state.flowing = false;
				state.paused = true;
			}
			return this;
		}

		isPaused() {
			return this._readableState.paused;
		}

		setEncoding(enc) {
			this._encoding = enc;
			return this;
		}

		destroy(err) {
			const state = this._readableState;
			if (state.destroyed) return this;
			state.destroyed = true;

			if (err) {
				this.emit('error', err);
			}
			this.emit('close');
			return this;
		}

		[Symbol.asyncIterator]() {
			const stream = this;
			return {
				next() {
					return new Promise((resolve, reject) => {
						const chunk = stream.read();
						if (chunk !== null) {
							resolve({ value: chunk, done: false });
						} else if (stream._readableState.ended) {
							resolve({ done: true });
						} else {
							const onReadable = () => {
								cleanup();
								const chunk = stream.read();
								if (chunk !== null) {
									resolve({ value: chunk, done: false });
								} else {
									resolve({ done: true });
								}
							};
							const onEnd = () => {
								cleanup();
								resolve({ done: true });
							};
							const onError = (err) => {
								cleanup();
								reject(err);
							};
							const cleanup = () => {
								stream.removeListener('readable', onReadable);
								stream.removeListener('end', onEnd);
								stream.removeListener('error', onError);
							};
							stream.once('readable', onReadable);
							stream.once('end', onEnd);
							stream.once('error', onError);
						}
					});
				}
			};
		}
	}

	// Writable stream
	class Writable extends Stream {
		constructor(options) {
			super(options);
			this.writable = true;
			this._writableState = {
				buffer: [],
				writing: false,
				ended: false,
				finished: false,
				destroyed: false,
				highWaterMark: (options && options.highWaterMark) || 16384,
				objectMode: (options && options.objectMode) || false,
				needDrain: false,
				corked: 0,
				finalCalled: false
			};

			if (options && typeof options.write === 'function') {
				this._write = options.write;
			}
			if (options && typeof options.final === 'function') {
				this._final = options.final;
			}
		}

		_write(chunk, encoding, callback) {
			callback();
		}

		_final(callback) {
			callback();
		}

		write(chunk, encoding, callback) {
			const state = this._writableState;

			if (typeof encoding === 'function') {
				callback = encoding;
				encoding = null;
			}

			if (state.ended) {
				const err = new Error('write after end');
				if (callback) callback(err);
				this.emit('error', err);
				return false;
			}

			if (state.destroyed) {
				return false;
			}

			state.buffer.push({ chunk, encoding, callback });
			this._processBuffer();

			const needDrain = state.buffer.length >= state.highWaterMark;
			state.needDrain = needDrain;
			return !needDrain;
		}

		_processBuffer() {
			const state = this._writableState;

			if (state.writing || state.corked > 0 || state.buffer.length === 0) {
				return;
			}

			const entry = state.buffer.shift();
			state.writing = true;

			this._write(entry.chunk, entry.encoding, (err) => {
				state.writing = false;

				if (err) {
					if (entry.callback) entry.callback(err);
					this.emit('error', err);
					return;
				}

				if (entry.callback) entry.callback();

				if (state.needDrain && state.buffer.length === 0) {
					state.needDrain = false;
					this.emit('drain');
				}

				if (state.buffer.length > 0) {
					this._processBuffer();
				} else if (state.ended && !state.finished) {
					this._finishMaybe();
				}
			});
		}

		cork() {
			this._writableState.corked++;
		}

		uncork() {
			const state = this._writableState;
			if (state.corked > 0) {
				state.corked--;
				if (state.corked === 0) {
					this._processBuffer();
				}
			}
		}

		end(chunk, encoding, callback) {
			const state = this._writableState;

			if (typeof chunk === 'function') {
				callback = chunk;
				chunk = null;
				encoding = null;
			} else if (typeof encoding === 'function') {
				callback = encoding;
				encoding = null;
			}

			if (chunk !== null && chunk !== undefined) {
				this.write(chunk, encoding);
			}

			state.ended = true;

			if (callback) {
				this.once('finish', callback);
			}

			this._finishMaybe();

			return this;
		}

		_finishMaybe() {
			const state = this._writableState;

			if (state.ended && !state.finished && state.buffer.length === 0 && !state.writing) {
				if (!state.finalCalled) {
					state.finalCalled = true;
					this._final((err) => {
						if (err) {
							this.emit('error', err);
							return;
						}
						state.finished = true;
						this.emit('finish');
					});
				}
			}
		}

		destroy(err) {
			const state = this._writableState;
			if (state.destroyed) return this;
			state.destroyed = true;

			if (err) {
				this.emit('error', err);
			}
			this.emit('close');
			return this;
		}

		setDefaultEncoding(encoding) {
			this._defaultEncoding = encoding;
			return this;
		}
	}

	// Duplex stream (both readable and writable)
	class Duplex extends Readable {
		constructor(options) {
			super(options);

			// Add writable state
			this.writable = true;
			this._writableState = {
				buffer: [],
				writing: false,
				ended: false,
				finished: false,
				destroyed: false,
				highWaterMark: (options && options.highWaterMark) || 16384,
				objectMode: (options && options.objectMode) || false,
				needDrain: false,
				corked: 0,
				finalCalled: false
			};

			if (options && typeof options.write === 'function') {
				this._write = options.write;
			}
			if (options && typeof options.final === 'function') {
				this._final = options.final;
			}
		}
	}

	// Copy Writable methods to Duplex prototype
	Duplex.prototype._write = Writable.prototype._write;
	Duplex.prototype._final = Writable.prototype._final;
	Duplex.prototype.write = Writable.prototype.write;
	Duplex.prototype._processBuffer = Writable.prototype._processBuffer;
	Duplex.prototype.cork = Writable.prototype.cork;
	Duplex.prototype.uncork = Writable.prototype.uncork;
	Duplex.prototype.end = Writable.prototype.end;
	Duplex.prototype._finishMaybe = Writable.prototype._finishMaybe;
	Duplex.prototype.setDefaultEncoding = Writable.prototype.setDefaultEncoding;

	// Transform stream
	class Transform extends Duplex {
		constructor(options) {
			super(options);
			this._transformState = {
				transforming: false,
				pendingCallback: null
			};

			if (options && typeof options.transform === 'function') {
				this._transform = options.transform;
			}
			if (options && typeof options.flush === 'function') {
				this._flush = options.flush;
			}
		}

		_transform(chunk, encoding, callback) {
			callback(null, chunk);
		}

		_flush(callback) {
			callback();
		}

		_write(chunk, encoding, callback) {
			const state = this._transformState;
			state.transforming = true;

			this._transform(chunk, encoding, (err, data) => {
				state.transforming = false;

				if (err) {
					callback(err);
					return;
				}

				if (data !== null && data !== undefined) {
					this.push(data);
				}

				callback();
			});
		}

		_final(callback) {
			this._flush((err, data) => {
				if (err) {
					callback(err);
					return;
				}
				if (data !== null && data !== undefined) {
					this.push(data);
				}
				this.push(null);
				callback();
			});
		}
	}

	// PassThrough stream (transform that passes data through unchanged)
	class PassThrough extends Transform {
		constructor(options) {
			super(options);
		}

		_transform(chunk, encoding, callback) {
			callback(null, chunk);
		}
	}

	// Finished utility
	function finished(stream, options, callback) {
		if (typeof options === 'function') {
			callback = options;
			options = {};
		}
		options = options || {};

		const readable = options.readable !== false && stream.readable;
		const writable = options.writable !== false && stream.writable;

		let readableEnded = !readable;
		let writableFinished = !writable;

		const onFinish = () => {
			writableFinished = true;
			if (readableEnded) callback();
		};

		const onEnd = () => {
			readableEnded = true;
			if (writableFinished) callback();
		};

		const onError = (err) => {
			callback(err);
		};

		const onClose = () => {
			if (readable && !readableEnded) {
				callback(new Error('Stream closed before end'));
			} else if (writable && !writableFinished) {
				callback(new Error('Stream closed before finish'));
			} else {
				callback();
			}
		};

		if (writable) stream.on('finish', onFinish);
		if (readable) stream.on('end', onEnd);
		stream.on('error', onError);
		stream.on('close', onClose);

		return function cleanup() {
			stream.removeListener('finish', onFinish);
			stream.removeListener('end', onEnd);
			stream.removeListener('error', onError);
			stream.removeListener('close', onClose);
		};
	}

	// Pipeline utility
	function pipeline(...args) {
		const callback = args.pop();
		const streams = args;

		if (streams.length < 2) {
			throw new Error('pipeline requires at least 2 streams');
		}

		let error;
		const destroys = [];

		function destroyer(stream, reading, writing) {
			return function(err) {
				if (err) error = err;
				if (reading && stream.destroy) stream.destroy();
				if (writing && stream.destroy) stream.destroy();
			};
		}

		let i = 0;
		for (; i < streams.length - 1; i++) {
			const source = streams[i];
			const dest = streams[i + 1];
			source.pipe(dest);
			destroys.push(finished(source, { writable: false }, destroyer(source, true, false)));
		}
		destroys.push(finished(streams[i], { readable: false }, destroyer(streams[i], false, true)));

		// Final stream callback
		finished(streams[streams.length - 1], (err) => {
			destroys.forEach(fn => fn());
			callback(err || error);
		});

		return streams[streams.length - 1];
	}

	return {
		Stream,
		Readable,
		Writable,
		Duplex,
		Transform,
		PassThrough,
		finished,
		pipeline
	};
})()
