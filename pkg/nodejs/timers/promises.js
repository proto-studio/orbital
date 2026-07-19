// timers/promises - Promise-based timer API
(function(global) {
  'use strict';

  const timersPromises = {
    /**
     * Returns a promise that resolves after the specified delay.
     * @param {number} delay - The delay in milliseconds
     * @param {*} value - The value to resolve with
     * @param {object} options - Options object
     * @param {AbortSignal} options.signal - An AbortSignal to cancel the timer
     * @returns {Promise}
     */
    setTimeout: function(delay, value, options) {
      return new Promise((resolve, reject) => {
        const signal = options && options.signal;
        
        if (signal && signal.aborted) {
          reject(new DOMException('The operation was aborted', 'AbortError'));
          return;
        }
        
        const timeoutId = global.setTimeout(() => {
          resolve(value);
        }, delay || 0);
        
        if (signal) {
          signal.addEventListener('abort', () => {
            global.clearTimeout(timeoutId);
            reject(new DOMException('The operation was aborted', 'AbortError'));
          }, { once: true });
        }
      });
    },

    /**
     * Returns an async iterator that yields at the specified interval.
     * @param {number} delay - The interval in milliseconds
     * @param {*} value - The value to yield
     * @param {object} options - Options object
     * @param {AbortSignal} options.signal - An AbortSignal to stop the iterator
     * @returns {AsyncIterable}
     */
    setInterval: function(delay, value, options) {
      const signal = options && options.signal;
      
      return {
        [Symbol.asyncIterator]() {
          let intervalId = null;
          let resolveNext = null;
          let stopped = false;
          
          const stop = () => {
            stopped = true;
            if (intervalId !== null) {
              global.clearInterval(intervalId);
              intervalId = null;
            }
          };
          
          if (signal) {
            if (signal.aborted) {
              stopped = true;
            } else {
              signal.addEventListener('abort', stop, { once: true });
            }
          }
          
          return {
            next() {
              if (stopped) {
                return Promise.resolve({ done: true, value: undefined });
              }
              
              return new Promise((resolve, reject) => {
                if (stopped) {
                  resolve({ done: true, value: undefined });
                  return;
                }
                
                if (intervalId === null) {
                  // Start the interval
                  intervalId = global.setInterval(() => {
                    if (resolveNext) {
                      resolveNext({ done: false, value: value });
                      resolveNext = null;
                    }
                  }, delay || 0);
                  
                  // First yield happens after delay
                  resolveNext = resolve;
                } else {
                  // Subsequent yields
                  resolveNext = resolve;
                }
              });
            },
            
            return() {
              stop();
              return Promise.resolve({ done: true, value: undefined });
            },
            
            throw(err) {
              stop();
              return Promise.reject(err);
            }
          };
        }
      };
    },

    /**
     * Returns a promise that resolves on the next iteration of the event loop.
     * @param {*} value - The value to resolve with
     * @param {object} options - Options object
     * @param {AbortSignal} options.signal - An AbortSignal to cancel
     * @returns {Promise}
     */
    setImmediate: function(value, options) {
      return new Promise((resolve, reject) => {
        const signal = options && options.signal;
        
        if (signal && signal.aborted) {
          reject(new DOMException('The operation was aborted', 'AbortError'));
          return;
        }
        
        const immediateId = global.setImmediate(() => {
          resolve(value);
        });
        
        if (signal) {
          signal.addEventListener('abort', () => {
            global.clearImmediate(immediateId);
            reject(new DOMException('The operation was aborted', 'AbortError'));
          }, { once: true });
        }
      });
    },

    /**
     * A scheduler object compatible with the Scheduler API proposal.
     */
    scheduler: {
      wait: function(delay, options) {
        return timersPromises.setTimeout(delay, undefined, options);
      },
      
      yield: function() {
        return timersPromises.setImmediate();
      }
    }
  };

  // Export
  global.__timers_promises_module = timersPromises;

})(globalThis);
