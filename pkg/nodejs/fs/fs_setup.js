'use strict';
// Attach Node fs.Stats-style predicate methods to the plain objects returned by
// the native stat implementations. The native layer (Go) only sets the boolean
// props (_isFile/_isDirectory/...); Node code universally calls the *methods*
// (stat.isFile(), stat.isDirectory(), ...), so real-world packages (TypeScript's
// compiler host, etc.) break without them. Wrapping the sync entry points here
// keeps a single Stats shape across the sync, callback, and promise APIs.
(function () {
  const fs = globalThis.__fs_module;
  if (!fs) return;

  function addStatsMethods(s) {
    if (!s || typeof s !== 'object') return s;
    s.isDirectory = function () { return !!this._isDirectory; };
    s.isFile = function () { return !!this._isFile; };
    s.isSymbolicLink = function () { return !!this._isSymbolicLink; };
    s.isBlockDevice = function () { return !!this._isBlockDevice; };
    s.isCharacterDevice = function () { return !!this._isCharacterDevice; };
    s.isFIFO = function () { return !!this._isFIFO; };
    s.isSocket = function () { return !!this._isSocket; };
    // Node exposes timestamps as Date objects (mtime/atime/ctime/birthtime)
    // alongside the numeric *Ms fields. The native layer only sets the *Ms
    // numbers; build the Dates here so consumers like `send`
    // (stat.mtime.toUTCString()) and `etag` (stat.mtime.getTime()) work.
    if (typeof s.mtimeMs === 'number' && !(s.mtime instanceof Date)) {
      s.mtime = new Date(s.mtimeMs);
      s.atime = new Date(typeof s.atimeMs === 'number' ? s.atimeMs : s.mtimeMs);
      s.ctime = new Date(typeof s.ctimeMs === 'number' ? s.ctimeMs : s.mtimeMs);
      s.birthtime = new Date(typeof s.birthtimeMs === 'number' ? s.birthtimeMs : s.mtimeMs);
    }
    return s;
  }
  // Exposed so the promise/callback layers can share the exact same shape.
  fs.__addStatsMethods = addStatsMethods;

  function wrapStat(name) {
    const orig = fs[name];
    if (typeof orig !== 'function') return;
    fs[name] = function () {
      return addStatsMethods(orig.apply(this, arguments));
    };
  }
  wrapStat('statSync');
  wrapStat('lstatSync');
  wrapStat('fstatSync');

  // Everything below moves file contents as base64, because a string reaching Go stops at its
  // first NUL.

  // Buffer registers after fs, and an embedder can register fs without it. These move UTF-8
  // text and raw bytes without Buffer when it is missing; with it, they use Buffer.
  const hasBuffer = () => typeof Buffer === 'function';
  const B64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  function utf8Bytes(str) {
    const out = [];
    for (let i = 0; i < str.length; i++) {
      let c = str.charCodeAt(i);
      // 0xD800-0xDBFF is a UTF-16 high surrogate, the first half of a pair.
      if (c >= 0xd800 && c <= 0xdbff && i + 1 < str.length) {
        const next = str.charCodeAt(i + 1);
        // 0xDC00-0xDFFF is a UTF-16 low surrogate, the second half of a pair.
        if (next >= 0xdc00 && next <= 0xdfff) {
          // Combine the pair into one code point. 0x10000 is the first code point past the Basic Multilingual Plane.
          c = 0x10000 + ((c - 0xd800) << 10) + (next - 0xdc00);
          i++;
        }
      }
      // A lone surrogate (0xD800-0xDFFF) becomes 0xFFFD, U+FFFD the replacement character.
      if (c >= 0xd800 && c <= 0xdfff) c = 0xfffd;
      // Below 0x80 is ASCII, written as one byte.
      if (c < 0x80) out.push(c);
      // Below 0x800 takes two bytes: 0xC0 marks a 2-byte lead (110xxxxx) and 0x80 a continuation byte (10xxxxxx).
      else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
      // Below 0x10000 takes three bytes: 0xE0 marks a 3-byte lead (1110xxxx), then two 0x80 continuation bytes.
      else if (c < 0x10000) out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
      // Anything else takes four bytes: 0xF0 marks a 4-byte lead (11110xxx), then three 0x80 continuation bytes.
      else out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
    }
    return new Uint8Array(out);
  }
  function base64Of(bytes) {
    if (hasBuffer()) return Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength).toString('base64');
    let out = '';
    for (let i = 0; i < bytes.length; i += 3) {
      const n = (bytes[i] << 16) | ((bytes[i + 1] || 0) << 8) | (bytes[i + 2] || 0);
      out += B64[n >> 18] + B64[(n >> 12) & 63] +
        (i + 1 < bytes.length ? B64[(n >> 6) & 63] : '=') + (i + 2 < bytes.length ? B64[n & 63] : '=');
    }
    return out;
  }
  function bytesOf(base64) {
    if (hasBuffer()) return Buffer.from(base64, 'base64');
    const clean = base64.replace(/=+$/, '');
    const out = new Uint8Array(Math.floor(clean.length * 3 / 4));
    let bits = 0;
    let value = 0;
    let j = 0;
    for (let i = 0; i < clean.length; i++) {
      value = (value << 6) | B64.indexOf(clean[i]);
      bits += 6;
      if (bits >= 8) {
        bits -= 8;
        out[j++] = (value >> bits) & 255;
      }
    }
    return out;
  }
  function codedError(Ctor, code, message) {
    const e = new Ctor(message);
    e.code = code;
    return e;
  }

  function encodingOf(options) {
    const encoding = typeof options === 'string'
      ? options
      : (options && typeof options === 'object' ? options.encoding : null);
    const known = hasBuffer() ? Buffer.isEncoding(encoding) : isUtf8(encoding);
    if (encoding && encoding !== 'buffer' && !known) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', "The argument 'encoding' is invalid encoding. Received '" + encoding + "'");
    }
    return encoding || null;
  }

  // The native reads return UTF-8 text directly, which is cheaper than base64 and is what most
  // reads of source files ask for.
  function isUtf8(encoding) {
    return !!encoding && /^utf-?8$/i.test(encoding);
  }

  function decodeContents(contents, encoding) {
    if (isUtf8(encoding)) return contents;
    const buffer = bytesOf(contents);
    return encoding ? buffer.toString(encoding) : buffer;
  }

  function viewBytes(view) {
    return hasBuffer()
      ? Buffer.from(view.buffer, view.byteOffset, view.byteLength)
      : new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
  }

  function dataBytes(data, encoding) {
    if (typeof data === 'string') return hasBuffer() ? Buffer.from(data, encoding || 'utf8') : utf8Bytes(data);
    if (ArrayBuffer.isView(data)) return viewBytes(data);
    throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "data" argument must be of type string or an instance of Buffer, TypedArray, or DataView.');
  }

  function checkCallback(callback) {
    if (typeof callback !== 'function') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "cb" argument must be of type function.');
    }
  }

  function checkRange(name, value, max) {
    if (!Number.isInteger(value) || value < 0 || value > max) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be >= 0 && <= ' + max + '. Received ' + value);
    }
  }

  const positionArg = (position) => (typeof position === 'number' ? position : -1);

  // readFileSync: Node ALWAYS throws when a read fails (ENOENT for a missing
  // path, etc.); libraries lean on that catchable error (e.g. y18n does
  // `JSON.parse(readFileSync(...))` inside a try/catch and treats an ENOENT as
  // "no locale file"). The native layer returns `undefined` on any read failure
  // instead of throwing, so a missing file silently became `JSON.parse(undefined)`
  // -> SyntaxError. Convert that sentinel back into a real Node-style error.
  const _readFileSync = fs.readFileSync;
  if (typeof _readFileSync === 'function') {
    fs.readFileSync = function (p, options) {
      const encoding = encodingOf(options);
      const result = _readFileSync.call(this, p, isUtf8(encoding));
      if (result === null || result === undefined) {
        const path = typeof p === 'string' ? p : String(p);
        const exists = typeof fs.existsSync === 'function' && fs.existsSync(path);
        const e = new Error(
          (exists
            ? "EISDIR: illegal operation on a directory, read"
            : "ENOENT: no such file or directory, open '" + path + "'")
        );
        e.code = exists ? 'EISDIR' : 'ENOENT';
        e.errno = exists ? -21 : -2;
        e.syscall = exists ? 'read' : 'open';
        e.path = path;
        throw e;
      }
      return decodeContents(result, encoding);
    };
  }

  const _writeFileSync = fs.writeFileSync;
  fs.writeFileSync = function (path, data, options) {
    _writeFileSync.call(this, path, base64Of(dataBytes(data, encodingOf(options))));
  };

  const _appendFileSync = fs.appendFileSync;
  fs.appendFileSync = function (path, data, options) {
    _appendFileSync.call(this, path, base64Of(dataBytes(data, encodingOf(options))));
  };

  const _readFile = fs.readFile;
  fs.readFile = function (path, options, callback) {
    if (typeof options === 'function') {
      callback = options;
      options = undefined;
    }
    checkCallback(callback);
    const encoding = encodingOf(options);
    _readFile.call(this, path, isUtf8(encoding), (err, contents) => {
      if (err) callback(err);
      else callback(null, decodeContents(contents, encoding));
    });
  };

  const _writeFile = fs.writeFile;
  fs.writeFile = function (path, data, options, callback) {
    if (typeof options === 'function') {
      callback = options;
      options = undefined;
    }
    checkCallback(callback);
    _writeFile.call(this, path, base64Of(dataBytes(data, encodingOf(options))), callback);
  };

  // writeSync(fd, buffer[, offset[, length[, position]]]), writeSync(fd, buffer[, options])
  // or writeSync(fd, string[, position[, encoding]]).
  const _writeSync = fs.writeSync;
  fs.writeSync = function (fd, data, offsetOrPosition, lengthOrEncoding, position) {
    if (typeof data === 'string') {
      return _writeSync.call(this, fd, base64Of(dataBytes(data, lengthOrEncoding)), positionArg(offsetOrPosition));
    }
    if (!ArrayBuffer.isView(data)) dataBytes(data);
    let offset = offsetOrPosition;
    let length = lengthOrEncoding;
    if (offset !== null && typeof offset === 'object') {
      ({ offset = 0, length = data.byteLength - offset, position = null } = offset);
    }
    if (offset === undefined || offset === null) offset = 0;
    checkRange('offset', offset, data.byteLength);
    if (typeof length !== 'number') length = data.byteLength - offset;
    checkRange('length', length, data.byteLength - offset);
    const bytes = viewBytes(data).subarray(offset, offset + length);
    return _writeSync.call(this, fd, base64Of(bytes), positionArg(position));
  };

  // readSync(fd, buffer[, offset[, length[, position]]]) or readSync(fd, buffer[, options]).
  const _readSync = fs.readSync;
  fs.readSync = function (fd, buffer, offsetOrOptions, length, position) {
    if (!ArrayBuffer.isView(buffer)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "buffer" argument must be an instance of Buffer, TypedArray, or DataView.');
    }
    let offset = offsetOrOptions;
    if (arguments.length <= 3 || (offset !== null && typeof offset === 'object')) {
      ({ offset = 0, length = buffer.byteLength - offset, position = null } = offset || {});
    }
    if (offset === undefined || offset === null) offset = 0;
    checkRange('offset', offset, buffer.byteLength);
    length |= 0;
    if (length === 0) return 0;
    checkRange('length', length, buffer.byteLength - offset);
    const bytes = bytesOf(_readSync.call(this, fd, length, positionArg(position)));
    new Uint8Array(buffer.buffer, buffer.byteOffset, buffer.byteLength).set(bytes, offset);
    return bytes.length;
  };

  // readdirSync(path, { withFileTypes: true }) returns Dirent-like objects; the
  // native layer sets the boolean props, so attach the predicate methods here.
  const _readdirSync = fs.readdirSync;
  if (typeof _readdirSync === 'function') {
    fs.readdirSync = function () {
      const result = _readdirSync.apply(this, arguments);
      if (Array.isArray(result)) {
        for (const e of result) {
          if (e && typeof e === 'object') addStatsMethods(e);
        }
      }
      return result;
    };
  }

  // lstatSync: Orbital's Stat follows symlinks and does not track them
  // separately, so lstat behaves like stat here (adequate for consumers that
  // only need file/directory classification).
  if (typeof fs.lstatSync !== 'function' && typeof fs.statSync === 'function') {
    fs.lstatSync = fs.statSync;
  }

  // realpathSync: without a native symlink resolver, canonicalization is a
  // no-op beyond existence checking. Node's realpath on a non-symlink path
  // returns that same path, so returning the (already absolute) input matches
  // for the common case while still throwing ENOENT for missing paths.
  if (typeof fs.realpathSync !== 'function' && typeof fs.existsSync === 'function') {
    fs.realpathSync = function (p) {
      if (!fs.existsSync(p)) {
        const e = new Error("ENOENT: no such file or directory, realpath '" + p + "'");
        e.code = 'ENOENT';
        e.errno = -2;
        e.syscall = 'realpath';
        e.path = p;
        throw e;
      }
      return p;
    };
  }

  // Node errno values for the codes the native async layer produces. Real
  // packages (send, http-errors) branch on err.code, and occasionally err.errno.
  const ERRNO = { ENOENT: -2, EACCES: -13, EINVAL: -22, EIO: -5, EBADF: -9 };

  // Shared error factory: the Go async layer (fs.stat/access/createReadStream)
  // calls this so failures surface as real Error instances with a Node `code`,
  // rather than bare strings. Kept here because constructing/adorning Errors is
  // far simpler in JS than across the V8 boundary.
  fs.__makeFsError = function (message, code, path, syscall) {
    const e = new Error(message || (code ? code + ': fs error' : 'fs error'));
    if (code) { e.code = code; e.errno = ERRNO[code] !== undefined ? ERRNO[code] : -1; }
    if (path) e.path = path;
    if (syscall) e.syscall = syscall;
    return e;
  };

  // fs.createReadStream — a thin Readable adapter over the native streaming
  // reader (fs._readStreamOpen/_readStreamRead/_readStreamClose). All bulk I/O
  // happens in Go off the JS thread; this class only pulls chunks and re-emits
  // them as Node stream events. The stream module registers *after* fs, so the
  // class is built lazily on first use (by which point __stream_module exists).
  let _ReadStream = null;
  function readStreamClass() {
    if (_ReadStream) return _ReadStream;
    const streamMod = globalThis.__stream_module;
    if (!streamMod || !streamMod.Readable) return null;
    const Readable = streamMod.Readable;

    _ReadStream = class ReadStream extends Readable {
      constructor(path, options) {
        options = options || {};
        const hwm = typeof options.highWaterMark === 'number' ? options.highWaterMark : 65536;
        super({ highWaterMark: hwm });
        this.path = path;
        this.bytesRead = 0;
        this.pending = true;
        this._fsId = -1;

        const start = typeof options.start === 'number' ? options.start : -1;
        const end = typeof options.end === 'number' ? options.end : -1;
        const res = fs._readStreamOpen(String(path), start, end);
        if (!res || res.error) {
          this.destroy(fs.__makeFsError(
            (res && res.error) || 'open failed',
            (res && res.code) || 'ENOENT', path, 'open'));
          return;
        }
        this._fsId = res.id;
        this.pending = false;
        process.nextTick(() => {
          if (this.destroyed) return;
          this.emit('open', this._fsId);
          this.emit('ready');
        });
      }

      _read(size) {
        if (this._fsId < 0) return;
        fs._readStreamRead(this._fsId, size, (err, chunk) => {
          if (this.destroyed) return;
          if (err) {
            this.destroy(fs.__makeFsError(err, 'EIO', this.path, 'read'));
            return;
          }
          if (chunk === null || chunk === undefined) {
            this.push(null);
            return;
          }
          const buf = Buffer.from(chunk, 'latin1');
          this.bytesRead += buf.length;
          this.push(buf);
        });
      }

      _destroy(err, callback) {
        if (this._fsId >= 0) {
          try { fs._readStreamClose(this._fsId); } catch (e) {}
          this._fsId = -1;
        }
        callback(err);
      }

      close(cb) {
        if (typeof cb === 'function') this.once('close', cb);
        return this.destroy();
      }
    };
    return _ReadStream;
  }

  // Expose fs.ReadStream via a getter so `require('fs').ReadStream` (used by the
  // `destroy` package's instanceof check) resolves the lazily-built class once
  // the stream module is available.
  if (typeof fs.createReadStream !== 'function') {
    Object.defineProperty(fs, 'ReadStream', {
      configurable: true,
      enumerable: true,
      get() { return readStreamClass(); }
    });
    fs.createReadStream = function (path, options) {
      const C = readStreamClass();
      if (!C) throw new Error('fs.createReadStream requires the stream module');
      return new C(path, options);
    };
  }
})();
