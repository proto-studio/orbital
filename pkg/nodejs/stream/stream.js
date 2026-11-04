(function() {
	'use strict';

	const EventEmitter = globalThis.__events_module;
	const kDefaultHighWaterMark = 16384;

	function codedError(Type, code, message) {
		const err = new Type(message);
		err.code = code;
		return err;
	}

	function isUint8Array(chunk) {
		return chunk instanceof Uint8Array;
	}

	function highWaterMarkFrom(options, sideKey, objectMode) {
		if (options) {
			if (typeof options.highWaterMark === 'number') return Math.floor(options.highWaterMark);
			if (typeof options[sideKey] === 'number') return Math.floor(options[sideKey]);
		}
		return objectMode ? 16 : kDefaultHighWaterMark;
	}

	// State shared by both sides of a stream's destroy sequence. Each side has its own
	// state object, but a Duplex is destroyed as one stream.
	function sides(stream) {
		const out = [];
		if (stream._readableState) out.push(stream._readableState);
		if (stream._writableState) out.push(stream._writableState);
		return out;
	}

	function destroyStream(stream, err, cb) {
		const states = sides(stream);
		if (states.some((s) => s.destroyed)) {
			if (typeof cb === 'function') cb();
			return stream;
		}
		for (const s of states) {
			s.destroyed = true;
			if (err && !s.errored) s.errored = err;
		}
		const finish = (error) => {
			if (error) {
				for (const s of states) if (!s.errored) s.errored = error;
			}
			for (const s of states) s.closed = true;
			if (typeof cb === 'function') cb(error);
			process.nextTick(() => {
				const first = states[0];
				if (error && !states.some((s) => s.errorEmitted)) {
					for (const s of states) s.errorEmitted = true;
					stream.emit('error', error);
				}
				if (first && first.emitClose && !states.some((s) => s.closeEmitted)) {
					for (const s of states) s.closeEmitted = true;
					stream.emit('close');
				}
			});
		};
		let called = false;
		const once = (error) => {
			if (called) return;
			called = true;
			finish(error || null);
		};
		try {
			const result = stream._destroy(err || null, once);
			if (result && typeof result.then === 'function') {
				result.then(() => once(), once);
			}
		} catch (e) {
			once(e);
		}
		return stream;
	}

	// An error while the stream is working: destroys it if it destroys itself, as streams
	// do by default, and otherwise just reports it.
	function errorOrDestroy(stream, err) {
		const states = sides(stream);
		if (states.some((s) => s.destroyed)) return;
		if (states.some((s) => s.autoDestroy)) {
			stream.destroy(err);
			return;
		}
		if (!states.some((s) => s.errorEmitted)) {
			for (const s of states) {
				s.errorEmitted = true;
				if (!s.errored) s.errored = err;
			}
			stream.emit('error', err);
		}
	}

	function autoDestroyIfDone(stream) {
		const r = stream._readableState;
		const w = stream._writableState;
		if ((r && !r.autoDestroy) || (w && !w.autoDestroy)) return;
		if (r && !r.endEmitted) return;
		if (w && !w.finished) return;
		stream.destroy();
	}

	// Legacy base class: a pipe with nothing but events.
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
					if (destination.write(chunk) === false && source.pause) source.pause();
				}
			}
			function ondrain() {
				if (source.readable && source.resume) source.resume();
			}
			function onend() {
				if (options.end !== false && destination.end) destination.end();
			}
			function cleanup() {
				source.removeListener('data', ondata);
				destination.removeListener('drain', ondrain);
				source.removeListener('end', onend);
				source.removeListener('close', cleanup);
				destination.removeListener('close', cleanup);
			}

			source.on('data', ondata);
			destination.on('drain', ondrain);
			source.on('end', onend);
			source.on('close', cleanup);
			destination.on('close', cleanup);
			destination.emit('pipe', source);
			return destination;
		}
	}

	class ReadableState {
		constructor(options, stream) {
			options = options || {};
			this.objectMode = !!(options.objectMode || options.readableObjectMode);
			this.highWaterMark = highWaterMarkFrom(options, 'readableHighWaterMark', this.objectMode);
			this.buffer = [];
			this.length = 0;
			this.pipes = [];
			this.flowing = null;
			this.ended = false;
			this.endEmitted = false;
			this.reading = false;
			this.sync = true;
			this.needReadable = false;
			this.emittedReadable = false;
			this.readableListening = false;
			this.resumeScheduled = false;
			this.paused = null;
			this.awaitDrainWriters = null;
			this.errored = null;
			this.errorEmitted = false;
			this.closed = false;
			this.closeEmitted = false;
			this.destroyed = false;
			this.dataEmitted = false;
			this.autoDestroy = options.autoDestroy !== false;
			this.emitClose = options.emitClose !== false;
			this.defaultEncoding = options.defaultEncoding || 'utf8';
			this.decoder = null;
			this.encoding = null;
			if (options.encoding) {
				this.decoder = newDecoder(options.encoding);
				this.encoding = options.encoding;
			}
		}
	}

	function newDecoder(encoding) {
		const mod = globalThis.__string_decoder_module;
		if (mod && mod.StringDecoder) return new mod.StringDecoder(encoding);
		// string_decoder registers after stream. Until then a whole-chunk decode is the best
		// available, and nothing runs that early.
		return { write: (b) => Buffer.from(b).toString(encoding), end: () => '' };
	}

	class Readable extends Stream {
		constructor(options) {
			super(options);
			this._readableState = new ReadableState(options, this);
			if (options) {
				if (typeof options.read === 'function') this._read = options.read;
				if (typeof options.destroy === 'function') this._destroy = options.destroy;
				if (options.signal) addAbortSignal(options.signal, this);
			}
		}

		get readable() {
			const s = this._readableState;
			return !!s && s.readable !== false && !s.destroyed && !s.errorEmitted && !s.endEmitted;
		}
		// Some packages assign readable directly.
		set readable(v) {
			if (this._readableState) this._readableState.readable = !!v;
		}
		get readableFlowing() { return this._readableState.flowing; }
		set readableFlowing(v) { this._readableState.flowing = v; }
		get readableLength() { return this._readableState.length; }
		get readableHighWaterMark() { return this._readableState.highWaterMark; }
		get readableObjectMode() { return this._readableState.objectMode; }
		get readableEncoding() { return this._readableState.encoding; }
		get readableEnded() { return this._readableState.endEmitted; }
		get readableBuffer() { return this._readableState.buffer; }
		get readableDidRead() { return this._readableState.dataEmitted; }
		get readableAborted() {
			const s = this._readableState;
			return (s.destroyed || !!s.errored) && !s.endEmitted;
		}
		get destroyed() {
			const s = this._readableState;
			return !!s && s.destroyed;
		}
		set destroyed(v) {
			for (const s of sides(this)) s.destroyed = v;
		}
		get closed() { return this._readableState.closed; }
		get errored() { return this._readableState.errored; }

		_read() {
			throw codedError(Error, 'ERR_METHOD_NOT_IMPLEMENTED', 'The _read() method is not implemented');
		}

		_destroy(err, cb) {
			cb(err);
		}

		destroy(err, cb) {
			return destroyStream(this, err, cb);
		}

		push(chunk, encoding) {
			return addChunk(this, chunk, encoding, false);
		}

		unshift(chunk, encoding) {
			return addChunk(this, chunk, encoding, true);
		}

		setEncoding(encoding) {
			const s = this._readableState;
			s.decoder = newDecoder(encoding);
			s.encoding = encoding;
			// Anything already buffered is re-decoded so reads stay consistent.
			let content = '';
			for (const part of s.buffer) content += s.decoder.write(part);
			s.buffer = content ? [content] : [];
			s.length = content.length;
			return this;
		}

		isPaused() {
			const s = this._readableState;
			return s.paused === true || s.flowing === false;
		}

		read(n) {
			const s = this._readableState;
			const requested = n === undefined ? NaN : parseInt(n, 10);
			let size = requested;
			if (size > s.highWaterMark) {
				s.highWaterMark = nextHighWaterMark(size);
			}
			if (size !== 0) s.emittedReadable = false;

			if (size === 0 && s.needReadable &&
				((s.highWaterMark !== 0 ? s.length >= s.highWaterMark : s.length > 0) || s.ended)) {
				if (s.length === 0 && s.ended) endReadable(this);
				else emitReadable(this);
				return null;
			}

			size = howMuchToRead(size, s);
			if (size === 0 && s.ended) {
				if (s.length === 0) endReadable(this);
				return null;
			}

			let doRead = s.needReadable;
			if (s.length === 0 || s.length - size < s.highWaterMark) doRead = true;
			if (s.ended || s.reading || s.destroyed || s.errored) doRead = false;
			else if (doRead) {
				s.reading = true;
				s.sync = true;
				if (s.length === 0) s.needReadable = true;
				try {
					this._read(s.highWaterMark);
				} catch (err) {
					errorOrDestroy(this, err);
				}
				s.sync = false;
				if (!s.reading) size = howMuchToRead(requested, s);
			}

			let ret = size > 0 ? takeFromBuffer(size, s) : null;
			if (ret === null) {
				s.needReadable = s.length <= s.highWaterMark;
				size = 0;
			}

			if (s.length === 0) {
				if (!s.ended) s.needReadable = true;
				if (requested !== size && s.ended) endReadable(this);
			}

			if (ret !== null && !s.errorEmitted && !s.closeEmitted) {
				s.dataEmitted = true;
				this.emit('data', ret);
			}
			return ret;
		}

		on(event, listener) {
			const res = EventEmitter.prototype.addListener.call(this, event, listener);
			this._onListenerAdded(event);
			return res;
		}

		addListener(event, listener) {
			return this.on(event, listener);
		}

		once(event, listener) {
			const res = super.once(event, listener);
			this._onListenerAdded(event);
			return res;
		}

		_onListenerAdded(event) {
			const s = this._readableState;
			if (event === 'data') {
				s.readableListening = this.listenerCount('readable') > 0;
				if (s.flowing !== false) this.resume();
			} else if (event === 'readable') {
				if (!s.endEmitted && !s.readableListening) {
					s.readableListening = s.needReadable = true;
					s.flowing = false;
					s.emittedReadable = false;
					if (s.length) emitReadable(this);
					else if (!s.reading) process.nextTick(() => this.read(0));
				}
			}
		}

		removeListener(event, listener) {
			const res = super.removeListener(event, listener);
			if (event === 'readable') process.nextTick(() => updateReadableListening(this));
			return res;
		}

		removeAllListeners(event) {
			const res = super.removeAllListeners(event);
			if (event === 'readable' || event === undefined) process.nextTick(() => updateReadableListening(this));
			return res;
		}

		resume() {
			const s = this._readableState;
			if (!s.flowing) {
				s.flowing = !s.readableListening;
				if (!s.resumeScheduled) {
					s.resumeScheduled = true;
					process.nextTick(() => {
						s.resumeScheduled = false;
						if (!s.reading) this.read(0);
						this.emit('resume');
						flow(this);
						if (s.flowing && !s.reading) this.read(0);
					});
				}
			}
			s.paused = false;
			return this;
		}

		pause() {
			const s = this._readableState;
			if (s.flowing !== false) {
				s.flowing = false;
				this.emit('pause');
			}
			s.paused = true;
			return this;
		}

		pipe(dest, options) {
			const src = this;
			const s = this._readableState;
			if (s.pipes.length === 1 && !s.multiAwaitDrain) {
				s.multiAwaitDrain = true;
				s.awaitDrainWriters = new Set(s.awaitDrainWriters ? [s.awaitDrainWriters] : []);
			}
			s.pipes.push(dest);

			const doEnd = (!options || options.end !== false) &&
				dest !== (globalThis.process && process.stdout) &&
				dest !== (globalThis.process && process.stderr);
			const endFn = doEnd ? onend : unpipe;
			if (s.endEmitted) process.nextTick(endFn);
			else src.once('end', endFn);

			dest.on('unpipe', onunpipe);
			function onunpipe(readable, info) {
				if (readable === src && info && info.hasUnpiped === false) {
					info.hasUnpiped = true;
					cleanup();
				}
			}
			function onend() {
				dest.end();
			}

			let ondrain;
			let cleanedUp = false;
			function cleanup() {
				dest.removeListener('close', onclose);
				dest.removeListener('finish', onfinish);
				if (ondrain) dest.removeListener('drain', ondrain);
				dest.removeListener('error', onerror);
				dest.removeListener('unpipe', onunpipe);
				src.removeListener('end', onend);
				src.removeListener('end', unpipe);
				src.removeListener('data', ondata);
				cleanedUp = true;
				const w = dest._writableState;
				if (ondrain && s.awaitDrainWriters && (!w || w.needDrain)) ondrain();
			}

			function pauseForDrain() {
				if (!cleanedUp) {
					if (s.pipes.length === 1 && s.pipes[0] === dest) {
						s.awaitDrainWriters = dest;
						s.multiAwaitDrain = false;
					} else if (s.pipes.length > 1 && s.pipes.includes(dest)) {
						s.awaitDrainWriters.add(dest);
					}
					src.pause();
				}
				if (!ondrain) {
					ondrain = pipeOnDrain(src, dest);
					dest.on('drain', ondrain);
				}
			}

			src.on('data', ondata);
			function ondata(chunk) {
				if (dest.write(chunk) === false) pauseForDrain();
			}

			function onerror(err) {
				unpipe();
				dest.removeListener('error', onerror);
				if (dest.listenerCount('error') === 0) {
					const w = dest._writableState || dest._readableState;
					if (w && !w.errorEmitted) errorOrDestroy(dest, err);
					else dest.emit('error', err);
				}
			}
			dest.prependListener('error', onerror);

			function onclose() {
				dest.removeListener('finish', onfinish);
				unpipe();
			}
			dest.once('close', onclose);
			function onfinish() {
				dest.removeListener('close', onclose);
				unpipe();
			}
			dest.once('finish', onfinish);
			function unpipe() {
				src.unpipe(dest);
			}

			dest.emit('pipe', src);

			if (dest.writableNeedDrain === true) {
				if (s.flowing) pauseForDrain();
			} else if (!s.flowing) {
				src.resume();
			}
			return dest;
		}

		unpipe(dest) {
			const s = this._readableState;
			const info = { hasUnpiped: false };
			if (s.pipes.length === 0) return this;
			if (!dest) {
				const dests = s.pipes;
				s.pipes = [];
				this.pause();
				for (const d of dests) d.emit('unpipe', this, { hasUnpiped: false });
				return this;
			}
			const index = s.pipes.indexOf(dest);
			if (index === -1) return this;
			s.pipes.splice(index, 1);
			if (s.pipes.length === 0) this.pause();
			dest.emit('unpipe', this, info);
			return this;
		}

		wrap(oldStream) {
			let paused = false;
			oldStream.on('data', (chunk) => {
				if (!this.push(chunk) && oldStream.pause) {
					paused = true;
					oldStream.pause();
				}
			});
			oldStream.on('end', () => this.push(null));
			oldStream.on('error', (err) => errorOrDestroy(this, err));
			oldStream.on('close', () => this.destroy());
			oldStream.on('destroy', () => this.destroy());
			oldStream.on('pause', () => this.pause());
			oldStream.on('resume', () => this.resume());
			this._read = () => {
				if (paused && oldStream.resume) {
					paused = false;
					oldStream.resume();
				}
			};
			return this;
		}

		[Symbol.asyncIterator]() {
			return createAsyncIterator(this);
		}

		iterator(options) {
			return createAsyncIterator(this, options);
		}
	}

	function nextHighWaterMark(n) {
		if (n >= 0x40000000) throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'highWaterMark is too large');
		let v = n - 1;
		v |= v >>> 1;
		v |= v >>> 2;
		v |= v >>> 4;
		v |= v >>> 8;
		v |= v >>> 16;
		return v + 1;
	}

	function howMuchToRead(n, s) {
		if (n <= 0 || (s.length === 0 && s.ended)) return 0;
		if (s.objectMode) return 1;
		if (Number.isNaN(n)) {
			if (s.flowing && s.length) return s.buffer[0].length;
			return s.length;
		}
		if (n <= s.length) return n;
		return s.ended ? s.length : 0;
	}

	function takeFromBuffer(n, s) {
		if (s.length === 0) return null;
		let ret;
		if (s.objectMode) {
			ret = s.buffer.shift();
			s.length -= 1;
			return ret;
		}
		if (n >= s.length) {
			if (s.decoder) ret = s.buffer.join('');
			else if (s.buffer.length === 1) ret = s.buffer[0];
			else ret = Buffer.concat(s.buffer, s.length);
			s.buffer = [];
			s.length = 0;
			return ret;
		}
		const parts = [];
		let need = n;
		while (need > 0) {
			const head = s.buffer[0];
			if (head.length <= need) {
				parts.push(head);
				need -= head.length;
				s.buffer.shift();
			} else {
				parts.push(head.slice(0, need));
				s.buffer[0] = head.slice(need);
				need = 0;
			}
		}
		s.length -= n;
		if (s.decoder) return parts.join('');
		return parts.length === 1 ? parts[0] : Buffer.concat(parts, n);
	}

	function addChunk(stream, chunk, encoding, addToFront) {
		const s = stream._readableState;
		let err;
		if (!s.objectMode) {
			if (typeof chunk === 'string') {
				encoding = encoding || s.defaultEncoding;
				if (s.encoding !== encoding) {
					if (addToFront && s.encoding) {
						chunk = Buffer.from(chunk, encoding).toString(s.encoding);
					} else {
						chunk = Buffer.from(chunk, encoding);
						encoding = '';
					}
				}
			} else if (chunk instanceof Buffer) {
				encoding = '';
			} else if (isUint8Array(chunk)) {
				chunk = Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
				encoding = '';
			} else if (chunk !== null && chunk !== undefined) {
				err = codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
					'The "chunk" argument must be of type string or an instance of Buffer or Uint8Array');
			}
		}

		if (err) {
			errorOrDestroy(stream, err);
		} else if (chunk === null) {
			s.reading = false;
			onEof(stream, s);
		} else if (s.objectMode || (chunk && chunk.length > 0)) {
			if (addToFront) {
				if (s.endEmitted) {
					errorOrDestroy(stream, codedError(Error, 'ERR_STREAM_UNSHIFT_AFTER_END_EVENT', 'stream.unshift() after end event'));
				} else if (!s.destroyed && !s.errored) {
					insertChunk(stream, s, chunk, true);
				}
			} else if (s.ended) {
				errorOrDestroy(stream, codedError(Error, 'ERR_STREAM_PUSH_AFTER_EOF', 'stream.push() after EOF'));
			} else if (s.destroyed || s.errored) {
				return false;
			} else {
				s.reading = false;
				if (s.decoder && !encoding) {
					chunk = s.decoder.write(chunk);
					if (s.objectMode || chunk.length !== 0) insertChunk(stream, s, chunk, false);
					else maybeReadMore(stream, s);
				} else {
					insertChunk(stream, s, chunk, false);
				}
			}
		} else if (!addToFront) {
			s.reading = false;
			maybeReadMore(stream, s);
		}

		return !s.ended && (s.length < s.highWaterMark || s.length === 0);
	}

	function insertChunk(stream, s, chunk, addToFront) {
		if (s.flowing && s.length === 0 && !s.sync && stream.listenerCount('data') > 0) {
			if (s.multiAwaitDrain) s.awaitDrainWriters.clear();
			else s.awaitDrainWriters = null;
			s.dataEmitted = true;
			stream.emit('data', chunk);
		} else {
			s.length += s.objectMode ? 1 : chunk.length;
			if (addToFront) s.buffer.unshift(chunk);
			else s.buffer.push(chunk);
			if (s.needReadable) emitReadable(stream);
		}
		maybeReadMore(stream, s);
	}

	function onEof(stream, s) {
		if (s.ended) return;
		if (s.decoder) {
			const rest = s.decoder.end();
			if (rest && rest.length) {
				s.buffer.push(rest);
				s.length += s.objectMode ? 1 : rest.length;
			}
		}
		s.ended = true;
		if (s.sync) {
			emitReadable(stream);
		} else {
			s.needReadable = false;
			s.emittedReadable = true;
			emitReadable_(stream);
		}
	}

	function emitReadable(stream) {
		const s = stream._readableState;
		s.needReadable = false;
		if (!s.emittedReadable) {
			s.emittedReadable = true;
			process.nextTick(() => emitReadable_(stream));
		}
	}

	function emitReadable_(stream) {
		const s = stream._readableState;
		if (!s.destroyed && !s.errored && (s.length || s.ended)) {
			stream.emit('readable');
			s.emittedReadable = false;
		}
		s.needReadable = !s.flowing && !s.ended && s.length <= s.highWaterMark;
		flow(stream);
	}

	function maybeReadMore(stream, s) {
		if (!s.readingMore) {
			s.readingMore = true;
			process.nextTick(() => {
				while (!s.reading && !s.ended &&
					(s.length < s.highWaterMark || (s.flowing && s.length === 0))) {
					const len = s.length;
					stream.read(0);
					if (len === s.length) break;
				}
				s.readingMore = false;
			});
		}
	}

	function pipeOnDrain(src, dest) {
		return function() {
			const s = src._readableState;
			if (s.awaitDrainWriters === dest) {
				s.awaitDrainWriters = null;
			} else if (s.multiAwaitDrain) {
				s.awaitDrainWriters.delete(dest);
			}
			if ((!s.awaitDrainWriters || s.awaitDrainWriters.size === 0) && src.listenerCount('data')) {
				src.resume();
			}
		};
	}

	function updateReadableListening(stream) {
		const s = stream._readableState;
		s.readableListening = stream.listenerCount('readable') > 0;
		if (s.resumeScheduled && s.paused === false) {
			s.flowing = true;
		} else if (stream.listenerCount('data') > 0) {
			stream.resume();
		} else if (!s.readableListening) {
			s.flowing = null;
		}
	}

	function flow(stream) {
		const s = stream._readableState;
		while (s.flowing && stream.read() !== null);
	}

	function endReadable(stream) {
		const s = stream._readableState;
		if (!s.endEmitted) {
			s.ended = true;
			process.nextTick(() => {
				if (!s.errored && !s.closeEmitted && !s.endEmitted && s.length === 0) {
					s.endEmitted = true;
					stream.emit('end');
					const w = stream._writableState;
					if (w && stream.allowHalfOpen === false && !w.ending) {
						stream.end();
					} else {
						autoDestroyIfDone(stream);
					}
				}
			});
		}
	}

	function createAsyncIterator(stream, options) {
		let wake = null;
		const onReadable = () => {
			if (wake) {
				const w = wake;
				wake = null;
				w();
			}
		};
		stream.on('readable', onReadable);
		let error;
		let done = false;
		const cleanup = finished(stream, { writable: false }, (err) => {
			error = err || null;
			done = true;
			onReadable();
		});
		const destroyOnReturn = !options || options.destroyOnReturn !== false;

		const iterator = {
			async next() {
				while (true) {
					const chunk = stream.destroyed ? null : stream.read();
					if (chunk !== null) return { value: chunk, done: false };
					if (error) throw error;
					if (done) {
						cleanup();
						return { value: undefined, done: true };
					}
					await new Promise((resolve) => { wake = resolve; });
				}
			},
			async return(value) {
				cleanup();
				stream.removeListener('readable', onReadable);
				if (destroyOnReturn && !stream.destroyed) stream.destroy();
				return { value, done: true };
			},
			async throw(err) {
				cleanup();
				stream.destroy(err);
				throw err;
			},
			[Symbol.asyncIterator]() {
				return iterator;
			}
		};
		return iterator;
	}

	Readable.from = function(iterable, options) {
		if (typeof iterable === 'string' || iterable instanceof Buffer) {
			const single = iterable;
			iterable = [single];
			options = { objectMode: false, ...options };
		}
		const iter = iterable[Symbol.asyncIterator] ? iterable[Symbol.asyncIterator]()
			: iterable[Symbol.iterator] ? iterable[Symbol.iterator]() : null;
		if (!iter) {
			throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "iterable" argument must be an instance of Iterable');
		}
		const readable = new Readable({ objectMode: true, highWaterMark: 1, ...options });
		let reading = false;
		readable._read = function() {
			if (reading) return;
			reading = true;
			next();
		};
		readable._destroy = function(err, cb) {
			const finish = () => cb(err);
			if (iter.return) Promise.resolve(iter.return()).then(finish, finish);
			else finish();
		};
		async function next() {
			while (true) {
				try {
					const { value, done } = await iter.next();
					if (done) {
						readable.push(null);
						return;
					}
					const chunk = value && typeof value.then === 'function' ? await value : value;
					if (chunk === null) {
						reading = false;
						throw codedError(TypeError, 'ERR_STREAM_NULL_VALUES', 'May not write null values to stream');
					}
					if (!readable.push(chunk)) {
						reading = false;
						return;
					}
				} catch (err) {
					readable.destroy(err);
					return;
				}
			}
		}
		return readable;
	};

	class WritableState {
		constructor(options) {
			options = options || {};
			this.objectMode = !!(options.objectMode || options.writableObjectMode);
			this.highWaterMark = highWaterMarkFrom(options, 'writableHighWaterMark', this.objectMode);
			this.decodeStrings = options.decodeStrings !== false;
			this.defaultEncoding = options.defaultEncoding || 'utf8';
			this.buffer = [];
			this.length = 0;
			this.writing = false;
			this.sync = true;
			this.corked = 0;
			this.bufferProcessing = false;
			this.needDrain = false;
			this.ending = false;
			this.ended = false;
			this.finished = false;
			this.finalCalled = false;
			this.prefinished = false;
			this.finishCallbacks = [];
			this.pendingCallbacks = 0;
			this.errored = null;
			this.errorEmitted = false;
			this.closed = false;
			this.closeEmitted = false;
			this.destroyed = false;
			this.autoDestroy = options.autoDestroy !== false;
			this.emitClose = options.emitClose !== false;
		}
	}

	function initWritable(stream, options) {
		stream._writableState = new WritableState(options);
		if (options) {
			if (typeof options.write === 'function') stream._write = options.write;
			if (typeof options.writev === 'function') stream._writev = options.writev;
			if (typeof options.final === 'function') stream._final = options.final;
			if (typeof options.destroy === 'function') stream._destroy = options.destroy;
		}
	}

	const writableMethods = {
		_write(chunk, encoding, cb) {
			if (this._writev) {
				this._writev([{ chunk, encoding }], cb);
				return;
			}
			throw codedError(Error, 'ERR_METHOD_NOT_IMPLEMENTED', 'The _write() method is not implemented');
		},

		write(chunk, encoding, cb) {
			const s = this._writableState;
			if (typeof encoding === 'function') {
				cb = encoding;
				encoding = null;
			}
			if (!encoding) encoding = s.defaultEncoding;
			if (typeof cb !== 'function') cb = null;

			if (chunk === null) {
				throw codedError(TypeError, 'ERR_STREAM_NULL_VALUES', 'May not write null values to stream');
			}
			if (!s.objectMode) {
				if (typeof chunk === 'string') {
					if (s.decodeStrings) {
						chunk = Buffer.from(chunk, encoding);
						encoding = 'buffer';
					}
				} else if (chunk instanceof Buffer) {
					encoding = 'buffer';
				} else if (isUint8Array(chunk)) {
					chunk = Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
					encoding = 'buffer';
				} else {
					throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
						'The "chunk" argument must be of type string or an instance of Buffer or Uint8Array');
				}
			}

			let err;
			if (s.ending) err = codedError(Error, 'ERR_STREAM_WRITE_AFTER_END', 'write after end');
			else if (s.destroyed) err = codedError(Error, 'ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed');
			if (err) {
				if (cb) process.nextTick(cb, err);
				errorOrDestroy(this, err);
				return false;
			}

			s.pendingCallbacks++;
			const len = s.objectMode ? 1 : chunk.length;
			s.length += len;
			const ret = s.length < s.highWaterMark;
			if (!ret) s.needDrain = true;

			if (s.writing || s.corked || s.errored) {
				s.buffer.push({ chunk, encoding, cb });
			} else {
				doWrite(this, s, chunk, encoding, len, cb);
			}
			return ret && !s.errored && !s.destroyed;
		},

		cork() {
			this._writableState.corked++;
		},

		uncork() {
			const s = this._writableState;
			if (s.corked) {
				s.corked--;
				if (!s.writing) clearBuffer(this, s);
			}
		},

		setDefaultEncoding(encoding) {
			if (typeof encoding === 'string') encoding = encoding.toLowerCase();
			this._writableState.defaultEncoding = encoding;
			return this;
		},

		end(chunk, encoding, cb) {
			const s = this._writableState;
			if (typeof chunk === 'function') {
				cb = chunk;
				chunk = null;
				encoding = null;
			} else if (typeof encoding === 'function') {
				cb = encoding;
				encoding = null;
			}

			let err;
			if (chunk !== null && chunk !== undefined) {
				this.write(chunk, encoding);
			}
			if (s.corked) {
				s.corked = 1;
				this.uncork();
			}

			if (!s.errored && !s.ending) {
				s.ending = true;
				finishMaybe(this, s, true);
				s.ended = true;
			} else if (s.finished) {
				err = codedError(Error, 'ERR_STREAM_ALREADY_FINISHED', 'Cannot call end after a stream was finished');
			} else if (s.destroyed) {
				err = codedError(Error, 'ERR_STREAM_DESTROYED', 'Cannot call end after a stream was destroyed');
			}

			if (typeof cb === 'function') {
				if (err) process.nextTick(cb, err);
				else if (s.finished) process.nextTick(cb);
				else s.finishCallbacks.push(cb);
			}
			return this;
		}
	};

	const writableAccessors = {
		writable: {
			get() {
				const s = this._writableState;
				return !!s && s.writable !== false && !s.destroyed && !s.errored && !s.ending && !s.ended;
			},
			set(v) {
				if (this._writableState) this._writableState.writable = !!v;
			}
		},
		writableFinished: { get() { return this._writableState.finished; } },
		writableEnded: { get() { return this._writableState.ending; } },
		writableLength: { get() { return this._writableState.length; } },
		writableHighWaterMark: { get() { return this._writableState.highWaterMark; } },
		writableObjectMode: { get() { return this._writableState.objectMode; } },
		writableCorked: { get() { return this._writableState.corked; } },
		writableNeedDrain: {
			get() {
				const s = this._writableState;
				return !s.destroyed && !s.ending && s.needDrain;
			}
		},
		writableBuffer: { get() { return this._writableState.buffer.slice(); } },
		writableAborted: {
			get() {
				const s = this._writableState;
				return (s.destroyed || !!s.errored) && !s.finished;
			}
		}
	};

	function doWrite(stream, s, chunk, encoding, len, cb) {
		s.writing = true;
		s.sync = true;
		s.writeLen = len;
		let called = false;
		const onwrite = (err) => {
			if (called) {
				errorOrDestroy(stream, codedError(Error, 'ERR_MULTIPLE_CALLBACK', 'Callback called multiple times'));
				return;
			}
			called = true;
			onWrite(stream, s, err, cb);
		};
		try {
			if (s.destroyed) onwrite(codedError(Error, 'ERR_STREAM_DESTROYED', 'write'));
			else stream._write(chunk, encoding, onwrite);
		} catch (err) {
			if (!called) onwrite(err);
			else throw err;
		}
		s.sync = false;
	}

	function onWrite(stream, s, err, cb) {
		const sync = s.sync;
		s.writing = false;
		s.length -= s.writeLen;
		s.writeLen = 0;

		if (err) {
			if (!s.errored) s.errored = err;
			if (stream._readableState && !stream._readableState.errored) {
				stream._readableState.errored = err;
			}
			s.pendingCallbacks--;
			const fail = () => {
				if (cb) cb(err);
				errorBuffer(s, err);
				errorOrDestroy(stream, err);
			};
			if (sync) process.nextTick(fail);
			else fail();
			return;
		}

		if (s.buffer.length && !s.corked && !s.destroyed) clearBuffer(stream, s);
		if (sync) process.nextTick(afterWrite, stream, s, cb);
		else afterWrite(stream, s, cb);
	}

	function afterWrite(stream, s, cb) {
		const needDrain = !s.ending && !s.destroyed && s.length === 0 && s.needDrain;
		if (needDrain) {
			s.needDrain = false;
			stream.emit('drain');
		}
		s.pendingCallbacks--;
		if (cb) cb(null);
		finishMaybe(stream, s);
	}

	function errorBuffer(s, err) {
		const pending = s.buffer.splice(0);
		for (const entry of pending) {
			s.length -= s.objectMode ? 1 : entry.chunk.length;
			s.pendingCallbacks--;
			if (entry.cb) entry.cb(err || codedError(Error, 'ERR_STREAM_DESTROYED', 'write'));
		}
		for (const cb of s.finishCallbacks.splice(0)) {
			cb(err || codedError(Error, 'ERR_STREAM_DESTROYED', 'end'));
		}
	}

	function clearBuffer(stream, s) {
		// A write that finishes synchronously comes back here through onWrite. The loop
		// below carries on from where it left off, so a long buffer does not recurse.
		if (s.bufferProcessing) return;
		s.bufferProcessing = true;
		try {
			while (s.buffer.length && !s.writing && !s.corked && !s.destroyed) {
				if (s.buffer.length > 1 && typeof stream._writev === 'function') {
					doWritev(stream, s);
				} else {
					const { chunk, encoding, cb } = s.buffer.shift();
					doWrite(stream, s, chunk, encoding, s.objectMode ? 1 : chunk.length, cb);
				}
			}
		} finally {
			s.bufferProcessing = false;
		}
	}

	function doWritev(stream, s) {
		const entries = s.buffer.splice(0);
		let total = 0;
		for (const e of entries) total += s.objectMode ? 1 : e.chunk.length;
		s.pendingCallbacks -= entries.length - 1;
		const finishAll = (err) => {
			for (const e of entries) if (e.cb) e.cb(err || null);
		};
		s.writing = true;
		s.sync = true;
		s.writeLen = total;
		let called = false;
		try {
			stream._writev(entries, (err) => {
				if (called) return;
				called = true;
				onWrite(stream, s, err, finishAll);
			});
		} catch (err) {
			if (!called) {
				called = true;
				onWrite(stream, s, err, finishAll);
			}
		}
		s.sync = false;
	}

	function needFinish(s) {
		return s.ending && !s.destroyed && s.length === 0 && !s.errored &&
			s.buffer.length === 0 && !s.finished && !s.writing && !s.errorEmitted && !s.closeEmitted;
	}

	function prefinish(stream, s) {
		if (s.prefinished || s.finalCalled) return;
		if (typeof stream._final === 'function' && !s.destroyed) {
			s.finalCalled = true;
			s.pendingCallbacks++;
			let called = false;
			const done = (err) => {
				if (called) return;
				called = true;
				s.pendingCallbacks--;
				if (err) {
					for (const cb of s.finishCallbacks.splice(0)) cb(err);
					errorOrDestroy(stream, err);
				} else if (needFinish(s)) {
					s.prefinished = true;
					stream.emit('prefinish');
					s.pendingCallbacks++;
					process.nextTick(finish, stream, s);
				}
			};
			s.sync = true;
			try {
				const result = stream._final(done);
				if (result && typeof result.then === 'function') {
					result.then(() => done(), done);
				}
			} catch (err) {
				done(err);
			}
			s.sync = false;
		} else {
			s.prefinished = true;
			stream.emit('prefinish');
		}
	}

	function finishMaybe(stream, s, sync) {
		if (!needFinish(s)) return;
		prefinish(stream, s);
		if (s.pendingCallbacks === 0 && needFinish(s)) {
			s.pendingCallbacks++;
			if (sync) {
				process.nextTick(() => {
					if (needFinish(s)) finish(stream, s);
					else s.pendingCallbacks--;
				});
			} else if (needFinish(s)) {
				finish(stream, s);
			} else {
				s.pendingCallbacks--;
			}
		}
	}

	function finish(stream, s) {
		s.pendingCallbacks--;
		s.finished = true;
		for (const cb of s.finishCallbacks.splice(0)) cb(null);
		stream.emit('finish');
		autoDestroyIfDone(stream);
	}

	function hasWritableMark(object) {
		return !!object && !!object._writableState && object._writableState instanceof WritableState;
	}

	class Writable extends Stream {
		constructor(options) {
			super(options);
			initWritable(this, options);
			if (options && options.signal) addAbortSignal(options.signal, this);
		}

		// A Duplex also counts as a Writable, as in Node.
		static [Symbol.hasInstance](object) {
			if (Function.prototype[Symbol.hasInstance].call(this, object)) return true;
			if (this !== Writable) return false;
			return hasWritableMark(object);
		}

		get destroyed() { return this._writableState.destroyed; }
		set destroyed(v) { this._writableState.destroyed = v; }
		get closed() { return this._writableState.closed; }
		get errored() { return this._writableState.errored; }

		_destroy(err, cb) {
			cb(err);
		}

		destroy(err, cb) {
			const s = this._writableState;
			const result = destroyStream(this, err, cb);
			errorBuffer(s, err);
			return result;
		}
	}
	Object.assign(Writable.prototype, writableMethods);
	Object.defineProperties(Writable.prototype, writableAccessors);

	class Duplex extends Readable {
		constructor(options) {
			super(options);
			initWritable(this, options);
			this.allowHalfOpen = !options || options.allowHalfOpen !== false;
			if (options) {
				if (options.readable === false) {
					this._readableState.ended = true;
					this._readableState.endEmitted = true;
				}
				if (options.writable === false) {
					const w = this._writableState;
					w.ending = true;
					w.ended = true;
					w.finished = true;
				}
			}
		}

		destroy(err, cb) {
			const s = this._writableState;
			const result = destroyStream(this, err, cb);
			errorBuffer(s, err);
			return result;
		}
	}
	Object.assign(Duplex.prototype, writableMethods);
	Object.defineProperties(Duplex.prototype, writableAccessors);

	class Transform extends Duplex {
		constructor(options) {
			super(options);
			// Each side buffers independently, and the readable side should not hide
			// backpressure behind a large buffer of its own.
			this._readableState.sync = false;
			this._transformCallback = null;
			if (options) {
				if (typeof options.transform === 'function') this._transform = options.transform;
				if (typeof options.flush === 'function') this._flush = options.flush;
			}
		}

		_transform() {
			throw codedError(Error, 'ERR_METHOD_NOT_IMPLEMENTED', 'The _transform() method is not implemented');
		}

		_final(cb) {
			const finishRead = (err, data) => {
				if (err) {
					cb(err);
					return;
				}
				if (data !== null && data !== undefined) this.push(data);
				this.push(null);
				cb();
			};
			if (typeof this._flush === 'function' && !this.destroyed) {
				let called = false;
				const once = (err, data) => {
					if (called) return;
					called = true;
					finishRead(err, data);
				};
				const result = this._flush(once);
				if (result && typeof result.then === 'function') {
					result.then((v) => once(null, v), once);
				}
			} else {
				finishRead();
			}
		}

		_write(chunk, encoding, callback) {
			const r = this._readableState;
			const w = this._writableState;
			const before = r.length;
			this._transform(chunk, encoding, (err, data) => {
				if (err) {
					callback(err);
					return;
				}
				if (data !== null && data !== undefined) this.push(data);
				if (w.ended || before === r.length || r.length < r.highWaterMark) {
					callback();
				} else {
					this._transformCallback = callback;
				}
			});
		}

		_read() {
			if (this._transformCallback) {
				const callback = this._transformCallback;
				this._transformCallback = null;
				callback();
			}
		}
	}

	class PassThrough extends Transform {
		_transform(chunk, encoding, callback) {
			callback(null, chunk);
		}
	}

	function isReadableNodeStream(stream) {
		return !!stream && typeof stream.pipe === 'function' && typeof stream.on === 'function';
	}

	// finished calls back once when the stream is done, or has failed or closed early.
	function finished(stream, options, callback) {
		if (typeof options === 'function') {
			callback = options;
			options = {};
		}
		options = options || {};

		const rs = stream._readableState;
		const ws = stream._writableState;
		const wantReadable = options.readable !== undefined ? options.readable : !!rs || stream.readable === true;
		const wantWritable = options.writable !== undefined ? options.writable : !!ws || stream.writable === true;

		let readableDone = !wantReadable || (rs ? rs.endEmitted : false);
		let writableDone = !wantWritable || (ws ? ws.finished : false);
		let called = false;
		let cleanup;
		const done = (err) => {
			if (called) return;
			called = true;
			cleanup();
			callback.call(stream, err);
		};

		const onend = () => {
			readableDone = true;
			if (writableDone) done();
		};
		const onfinish = () => {
			writableDone = true;
			if (readableDone) done();
		};
		const onerror = (err) => done(err);
		const onclose = () => {
			const err = (rs && rs.errored) || (ws && ws.errored);
			if (err) {
				done(err);
				return;
			}
			if ((wantReadable && !readableDone && !(rs && rs.ended && rs.length === 0 && rs.endEmitted)) ||
				(wantWritable && !writableDone)) {
				done(codedError(Error, 'ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
				return;
			}
			done();
		};
		const onrequest = () => {
			if (stream.req) stream.req.on('finish', onfinish);
		};

		cleanup = () => {
			stream.removeListener('end', onend);
			stream.removeListener('finish', onfinish);
			stream.removeListener('error', onerror);
			stream.removeListener('close', onclose);
			stream.removeListener('aborted', onclose);
			if (options.signal) options.signal.removeEventListener('abort', onabort);
		};
		const onabort = () => {
			const err = codedError(Error, 'ABORT_ERR', 'The operation was aborted');
			err.name = 'AbortError';
			done(err);
		};

		stream.on('end', onend);
		stream.on('finish', onfinish);
		if (options.error !== false) stream.on('error', onerror);
		stream.on('close', onclose);
		stream.on('aborted', onclose);
		if (stream.setHeader && stream.abort) onrequest();
		if (options.signal) {
			if (options.signal.aborted) process.nextTick(onabort);
			else options.signal.addEventListener('abort', onabort, { once: true });
		}

		const closedAlready = (rs && rs.closed) || (ws && ws.closed);
		if (closedAlready) {
			process.nextTick(onclose);
		} else if (readableDone && writableDone) {
			process.nextTick(() => done());
		}
		return cleanup;
	}

	// pipeline connects streams and destroys them all when any of them fails.
	function pipeline(...args) {
		let callback = args.length && typeof args[args.length - 1] === 'function' ? args.pop() : null;
		if (!callback) {
			throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function');
		}
		let parts = args.length === 1 && Array.isArray(args[0]) ? args[0] : args;
		if (parts.length < 2) {
			throw codedError(TypeError, 'ERR_MISSING_ARGS', 'The "streams" argument must be specified');
		}

		parts = parts.map((part, i) => {
			if (i === 0 && !isReadableNodeStream(part) && part &&
				(part[Symbol.asyncIterator] || part[Symbol.iterator])) {
				return Readable.from(part);
			}
			return part;
		});

		let error = null;
		let pending = parts.length;
		const cleanups = [];
		const destroyAll = (err) => {
			for (const stream of parts) {
				if (!stream.destroyed && typeof stream.destroy === 'function') stream.destroy(err);
			}
		};
		parts.forEach((stream, i) => {
			const isLast = i === parts.length - 1;
			const isFirst = i === 0;
			cleanups.push(finished(stream, {
				readable: !isLast,
				writable: !isFirst
			}, (err) => {
				if (err && err.code === 'ERR_STREAM_PREMATURE_CLOSE' && error) err = null;
				if (err && !error) {
					error = err;
					destroyAll(err);
				}
				pending--;
				if (pending === 0 || isLast) {
					const cb = callback;
					callback = null;
					if (cb) {
						for (const c of cleanups) c();
						process.nextTick(cb, error || undefined);
					}
				}
			}));
		});

		let current = parts[0];
		for (let i = 1; i < parts.length; i++) current = current.pipe(parts[i]);
		return parts[parts.length - 1];
	}

	function addAbortSignal(signal, stream) {
		const onabort = () => {
			const err = codedError(Error, 'ABORT_ERR', 'The operation was aborted');
			err.name = 'AbortError';
			err.cause = signal.reason;
			stream.destroy(err);
		};
		if (signal.aborted) {
			process.nextTick(onabort);
		} else {
			signal.addEventListener('abort', onabort, { once: true });
			finished(stream, () => signal.removeEventListener('abort', onabort));
		}
		return stream;
	}

	// Match Node's module shape: `require('stream')` IS the legacy `Stream`
	// constructor, with the other stream classes attached as properties. Real
	// packages rely on two things Node's `Stream` provides that an ES6 `class`
	// does not:
	//   1. `util.inherits(X, require('stream'))` — needs a constructor with a
	//      prototype (e.g. `send`).
	//   2. `Stream.call(this)` — the classic ES5 super-constructor call used by
	//      packages like `superagent` (`function Request(){ Stream.call(this) }`).
	//      An ES6 class throws "Class constructor cannot be invoked without 'new'",
	//      so the export must be a plain function callable without `new`.
	// So expose a callable ES5 shim whose prototype is the class hierarchy's
	// prototype (subclasses/instanceof keep working) and whose body initializes the
	// EventEmitter fields inline (the base EventEmitter is itself a class and can't
	// be `.call()`-ed). `new StreamCtor()`, `StreamCtor.call(obj)`, and
	// `class X extends Stream` all keep working.
	function StreamCtor(options) {
		if (this._events === undefined || this._events === null) this._events = {};
		if (this._maxListeners === undefined) this._maxListeners = undefined;
		this._options = options || {};
		return this;
	}
	StreamCtor.prototype = Stream.prototype;

	StreamCtor.Stream = StreamCtor;
	StreamCtor.Readable = Readable;
	StreamCtor.Writable = Writable;
	StreamCtor.Duplex = Duplex;
	StreamCtor.Transform = Transform;
	StreamCtor.PassThrough = PassThrough;
	StreamCtor.finished = finished;
	StreamCtor.pipeline = pipeline;
	StreamCtor.addAbortSignal = addAbortSignal;
	StreamCtor.getDefaultHighWaterMark = (objectMode) => (objectMode ? 16 : kDefaultHighWaterMark);
	StreamCtor.isErrored = (s) => !!(s && ((s._readableState && s._readableState.errored) || (s._writableState && s._writableState.errored)));
	StreamCtor.isDisturbed = (s) => !!(s && s._readableState && (s._readableState.dataEmitted || s._readableState.reading));
	return StreamCtor;
})()
