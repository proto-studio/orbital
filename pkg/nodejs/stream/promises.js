// stream/promises - Promise-based stream utilities
(function(global) {
  'use strict';

  const streamPromises = {
    /**
     * A utility for chaining streams together via pipe and returning a promise.
     * @param {...stream} streams - Streams to pipe together
     * @returns {Promise}
     */
    pipeline: function(...streams) {
      return new Promise((resolve, reject) => {
        const last = streams[streams.length - 1];
        let signal;
        if (last && typeof last.pipe !== 'function' && typeof last !== 'function' &&
            !last[Symbol.asyncIterator] && !last[Symbol.iterator]) {
          signal = streams.pop().signal;
        }
        if (signal) {
          const source = streams.length === 1 && Array.isArray(streams[0]) ? streams[0][0] : streams[0];
          if (source && source.destroy) global.__stream_module.addAbortSignal(signal, source);
        }
        global.__stream_module.pipeline(...streams, (err) => {
          if (err) reject(err);
          else resolve();
        });
      });
    },

    /**
     * Consumes a readable stream and returns a promise that resolves
     * to the complete content as a Buffer or string.
     * @param {Readable} stream - The readable stream
     * @param {object} options - Options
     * @returns {Promise<Buffer|string>}
     */
    finished: function(stream, options) {
      return new Promise((resolve, reject) => {
        global.__stream_module.finished(stream, options || {}, (err) => {
          if (err) reject(err);
          else resolve();
        });
      });
    },

    /**
     * Collects all data from a readable stream.
     * @param {Readable} stream - The readable stream
     * @returns {Promise<Buffer>}
     */
    collect: async function(stream) {
      const chunks = [];
      
      return new Promise((resolve, reject) => {
        const onData = (chunk) => {
          chunks.push(chunk);
        };

        const onEnd = () => {
          cleanup();
          // Combine chunks
          if (chunks.length === 0) {
            resolve(new Uint8Array(0));
            return;
          }
          
          // Calculate total length
          let totalLength = 0;
          for (const chunk of chunks) {
            totalLength += chunk.length || chunk.byteLength || 0;
          }
          
          // Create combined buffer
          const result = new Uint8Array(totalLength);
          let offset = 0;
          for (const chunk of chunks) {
            const bytes = typeof chunk === 'string' 
              ? new TextEncoder().encode(chunk)
              : new Uint8Array(chunk.buffer || chunk);
            result.set(bytes, offset);
            offset += bytes.length;
          }
          
          resolve(result);
        };

        const onError = (err) => {
          cleanup();
          reject(err);
        };

        const cleanup = () => {
          if (stream.removeListener) {
            stream.removeListener('data', onData);
            stream.removeListener('end', onEnd);
            stream.removeListener('error', onError);
          }
        };

        if (stream.on) {
          stream.on('data', onData);
          stream.on('end', onEnd);
          stream.on('error', onError);
        } else {
          reject(new Error('Not a readable stream'));
        }
      });
    },

    /**
     * Creates a readable stream from an async iterable.
     * @param {AsyncIterable} iterable - The async iterable
     * @returns {Readable}
     */
    Readable: {
      from: function(iterable, options) {
        const Stream = global.__stream_module;
        if (!Stream || !Stream.Readable) {
          throw new Error('Stream module not available');
        }

        const readable = new Stream.Readable({
          read() {},
          ...options
        });

        (async () => {
          try {
            for await (const chunk of iterable) {
              readable.push(chunk);
            }
            readable.push(null);
          } catch (err) {
            readable.destroy(err);
          }
        })();

        return readable;
      },

      /**
       * Convert a readable stream to an async iterator.
       */
      toWeb: function(readable) {
        // Return an async iterator
        return {
          [Symbol.asyncIterator]() {
            const chunks = [];
            let ended = false;
            let error = null;
            let resolveNext = null;

            readable.on('data', (chunk) => {
              if (resolveNext) {
                resolveNext({ done: false, value: chunk });
                resolveNext = null;
              } else {
                chunks.push(chunk);
              }
            });

            readable.on('end', () => {
              ended = true;
              if (resolveNext) {
                resolveNext({ done: true, value: undefined });
                resolveNext = null;
              }
            });

            readable.on('error', (err) => {
              error = err;
              if (resolveNext) {
                resolveNext = null;
              }
            });

            return {
              next() {
                if (error) {
                  return Promise.reject(error);
                }
                if (chunks.length > 0) {
                  return Promise.resolve({ done: false, value: chunks.shift() });
                }
                if (ended) {
                  return Promise.resolve({ done: true, value: undefined });
                }
                return new Promise((resolve) => {
                  resolveNext = resolve;
                });
              }
            };
          }
        };
      }
    }
  };

  // Export
  global.__stream_promises_module = streamPromises;

})(globalThis);
