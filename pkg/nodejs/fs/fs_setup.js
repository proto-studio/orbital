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
  // first NUL. Buffer registers after fs, so it is only looked up when these run.
  function codedError(Ctor, code, message) {
    const e = new Ctor(message);
    e.code = code;
    return e;
  }

  function encodingOf(options) {
    const encoding = typeof options === 'string'
      ? options
      : (options && typeof options === 'object' ? options.encoding : null);
    if (encoding && encoding !== 'buffer' && !Buffer.isEncoding(encoding)) {
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
    const buffer = Buffer.from(contents, 'base64');
    return encoding ? buffer.toString(encoding) : buffer;
  }

  function viewBytes(view) {
    return Buffer.from(view.buffer, view.byteOffset, view.byteLength);
  }

  function dataBytes(data, encoding) {
    if (typeof data === 'string') return Buffer.from(data, encoding || 'utf8');
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

  // The native calls return a description of a failure rather than throwing, because they
  // can only throw a string. This throws it as Node's Error.
  function check(result) {
    if (result && result.__fsError) {
      const e = new Error(result.message);
      e.errno = result.errno;
      e.code = result.code;
      e.syscall = result.syscall;
      e.path = result.path;
      throw e;
    }
    return result;
  }

  const OPEN_FLAGS = ['r', 'rs', 'sr', 'r+', 'rs+', 'sr+', 'w', 'wx', 'xw', 'w+', 'wx+', 'xw+',
    'a', 'ax', 'xa', 'as', 'sa', 'a+', 'ax+', 'xa+', 'as+', 'sa+'];

  // The flag and mode a write was given, with Node's defaults.
  function writeFlag(options, fallback) {
    const flag = options && typeof options === 'object' && options.flag != null ? options.flag : fallback;
    if (typeof flag === 'number') return flag;
    if (!OPEN_FLAGS.includes(flag)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', "The argument 'flags' is invalid. Received " + JSON.stringify(flag).replace(/"/g, "'"));
    }
    // Go has no synchronous-mode flags, and every write here is already flushed on close.
    return flag.replace('s', '');
  }

  function writeMode(options) {
    const mode = options && typeof options === 'object' ? options.mode : undefined;
    if (mode === undefined || mode === null) return 0o666;
    return typeof mode === 'string' ? parseInt(mode, 8) : mode;
  }

  const _openSync = fs.openSync;
  fs.openSync = function () {
    return check(_openSync.apply(this, arguments));
  };

  const _readFileSync = fs.readFileSync;
  fs.readFileSync = function (p, options) {
    const encoding = encodingOf(options);
    return decodeContents(check(_readFileSync.call(this, p, isUtf8(encoding))), encoding);
  };

  const _writeFileSync = fs.writeFileSync;
  fs.writeFileSync = function (path, data, options) {
    const bytes = dataBytes(data, encodingOf(options)).toString('base64');
    check(_writeFileSync.call(this, path, bytes, writeFlag(options, 'w'), writeMode(options)));
  };

  const _appendFileSync = fs.appendFileSync;
  fs.appendFileSync = function (path, data, options) {
    const bytes = dataBytes(data, encodingOf(options)).toString('base64');
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
    const bytes = dataBytes(data, encodingOf(options)).toString('base64');
    _writeFile.call(this, path, bytes, writeFlag(options, 'w'), writeMode(options), callback);
  };

  // writeSync(fd, buffer[, offset[, length[, position]]]), writeSync(fd, buffer[, options])
  // or writeSync(fd, string[, position[, encoding]]).
  const _writeSync = fs.writeSync;
  fs.writeSync = function (fd, data, offsetOrPosition, lengthOrEncoding, position) {
    if (typeof data === 'string') {
      return _writeSync.call(this, fd, dataBytes(data, lengthOrEncoding).toString('base64'), positionArg(offsetOrPosition));
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
    return _writeSync.call(this, fd, bytes.toString('base64'), positionArg(position));
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
    const bytes = Buffer.from(_readSync.call(this, fd, length, positionArg(position)), 'base64');
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
        super({ highWaterMark: options.highWaterMark, decodeStrings: true, emitClose: options.emitClose !== false });
        this.path = options.fd != null ? undefined : path;
        this.flags = options.flags === undefined ? 'w' : options.flags;
        this.mode = options.mode === undefined ? 0o666 : options.mode;
        this.start = options.start;
        this.pos = typeof options.start === 'number' ? options.start : undefined;
        this.autoClose = options.autoClose !== false;
        this.bytesWritten = 0;
        this.fd = null;
        this.pending = true;
        if (options.encoding) this.setDefaultEncoding(options.encoding);

        if (options.fd != null) {
          this.fd = options.fd;
          this.pending = false;
          return;
        }
        // Node opens asynchronously, so an open error arrives as an 'error' event.
        let fd;
        try {
          fd = fs.openSync(path, this.flags, this.mode);
        } catch (e) {
          process.nextTick(() => this.destroy(e));
          return;
        }
        this.fd = fd;
        process.nextTick(() => {
          this.pending = false;
          if (this.destroyed) return;
          this.emit('open', fd);
          this.emit('ready');
        });
      }

      _write(chunk, encoding, callback) {
        try {
          const n = fs.writeSync(this.fd, chunk, 0, chunk.length, this.pos === undefined ? null : this.pos);
          if (this.pos !== undefined) this.pos += n;
          this.bytesWritten += n;
          callback();
        } catch (e) {
          callback(e);
        }
      }

      _destroy(err, callback) {
        if (this.fd === null || !this.autoClose) {
          callback(err);
          return;
        }
        const fd = this.fd;
        this.fd = null;
        try {
          fs.closeSync(fd);
        } catch (e) {
          if (!err) err = e;
        }
        callback(err);
      }

      close(cb) {
        if (typeof cb === 'function') {
          if (this.closed) process.nextTick(cb);
          else this.once('close', cb);
        }
        if (!this.writableEnded) this.end();
        else if (!this.destroyed) this.destroy();
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

  // Callback forms of the synchronous calls. They run the synchronous call on this thread and
  // call back on the next tick, as Node's always call back asynchronously.
  function callbackForm(sync, shape) {
    return function () {
      const args = Array.prototype.slice.call(arguments);
      const callback = args.pop();
      checkCallback(callback);
      let result;
      try {
        result = sync.apply(fs, args);
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
    mkdir: 'mkdirSync', rmdir: 'rmdirSync', unlink: 'unlinkSync', rename: 'renameSync',
    readdir: 'readdirSync', copyFile: 'copyFileSync', realpath: 'realpathSync'
  };
  for (const name of Object.keys(CALLBACK_FORMS)) {
    const sync = fs[CALLBACK_FORMS[name]];
    if (typeof fs[name] !== 'function' && typeof sync === 'function') fs[name] = callbackForm(sync);
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
    fs.write = callbackForm(fs.writeSync, (n, args) => [n, args[1]]);
  }
  if (typeof fs.read !== 'function') {
    const read = callbackForm(fs.readSync, (n, args) => [n, args[1]]);
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

  if (!fs.constants) {
    fs.constants = {
      F_OK: 0, R_OK: 4, W_OK: 2, X_OK: 1,
      O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2,
      COPYFILE_EXCL: 1, COPYFILE_FICLONE: 2, COPYFILE_FICLONE_FORCE: 4
    };
  }

  // A path reaches the filesystem as a C string, which ends at the first NUL, so 'a\0b' would
  // name the file 'a'. Node refuses such paths everywhere, before touching the disk.
  function quoted(value) {
    return "'" + value.replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\0/g, '\\x00') + "'";
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
    access: ['path'], open: ['path'], createReadStream: ['path'], createWriteStream: ['path']
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
