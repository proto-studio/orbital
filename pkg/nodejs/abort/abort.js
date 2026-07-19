// AbortController and AbortSignal implementation
(function(global) {
  'use strict';

  /**
   * DOMException class for abort errors.
   */
  if (typeof global.DOMException === 'undefined') {
    class DOMException extends Error {
      constructor(message, name) {
        super(message);
        this.name = name || 'Error';
        this.code = DOMException.getCode(name);
      }

      static getCode(name) {
        const codes = {
          'IndexSizeError': 1,
          'DOMStringSizeError': 2,
          'HierarchyRequestError': 3,
          'WrongDocumentError': 4,
          'InvalidCharacterError': 5,
          'NoDataAllowedError': 6,
          'NoModificationAllowedError': 7,
          'NotFoundError': 8,
          'NotSupportedError': 9,
          'InUseAttributeError': 10,
          'InvalidStateError': 11,
          'SyntaxError': 12,
          'InvalidModificationError': 13,
          'NamespaceError': 14,
          'InvalidAccessError': 15,
          'ValidationError': 16,
          'TypeMismatchError': 17,
          'SecurityError': 18,
          'NetworkError': 19,
          'AbortError': 20,
          'URLMismatchError': 21,
          'QuotaExceededError': 22,
          'TimeoutError': 23,
          'InvalidNodeTypeError': 24,
          'DataCloneError': 25
        };
        return codes[name] || 0;
      }

      get [Symbol.toStringTag]() {
        return 'DOMException';
      }
    }
    global.DOMException = DOMException;
  }

  /**
   * Simple EventTarget polyfill if not available.
   */
  if (typeof global.EventTarget === 'undefined') {
    class EventTarget {
      constructor() {
        this._listeners = new Map();
      }

      addEventListener(type, listener, options) {
        if (!this._listeners.has(type)) {
          this._listeners.set(type, []);
        }
        this._listeners.get(type).push({ listener, options });
      }

      removeEventListener(type, listener) {
        const listeners = this._listeners.get(type);
        if (listeners) {
          const index = listeners.findIndex(l => l.listener === listener);
          if (index !== -1) {
            listeners.splice(index, 1);
          }
        }
      }

      dispatchEvent(event) {
        const listeners = this._listeners.get(event.type);
        if (listeners) {
          for (const { listener, options } of listeners.slice()) {
            try {
              if (typeof listener === 'function') {
                listener.call(this, event);
              } else if (listener && typeof listener.handleEvent === 'function') {
                listener.handleEvent(event);
              }
            } catch (e) {
              console.error('Error in event listener:', e);
            }
            
            if (options && options.once) {
              this.removeEventListener(event.type, listener);
            }
          }
        }
        return true;
      }
    }
    global.EventTarget = EventTarget;
  }

  /**
   * Simple Event class if not available.
   */
  if (typeof global.Event === 'undefined') {
    class Event {
      constructor(type, options) {
        this.type = type;
        this.bubbles = options && options.bubbles || false;
        this.cancelable = options && options.cancelable || false;
        this.defaultPrevented = false;
        this.timeStamp = Date.now();
        this.target = null;
        this.currentTarget = null;
      }

      preventDefault() {
        if (this.cancelable) {
          this.defaultPrevented = true;
        }
      }

      stopPropagation() {}
      stopImmediatePropagation() {}
    }
    global.Event = Event;
  }

  /**
   * AbortSignal represents a signal object that can communicate with
   * an async operation and abort it via an AbortController.
   */
  class AbortSignal extends global.EventTarget {
    constructor() {
      super();
      this._aborted = false;
      this._reason = undefined;
      this._onabort = null;
    }

    /**
     * Returns true if the signal has been aborted.
     */
    get aborted() {
      return this._aborted;
    }

    /**
     * Returns the abort reason, if any.
     */
    get reason() {
      return this._reason;
    }

    /**
     * Gets/sets the onabort event handler.
     */
    get onabort() {
      return this._onabort;
    }

    set onabort(handler) {
      this._onabort = handler;
    }

    /**
     * Throws if the signal has been aborted.
     */
    throwIfAborted() {
      if (this._aborted) {
        throw this._reason;
      }
    }

    /**
     * Creates an AbortSignal that is already aborted.
     */
    static abort(reason) {
      const signal = new AbortSignal();
      signal._aborted = true;
      signal._reason = reason !== undefined ? reason : new DOMException('The operation was aborted', 'AbortError');
      return signal;
    }

    /**
     * Creates an AbortSignal that automatically aborts after a timeout.
     */
    static timeout(milliseconds) {
      const controller = new AbortController();
      setTimeout(() => {
        controller.abort(new DOMException('The operation was aborted due to timeout', 'TimeoutError'));
      }, milliseconds);
      return controller.signal;
    }

    /**
     * Creates an AbortSignal that aborts when any of the given signals abort.
     */
    static any(signals) {
      const controller = new AbortController();
      
      for (const signal of signals) {
        if (signal.aborted) {
          controller.abort(signal.reason);
          return controller.signal;
        }
        
        signal.addEventListener('abort', () => {
          if (!controller.signal.aborted) {
            controller.abort(signal.reason);
          }
        }, { once: true });
      }
      
      return controller.signal;
    }

    /**
     * Returns the string tag.
     */
    get [Symbol.toStringTag]() {
      return 'AbortSignal';
    }
  }

  /**
   * AbortController allows sending abort signals to async operations.
   */
  class AbortController {
    constructor() {
      this._signal = new AbortSignal();
    }

    /**
     * Returns the AbortSignal associated with this controller.
     */
    get signal() {
      return this._signal;
    }

    /**
     * Aborts the signal with an optional reason.
     */
    abort(reason) {
      if (this._signal._aborted) {
        return;
      }

      this._signal._aborted = true;
      this._signal._reason = reason !== undefined ? reason : new DOMException('The operation was aborted', 'AbortError');

      // Create abort event
      const event = new Event('abort');
      
      // Call onabort handler if set
      if (typeof this._signal._onabort === 'function') {
        try {
          this._signal._onabort.call(this._signal, event);
        } catch (e) {
          console.error('Error in onabort handler:', e);
        }
      }
      
      // Dispatch event
      this._signal.dispatchEvent(event);
    }

    /**
     * Returns the string tag.
     */
    get [Symbol.toStringTag]() {
      return 'AbortController';
    }
  }

  // Export as globals
  global.AbortController = AbortController;
  global.AbortSignal = AbortSignal;

})(globalThis);
