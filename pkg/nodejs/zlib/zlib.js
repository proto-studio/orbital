// zlib - Compression module
(function(global) {
  'use strict';

  const internal = global.__zlib_internal;

  // Helper to convert string/Buffer to base64 for Go
  function toBase64(data) {
    if (typeof data === 'string') {
      // Encode string as UTF-8 bytes then base64
      const bytes = new TextEncoder().encode(data);
      let binary = '';
      for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i]);
      }
      return btoa(binary);
    } else if (data instanceof Uint8Array || data instanceof ArrayBuffer) {
      const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : data;
      let binary = '';
      for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i]);
      }
      return btoa(binary);
    } else if (data && data.buffer) {
      // Buffer-like object
      const bytes = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
      let binary = '';
      for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i]);
      }
      return btoa(binary);
    }
    return String(data);
  }

  // Helper to convert base64 result to Buffer/Uint8Array
  function fromBase64(str) {
    const binary = atob(str);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  }

  // The largest maxOutputLength Node 20 accepts, its buffer.constants.MAX_LENGTH. Output
  // stops sooner here anyway, at kMaxOutput.
  const kMaxLength = 2 ** 32;
  // The most bytes one call can hand back here (zlib.go): its result reaches
  // JavaScript as one base64 string.
  const kMaxOutput = internal.maxOutput;

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  // Node's description of a rejected value, as in its ERR_INVALID_ARG_TYPE.
  function describeReceived(value) {
    if (value === null || value === undefined) return 'Received ' + value;
    if (typeof value === 'function') return 'Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') {
      if (value.constructor && value.constructor.name) {
        return 'Received an instance of ' + value.constructor.name;
      }
      return 'Received ' + String(value);
    }
    let shown;
    if (typeof value === 'string') {
      shown = value.length > 28 ? value.slice(0, 25) + '...' : value;
      shown = "'" + shown + "'";
    } else if (typeof value === 'bigint') {
      shown = String(value) + 'n';
    } else if (typeof value === 'symbol') {
      shown = String(value);
    } else {
      shown = Object.is(value, -0) ? '-0' : String(value);
    }
    return 'Received type ' + typeof value + ' (' + shown + ')';
  }

  // A number as Node's ERR_OUT_OF_RANGE shows it: integers of 2^32 and more
  // grouped with underscores.
  function describeNumber(value) {
    if (Number.isInteger(value) && Math.abs(value) > 2 ** 32) {
      const digits = String(Math.abs(value));
      let grouped = '';
      for (let i = digits.length; i > 0; i -= 3) {
        grouped = digits.slice(Math.max(0, i - 3), i) + (grouped ? '_' + grouped : '');
      }
      return (value < 0 ? '-' : '') + grouped;
    }
    return String(value);
  }

  // Node's checkRangesOrGetDefault for options.maxOutputLength: undefined and
  // NaN mean the default, anything else must be a finite number in range.
  function maxOutputLength(options) {
    const name = 'options.maxOutputLength';
    const value = options === null || options === undefined ? undefined : options.maxOutputLength;
    if (value === undefined || Number.isNaN(value)) return kMaxLength;
    if (typeof value !== 'number') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "' + name + '" property must be of type number. ' + describeReceived(value));
    }
    if (!Number.isFinite(value)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        'The value of "' + name + '" is out of range. It must be a finite number. Received ' + describeNumber(value));
    }
    if (value < 1 || value > kMaxLength) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        'The value of "' + name + '" is out of range. It must be >= 1 and <= ' + kMaxLength +
        '. Received ' + describeNumber(value));
    }
    return value;
  }

  // Runs one native sync codec with the call's output limit. Over it, the
  // native returns false and this throws Node's ERR_BUFFER_TOO_LARGE, naming the
  // limit that applied: the caller's, or the most one call can return here.
  function runSync(native, buffer, options, failure) {
    const requested = maxOutputLength(options);
    const limit = requested > kMaxOutput ? kMaxOutput : requested;
    const result = native(toBase64(buffer), limit);
    if (result === false) {
      throw codedError(RangeError, 'ERR_BUFFER_TOO_LARGE',
        'Cannot create a Buffer larger than ' + limit + ' bytes');
    }
    if (result === null || result === undefined) {
      throw new Error(failure);
    }
    return fromBase64(result);
  }

  // Compression levels
  const constants = {
    Z_NO_COMPRESSION: 0,
    Z_BEST_SPEED: 1,
    Z_BEST_COMPRESSION: 9,
    Z_DEFAULT_COMPRESSION: -1,
    Z_FILTERED: 1,
    Z_HUFFMAN_ONLY: 2,
    Z_RLE: 3,
    Z_FIXED: 4,
    Z_DEFAULT_STRATEGY: 0,
    Z_OK: 0,
    Z_STREAM_END: 1,
    Z_NEED_DICT: 2,
    Z_ERRNO: -1,
    Z_STREAM_ERROR: -2,
    Z_DATA_ERROR: -3,
    Z_MEM_ERROR: -4,
    Z_BUF_ERROR: -5,
    Z_VERSION_ERROR: -6,
    GZIP: 16,
    DEFLATE: 0,
    DEFLATERAW: -15,
    INFLATE: 16,
    INFLATERAW: -15,
    GUNZIP: 16,
    UNZIP: 32
  };

  const zlib = {
    constants,

    // Synchronous functions

    /**
     * Compress data using gzip.
     */
    gzipSync: function(buffer, options) {
      return runSync(internal.gzipSync, buffer, options, 'Gzip compression failed');
    },

    /**
     * Decompress gzip data.
     */
    gunzipSync: function(buffer, options) {
      return runSync(internal.gunzipSync, buffer, options, 'Gzip decompression failed');
    },

    /**
     * Compress data using deflate (zlib format).
     */
    deflateSync: function(buffer, options) {
      return runSync(internal.deflateSync, buffer, options, 'Deflate compression failed');
    },

    /**
     * Decompress deflate data (zlib format).
     */
    inflateSync: function(buffer, options) {
      return runSync(internal.inflateSync, buffer, options, 'Inflate decompression failed');
    },

    /**
     * Compress data using raw deflate (no zlib header).
     */
    deflateRawSync: function(buffer, options) {
      return runSync(internal.deflateRawSync, buffer, options, 'DeflateRaw compression failed');
    },

    /**
     * Decompress raw deflate data (no zlib header).
     */
    inflateRawSync: function(buffer, options) {
      return runSync(internal.inflateRawSync, buffer, options, 'InflateRaw decompression failed');
    },

    /**
     * Decompress data (auto-detect format).
     */
    unzipSync: function(buffer, options) {
      maxOutputLength(options);
      // Try gzip first
      try {
        return zlib.gunzipSync(buffer, options);
      } catch (e) {
        if (e && e.code === 'ERR_BUFFER_TOO_LARGE') throw e;
        // Try deflate
        try {
          return zlib.inflateSync(buffer, options);
        } catch (e2) {
          if (e2 && e2.code === 'ERR_BUFFER_TOO_LARGE') throw e2;
          throw new Error('Unzip failed: could not decompress data');
        }
      }
    },

    // Async functions (wrap sync for now)

    /**
     * Compress data using gzip (async).
     */
    gzip: function(buffer, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      // An invalid option throws here, as Node's does, not through the callback.
      maxOutputLength(options);
      try {
        const result = zlib.gzipSync(buffer, options);
        // With a callback, the result goes only there, as in Node.
        if (callback) {
          setImmediate(() => callback(null, result));
          return;
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
          return;
        }
        return Promise.reject(err);
      }
    },

    /**
     * Decompress gzip data (async).
     */
    gunzip: function(buffer, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      // An invalid option throws here, as Node's does, not through the callback.
      maxOutputLength(options);
      try {
        const result = zlib.gunzipSync(buffer, options);
        // With a callback, the result goes only there, as in Node.
        if (callback) {
          setImmediate(() => callback(null, result));
          return;
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
          return;
        }
        return Promise.reject(err);
      }
    },

    /**
     * Compress data using deflate (async).
     */
    deflate: function(buffer, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      // An invalid option throws here, as Node's does, not through the callback.
      maxOutputLength(options);
      try {
        const result = zlib.deflateSync(buffer, options);
        // With a callback, the result goes only there, as in Node.
        if (callback) {
          setImmediate(() => callback(null, result));
          return;
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
          return;
        }
        return Promise.reject(err);
      }
    },

    /**
     * Decompress deflate data (async).
     */
    inflate: function(buffer, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      // An invalid option throws here, as Node's does, not through the callback.
      maxOutputLength(options);
      try {
        const result = zlib.inflateSync(buffer, options);
        // With a callback, the result goes only there, as in Node.
        if (callback) {
          setImmediate(() => callback(null, result));
          return;
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
          return;
        }
        return Promise.reject(err);
      }
    },

    /**
     * Compress data using raw deflate (async).
     */
    deflateRaw: function(buffer, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      // An invalid option throws here, as Node's does, not through the callback.
      maxOutputLength(options);
      try {
        const result = zlib.deflateRawSync(buffer, options);
        // With a callback, the result goes only there, as in Node.
        if (callback) {
          setImmediate(() => callback(null, result));
          return;
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
          return;
        }
        return Promise.reject(err);
      }
    },

    /**
     * Decompress raw deflate data (async).
     */
    inflateRaw: function(buffer, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      // An invalid option throws here, as Node's does, not through the callback.
      maxOutputLength(options);
      try {
        const result = zlib.inflateRawSync(buffer, options);
        // With a callback, the result goes only there, as in Node.
        if (callback) {
          setImmediate(() => callback(null, result));
          return;
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
          return;
        }
        return Promise.reject(err);
      }
    },

    /**
     * Decompress data (async, auto-detect format).
     */
    unzip: function(buffer, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      // An invalid option throws here, as Node's does, not through the callback.
      maxOutputLength(options);
      try {
        const result = zlib.unzipSync(buffer, options);
        // With a callback, the result goes only there, as in Node.
        if (callback) {
          setImmediate(() => callback(null, result));
          return;
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
          return;
        }
        return Promise.reject(err);
      }
    },

    // Convenience aliases
    brotliCompressSync: function(buffer, options) {
      // Brotli not yet supported, fall back to gzip
      console.warn('zlib.brotliCompressSync: Brotli not supported, using gzip');
      return zlib.gzipSync(buffer, options);
    },

    brotliDecompressSync: function(buffer, options) {
      // Brotli not yet supported
      throw new Error('Brotli decompression not supported');
    },

    brotliCompress: function(buffer, options, callback) {
      console.warn('zlib.brotliCompress: Brotli not supported, using gzip');
      return zlib.gzip(buffer, options, callback);
    },

    brotliDecompress: function(buffer, options, callback) {
      const err = new Error('Brotli decompression not supported');
      if (callback) {
        setImmediate(() => callback(err));
        return;
      }
      return Promise.reject(err);
    }
  };

  // Streaming (de)compression classes. These are thin Transform adapters over
  // the native Go codecs (internal._streamCreate/_streamWrite/_streamEnd): the
  // bulk work streams through compress/gzip + compress/flate on a goroutine, and
  // output chunks come back here to be pushed downstream. body-parser relies on
  // zlib.createGunzip()/createInflate()/createUnzip() for request decompression.
  const streamMod = global.__stream_module;
  if (streamMod && streamMod.Transform && typeof internal._streamCreate === 'function') {
    const Transform = streamMod.Transform;

    class ZlibStream extends Transform {
      constructor(format, options) {
        super(options || {});
        this._zFinalCb = null;
        // Whether the native codec has finished its output. A codec can finish
        // before the writable side ends (an empty deflate stream ends itself), and
        // _final then completes at once.
        this._zDone = false;
        // Whether the native codec is waiting for this side to take its last
        // chunk; it's acknowledged once the readable side has room.
        this._zAckPending = false;
        this._zId = internal._streamCreate(format, (err, chunk) => {
          if (this.destroyed) return;
          if (err) {
            const e = new Error(err);
            e.code = 'Z_DATA_ERROR';
            this.destroy(e);
            return;
          }
          if (chunk === null || chunk === undefined) {
            this._zDone = true;
            this.push(null);
            if (this._zFinalCb) { const cb = this._zFinalCb; this._zFinalCb = null; cb(); }
            return;
          }
          if (this.push(Buffer.from(chunk, 'base64'))) {
            internal._streamAck(this._zId);
          } else {
            this._zAckPending = true;
          }
        });
      }

      _read(n) {
        if (this._zAckPending) {
          this._zAckPending = false;
          internal._streamAck(this._zId);
        }
        super._read(n);
      }

      _transform(chunk, encoding, callback) {
        const buf = Buffer.isBuffer(chunk)
          ? chunk
          : Buffer.from(chunk, typeof encoding === 'string' ? encoding : 'utf8');
        internal._streamWrite(this._zId, buf.toString('base64'), (err) => {
          callback(err ? new Error(err) : undefined);
        });
      }

      // The native codec signals the end of the output, not the Transform base class.
      _final(callback) {
        if (this._zDone) {
          callback();
          return;
        }
        this._zFinalCb = callback;
        internal._streamEnd(this._zId);
      }

      _destroy(err, callback) {
        try { internal._streamDestroy(this._zId); } catch (e) {}
        callback(err);
      }

      close(cb) {
        if (typeof cb === 'function') this.once('close', cb);
        return this.destroy();
      }
    }

    // Named subclasses so `stream instanceof zlib.Gunzip` etc. works (the
    // `destroy` package's isZlibStream check depends on these constructors).
    class Gzip extends ZlibStream { constructor(o) { super('gzip', o); } }
    class Gunzip extends ZlibStream { constructor(o) { super('gunzip', o); } }
    class Deflate extends ZlibStream { constructor(o) { super('deflate', o); } }
    class Inflate extends ZlibStream { constructor(o) { super('inflate', o); } }
    class DeflateRaw extends ZlibStream { constructor(o) { super('deflateraw', o); } }
    class InflateRaw extends ZlibStream { constructor(o) { super('inflateraw', o); } }
    class Unzip extends ZlibStream { constructor(o) { super('unzip', o); } }

    zlib.Gzip = Gzip;
    zlib.Gunzip = Gunzip;
    zlib.Deflate = Deflate;
    zlib.Inflate = Inflate;
    zlib.DeflateRaw = DeflateRaw;
    zlib.InflateRaw = InflateRaw;
    zlib.Unzip = Unzip;

    zlib.createGzip = function (options) { return new Gzip(options); };
    zlib.createGunzip = function (options) { return new Gunzip(options); };
    zlib.createDeflate = function (options) { return new Deflate(options); };
    zlib.createInflate = function (options) { return new Inflate(options); };
    zlib.createDeflateRaw = function (options) { return new DeflateRaw(options); };
    zlib.createInflateRaw = function (options) { return new InflateRaw(options); };
    zlib.createUnzip = function (options) { return new Unzip(options); };
  }

  // Export
  global.__zlib_module = zlib;

})(globalThis);
