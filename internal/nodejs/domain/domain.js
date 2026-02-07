// domain - Deprecated error handling (DEPRECATED)
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;

  // Stack of active domains
  const domainStack = [];

  /**
   * Domain class - Error handling domain (DEPRECATED).
   */
  class Domain extends EventEmitter {
    constructor() {
      super();
      this.members = [];
      this._disposed = false;
    }

    /**
     * Add an emitter or timer to this domain.
     */
    add(emitter) {
      if (this._disposed) {
        throw new Error('Cannot add to a disposed domain');
      }

      // Remove from previous domain if any
      if (emitter.domain) {
        emitter.domain.remove(emitter);
      }

      emitter.domain = this;
      this.members.push(emitter);
    }

    /**
     * Remove an emitter from this domain.
     */
    remove(emitter) {
      const index = this.members.indexOf(emitter);
      if (index !== -1) {
        this.members.splice(index, 1);
        emitter.domain = null;
      }
    }

    /**
     * Bind a callback to this domain.
     */
    bind(callback) {
      if (typeof callback !== 'function') {
        throw new TypeError('callback must be a function');
      }

      const self = this;
      
      const bound = function(...args) {
        try {
          return callback.apply(this, args);
        } catch (err) {
          self._emitError(err);
        }
      };

      bound.domain = this;
      return bound;
    }

    /**
     * Intercept a callback for error handling.
     */
    intercept(callback) {
      if (typeof callback !== 'function') {
        throw new TypeError('callback must be a function');
      }

      const self = this;

      const intercepted = function(err, ...args) {
        if (err) {
          self._emitError(err);
          return;
        }

        try {
          return callback.apply(this, args);
        } catch (err2) {
          self._emitError(err2);
        }
      };

      intercepted.domain = this;
      return intercepted;
    }

    /**
     * Enter this domain.
     */
    enter() {
      if (this._disposed) {
        throw new Error('Cannot enter a disposed domain');
      }

      domainStack.push(this);
      exports.active = this;
    }

    /**
     * Exit this domain.
     */
    exit() {
      const index = domainStack.lastIndexOf(this);
      if (index !== -1) {
        domainStack.splice(index);
      }

      exports.active = domainStack.length > 0 ? domainStack[domainStack.length - 1] : null;
    }

    /**
     * Run a function in this domain.
     */
    run(fn, ...args) {
      if (this._disposed) {
        throw new Error('Cannot run in a disposed domain');
      }

      this.enter();
      
      try {
        return fn.apply(null, args);
      } catch (err) {
        this._emitError(err);
      } finally {
        this.exit();
      }
    }

    /**
     * Dispose of this domain.
     */
    dispose() {
      if (this._disposed) return;
      
      this._disposed = true;
      
      // Remove all members
      for (const member of this.members.slice()) {
        this.remove(member);
      }
      
      this.removeAllListeners();
      this.exit();
      
      this.emit('dispose');
    }

    /**
     * Emit an error on this domain.
     */
    _emitError(err) {
      if (this.listenerCount('error') > 0) {
        this.emit('error', err);
      } else {
        // Re-throw if no error handler
        throw err;
      }
    }
  }

  /**
   * Create a new domain.
   */
  function create() {
    return new Domain();
  }

  // Module exports
  const exports = {
    create,
    Domain,
    active: null
  };

  // Deprecation warning (console if available)
  if (global.console && global.console.warn) {
    const warned = new Set();
    const warnDeprecated = (method) => {
      if (!warned.has(method)) {
        warned.add(method);
        // Only warn once per method
        // console.warn(`DeprecationWarning: domain.${method}() is deprecated.`);
      }
    };
    
    // We don't actually emit warnings as they're noisy, but track that it's deprecated
  }

  global.__domain_module = exports;

})(globalThis);
