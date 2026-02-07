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
        if (streams.length < 2) {
          reject(new Error('pipeline requires at least 2 streams'));
          return;
        }

        const source = streams[0];
        const dest = streams[streams.length - 1];
        let error = null;

        // Handle errors on all streams
        const onError = (err) => {
          error = err;
          cleanup();
          reject(err);
        };

        const cleanup = () => {
          streams.forEach(stream => {
            if (stream.removeListener) {
              stream.removeListener('error', onError);
            }
          });
        };

        // Attach error handlers
        streams.forEach(stream => {
          if (stream.on) {
            stream.on('error', onError);
          }
        });

        // Pipe through all streams
        let current = source;
        for (let i = 1; i < streams.length; i++) {
          current = current.pipe(streams[i]);
        }

        // Wait for completion
        if (dest.on) {
          dest.on('finish', () => {
            if (!error) {
              cleanup();
              resolve(dest);
            }
          });

          dest.on('close', () => {
            if (!error) {
              cleanup();
              resolve(dest);
            }
          });
        } else {
          // Non-standard stream
          setImmediate(() => {
            cleanup();
            resolve(dest);
          });
        }
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
        const onFinish = () => {
          cleanup();
          resolve();
        };

        const onEnd = () => {
          cleanup();
          resolve();
        };

        const onError = (err) => {
          cleanup();
          reject(err);
        };

        const onClose = () => {
          cleanup();
          resolve();
        };

        const cleanup = () => {
          if (stream.removeListener) {
            stream.removeListener('finish', onFinish);
            stream.removeListener('end', onEnd);
            stream.removeListener('error', onError);
            stream.removeListener('close', onClose);
          }
        };

        if (stream.on) {
          stream.on('finish', onFinish);
          stream.on('end', onEnd);
          stream.on('error', onError);
          stream.on('close', onClose);
        } else {
          // Non-standard stream
          setImmediate(() => resolve());
        }
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
