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

  // Export
  global.__zlib_module = zlib;

})(globalThis);
