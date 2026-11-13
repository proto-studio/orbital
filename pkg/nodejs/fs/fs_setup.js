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

  // How Node's range errors print a number: integers past 2^32 with _ between thousands.
  function receivedNumber(value) {
    if (!Number.isInteger(value) || Math.abs(value) <= 2 ** 32) return String(value);
    const digits = String(Math.abs(value));
    let out = '';
    for (let i = digits.length; i > 0; i -= 3) out = digits.slice(Math.max(0, i - 3), i) + (out ? '_' + out : '');
    return (value < 0 ? '-' : '') + out;
  }

  function checkRange(name, value, max) {
    if (!Number.isInteger(value) || value < 0 || value > max) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be >= 0 && <= ' + max + '. Received ' + receivedNumber(value));
    }
  }

  // Node's validateInteger(value, name, 0): a number, an integer, from 0 to 2^53 - 1.
  function checkInteger(name, value) {
    if (typeof value !== 'number') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type number. Received ' + receivedType(value));
    }
    if (!Number.isInteger(value)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be an integer. Received ' + value);
    }
    checkRange(name, value, Number.MAX_SAFE_INTEGER);
  }

  const positionArg = (position) => (typeof position === 'number' ? position : -1);

  // The native calls return a description of a failure rather than throwing, because they
  // can only throw a string. This throws it as Node's Error.
  function check(result) {
    if (result && result.__fsError) {
      const e = new Error(result.message);
      e.errno = result.errno;
      e.code = result.code;
      e.syscall = result.syscall;
      e.path = result.path;
      if (result.dest !== undefined) e.dest = result.dest;
      throw e;
    }
    return result;
  }

  const OPEN_FLAGS = ['r', 'rs', 'sr', 'r+', 'rs+', 'sr+', 'w', 'wx', 'xw', 'w+', 'wx+', 'xw+',
    'a', 'ax', 'xa', 'as', 'sa', 'a+', 'ax+', 'xa+', 'as+', 'sa+'];

  // The flag and mode a write was given, with Node's defaults.
  function writeFlag(options, fallback) {
    // Node falls back on any falsy flag, so 0 and NaN mean the default too.
    const flag = options && typeof options === 'object' && options.flag ? options.flag : fallback;
    if (typeof flag === 'number') {
      // A numeric flag must be a 32-bit signed integer, checked before the file is opened.
      if (!Number.isInteger(flag)) {
        throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'The value of "flags" is out of range. It must be an integer. Received ' + flag);
      }
      if (flag < -2147483648 || flag > 2147483647) {
        throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
          'The value of "flags" is out of range. It must be >= -2147483648 && <= 2147483647. Received ' + flag);
      }
      return flag;
    }
    if (!OPEN_FLAGS.includes(flag)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', "The argument 'flags' is invalid. Received " + JSON.stringify(flag).replace(/"/g, "'"));
    }
    return flag;
  }

  // How Node's errors describe a value of the wrong type after "Received".
  function receivedType(value) {
    if (value === null || value === undefined) return String(value);
    if (typeof value === 'function') return 'function ' + value.name;
    if (typeof value === 'string') {
      // Node quotes the string, then cuts anything past 28 characters to its first 25.
      const quotedValue = "'" + value + "'";
      return 'type string (' + (quotedValue.length > 28 ? quotedValue.slice(0, 25) + '...' : quotedValue) + ')';
    }
    if (typeof value === 'object') {
      const name = value.constructor && value.constructor.name;
      return name ? 'an instance of ' + name : 'an instance of Object';
    }
    return 'type ' + typeof value + ' (' + String(value) + ')';
  }

  // A write's mode, checked as Node checks it before touching the file: an integer from 0 to
  // 2^32 - 1, or a string of octal digits.
  function writeMode(options) {
    let mode = options && typeof options === 'object' ? options.mode : undefined;
    if (mode === undefined || mode === null) return 0o666;
    if (typeof mode === 'string') {
      if (!/^[0-7]+$/.test(mode)) {
        throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
          "The argument 'mode' must be a 32-bit unsigned integer or an octal string. Received '" + mode + "'");
      }
      mode = parseInt(mode, 8);
    }
    if (typeof mode !== 'number') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "mode" argument must be of type number. Received ' + receivedType(mode));
    }
    if (!Number.isInteger(mode)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'The value of "mode" is out of range. It must be an integer. Received ' + mode);
    }
    checkRange('mode', mode, 4294967295);
    return mode;
  }

  // The native open takes only a numeric mode, so an octal string such as '600' is parsed and
  // a bad mode refused here, as Node does.
  const _openSync = fs.openSync;
  fs.openSync = function () {
    const args = Array.prototype.slice.call(arguments);
    if (args.length >= 3) args[2] = writeMode({ mode: args[2] });
    return check(_openSync.apply(this, args));
  };

  // These calls return a failure description too.
  for (const name of ['mkdirSync', 'rmdirSync', 'unlinkSync', 'readdirSync', 'renameSync', 'copyFileSync',
    'closeSync', 'writeSync', 'readSync', 'fstatSync']) {
    const native = fs[name];
    if (typeof native === 'function') {
      fs[name] = function () {
        return check(native.apply(this, arguments));
      };
    }
  }

  // mkdirSync(path[, mode]) or mkdirSync(path[, { recursive, mode }]). The mode is checked as
  // Node checks it, before the directory is made, and defaults to 0o777.
  function mkdirOptions(options) {
    const object = options !== null && typeof options === 'object';
    const mode = object ? options.mode : options;
    return {
      recursive: object && !!options.recursive,
      mode: mode === undefined || mode === null ? 0o777 : writeMode({ mode })
    };
  }
  const _mkdirSync = fs.mkdirSync;
  fs.mkdirSync = function (path, options) {
    const { recursive, mode } = mkdirOptions(options);
    return _mkdirSync.call(this, path, { recursive }, mode);
  };

  const _readFileSync = fs.readFileSync;
  fs.readFileSync = function (p, options) {
    const encoding = encodingOf(options);
    return decodeContents(check(_readFileSync.call(this, p, isUtf8(encoding))), encoding);
  };

  // writeFile and appendFile given a descriptor write to it at its current offset, as Node
  // does, ignoring the flag; the native calls take only a path.
  function writeToDescriptor(fd, data, options) {
    const bytes = dataBytes(data, encodingOf(options));
    fs.writeSync(fd, bytes, 0, bytes.byteLength, null);
  }

  const _writeFileSync = fs.writeFileSync;
  fs.writeFileSync = function (path, data, options) {
    if (typeof path === 'number') return writeToDescriptor(path, data, options);
    const bytes = base64Of(dataBytes(data, encodingOf(options)));
    check(_writeFileSync.call(this, path, bytes, writeFlag(options, 'w'), writeMode(options)));
  };

  const _appendFileSync = fs.appendFileSync;
  fs.appendFileSync = function (path, data, options) {
    if (typeof path === 'number') return writeToDescriptor(path, data, options);
    const bytes = base64Of(dataBytes(data, encodingOf(options)));
    check(_appendFileSync.call(this, path, bytes, writeFlag(options, 'a'), writeMode(options)));
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
    if (typeof path === 'number') {
      const bytes = dataBytes(data, encodingOf(options));
      try {
        writeToDescriptor(path, bytes);
      } catch (e) {
        process.nextTick(callback, e);
        return;
      }
      process.nextTick(callback, null);
      return;
    }
    const bytes = base64Of(dataBytes(data, encodingOf(options)));
    _writeFile.call(this, path, bytes, writeFlag(options, 'w'), writeMode(options), callback);
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

  // fs.createWriteStream — a Writable over a descriptor from openSync. Writes go through
  // writeSync, so they reach the file in order and keep every byte.
  let _WriteStream = null;
  function writeStreamClass() {
    if (_WriteStream) return _WriteStream;
    const streamMod = globalThis.__stream_module;
    if (!streamMod || !streamMod.Writable) return null;

    _WriteStream = class WriteStream extends streamMod.Writable {
      constructor(path, options) {
        if (typeof options === 'string') options = { encoding: options };
        options = options || {};
        // Node checks start before anything else, so a bad one throws without opening, and
        // so without truncating, the file.
        if (options.start !== undefined) checkInteger('start', options.start);
        // autoClose is Writable's autoDestroy: with it off, finishing or failing leaves the
        // stream, and its descriptor, open.
        super({
          highWaterMark: options.highWaterMark, decodeStrings: true,
          emitClose: options.emitClose !== false, autoDestroy: options.autoClose !== false,
          signal: options.signal
        });
        this.path = options.fd != null ? undefined : path;
        this.flags = options.flags === undefined ? 'w' : options.flags;
        this.mode = options.mode === undefined ? 0o666 : options.mode;
        this.start = options.start;
        this.pos = options.start;
        this.autoClose = options.autoClose !== false;
        this._emitClose = options.emitClose !== false;
        this.bytesWritten = 0;
        this.fd = null;
        this.pending = true;
        if (options.encoding) this.setDefaultEncoding(options.encoding);

        if (options.fd != null) {
          // A FileHandle from fs.promises.open writes through its descriptor, and the stream
          // closes it by closing the handle.
          if (typeof options.fd === 'object' && typeof options.fd.close === 'function') {
            this._handle = options.fd;
            this.fd = options.fd.fd;
          } else {
            this.fd = options.fd;
          }
          this.pending = false;
          return;
        }
        // Node opens asynchronously: the outcome arrives on a later tick, and writes and the
        // final flush wait for it, so a failed open reaches them too.
        this._opening = true;
        this._openError = null;
        this._waiting = null;
        try {
          this.fd = fs.openSync(path, this.flags, this.mode);
          this._openedFd = this.fd;
        } catch (e) {
          this._openError = e;
        }
        process.nextTick(() => this._opened());
      }

      _opened() {
        this._opening = false;
        const waiting = this._waiting;
        this._waiting = null;
        const error = this._openError;
        if (!error) this.pending = false;
        if (this.destroyed) {
          const fail = () => {
            if (waiting) waiting(codedError(Error, 'ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
          };
          if (error) {
            fail();
            return;
          }
          // A good open that finishes after the stream was destroyed, by an abort signal say,
          // is still announced, after the destroy's own events, as in Node.
          const fd = this.fd === null ? this._openedFd : this.fd;
          const announce = () => {
            fail();
            this.emit('open', fd);
            this.emit('ready');
          };
          if (this._emitClose) this.once('close', () => process.nextTick(announce));
          else process.nextTick(announce);
          return;
        }
        if (error) {
          this._openErrorReported = true;
          // With autoClose off, Node only reports the error, and the writes wait for good.
          if (!this.autoClose) {
            this._waiting = waiting;
            this.emit('error', error);
          } else if (waiting) {
            // The write fails with the open's error, which fails the writes queued behind it
            // and destroys the stream, so 'error' is emitted once.
            waiting(error);
          } else {
            this.destroy(error);
          }
          return;
        }
        this.emit('open', this.fd);
        this.emit('ready');
        if (waiting) waiting(null);
      }

      // Node's writes call back asynchronously, so their callbacks come before 'prefinish'.
      // These write synchronously, so the final step waits a tick to keep that order.
      _final(callback) {
        if (this._opening || this._openError) {
          this._waiting = callback;
          return;
        }
        process.nextTick(callback);
      }

      _write(chunk, encoding, callback) {
        if (this._opening || this._openError) {
          this._waiting = (err) => (err ? callback(err) : this._write(chunk, encoding, callback));
          return;
        }
        try {
          const n = fs.writeSync(this.fd, chunk, 0, chunk.length, this.pos === undefined ? null : this.pos);
          if (this.pos !== undefined) this.pos += n;
          this.bytesWritten += n;
          callback();
        } catch (e) {
          callback(e);
        }
      }

      // Destroying closes the descriptor whatever autoClose says, as in Node.
      _destroy(err, callback) {
        if (this.fd === null) {
          // Destroyed before a failed open was reported: Node still reports it.
          callback(err || (this._openErrorReported ? null : this._openError));
          return;
        }
        const fd = this.fd;
        this.fd = null;
        if (this._handle) {
          const handle = this._handle;
          this._handle = null;
          handle.close().then(() => callback(err), (e) => callback(err || e));
          return;
        }
        try {
          fs.closeSync(fd);
        } catch (e) {
          if (!err) err = e;
        }
        // Node closes asynchronously, so 'error' and 'close' come a tick later.
        process.nextTick(callback, err);
      }

      // An explicit close ends the stream and then closes the descriptor even with autoClose
      // off, as in Node. Like Node's, it does nothing more once the stream has finished.
      close(cb) {
        if (typeof cb === 'function') {
          if (this.closed) {
            process.nextTick(cb);
            return;
          }
          this.on('close', cb);
        }
        if (!this.autoClose) this.on('finish', this.destroy);
        this.end();
      }
    };
    return _WriteStream;
  }
  if (typeof fs.createWriteStream !== 'function') {
    Object.defineProperty(fs, 'WriteStream', {
      configurable: true,
      enumerable: true,
      get() { return writeStreamClass(); }
    });
    fs.createWriteStream = function (path, options) {
      const C = writeStreamClass();
      if (!C) throw new Error('fs.createWriteStream requires the stream module');
      return new C(path, options);
    };
  }

  // rmSync(path[, { force, recursive }]): unlink a file, or with recursive a directory too.
  // force ignores a path that does not exist, and nothing else. Like Node, it looks at the path
  // with lstat, so a symbolic link is removed itself, even one whose target is missing or a
  // directory.
  const _rmTargetSync = fs.__rmTargetSync;
  delete fs.__rmTargetSync;
  if (typeof fs.rmSync !== 'function' && typeof _rmTargetSync === 'function') {
    fs.rmSync = function (path, options) {
      const force = !!(options && options.force);
      const recursive = !!(options && options.recursive);
      let stats;
      try {
        stats = addStatsMethods(check(_rmTargetSync.call(fs, path)));
      } catch (e) {
        if (force && e.code === 'ENOENT') return;
        throw e;
      }
      if (stats.isDirectory() && !recursive) {
        const e = new Error('Path is a directory: rm returned EISDIR (is a directory) ' + path);
        e.name = 'SystemError';
        e.code = 'ERR_FS_EISDIR';
        e.errno = 21;
        e.syscall = 'rm';
        e.path = String(path);
        throw e;
      }
      try {
        if (stats.isDirectory()) fs.rmdirSync(path, { recursive: true });
        else fs.unlinkSync(path);
      } catch (e) {
        if (!(force && e.code === 'ENOENT')) throw e;
      }
    };
  }

  // Callback forms of the synchronous calls. They run the synchronous call on this thread and
  // call back on the next tick, as Node's always call back asynchronously.
  // The synchronous function is looked up on each call, so the checks wrapped around it
  // later, such as refusing a path with a NUL, apply here too.
  function callbackForm(syncName, shape) {
    return function () {
      const args = Array.prototype.slice.call(arguments);
      const callback = args.pop();
      checkCallback(callback);
      let result;
      try {
        result = fs[syncName].apply(fs, args);
      } catch (e) {
        process.nextTick(callback, e);
        return;
      }
      const values = shape ? shape(result, args) : (result === undefined ? [] : [result]);
      process.nextTick(() => callback(null, ...values));
    };
  }
  const CALLBACK_FORMS = {
    appendFile: 'appendFileSync', open: 'openSync', close: 'closeSync', fstat: 'fstatSync',
    mkdir: 'mkdirSync', rmdir: 'rmdirSync', rm: 'rmSync', unlink: 'unlinkSync', rename: 'renameSync',
    readdir: 'readdirSync', copyFile: 'copyFileSync', realpath: 'realpathSync'
  };
  for (const name of Object.keys(CALLBACK_FORMS)) {
    const sync = fs[CALLBACK_FORMS[name]];
    if (typeof fs[name] !== 'function' && typeof sync === 'function') fs[name] = callbackForm(CALLBACK_FORMS[name]);
  }
  if (typeof fs.mkdir === 'function') {
    // Like Node, a bad mode throws rather than reaching the callback.
    const mkdir = fs.mkdir;
    fs.mkdir = function (path, options, callback) {
      if (typeof options === 'function') return mkdir(path, options);
      mkdirOptions(options);
      return mkdir(path, options, callback);
    };
  }
  if (typeof fs.appendFile === 'function') {
    // appendFile(path, data[, options], callback): the options are optional.
    const appendFile = fs.appendFile;
    fs.appendFile = function (path, data, options, callback) {
      if (typeof options === 'function') return appendFile(path, data, undefined, options);
      return appendFile(path, data, options, callback);
    };
  }
  // write calls back with (err, bytesWritten, buffer or string); read with (err, bytesRead, buffer).
  if (typeof fs.write !== 'function') {
    fs.write = callbackForm('writeSync', (n, args) => [n, args[1]]);
  }
  if (typeof fs.read !== 'function') {
    const read = callbackForm('readSync', (n, args) => [n, args[1]]);
    fs.read = function (fd, buffer, offset, length, position, callback) {
      // read(fd, callback) and read(fd, options, callback) read into a new 16 KiB buffer.
      if (typeof buffer === 'function' || (buffer && !ArrayBuffer.isView(buffer) && typeof offset === 'function')) {
        const options = typeof buffer === 'function' ? {} : buffer;
        callback = typeof buffer === 'function' ? buffer : offset;
        const target = options.buffer || Buffer.alloc(16384);
        return read(fd, target, options.offset || 0, options.length === undefined ? target.byteLength - (options.offset || 0) : options.length,
          options.position === undefined ? null : options.position, callback);
      }
      return read.apply(this, arguments);
    };
  }

  // The open flags come from Go, because their values depend on the platform.
  if (!fs.constants) {
    fs.constants = Object.assign({ F_OK: 0, R_OK: 4, W_OK: 2, X_OK: 1 }, fs.__openFlags,
      { COPYFILE_EXCL: 1, COPYFILE_FICLONE: 2, COPYFILE_FICLONE_FORCE: 4 });
  }
  delete fs.__openFlags;

  // A path reaches the filesystem as a C string, which ends at the first NUL, so 'a\0b' would
  // name the file 'a'. Node refuses such paths everywhere, before touching the disk.
  function quoted(value) {
    const text = "'" + value.replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\0/g, '\\x00') + "'";
    // Node shortens a long value in this message to 128 characters.
    return text.length > 128 ? text.slice(0, 128) + '...' : text;
  }
  function checkPath(value, name) {
    const text = typeof value === 'string' ? value
      : (ArrayBuffer.isView(value) ? Buffer.from(value.buffer, value.byteOffset, value.byteLength).toString('latin1') : null);
    if (text !== null && text.includes('\0')) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
        "The argument '" + name + "' must be a string, Uint8Array, or URL without null bytes. Received " + quoted(text));
    }
  }
  const PATH_ARGUMENTS = {
    readFileSync: ['path'], writeFileSync: ['path'], appendFileSync: ['path'], openSync: ['path'],
    mkdirSync: ['path'], rmdirSync: ['path'], rmSync: ['path'], unlinkSync: ['path'],
    readdirSync: ['path'], statSync: ['path'], lstatSync: ['path'], accessSync: ['path'],
    realpathSync: ['path'], renameSync: ['oldPath', 'newPath'], copyFileSync: ['src', 'dest'],
    readFile: ['path'], writeFile: ['path'], appendFile: ['path'], stat: ['path'], lstat: ['path'],
    access: ['path'], open: ['path'], createReadStream: ['path'], createWriteStream: ['path'],
    mkdir: ['path'], rmdir: ['path'], rm: ['path'], unlink: ['path'], readdir: ['path'], realpath: ['path'],
    rename: ['oldPath', 'newPath'], copyFile: ['src', 'dest']
  };
  for (const name of Object.keys(PATH_ARGUMENTS)) {
    const original = fs[name];
    if (typeof original !== 'function') continue;
    const names = PATH_ARGUMENTS[name];
    fs[name] = function () {
      names.forEach((argName, i) => checkPath(arguments[i], argName));
      return original.apply(this, arguments);
    };
  }
  const _existsSync = fs.existsSync;
  fs.existsSync = function (path) {
    if (typeof path === 'string' && path.includes('\0')) return false;
    return _existsSync.apply(this, arguments);
  };
})();
