// perf_hooks - Performance measurement
(function(global) {
  'use strict';

  // High-resolution time origin
  const timeOrigin = Date.now();

  // Performance entries storage
  const entries = [];
  const markMap = new Map();
  const observerCallbacks = [];

  /**
   * PerformanceEntry base class.
   */
  class PerformanceEntry {
    constructor(name, entryType, startTime, duration) {
      this._name = name;
      this._entryType = entryType;
      this._startTime = startTime;
      this._duration = duration;
    }

    get name() { return this._name; }
    get entryType() { return this._entryType; }
    get startTime() { return this._startTime; }
    get duration() { return this._duration; }

    toJSON() {
      return {
        name: this.name,
        entryType: this.entryType,
        startTime: this.startTime,
        duration: this.duration
      };
    }

    get [Symbol.toStringTag]() {
      return 'PerformanceEntry';
    }
  }

  /**
   * PerformanceMark for marking timestamps.
   */
  class PerformanceMark extends PerformanceEntry {
    constructor(name, options) {
      const startTime = options && options.startTime !== undefined 
        ? options.startTime 
        : performance.now();
      super(name, 'mark', startTime, 0);
      this._detail = options && options.detail || null;
    }

    get detail() { return this._detail; }

    get [Symbol.toStringTag]() {
      return 'PerformanceMark';
    }
  }

  /**
   * PerformanceMeasure for measuring between marks.
   */
  class PerformanceMeasure extends PerformanceEntry {
    constructor(name, startMark, endMark, duration, detail) {
      super(name, 'measure', startMark, duration);
      this._detail = detail || null;
    }

    get detail() { return this._detail; }

    get [Symbol.toStringTag]() {
      return 'PerformanceMeasure';
    }
  }

  /**
   * PerformanceResourceTiming for resource timing.
   */
  class PerformanceResourceTiming extends PerformanceEntry {
    constructor(name) {
      super(name, 'resource', performance.now(), 0);
    }

    get initiatorType() { return 'other'; }
    get nextHopProtocol() { return ''; }
    get workerStart() { return 0; }
    get redirectStart() { return 0; }
    get redirectEnd() { return 0; }
    get fetchStart() { return this.startTime; }
    get domainLookupStart() { return 0; }
    get domainLookupEnd() { return 0; }
    get connectStart() { return 0; }
    get connectEnd() { return 0; }
    get secureConnectionStart() { return 0; }
    get requestStart() { return 0; }
    get responseStart() { return 0; }
    get responseEnd() { return 0; }
    get transferSize() { return 0; }
    get encodedBodySize() { return 0; }
    get decodedBodySize() { return 0; }

    get [Symbol.toStringTag]() {
      return 'PerformanceResourceTiming';
    }
  }

  /**
   * PerformanceObserver for observing performance entries.
   */
  class PerformanceObserver {
    constructor(callback) {
      this._callback = callback;
      this._entryTypes = [];
      this._isActive = false;
    }

    observe(options) {
      if (options.entryTypes) {
        this._entryTypes = options.entryTypes;
      } else if (options.type) {
        this._entryTypes = [options.type];
      }
      this._isActive = true;
      observerCallbacks.push(this);
    }

    disconnect() {
      this._isActive = false;
      const idx = observerCallbacks.indexOf(this);
      if (idx !== -1) {
        observerCallbacks.splice(idx, 1);
      }
    }

    takeRecords() {
      return [];
    }

    static get supportedEntryTypes() {
      return ['mark', 'measure', 'resource', 'function'];
    }

    get [Symbol.toStringTag]() {
      return 'PerformanceObserver';
    }
  }

  /**
   * PerformanceObserverEntryList for observer callbacks.
   */
  class PerformanceObserverEntryList {
    constructor(entries) {
      this._entries = entries;
    }

    getEntries() {
      return this._entries.slice();
    }

    getEntriesByType(type) {
      return this._entries.filter(e => e.entryType === type);
    }

    getEntriesByName(name, type) {
      return this._entries.filter(e => {
        if (type && e.entryType !== type) return false;
        return e.name === name;
      });
    }

    get [Symbol.toStringTag]() {
      return 'PerformanceObserverEntryList';
    }
  }

  // Notify observers
  function notifyObservers(entry) {
    for (const observer of observerCallbacks) {
      if (observer._isActive && observer._entryTypes.includes(entry.entryType)) {
        const list = new PerformanceObserverEntryList([entry]);
        try {
          observer._callback(list, observer);
        } catch (e) {
          console.error('PerformanceObserver callback error:', e);
        }
      }
    }
  }

  /**
   * Performance API.
   */
  const performance = {
    /**
     * Returns the time origin.
     */
    get timeOrigin() {
      return timeOrigin;
    },

    /**
     * Returns high-resolution time since time origin.
     */
    now: function() {
      return Date.now() - timeOrigin;
    },

    /**
     * Creates a performance mark.
     */
    mark: function(name, options) {
      const mark = new PerformanceMark(name, options);
      markMap.set(name, mark);
      entries.push(mark);
      notifyObservers(mark);
      return mark;
    },

    /**
     * Clears performance marks.
     */
    clearMarks: function(name) {
      if (name) {
        markMap.delete(name);
        for (let i = entries.length - 1; i >= 0; i--) {
          if (entries[i].entryType === 'mark' && entries[i].name === name) {
            entries.splice(i, 1);
          }
        }
      } else {
        markMap.clear();
        for (let i = entries.length - 1; i >= 0; i--) {
          if (entries[i].entryType === 'mark') {
            entries.splice(i, 1);
          }
        }
      }
    },

    /**
     * Creates a performance measure between marks.
     */
    measure: function(name, startMarkOrOptions, endMark) {
      let startTime, endTime, detail;

      if (typeof startMarkOrOptions === 'object') {
        // Options object
        const options = startMarkOrOptions;
        startTime = options.start !== undefined ? options.start : 0;
        endTime = options.end !== undefined ? options.end : performance.now();
        detail = options.detail;

        if (typeof startTime === 'string') {
          const mark = markMap.get(startTime);
          startTime = mark ? mark.startTime : 0;
        }
        if (typeof endTime === 'string') {
          const mark = markMap.get(endTime);
          endTime = mark ? mark.startTime : performance.now();
        }
      } else {
        // Legacy arguments
        if (startMarkOrOptions) {
          const mark = markMap.get(startMarkOrOptions);
          startTime = mark ? mark.startTime : 0;
        } else {
          startTime = 0;
        }

        if (endMark) {
          const mark = markMap.get(endMark);
          endTime = mark ? mark.startTime : performance.now();
        } else {
          endTime = performance.now();
        }
      }

      const duration = endTime - startTime;
      const measure = new PerformanceMeasure(name, startTime, endTime, duration, detail);
      entries.push(measure);
      notifyObservers(measure);
      return measure;
    },

    /**
     * Clears performance measures.
     */
    clearMeasures: function(name) {
      if (name) {
        for (let i = entries.length - 1; i >= 0; i--) {
          if (entries[i].entryType === 'measure' && entries[i].name === name) {
            entries.splice(i, 1);
          }
        }
      } else {
        for (let i = entries.length - 1; i >= 0; i--) {
          if (entries[i].entryType === 'measure') {
            entries.splice(i, 1);
          }
        }
      }
    },

    /**
     * Returns all performance entries.
     */
    getEntries: function() {
      return entries.slice();
    },

    /**
     * Returns entries by type.
     */
    getEntriesByType: function(type) {
      return entries.filter(e => e.entryType === type);
    },

    /**
     * Returns entries by name.
     */
    getEntriesByName: function(name, type) {
      return entries.filter(e => {
        if (type && e.entryType !== type) return false;
        return e.name === name;
      });
    },

    /**
     * Clears resource timing entries.
     */
    clearResourceTimings: function() {
      for (let i = entries.length - 1; i >= 0; i--) {
        if (entries[i].entryType === 'resource') {
          entries.splice(i, 1);
        }
      }
    },

    /**
     * Sets resource timing buffer size.
     */
    setResourceTimingBufferSize: function(size) {
      // No-op
    },

    /**
     * Event handler placeholders.
     */
    onresourcetimingbufferfull: null,

    /**
     * Node.js specific: Returns GC entry.
     */
    get nodeTiming() {
      const self = this;
      return {
        name: 'node',
        entryType: 'node',
        startTime: 0,
        get duration() { return self.now(); },
        get nodeStart() { return 0; },
        get v8Start() { return 0; },
        get bootstrapComplete() { return 0; },
        get environment() { return 0; },
        get loopStart() { return 0; },
        get loopExit() { return -1; },
        get idleTime() { return 0; }
      };
    },

    /**
     * Convert to JSON.
     */
    toJSON: function() {
      return {
        timeOrigin: this.timeOrigin
      };
    },

    get [Symbol.toStringTag]() {
      return 'Performance';
    }
  };

  /**
   * Histogram class for recording values.
   */
  class Histogram {
    constructor() {
      this._values = [];
      this._min = Infinity;
      this._max = -Infinity;
      this._count = 0;
      this._sum = 0;
    }

    record(value) {
      this._values.push(value);
      this._count++;
      this._sum += value;
      if (value < this._min) this._min = value;
      if (value > this._max) this._max = value;
    }

    get min() { return this._min === Infinity ? 0 : this._min; }
    get max() { return this._max === -Infinity ? 0 : this._max; }
    get mean() { return this._count > 0 ? this._sum / this._count : 0; }
    get count() { return this._count; }
    get sum() { return this._sum; }

    percentile(p) {
      if (this._values.length === 0) return 0;
      const sorted = this._values.slice().sort((a, b) => a - b);
      const idx = Math.floor((p / 100) * sorted.length);
      return sorted[Math.min(idx, sorted.length - 1)];
    }

    reset() {
      this._values = [];
      this._min = Infinity;
      this._max = -Infinity;
      this._count = 0;
      this._sum = 0;
    }

    get [Symbol.toStringTag]() {
      return 'Histogram';
    }
  }

  /**
   * Creates a histogram for recording durations.
   */
  function createHistogram(options) {
    return new Histogram();
  }

  /**
   * Measures a function's duration.
   */
  function timerify(fn, options) {
    const name = options && options.name || fn.name || 'anonymous';
    
    return function(...args) {
      const start = performance.now();
      try {
        return fn.apply(this, args);
      } finally {
        const duration = performance.now() - start;
        const entry = new PerformanceEntry(name, 'function', start, duration);
        entries.push(entry);
        notifyObservers(entry);
      }
    };
  }

  /**
   * Monitors event loop delay.
   */
  function monitorEventLoopDelay(options) {
    const resolution = options && options.resolution || 10;
    const histogram = new Histogram();
    let enabled = false;
    let interval = null;
    let lastTime = null;

    return {
      enable() {
        if (enabled) return;
        enabled = true;
        lastTime = Date.now();
        interval = setInterval(() => {
          const now = Date.now();
          const delay = now - lastTime - resolution;
          if (delay > 0) {
            histogram.record(delay * 1e6); // Convert to nanoseconds
          }
          lastTime = now;
        }, resolution);
      },
      disable() {
        if (!enabled) return;
        enabled = false;
        if (interval) {
          clearInterval(interval);
          interval = null;
        }
      },
      reset() {
        histogram.reset();
      },
      get min() { return histogram.min; },
      get max() { return histogram.max; },
      get mean() { return histogram.mean; },
      get stddev() { return 0; }, // Simplified
      percentile(p) { return histogram.percentile(p); }
    };
  }

  const perf_hooks = {
    performance,
    PerformanceEntry,
    PerformanceMark,
    PerformanceMeasure,
    PerformanceObserver,
    PerformanceObserverEntryList,
    PerformanceResourceTiming,
    Histogram,
    createHistogram,
    timerify,
    monitorEventLoopDelay,
    constants: {
      NODE_PERFORMANCE_GC_MAJOR: 4,
      NODE_PERFORMANCE_GC_MINOR: 1,
      NODE_PERFORMANCE_GC_INCREMENTAL: 8,
      NODE_PERFORMANCE_GC_WEAKCB: 16,
      NODE_PERFORMANCE_GC_FLAGS_NO: 0,
      NODE_PERFORMANCE_GC_FLAGS_CONSTRUCT_RETAINED: 2,
      NODE_PERFORMANCE_GC_FLAGS_FORCED: 4,
      NODE_PERFORMANCE_GC_FLAGS_SYNCHRONOUS_PHANTOM_PROCESSING: 8,
      NODE_PERFORMANCE_GC_FLAGS_ALL_AVAILABLE_GARBAGE: 16,
      NODE_PERFORMANCE_GC_FLAGS_ALL_EXTERNAL_MEMORY: 32,
      NODE_PERFORMANCE_GC_FLAGS_SCHEDULE_IDLE: 64
    }
  };

  // Export
  global.__perf_hooks_module = perf_hooks;
  
  // Also set global performance if not exists
  if (typeof global.performance === 'undefined') {
    global.performance = performance;
  }

})(globalThis);
