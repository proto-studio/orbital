// diagnostics_channel - Pub/sub diagnostics channel
(function(global) {
  'use strict';

  // Channel registry
  const channels = new Map();

  /**
   * Channel class - A named channel for publishing diagnostic messages.
   */
  class Channel {
    constructor(name) {
      this._name = name;
      this._subscribers = new Set();
      this._hasSubscribers = false;
    }

    get name() {
      return this._name;
    }

    get hasSubscribers() {
      return this._subscribers.size > 0;
    }

    /**
     * Publish a message to all subscribers.
     */
    publish(message) {
      if (this._subscribers.size === 0) {
        return;
      }

      for (const subscriber of this._subscribers) {
        try {
          subscriber(message, this._name);
        } catch (e) {
          // Subscribers should not throw, but if they do, continue
          console.error('diagnostics_channel subscriber error:', e);
        }
      }
    }

    /**
     * Subscribe to messages on this channel.
     */
    subscribe(onMessage) {
      if (typeof onMessage !== 'function') {
        throw new TypeError('onMessage must be a function');
      }
      this._subscribers.add(onMessage);
    }

    /**
     * Unsubscribe from messages on this channel.
     */
    unsubscribe(onMessage) {
      return this._subscribers.delete(onMessage);
    }

    /**
     * Bind this channel to a store for context tracking.
     */
    bindStore(store, transform) {
      // Store binding for async context tracking (simplified)
      const self = this;
      return {
        runStore(context, fn, ...args) {
          return store.run(context, fn, ...args);
        }
      };
    }

    /**
     * Unbind store from this channel.
     */
    unbindStore(store) {
      // No-op in simplified implementation
    }
  }

  /**
   * TracingChannel - A collection of channels for tracing.
   */
  class TracingChannel {
    constructor(nameOrChannels) {
      if (typeof nameOrChannels === 'string') {
        this.start = channel(`tracing:${nameOrChannels}:start`);
        this.end = channel(`tracing:${nameOrChannels}:end`);
        this.asyncStart = channel(`tracing:${nameOrChannels}:asyncStart`);
        this.asyncEnd = channel(`tracing:${nameOrChannels}:asyncEnd`);
        this.error = channel(`tracing:${nameOrChannels}:error`);
      } else {
        this.start = nameOrChannels.start || channel('tracing:unnamed:start');
        this.end = nameOrChannels.end || channel('tracing:unnamed:end');
        this.asyncStart = nameOrChannels.asyncStart || channel('tracing:unnamed:asyncStart');
        this.asyncEnd = nameOrChannels.asyncEnd || channel('tracing:unnamed:asyncEnd');
        this.error = nameOrChannels.error || channel('tracing:unnamed:error');
      }
    }

    get hasSubscribers() {
      return this.start.hasSubscribers || 
             this.end.hasSubscribers || 
             this.asyncStart.hasSubscribers || 
             this.asyncEnd.hasSubscribers || 
             this.error.hasSubscribers;
    }

    /**
     * Subscribe to all channels in this tracing channel.
     */
    subscribe(handlers) {
      if (handlers.start) this.start.subscribe(handlers.start);
      if (handlers.end) this.end.subscribe(handlers.end);
      if (handlers.asyncStart) this.asyncStart.subscribe(handlers.asyncStart);
      if (handlers.asyncEnd) this.asyncEnd.subscribe(handlers.asyncEnd);
      if (handlers.error) this.error.subscribe(handlers.error);
    }

    /**
     * Unsubscribe from all channels in this tracing channel.
     */
    unsubscribe(handlers) {
      if (handlers.start) this.start.unsubscribe(handlers.start);
      if (handlers.end) this.end.unsubscribe(handlers.end);
      if (handlers.asyncStart) this.asyncStart.unsubscribe(handlers.asyncStart);
      if (handlers.asyncEnd) this.asyncEnd.unsubscribe(handlers.asyncEnd);
      if (handlers.error) this.error.unsubscribe(handlers.error);
    }

    /**
     * Trace a synchronous function.
     */
    traceSync(fn, context, thisArg, ...args) {
      const ctx = context || {};
      
      this.start.publish(ctx);
      
      try {
        const result = fn.apply(thisArg, args);
        ctx.result = result;
        this.end.publish(ctx);
        return result;
      } catch (error) {
        ctx.error = error;
        this.error.publish(ctx);
        throw error;
      }
    }

    /**
     * Trace a callback-based function.
     */
    traceCallback(fn, position, context, thisArg, ...args) {
      const ctx = context || {};
      const callbackPos = position === undefined ? args.length : position;
      
      this.start.publish(ctx);
      
      const originalCallback = args[callbackPos];
      args[callbackPos] = (...callbackArgs) => {
        const error = callbackArgs[0];
        if (error) {
          ctx.error = error;
          this.error.publish(ctx);
        } else {
          ctx.result = callbackArgs.slice(1);
          this.asyncEnd.publish(ctx);
        }
        
        if (typeof originalCallback === 'function') {
          return originalCallback.apply(thisArg, callbackArgs);
        }
      };
      
      try {
        const result = fn.apply(thisArg, args);
        this.end.publish(ctx);
        return result;
      } catch (error) {
        ctx.error = error;
        this.error.publish(ctx);
        throw error;
      }
    }

    /**
     * Trace a promise-returning function.
     */
    tracePromise(fn, context, thisArg, ...args) {
      const ctx = context || {};
      
      this.start.publish(ctx);
      
      try {
        const result = fn.apply(thisArg, args);
        
        this.end.publish(ctx);
        
        if (result && typeof result.then === 'function') {
          return result.then(
            (value) => {
              ctx.result = value;
              this.asyncEnd.publish(ctx);
              return value;
            },
            (error) => {
              ctx.error = error;
              this.error.publish(ctx);
              throw error;
            }
          );
        }
        
        return result;
      } catch (error) {
        ctx.error = error;
        this.error.publish(ctx);
        throw error;
      }
    }
  }

  /**
   * Get or create a channel by name.
   */
  function channel(name) {
    if (!channels.has(name)) {
      channels.set(name, new Channel(name));
    }
    return channels.get(name);
  }

  /**
   * Check if a channel has subscribers.
   */
  function hasSubscribers(name) {
    const ch = channels.get(name);
    return ch ? ch.hasSubscribers : false;
  }

  /**
   * Subscribe to a channel.
   */
  function subscribe(name, onMessage) {
    channel(name).subscribe(onMessage);
  }

  /**
   * Unsubscribe from a channel.
   */
  function unsubscribe(name, onMessage) {
    const ch = channels.get(name);
    if (ch) {
      return ch.unsubscribe(onMessage);
    }
    return false;
  }

  /**
   * Create a TracingChannel.
   */
  function tracingChannel(nameOrChannels) {
    return new TracingChannel(nameOrChannels);
  }

  // Export
  const diagnostics_channel = {
    channel,
    hasSubscribers,
    subscribe,
    unsubscribe,
    tracingChannel,
    Channel,
    TracingChannel
  };

  global.__diagnostics_channel_module = diagnostics_channel;

})(globalThis);
