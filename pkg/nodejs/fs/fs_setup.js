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

  // readFileSync: Node ALWAYS throws when a read fails (ENOENT for a missing
  // path, etc.); libraries lean on that catchable error (e.g. y18n does
  // `JSON.parse(readFileSync(...))` inside a try/catch and treats an ENOENT as
  // "no locale file"). The native layer returns `undefined` on any read failure
  // instead of throwing, so a missing file silently became `JSON.parse(undefined)`
  // -> SyntaxError. Convert that sentinel back into a real Node-style error.
  const _readFileSync = fs.readFileSync;
  if (typeof _readFileSync === 'function') {
    fs.readFileSync = function (p) {
      const result = _readFileSync.apply(this, arguments);
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
      return result;
    };
  }

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
        this._hwm = hwm;
        this._fsId = -1;
        this._fsReading = false;
        this._fsDestroyed = false;
        this._fsEnded = false;
        this._fsFlowing = false;
        this._fsPaused = false;

        const start = typeof options.start === 'number' ? options.start : -1;
        const end = typeof options.end === 'number' ? options.end : -1;
        const res = fs._readStreamOpen(String(path), start, end);
        if (!res || res.error) {
          const err = fs.__makeFsError(
            (res && res.error) || 'open failed',
            (res && res.code) || 'ENOENT', path, 'open');
          process.nextTick(() => { if (!this._fsDestroyed) this.emit('error', err); });
          return;
        }
        this._fsId = res.id;
        process.nextTick(() => {
          if (this._fsDestroyed) return;
          this.emit('open', this._fsId);
          this.emit('ready');
        });
      }

      // Attaching a 'data' listener switches a readable into flowing mode in
      // Node (and Stream.prototype.pipe relies on that). `on` is the base
      // EventEmitter primitive here (addListener/once both delegate to it), so
      // overriding it alone covers every listen path without recursion.
      on(event, listener) {
        super.on(event, listener);
        if (event === 'data') this.resume();
        return this;
      }

      resume() {
        if (this._fsDestroyed || this._fsEnded) return this;
        this._fsPaused = false;
        this._fsFlowing = true;
        this._fsPump();
        return this;
      }

      pause() {
        this._fsFlowing = false;
        this._fsPaused = true;
        return this;
      }

      _fsPump() {
        if (this._fsDestroyed || this._fsEnded || this._fsReading ||
          this._fsPaused || !this._fsFlowing || this._fsId < 0) {
          return;
        }
        this._fsReading = true;
        fs._readStreamRead(this._fsId, this._hwm, (err, chunk) => {
          this._fsReading = false;
          if (this._fsDestroyed) return;
          if (err) {
            const e = fs.__makeFsError(err, 'EIO', this.path, 'read');
            this._closeNative();
            this.emit('error', e);
            return;
          }
          if (chunk === null || chunk === undefined) {
            this._fsEnded = true;
            this._closeNative();
            this.emit('end');
            this.emit('close');
            return;
          }
          const buf = Buffer.from(chunk, 'latin1');
          this.bytesRead += buf.length;
          this.emit('data', buf);
          if (this._fsFlowing && !this._fsPaused) this._fsPump();
        });
      }

      _closeNative() {
        if (this._fsId >= 0) {
          try { fs._readStreamClose(this._fsId); } catch (e) {}
          this._fsId = -1;
        }
      }

      destroy(err) {
        if (this._fsDestroyed) return this;
        this._fsDestroyed = true;
        this.destroyed = true;
        this._closeNative();
        process.nextTick(() => {
          if (err) this.emit('error', err);
          this.emit('close');
        });
        return this;
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
