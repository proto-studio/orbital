// stream/web - Web Streams API
(function(global) {
  'use strict';

  /**
   * ReadableStreamDefaultReader
   */
  class ReadableStreamDefaultReader {
    constructor(stream) {
      if (stream._reader) {
        throw new TypeError('ReadableStream is already locked');
      }
      this._stream = stream;
      stream._reader = this;
      this._closed = false;
      this._closedPromise = new Promise((resolve, reject) => {
        this._closedResolve = resolve;
        this._closedReject = reject;
      });
    }

    get closed() {
      return this._closedPromise;
    }

    async read() {
      if (this._closed) {
        return { value: undefined, done: true };
      }

      const chunk = await this._stream._pullChunk();
      if (chunk === null) {
        this._closed = true;
        this._closedResolve(undefined);
        return { value: undefined, done: true };
      }

      return { value: chunk, done: false };
    }

    releaseLock() {
      if (this._stream) {
        this._stream._reader = null;
        this._stream = null;
      }
    }

    cancel(reason) {
      if (this._stream) {
        return this._stream.cancel(reason);
      }
      return Promise.resolve();
    }
  }

  /**
   * ReadableStreamBYOBReader (Bring Your Own Buffer)
   */
  class ReadableStreamBYOBReader {
    constructor(stream) {
      if (stream._reader) {
        throw new TypeError('ReadableStream is already locked');
      }
      if (!stream._byobSupported) {
        throw new TypeError('ReadableStream does not support BYOB');
      }
      this._stream = stream;
      stream._reader = this;
      this._closed = false;
      this._closedPromise = new Promise((resolve, reject) => {
        this._closedResolve = resolve;
        this._closedReject = reject;
      });
    }

    get closed() {
      return this._closedPromise;
    }

    async read(view) {
      if (!(view instanceof ArrayBufferView)) {
        throw new TypeError('view must be an ArrayBufferView');
      }

      if (this._closed) {
        return { value: view, done: true };
      }

      const chunk = await this._stream._pullChunk();
      if (chunk === null) {
        this._closed = true;
        this._closedResolve(undefined);
        return { value: view, done: true };
      }

      // Copy chunk into view
      const bytes = new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
      const source = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk);
      const copyLength = Math.min(bytes.length, source.length);
      bytes.set(source.subarray(0, copyLength));

      return { value: new Uint8Array(view.buffer, view.byteOffset, copyLength), done: false };
    }

    releaseLock() {
      if (this._stream) {
        this._stream._reader = null;
        this._stream = null;
      }
    }

    cancel(reason) {
      if (this._stream) {
        return this._stream.cancel(reason);
      }
      return Promise.resolve();
    }
  }

  /**
   * ReadableStreamDefaultController
   */
  class ReadableStreamDefaultController {
    constructor(stream) {
      this._stream = stream;
      this._closeRequested = false;
    }

    get desiredSize() {
      return this._stream._highWaterMark - this._stream._queue.length;
    }

    enqueue(chunk) {
      if (this._closeRequested) {
        throw new TypeError('Cannot enqueue after close');
      }
      this._stream._queue.push(chunk);
      this._stream._resolveWaiting();
    }

    close() {
      if (this._closeRequested) {
        throw new TypeError('Cannot close twice');
      }
      this._closeRequested = true;
      this._stream._closeRequested = true;
      this._stream._resolveWaiting();
    }

    error(e) {
      this._stream._error = e;
      this._stream._errored = true;
      this._stream._resolveWaiting();
    }
  }

  /**
   * ReadableStream
   */
  class ReadableStream {
    constructor(underlyingSource, queuingStrategy) {
      this._queue = [];
      this._reader = null;
      this._controller = new ReadableStreamDefaultController(this);
      this._closeRequested = false;
      this._errored = false;
      this._error = null;
      this._highWaterMark = (queuingStrategy && queuingStrategy.highWaterMark) || 1;
      this._byobSupported = underlyingSource && underlyingSource.type === 'bytes';
      this._started = false;
      this._pulling = false;
      this._waitingResolve = null;

      this._underlyingSource = underlyingSource || {};

      // Start the source
      if (this._underlyingSource.start) {
        const result = this._underlyingSource.start(this._controller);
        if (result && typeof result.then === 'function') {
          result.then(() => { this._started = true; }).catch(e => this._controller.error(e));
        } else {
          this._started = true;
        }
      } else {
        this._started = true;
      }
    }

    get locked() {
      return this._reader !== null;
    }

    _resolveWaiting() {
      if (this._waitingResolve) {
        this._waitingResolve();
        this._waitingResolve = null;
      }
    }

    async _pullChunk() {
      // Wait for start
      while (!this._started && !this._errored) {
        await new Promise(r => setTimeout(r, 0));
      }

      if (this._errored) {
        throw this._error;
      }

      // If queue has chunks, return one
      if (this._queue.length > 0) {
        return this._queue.shift();
      }

      // If close requested and queue empty, return null
      if (this._closeRequested) {
        return null;
      }

      // Try to pull more
      if (this._underlyingSource.pull && !this._pulling) {
        this._pulling = true;
        try {
          const result = this._underlyingSource.pull(this._controller);
          if (result && typeof result.then === 'function') {
            await result;
          }
        } catch (e) {
          this._controller.error(e);
        } finally {
          this._pulling = false;
        }
      }

      // Wait for data or close
      if (this._queue.length === 0 && !this._closeRequested && !this._errored) {
        await new Promise(resolve => {
          this._waitingResolve = resolve;
        });
      }

      if (this._errored) {
        throw this._error;
      }

      if (this._queue.length > 0) {
        return this._queue.shift();
      }

      return null;
    }

    getReader(options) {
      if (options && options.mode === 'byob') {
        return new ReadableStreamBYOBReader(this);
      }
      return new ReadableStreamDefaultReader(this);
    }

    cancel(reason) {
      if (this._underlyingSource.cancel) {
        return Promise.resolve(this._underlyingSource.cancel(reason));
      }
      return Promise.resolve();
    }

    pipeThrough(transform, options) {
      const reader = this.getReader();
      const writer = transform.writable.getWriter();

      async function pump() {
        while (true) {
          const { value, done } = await reader.read();
          if (done) {
            writer.close();
            break;
          }
          await writer.write(value);
        }
      }

      pump().catch(e => writer.abort(e));
      return transform.readable;
    }

    pipeTo(dest, options) {
      const reader = this.getReader();
      const writer = dest.getWriter();

      return (async () => {
        while (true) {
          const { value, done } = await reader.read();
          if (done) {
            await writer.close();
            break;
          }
          await writer.write(value);
        }
      })();
    }

    tee() {
      const reader = this.getReader();
      let canceled1 = false;
      let canceled2 = false;
      let reason1;
      let reason2;
      let reading = false;
      let queue1 = [];
      let queue2 = [];

      const stream1 = new ReadableStream({
        pull: async (controller) => {
          if (queue1.length > 0) {
            controller.enqueue(queue1.shift());
            return;
          }

          if (!reading) {
            reading = true;
            const { value, done } = await reader.read();
            reading = false;

            if (done) {
              if (!canceled1) controller.close();
              return;
            }

            if (!canceled2) queue2.push(value);
            controller.enqueue(value);
          }
        },
        cancel: (reason) => {
          canceled1 = true;
          reason1 = reason;
          if (canceled2) {
            reader.cancel(reason);
          }
        }
      });

      const stream2 = new ReadableStream({
        pull: async (controller) => {
          if (queue2.length > 0) {
            controller.enqueue(queue2.shift());
            return;
          }

          if (!reading) {
            reading = true;
            const { value, done } = await reader.read();
            reading = false;

            if (done) {
              if (!canceled2) controller.close();
              return;
            }

            if (!canceled1) queue1.push(value);
            controller.enqueue(value);
          }
        },
        cancel: (reason) => {
          canceled2 = true;
          reason2 = reason;
          if (canceled1) {
            reader.cancel(reason);
          }
        }
      });

      return [stream1, stream2];
    }

    async *[Symbol.asyncIterator]() {
      const reader = this.getReader();
      try {
        while (true) {
          const { value, done } = await reader.read();
          if (done) return;
          yield value;
        }
      } finally {
        reader.releaseLock();
      }
    }

    static from(asyncIterable) {
      const iterator = asyncIterable[Symbol.asyncIterator] ? 
        asyncIterable[Symbol.asyncIterator]() :
        asyncIterable[Symbol.iterator]();

      return new ReadableStream({
        async pull(controller) {
          const { value, done } = await iterator.next();
          if (done) {
            controller.close();
          } else {
            controller.enqueue(value);
          }
        }
      });
    }
  }

  /**
   * WritableStreamDefaultWriter
   */
  class WritableStreamDefaultWriter {
    constructor(stream) {
      if (stream._writer) {
        throw new TypeError('WritableStream is already locked');
      }
      this._stream = stream;
      stream._writer = this;
      this._closedPromise = new Promise((resolve, reject) => {
        this._closedResolve = resolve;
        this._closedReject = reject;
      });
      this._readyPromise = Promise.resolve();
    }

    get closed() {
      return this._closedPromise;
    }

    get ready() {
      return this._readyPromise;
    }

    get desiredSize() {
      return this._stream._controller.desiredSize;
    }

    write(chunk) {
      return this._stream._writeChunk(chunk);
    }

    close() {
      return this._stream._close().then(() => {
        this._closedResolve(undefined);
      });
    }

    abort(reason) {
      return this._stream.abort(reason).then(() => {
        this._closedReject(reason);
      });
    }

    releaseLock() {
      if (this._stream) {
        this._stream._writer = null;
        this._stream = null;
      }
    }
  }

  /**
   * WritableStreamDefaultController
   */
  class WritableStreamDefaultController {
    constructor(stream) {
      this._stream = stream;
    }

    get signal() {
      return this._stream._signal;
    }

    error(e) {
      this._stream._error = e;
      this._stream._errored = true;
    }
  }

  /**
   * WritableStream
   */
  class WritableStream {
    constructor(underlyingSink, queuingStrategy) {
      this._writer = null;
      this._controller = new WritableStreamDefaultController(this);
      this._errored = false;
      this._error = null;
      this._closed = false;
      this._highWaterMark = (queuingStrategy && queuingStrategy.highWaterMark) || 1;
      this._signal = new AbortController().signal;

      this._underlyingSink = underlyingSink || {};

      // Start the sink
      if (this._underlyingSink.start) {
        this._underlyingSink.start(this._controller);
      }
    }

    get locked() {
      return this._writer !== null;
    }

    getWriter() {
      return new WritableStreamDefaultWriter(this);
    }

    abort(reason) {
      if (this._underlyingSink.abort) {
        return Promise.resolve(this._underlyingSink.abort(reason));
      }
      return Promise.resolve();
    }

    close() {
      const writer = this.getWriter();
      return writer.close().finally(() => writer.releaseLock());
    }

    _writeChunk(chunk) {
      if (this._errored) {
        return Promise.reject(this._error);
      }

      if (this._underlyingSink.write) {
        return Promise.resolve(this._underlyingSink.write(chunk, this._controller));
      }

      return Promise.resolve();
    }

    _close() {
      if (this._closed) {
        return Promise.resolve();
      }
      this._closed = true;

      if (this._underlyingSink.close) {
        return Promise.resolve(this._underlyingSink.close());
      }

      return Promise.resolve();
    }
  }

  /**
   * TransformStream
   */
  class TransformStream {
    constructor(transformer, writableStrategy, readableStrategy) {
      this._transformer = transformer || {};
      
      const transformController = {
        enqueue: (chunk) => {
          this._readableController.enqueue(chunk);
        },
        error: (e) => {
          this._readableController.error(e);
        },
        terminate: () => {
          this._readableController.close();
        }
      };

      const self = this;

      this._readable = new ReadableStream({
        start(controller) {
          self._readableController = controller;
        }
      }, readableStrategy);

      this._writable = new WritableStream({
        start(controller) {
          if (self._transformer.start) {
            self._transformer.start(transformController);
          }
        },
        write(chunk, controller) {
          if (self._transformer.transform) {
            return self._transformer.transform(chunk, transformController);
          }
          transformController.enqueue(chunk);
        },
        close() {
          if (self._transformer.flush) {
            return self._transformer.flush(transformController);
          }
          transformController.terminate();
        },
        abort(reason) {
          transformController.error(reason);
        }
      }, writableStrategy);
    }

    get readable() {
      return this._readable;
    }

    get writable() {
      return this._writable;
    }
  }

  /**
   * ByteLengthQueuingStrategy
   */
  class ByteLengthQueuingStrategy {
    constructor(options) {
      this.highWaterMark = options.highWaterMark;
    }

    size(chunk) {
      return chunk.byteLength;
    }
  }

  /**
   * CountQueuingStrategy
   */
  class CountQueuingStrategy {
    constructor(options) {
      this.highWaterMark = options.highWaterMark;
    }

    size() {
      return 1;
    }
  }

  // Export
  const webstream = {
    ReadableStream,
    ReadableStreamDefaultReader,
    ReadableStreamBYOBReader,
    WritableStream,
    WritableStreamDefaultWriter,
    TransformStream,
    ByteLengthQueuingStrategy,
    CountQueuingStrategy
  };

  global.__stream_web_module = webstream;

  // Also set as globals
  global.ReadableStream = ReadableStream;
  global.WritableStream = WritableStream;
  global.TransformStream = TransformStream;
  global.ByteLengthQueuingStrategy = ByteLengthQueuingStrategy;
  global.CountQueuingStrategy = CountQueuingStrategy;

})(globalThis);
