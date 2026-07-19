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
      const result = internal.gzipSync(toBase64(buffer));
      if (result === null || result === undefined) {
        throw new Error('Gzip compression failed');
      }
      return fromBase64(result);
    },

    /**
     * Decompress gzip data.
     */
    gunzipSync: function(buffer, options) {
      const result = internal.gunzipSync(toBase64(buffer));
      if (result === null || result === undefined) {
        throw new Error('Gzip decompression failed');
      }
      return fromBase64(result);
    },

    /**
     * Compress data using deflate (zlib format).
     */
    deflateSync: function(buffer, options) {
      const result = internal.deflateSync(toBase64(buffer));
      if (result === null || result === undefined) {
        throw new Error('Deflate compression failed');
      }
      return fromBase64(result);
    },

    /**
     * Decompress deflate data (zlib format).
     */
    inflateSync: function(buffer, options) {
      const result = internal.inflateSync(toBase64(buffer));
      if (result === null || result === undefined) {
        throw new Error('Inflate decompression failed');
      }
      return fromBase64(result);
    },

    /**
     * Compress data using raw deflate (no zlib header).
     */
    deflateRawSync: function(buffer, options) {
      const result = internal.deflateRawSync(toBase64(buffer));
      if (result === null || result === undefined) {
        throw new Error('DeflateRaw compression failed');
      }
      return fromBase64(result);
    },

    /**
     * Decompress raw deflate data (no zlib header).
     */
    inflateRawSync: function(buffer, options) {
      const result = internal.inflateRawSync(toBase64(buffer));
      if (result === null || result === undefined) {
        throw new Error('InflateRaw decompression failed');
      }
      return fromBase64(result);
    },

    /**
     * Decompress data (auto-detect format).
     */
    unzipSync: function(buffer, options) {
      // Try gzip first
      try {
        return zlib.gunzipSync(buffer, options);
      } catch (e) {
        // Try deflate
        try {
          return zlib.inflateSync(buffer, options);
        } catch (e2) {
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
      try {
        const result = zlib.gzipSync(buffer, options);
        if (callback) {
          setImmediate(() => callback(null, result));
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
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
      try {
        const result = zlib.gunzipSync(buffer, options);
        if (callback) {
          setImmediate(() => callback(null, result));
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
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
      try {
        const result = zlib.deflateSync(buffer, options);
        if (callback) {
          setImmediate(() => callback(null, result));
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
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
      try {
        const result = zlib.inflateSync(buffer, options);
        if (callback) {
          setImmediate(() => callback(null, result));
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
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
      try {
        const result = zlib.deflateRawSync(buffer, options);
        if (callback) {
          setImmediate(() => callback(null, result));
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
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
      try {
        const result = zlib.inflateRawSync(buffer, options);
        if (callback) {
          setImmediate(() => callback(null, result));
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
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
      try {
        const result = zlib.unzipSync(buffer, options);
        if (callback) {
          setImmediate(() => callback(null, result));
        }
        return Promise.resolve(result);
      } catch (err) {
        if (callback) {
          setImmediate(() => callback(err));
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
        // Manage the readable side directly: the base Readable.push buffers even
        // in flowing mode and never re-emits 'end', so drive 'data'/'end'
        // ourselves. _zBuf holds output produced before a consumer starts
        // flowing (Node buffers until a 'data' listener / resume()).
        this._zBuf = [];
        this._zFlowing = false;
        this._zEnded = false;
        this._zEndEmitted = false;
        this._zDestroyed = false;
        this._zFinalCb = null;
        this._zId = internal._streamCreate(format, (err, chunk) => {
          if (this._zDestroyed) return;
          if (err) {
            const e = new Error(err);
            e.code = 'Z_DATA_ERROR';
            this.emit('error', e);
            return;
          }
          if (chunk === null || chunk === undefined) {
            // Native codec finished: complete the writable side ('finish') and
            // let the readable side flush any buffered output then emit 'end'.
            this._zEnded = true;
            if (this._zFinalCb) { const cb = this._zFinalCb; this._zFinalCb = null; cb(); }
            this._zDrain();
            return;
          }
          this._zBuf.push(Buffer.from(chunk, 'base64'));
          this._zDrain();
        });
      }

      _zDrain() {
        if (!this._zFlowing) return;
        while (this._zBuf.length > 0 && this._zFlowing) {
          this.emit('data', this._zBuf.shift());
        }
        if (this._zEnded && this._zBuf.length === 0 && !this._zEndEmitted) {
          this._zEndEmitted = true;
          this.emit('end');
        }
      }

      // Attaching a 'data' listener puts the readable side into flowing mode
      // (raw-body reads via on('data')/on('end')). `on` is the EventEmitter
      // primitive; overriding it covers on/once/addListener without recursion.
      on(event, listener) {
        super.on(event, listener);
        if (event === 'data') this.resume();
        return this;
      }

      resume() {
        this._zFlowing = true;
        this._zDrain();
        return this;
      }

      pause() {
        this._zFlowing = false;
        return this;
      }

      _transform(chunk, encoding, callback) {
        const buf = Buffer.isBuffer(chunk)
          ? chunk
          : Buffer.from(chunk, typeof encoding === 'string' ? encoding : 'utf8');
        internal._streamWrite(this._zId, buf.toString('base64'));
        callback();
      }

      // Override _final (not _flush) so we do NOT go through Transform's
      // push(null) path; 'end' is emitted by _zDrain when the native codec
      // signals completion. Signal the codec to flush and remember the callback
      // that fires 'finish' on the writable side.
      _final(callback) {
        if (this._zEnded) { callback(); return; }
        this._zFinalCb = callback;
        internal._streamEnd(this._zId);
      }

      destroy(err) {
        if (this._zDestroyed) return this;
        this._zDestroyed = true;
        this.destroyed = true;
        try { internal._streamDestroy(this._zId); } catch (e) {}
        if (err) this.emit('error', err);
        this.emit('close');
        return this;
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
