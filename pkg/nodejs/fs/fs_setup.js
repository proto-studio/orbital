'use strict';
// fs.Stats and fs.Dirent, and the wrappers that make the native layer's plain objects into
// them. Node code calls their methods (stat.isFile(), dirent.isDirectory()), and some checks
// `instanceof fs.Stats`, so the sync, callback and promise APIs all return these classes.
(function () {
  const fs = globalThis.__fs_module;
  if (!fs) return;

  const S_IFMT = 0o170000, S_IFREG = 0o100000, S_IFDIR = 0o040000, S_IFCHR = 0o020000,
    S_IFBLK = 0o060000, S_IFIFO = 0o010000, S_IFLNK = 0o120000, S_IFSOCK = 0o140000;

  function dateFromMs(ms) {
    return new Date(Math.round(Number(ms)));
  }

  // new fs.Stats(dev, mode, nlink, uid, gid, rdev, blksize, ino, size, blocks, atimeMs,
  // mtimeMs, ctimeMs, birthtimeMs), as in Node, where it is also callable without new.
  function Stats(dev, mode, nlink, uid, gid, rdev, blksize, ino, size, blocks,
    atimeMs, mtimeMs, ctimeMs, birthtimeMs) {
    if (!(this instanceof Stats)) {
      return new Stats(dev, mode, nlink, uid, gid, rdev, blksize, ino, size, blocks,
        atimeMs, mtimeMs, ctimeMs, birthtimeMs);
    }
    this.dev = dev;
    this.mode = mode;
    this.nlink = nlink;
    this.uid = uid;
    this.gid = gid;
    this.rdev = rdev;
    this.blksize = blksize;
    this.ino = ino;
    this.size = size;
    this.blocks = blocks;
    this.atimeMs = atimeMs;
    this.mtimeMs = mtimeMs;
    this.ctimeMs = ctimeMs;
    this.birthtimeMs = birthtimeMs;
    this.atime = dateFromMs(atimeMs);
    this.mtime = dateFromMs(mtimeMs);
    this.ctime = dateFromMs(ctimeMs);
    this.birthtime = dateFromMs(birthtimeMs);
  }
  function isType(type) {
    return function () { return (Number(this.mode) & S_IFMT) === type; };
  }
  Object.assign(Stats.prototype, {
    isDirectory: isType(S_IFDIR),
    isFile: isType(S_IFREG),
    isBlockDevice: isType(S_IFBLK),
    isCharacterDevice: isType(S_IFCHR),
    isSymbolicLink: isType(S_IFLNK),
    isFIFO: isType(S_IFIFO),
    isSocket: isType(S_IFSOCK)
  });

  // libuv's entry types, which the native readdir reports.
  const UV_DIRENT_FILE = 1, UV_DIRENT_DIR = 2, UV_DIRENT_LINK = 3, UV_DIRENT_FIFO = 4,
    UV_DIRENT_SOCKET = 5, UV_DIRENT_CHAR = 6, UV_DIRENT_BLOCK = 7;
  const kType = Symbol('type');
  class Dirent {
    constructor(name, type, path) {
      this.name = name;
      this.parentPath = path;
      this.path = path;
      this[kType] = type;
    }
    isDirectory() { return this[kType] === UV_DIRENT_DIR; }
    isFile() { return this[kType] === UV_DIRENT_FILE; }
    isBlockDevice() { return this[kType] === UV_DIRENT_BLOCK; }
    isCharacterDevice() { return this[kType] === UV_DIRENT_CHAR; }
    isSymbolicLink() { return this[kType] === UV_DIRENT_LINK; }
    isFIFO() { return this[kType] === UV_DIRENT_FIFO; }
    isSocket() { return this[kType] === UV_DIRENT_SOCKET; }
  }
  fs.Stats = Stats;

  // The Stats that { bigint: true } asks for: BigInt fields, and times in nanoseconds too.
  class BigIntStats {}
  Object.assign(BigIntStats.prototype, Stats.prototype);
  const NS_PER_MS = 1000000n;
  // The exact times in nanoseconds, as decimal strings, which the native layer gives beside
  // the millisecond numbers, since a Number can't hold them.
  const kNs = Symbol('ns');
  const kExact = Symbol('exact');
  function toBigIntStats(s) {
    const b = new BigIntStats();
    ['dev', 'mode', 'nlink', 'uid', 'gid', 'rdev', 'blksize', 'ino', 'size', 'blocks'].forEach((key, i) => {
      b[key] = s[kExact] ? BigInt(s[kExact][i]) : BigInt(Math.trunc(s[key]));
    });
    // Whole milliseconds come from the exact nanoseconds when there are any, since the
    // Number can round up into the next millisecond.
    ['atime', 'mtime', 'ctime', 'birthtime'].forEach((key, i) => {
      if (s[kNs]) {
        const ns = BigInt(s[kNs][i]);
        b[key + 'Ms'] = ns >= 0n ? ns / NS_PER_MS : -((-ns + NS_PER_MS - 1n) / NS_PER_MS);
      } else {
        b[key + 'Ms'] = BigInt(Math.floor(s[key + 'Ms']));
      }
    });
    ['atime', 'mtime', 'ctime', 'birthtime'].forEach((key, i) => {
      const ms = s[key + 'Ms'];
      b[key + 'Ns'] = s[kNs] ? BigInt(s[kNs][i])
        : BigInt(Math.floor(ms)) * NS_PER_MS + BigInt(Math.round((ms - Math.floor(ms)) * 1e6));
    });
    for (const key of ['atime', 'mtime', 'ctime', 'birthtime']) b[key] = s[key];
    return b;
  }
  fs.__toBigIntStats = toBigIntStats;
  fs.Dirent = Dirent;

  // An fs.Stats from the native layer's description, or the value itself if it is a failure
  // or already a Stats.
  function addStatsMethods(s) {
    if (!s || typeof s !== 'object' || s.__fsError || s instanceof Stats) return s;
    const stats = new Stats(s.dev, s.mode, s.nlink, s.uid, s.gid, s.rdev, s.blksize, s.ino, s.size,
      s.blocks, s.atimeMs, s.mtimeMs, s.ctimeMs, s.birthtimeMs);
    if (Array.isArray(s.timesNs)) Object.defineProperty(stats, kNs, { value: s.timesNs });
    if (Array.isArray(s.exact)) Object.defineProperty(stats, kExact, { value: s.exact });
    return stats;
  }
  // Exposed so the promise and callback layers return the same class.
  fs.__addStatsMethods = addStatsMethods;
  // A Dirent for each { name, type } entry the native readdir returns for withFileTypes.
  fs.__direntsOf = function (entries, path) {
    if (!Array.isArray(entries)) return entries;
    const parent = typeof path === 'string' ? path : String(path);
    return entries.map((e) => (e && typeof e === 'object' && !(e instanceof Dirent) ? new Dirent(e.name, e.type, parent) : e));
  };

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
  // Decodes UTF-8 without Buffer, as Buffer does: a byte that doesn't fit gives U+FFFD.
  function utf8Text(bytes) {
    let out = '';
    for (let i = 0; i < bytes.length;) {
      const b = bytes[i];
      let need = 0;
      let cp = 0;
      let min = 0;
      if (b < 0x80) {
        out += String.fromCharCode(b);
        i++;
        continue;
      } else if (b >= 0xc2 && b <= 0xdf) {
        need = 1; cp = b & 0x1f; min = 0x80;
      } else if (b >= 0xe0 && b <= 0xef) {
        need = 2; cp = b & 0x0f; min = 0x800;
      } else if (b >= 0xf0 && b <= 0xf4) {
        need = 3; cp = b & 0x07; min = 0x10000;
      } else {
        out += '\ufffd';
        i++;
        continue;
      }
      let j = 1;
      for (; j <= need && i + j < bytes.length && (bytes[i + j] & 0xc0) === 0x80; j++) cp = (cp << 6) | (bytes[i + j] & 0x3f);
      if (j <= need || cp < min || cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff)) {
        out += '\ufffd';
        i += j; // the lead byte and the continuation bytes that fit
        continue;
      }
      out += String.fromCodePoint(cp);
      i += need + 1;
    }
    return out;
  }

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
      if (result.errorType === 'RangeError') {
        throw codedError(RangeError, result.code, result.message);
      }
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

  // The asynchronous forms of the native calls (fs.__mkdirAsync and so on) do their work off
  // the JavaScript thread, as Node's threadpool does, and call back with (failure, value).
  const asyncNative = {};
  for (const key of Object.keys(fs)) {
    const match = /^__(\w+)Async$/.exec(key);
    if (match) {
      asyncNative[match[1]] = fs[key];
      delete fs[key];
    }
  }
  // Runs a native call off the thread, and calls back with Node's error or finish(value).
  function runAsync(name, args, callback, finish) {
    asyncNative[name].apply(fs, args.concat((failure, value) => {
      let error = null;
      let result;
      try {
        check(failure);
        result = finish ? finish(value) : value;
      } catch (e) {
        error = e;
      }
      if (error) callback(error);
      else if (result === undefined) callback(null);
      else callback(null, result);
    }));
  }

  // The native open takes only a numeric mode, so an octal string such as '600' is parsed and
  // a bad mode refused here, as Node does.
  function openArgs(args) {
    if (args.length >= 3) args[2] = writeMode({ mode: args[2] });
    return args;
  }
  const _openSync = fs.openSync;
  fs.openSync = function () {
    return check(_openSync.apply(this, openArgs(Array.prototype.slice.call(arguments))));
  };

  // These calls return a failure description too.
  for (const name of ['mkdirSync', 'rmdirSync', 'unlinkSync', 'readdirSync', 'renameSync', 'copyFileSync',
    'closeSync', 'fstatSync']) {
    const native = fs[name];
    if (typeof native === 'function') {
      fs[name] = function () {
        return check(native.apply(this, arguments));
      };
    }
  }

  // statSync and lstatSync throw Node's error, except that { throwIfNoEntry: false } makes a
  // missing path return undefined instead.
  for (const name of ['statSync', 'lstatSync']) {
    const native = fs[name];
    if (typeof native !== 'function') continue;
    fs[name] = function (path, options) {
      const result = native.call(this, path);
      if (result && result.__fsError && result.code === 'ENOENT' &&
          options && typeof options === 'object' && options.throwIfNoEntry === false) {
        return undefined;
      }
      const stats = check(result);
      return options && typeof options === 'object' && options.bigint ? toBigIntStats(stats) : stats;
    };
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
      if (err) return callback(err);
      let decoded;
      try {
        decoded = decodeContents(contents, encoding);
      } catch (e) {
        return callback(e);
      }
      callback(null, decoded);
    });
  };

  const _writeFile = fs.writeFile;
  fs.writeFile = function (path, data, options, callback) {
    if (typeof options === 'function') {
      callback = options;
      options = undefined;
    }
    checkCallback(callback);
    if (typeof path === 'number') return writeAll(path, dataBytes(data, encodingOf(options)), callback);
    const bytes = base64Of(dataBytes(data, encodingOf(options)));
    _writeFile.call(this, path, bytes, writeFlag(options, 'w'), writeMode(options), callback);
  };

  // writeSync(fd, buffer[, offset[, length[, position]]]), writeSync(fd, buffer[, options])
  // or writeSync(fd, string[, position[, encoding]]).
  // writeArgs gives the native write's arguments: the descriptor, the bytes as base64, and
  // the position or -1.
  function writeArgs(fd, data, offsetOrPosition, lengthOrEncoding, position) {
    if (typeof data === 'string') {
      return [fd, base64Of(dataBytes(data, lengthOrEncoding)), positionArg(offsetOrPosition)];
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
    return [fd, base64Of(bytes), positionArg(position)];
  }
  const _writeSync = fs.writeSync;
  fs.writeSync = function () {
    return check(_writeSync.apply(this, writeArgs.apply(null, arguments)));
  };

  // readSync(fd, buffer[, offset[, length[, position]]]) or readSync(fd, buffer[, options]).
  // readArgs checks a read's arguments. It gives the native read's arguments, and where the
  // bytes go, or null for a read of nothing.
  function readArgs(argc, fd, buffer, offsetOrOptions, length, position) {
    if (!ArrayBuffer.isView(buffer)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "buffer" argument must be an instance of Buffer, TypedArray, or DataView.');
    }
    let offset = offsetOrOptions;
    if (argc <= 3 || (offset !== null && typeof offset === 'object')) {
      ({ offset = 0, length = buffer.byteLength - offset, position = null } = offset || {});
    }
    if (offset === undefined || offset === null) offset = 0;
    checkRange('offset', offset, buffer.byteLength);
    length |= 0;
    if (length === 0) return null;
    checkRange('length', length, buffer.byteLength - offset);
    return { args: [fd, length, positionArg(position)], buffer, offset };
  }
  // Copies the bytes a read returned into its buffer, and gives how many there were.
  function readInto(read, contents) {
    const bytes = bytesOf(contents);
    new Uint8Array(read.buffer.buffer, read.buffer.byteOffset, read.buffer.byteLength).set(bytes, read.offset);
    return bytes.length;
  }
  const _readSync = fs.readSync;
  fs.readSync = function (fd, buffer, offsetOrOptions, length, position) {
    const read = readArgs(arguments.length, fd, buffer, offsetOrOptions, length, position);
    if (!read) return 0;
    return readInto(read, check(_readSync.apply(this, read.args)));
  };

  // readdirSync(path, { withFileTypes: true }) returns fs.Dirent objects, which the native
  // layer describes by name and type.
  const _readdirSync = fs.readdirSync;
  if (typeof _readdirSync === 'function') {
    fs.readdirSync = function (path) {
      return fs.__direntsOf(_readdirSync.apply(this, arguments), path);
    };
  }


  // Node errno values for the codes the native async layer produces. Real
  // packages (send, http-errors) branch on err.code, and occasionally err.errno.
  const ERRNO = { ENOENT: -2, EACCES: -13, EINVAL: -22, EIO: -5, EBADF: -9, EMFILE: -24 };

  // Shared error factory: the Go async layer (fs.stat/access/createReadStream)
  // calls this so failures surface as real Error instances with a Node `code`,
  // rather than bare strings. Kept here because constructing/adorning Errors is
  // far simpler in JS than across the V8 boundary.
  // The Error a native failure describes, for the natives' callbacks.
  fs.__fromFailure = function (failure) {
    try {
      check(failure);
    } catch (e) {
      return e;
    }
    return failure;
  };

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
        // A Buffer path in the path wrapper's byte form goes on as it is, for the native side.
        const res = fs._readStreamOpen(path && typeof path === 'object' && path.__pathBytes ? path : String(path), start, end);
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
        // Node destroys a stream whose signal has already fired before it starts the open, so
        // that destroy doesn't wait for the open.
        this._destroyWaitsForOpen = !(options.signal && options.signal.aborted);
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
          // Destroyed while the open was under way: as in Node, the open is announced first,
          // and then the destroy goes ahead and closes the descriptor.
          const destroyAfterOpen = this._destroyAfterOpen;
          if (destroyAfterOpen) {
            this._destroyAfterOpen = null;
            fail();
            this.emit('open', this.fd);
            this.emit('ready');
            destroyAfterOpen();
            return;
          }
          // Destroyed before the open began, by an abort signal that had already fired: the
          // good open is still announced, after the destroy's own events, as in Node.
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

      _final(callback) {
        if (this._opening || this._openError) {
          this._waiting = callback;
          return;
        }
        callback();
      }

      _write(chunk, encoding, callback) {
        if (this._opening || this._openError) {
          this._waiting = (err) => (err ? callback(err) : this._write(chunk, encoding, callback));
          return;
        }
        // The write runs off the thread; the next waits for it, as Writable writes one at a time.
        const position = this.pos === undefined ? null : this.pos;
        if (this.pos !== undefined) this.pos += chunk.length;
        runAsync('write', writeArgs(this.fd, chunk, 0, chunk.length, position), (err, n) => {
          if (err) return callback(err);
          this.bytesWritten += n;
          callback();
        });
      }

      // Destroying closes the descriptor whatever autoClose says, as in Node.
      _destroy(err, callback) {
        if (this._opening && !this._openError && this._destroyWaitsForOpen) {
          // Node waits for an open in progress, and closes the descriptor it gives.
          this._destroyAfterOpen = () => this._destroy(err, callback);
          return;
        }
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
        // The close waits for the writes on the descriptor before it.
        runAsync('close', [fd], (e) => callback(err || e));
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

  function rmIsDirectory(path) {
    const e = new Error('Path is a directory: rm returned EISDIR (is a directory) ' + path);
    e.name = 'SystemError';
    e.code = 'ERR_FS_EISDIR';
    e.errno = 21;
    e.syscall = 'rm';
    e.path = String(path);
    return e;
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
      if (stats.isDirectory() && !recursive) throw rmIsDirectory(path);
      try {
        if (stats.isDirectory()) fs.rmdirSync(path, { recursive: true });
        else fs.unlinkSync(path);
      } catch (e) {
        if (!(force && e.code === 'ENOENT')) throw e;
      }
    };
  }

  // The callback forms. Their native calls run off the JavaScript thread, as Node's do on its
  // threadpool; calls on one descriptor run in the order they were made. Bad arguments throw
  // at once, as in Node.

  // Splits the callback, which is always last, from the arguments before it.
  function withCallback(args) {
    args = Array.prototype.slice.call(args);
    const callback = args.pop();
    checkCallback(callback);
    return [args, callback];
  }
  // Calls back on the next tick, for a call that has nothing to do off the thread.
  function later(callback, error, ...values) {
    process.nextTick(() => (error ? callback(error) : callback(null, ...values)));
  }

  fs.open = function (path, flags, mode, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('open', openArgs(args), cb);
  };
  fs.close = function (fd, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('close', args.slice(0, 1), cb);
  };
  fs.fstat = function (fd, options, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('fstat', args.slice(0, 1), cb, addStatsMethods);
  };
  // mkdir(path[, mode], callback) or mkdir(path[, { recursive, mode }], callback).
  fs.mkdir = function (path, options, callback) {
    const [args, cb] = withCallback(arguments);
    const { recursive, mode } = mkdirOptions(args[1]);
    runAsync('mkdir', [args[0], { recursive }, mode], cb);
  };
  fs.rmdir = function (path, options, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('rmdir', args.slice(0, 2), cb);
  };
  fs.unlink = function (path, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('unlink', args.slice(0, 1), cb);
  };
  fs.rename = function (oldPath, newPath, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('rename', args.slice(0, 2), cb);
  };
  fs.copyFile = function (src, dest, mode, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('copyFile', args.slice(0, 3), cb);
  };
  fs.readdir = function (path, options, callback) {
    const [args, cb] = withCallback(arguments);
    runAsync('readdir', args.slice(0, 2), cb, (entries) => fs.__direntsOf(entries, args[0]));
  };
  // appendFile(path, data[, options], callback); given a descriptor it writes there.
  fs.appendFile = function (path, data, options, callback) {
    if (typeof options === 'function') {
      callback = options;
      options = undefined;
    }
    checkCallback(callback);
    if (typeof path === 'number') return writeAll(path, dataBytes(data, encodingOf(options)), callback);
    const bytes = base64Of(dataBytes(data, encodingOf(options)));
    _writeFile.call(fs, path, bytes, writeFlag(options, 'a'), writeMode(options), callback);
  };
  // Writes all of bytes at the descriptor's offset, as writeFile and appendFile given one do.
  function writeAll(fd, bytes, callback) {
    runAsync('write', [fd, base64Of(bytes), -1], (err) => callback(err || null));
  }
  // write calls back with (err, bytesWritten, buffer or string).
  fs.write = function (fd, data) {
    const [args, cb] = withCallback(arguments);
    runAsync('write', writeArgs.apply(null, args), (err, n) => (err ? cb(err) : cb(null, n, data)));
  };
  // read calls back with (err, bytesRead, buffer). read(fd, callback) and read(fd, options,
  // callback) read into a new 16 KiB buffer.
  fs.read = function (fd, buffer, offset, length, position, callback) {
    let [args, cb] = withCallback(arguments);
    if (args.length === 1 || (args.length === 2 && args[1] && !ArrayBuffer.isView(args[1]))) {
      const options = args[1] || {};
      const target = options.buffer || Buffer.alloc(16384);
      const start = options.offset || 0;
      args = [fd, target, start, options.length === undefined ? target.byteLength - start : options.length,
        options.position === undefined ? null : options.position];
    }
    const read = readArgs(args.length, ...args);
    if (!read) return later(cb, null, 0, args[1]);
    runAsync('read', read.args, (err, contents) => (err ? cb(err) : cb(null, readInto(read, contents), read.buffer)));
  };
  // rm(path[, options], callback): as rmSync, with each step off the thread.
  fs.rm = function (path, options, callback) {
    const [args, cb] = withCallback(arguments);
    const force = !!(args[1] && args[1].force);
    const recursive = !!(args[1] && args[1].recursive);
    runAsync('rmTarget', [path], (err, stats) => {
      if (err) return cb(force && err.code === 'ENOENT' ? null : err);
      if (stats.isDirectory() && !recursive) return cb(rmIsDirectory(path));
      const done = (e) => cb(e && !(force && e.code === 'ENOENT') ? e : null);
      if (stats.isDirectory()) runAsync('rmdir', [path, { recursive: true }], done);
      else runAsync('unlink', [path], done);
    }, addStatsMethods);
  };

  // Argument checks for the calls below, with Node's errors.
  function checkInt(name, value, min, max) {
    if (typeof value !== 'number') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type number. Received ' + receivedType(value));
    }
    if (!Number.isInteger(value)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be an integer. Received ' + value);
    }
    if (value < min || value > max) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be >= ' + min + ' && <= ' + max + '. Received ' + receivedNumber(value));
    }
    return value;
  }
  const fdArg = (fd) => checkInt('fd', fd, 0, 2147483647);
  const idArg = (name, id) => checkInt(name, id, -1, 4294967295);
  // A mode that chmod requires: a number, or a string of octal digits.
  function modeArg(mode) {
    if (mode === undefined || mode === null) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "mode" argument must be of type number. Received ' + receivedType(mode));
    }
    return writeMode({ mode });
  }
  // Node's toUnixTimestamp: seconds, from a number, a numeric string or a Date. A negative
  // number means now.
  function timeArg(time) {
    if (typeof time === 'string' && +time == time) return +time; // eslint-disable-line eqeqeq
    if (typeof time === 'number' && Number.isFinite(time)) return time < 0 ? Date.now() / 1000 : time;
    if (time instanceof Date) return time.getTime() / 1000;
    throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "time" argument must be an instance of Date or an Time in seconds. Received ' + receivedType(time));
  }
  // A length for truncate: an integer, where a negative one means 0.
  function lenArg(len) {
    if (len === undefined || len === null) return 0;
    return Math.max(0, checkInt('len', len, Number.MIN_SAFE_INTEGER, Number.MAX_SAFE_INTEGER));
  }
  // access's mode: F_OK when missing, or an integer from 0 to 7.
  function accessMode(mode) {
    if (mode === undefined || mode === null) return 0;
    if (typeof mode !== 'number' || mode !== (mode | 0)) {
      if (typeof mode === 'number' && Number.isFinite(mode)) mode |= 0;
      else throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'mode must be int32 or null/undefined');
    }
    if (mode < 0 || mode > 7) throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'mode is out of range: >= 0 && <= 7');
    return mode;
  }
  function checkBuffers(buffers) {
    if (!Array.isArray(buffers) || !buffers.every((b) => ArrayBuffer.isView(b))) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "buffers" argument must be an ArrayBufferView[]. Received ' + receivedType(buffers));
    }
    return buffers;
  }

  // Defines fs[name + 'Sync'] and its callback form fs[name] over a native call. prepare
  // checks and normalizes the arguments, throwing as Node does before touching the file, so a
  // bad argument throws from the callback form too. nativeOf names the native call for the
  // prepared arguments, and finish makes its result into the call's value.
  const nativeSync = {};
  function defineCall(name, prepare, nativeOf, finish) {
    const pick = typeof nativeOf === 'function' ? nativeOf : () => nativeOf;
    fs[name + 'Sync'] = function () {
      const prepared = prepare.apply(null, arguments);
      const result = check(nativeSync[pick(prepared)].apply(fs, prepared));
      return finish ? finish(result, prepared) : result;
    };
    fs[name] = function () {
      const [args, callback] = withCallback(arguments);
      const prepared = prepare.apply(null, args);
      runAsync(pick(prepared), prepared, callback, finish && ((result) => finish(result, prepared)));
    };
  }
  for (const name of ['truncate', 'ftruncate', 'fsync', 'fdatasync', 'chmod', 'fchmod', 'chown', 'fchown',
    'utimes', 'futimes', 'access', 'statfs', 'symlink', 'readlink', 'link', 'realpath', 'mkdtemp']) {
    nativeSync[name] = fs[name + 'Sync'];
    delete fs[name + 'Sync'];
  }

  // truncate also takes a descriptor, as Node's still does.
  defineCall('truncate', (path, len) => [typeof path === 'number' ? fdArg(path) : path, lenArg(len)], ([path]) => (typeof path === 'number' ? 'ftruncate' : 'truncate'));
  defineCall('ftruncate', (fd, len) => [fdArg(fd), lenArg(len)], 'ftruncate');
  defineCall('fsync', (fd) => [fdArg(fd)], 'fsync');
  defineCall('fdatasync', (fd) => [fdArg(fd)], 'fdatasync');
  defineCall('chmod', (path, mode) => [path, modeArg(mode)], 'chmod');
  defineCall('fchmod', (fd, mode) => [fdArg(fd), modeArg(mode)], 'fchmod');
  defineCall('chown', (path, uid, gid) => [path, idArg('uid', uid), idArg('gid', gid)], 'chown');
  defineCall('fchown', (fd, uid, gid) => [fdArg(fd), idArg('uid', uid), idArg('gid', gid)], 'fchown');
  defineCall('utimes', (path, atime, mtime) => [path, timeArg(atime), timeArg(mtime)], 'utimes');
  defineCall('futimes', (fd, atime, mtime) => [fdArg(fd), timeArg(atime), timeArg(mtime)], 'futimes');
  // access(path[, mode], callback).
  defineCall('access', (path, mode) => [path, accessMode(mode)], 'access', () => undefined);
  // symlink(target, path[, type]): type matters only on Windows, but is checked as in Node.
  function symlinkArgs(target, path, type) {
    if (type !== undefined && type !== null && !['dir', 'file', 'junction'].includes(type)) {
      throw codedError(Error, 'ERR_FS_INVALID_SYMLINK_TYPE',
        'Symlink type must be one of "dir", "file", or "junction". Received ' + JSON.stringify(type));
    }
    // The target goes to the native layer as base64 of its bytes, so a Buffer target that
    // isn't UTF-8 keeps its bytes.
    let bytes;
    if (ArrayBuffer.isView(target)) {
      checkPath(target, 'target');
      bytes = viewBytes(target);
    } else {
      bytes = dataBytes(checkedPath(target, 'target'));
    }
    return [base64Of(bytes), checkedPath(path, 'path')];
  }
  // pathString, refusing a path with a NUL, which a file: URL can hold as %00, as the other
  // calls refuse it.
  function checkedPath(p, name) {
    const text = pathString(p);
    checkPath(text, name);
    return text;
  }
  // A path given as a string, a Buffer or a file: URL, as a string. A URL is checked as
  // Node's fileURLToPath checks it.
  function pathString(p) {
    if (typeof p === 'string') return p;
    if (ArrayBuffer.isView(p)) return Buffer.from(p.buffer, p.byteOffset, p.byteLength).toString();
    if (p && typeof p === 'object' && typeof p.href === 'string' && typeof p.protocol === 'string') {
      if (p.protocol !== 'file:') throw codedError(TypeError, 'ERR_INVALID_URL_SCHEME', 'The URL must be of scheme file');
      if (p.hostname !== '' && p.hostname !== 'localhost') {
        throw codedError(TypeError, 'ERR_INVALID_FILE_URL_HOST', 'File URL host must be "localhost" or empty on ' + process.platform);
      }
      if (/%2f/i.test(p.pathname)) {
        throw codedError(TypeError, 'ERR_INVALID_FILE_URL_PATH', 'File URL path must not include encoded / characters');
      }
      return decodeURIComponent(p.pathname);
    }
    return p;
  }
  // A path the native layer gives, in the encoding options ask for: a string, or a Buffer for
  // 'buffer'.
  // The options, after checking their encoding, so a bad one fails before anything is done.
  function checkedEncoding(options) {
    encodingOf(options);
    return options;
  }
  // Bytes the native layer gives as a name, in the encoding options ask for.
  function bytesNamedIn(bytes, options) {
    const encoding = encodingOf(options);
    // Without Buffer, only UTF-8 is known, as encodingOf has checked.
    if (!hasBuffer()) return utf8Text(bytes);
    const buffer = Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    if (encoding === 'buffer') return buffer;
    return buffer.toString(encoding || 'utf8');
  }
  function namedIn(name, options) {
    const encoding = encodingOf(options);
    if (encoding === 'buffer') return Buffer.from(name);
    if (encoding && !isUtf8(encoding)) return Buffer.from(name).toString(encoding);
    return name;
  }
  defineCall('symlink', symlinkArgs, 'symlink');
  // file: URLs and Buffers become paths before they reach the native layer.
  // The native readlink gives the target's bytes as base64, since they need not be UTF-8.
  defineCall('readlink', (path, options) => [checkedPath(path, 'path'), checkedEncoding(options)], 'readlink',
    (target, [, options]) => bytesNamedIn(bytesOf(target), options));
  defineCall('link', (existingPath, newPath) => [checkedPath(existingPath, 'existingPath'), checkedPath(newPath, 'newPath')], 'link');
  // mkdtemp(prefix[, options]) makes prefix followed by six random characters, mode 0o700.
  function prefixArg(prefix) {
    // A Buffer prefix that isn't UTF-8 arrives already checked, in the path wrapper's byte form.
    if (prefix && typeof prefix === 'object' && typeof prefix.__pathBytes === 'string') return prefix;
    if (typeof prefix !== 'string' && !ArrayBuffer.isView(prefix) && !(prefix && typeof prefix.href === 'string')) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "prefix" argument must be of type string or an instance of Buffer or URL. Received ' + receivedType(prefix));
    }
    return checkedPath(prefix, 'prefix');
  }
  defineCall('mkdtemp', (prefix, options) => [prefixArg(prefix), checkedEncoding(options)], 'mkdtemp', (path, [, options]) => bytesNamedIn(bytesOf(path), options));

  // realpath.native names realpath in its errors, as the C call does.
  defineCall('realpath', (path, options) => [checkedPath(path, 'path'), checkedEncoding(options)], 'realpath', (real, [, options]) => bytesNamedIn(bytesOf(real), options));
  const realpathNativeSync = fs.realpathSync;
  const realpathNative = fs.realpath;
  // Node's JavaScript realpath walks an absolute path one name at a time. It fails at the
  // first name it can't get past, with that name's lstat error, or its stat error if the name
  // is a link, by the path the links before it led to. Once a prefix of the path fails to
  // resolve every longer one does, so that name is found by halving: a few calls, however
  // many names the path has. A prefix fails if the filesystem's realpath can't resolve it, or
  // if stat finds a loop there: a filesystem's realpath may follow more links than stat does.
  function asJsRealpathError(e, path) {
    if (!e || e.syscall !== 'realpath' || typeof path !== 'string' || !path.startsWith('/')) return e;
    const names = path.split('/').filter((name) => name !== '' && name !== '.');
    const prefix = (k) => '/' + names.slice(0, k).join('/');
    const fails = (k) => {
      try {
        fs.statSync(prefix(k));
      } catch (s) {
        if (s && s.code === 'ELOOP') return true;
      }
      try {
        realpathNativeSync(prefix(k));
        return false;
      } catch (ignored) {
        return true;
      }
    };
    let lo = 1;
    let hi = names.length;
    if (hi === 0 || !fails(hi)) return e;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (fails(mid)) hi = mid;
      else lo = mid + 1;
    }
    let base;
    try {
      base = lo === 1 ? '/' : realpathNativeSync(prefix(lo - 1));
    } catch (ignored) {
      return e;
    }
    const next = (base === '/' ? '' : base) + '/' + names[lo - 1];
    try {
      fs.lstatSync(next);
    } catch (l) {
      return l;
    }
    try {
      fs.statSync(next);
    } catch (s) {
      return s;
    }
    return e;
  }
  // Node's JavaScript realpath makes the path absolute and cleans it first; the native one
  // leaves ".." to apply after the links ahead of it.
  const lexical = (p) => (typeof p === 'string' && pathModule() ? fsResolve(p) : p);
  fs.realpathSync = function (path, options) {
    const absolute = lexical(pathString(path));
    try {
      return realpathNativeSync(absolute, options);
    } catch (e) {
      throw asJsRealpathError(e, absolute);
    }
  };
  fs.realpathSync.native = realpathNativeSync;
  fs.realpath = function (path, options, callback) {
    const [args, cb] = withCallback(arguments);
    args[0] = lexical(pathString(args[0]));
    realpathNative(...args, (err, real) => (err ? cb(asJsRealpathError(err, args[0])) : cb(null, real)));
  };
  fs.realpath.native = realpathNative;

  // exists(path, callback) calls back with only true or false.
  fs.exists = function (path, callback) {
    checkCallback(callback);
    try {
      fs.access(path, 0, (err) => callback(!err));
    } catch (e) {
      process.nextTick(callback, false);
    }
  };

  // fs.statfs: the description of the filesystem a path is on, with BigInt fields when asked.
  class StatFs {
    constructor(type, bsize, blocks, bfree, bavail, files, ffree) {
      this.type = type;
      this.bsize = bsize;
      this.blocks = blocks;
      this.bfree = bfree;
      this.bavail = bavail;
      this.files = files;
      this.ffree = ffree;
    }
  }
  defineCall('statfs', (path, options) => [path, !!(options && typeof options === 'object' && options.bigint)], 'statfs',
    (s, [, bigint]) => {
      const v = (n) => (bigint ? BigInt(n) : n);
      return new StatFs(v(s.type), v(s.bsize), v(s.blocks), v(s.bfree), v(s.bavail), v(s.files), v(s.ffree));
    });

  // readv and writev read into, or write from, each buffer in turn, stopping at the end of
  // the file. A numeric position moves along with them; any other means the file's offset.
  // They call back with (err, bytes, buffers).
  // A negative position, like none, means the file's offset.
  const vectorArgs = (fd, buffers, position) => [fdArg(fd), checkBuffers(buffers), typeof position === 'number' && position >= 0 ? position : -1];
  // Each vector goes to the native layer whole, as one call on the descriptor.
  const nativeVector = {};
  for (const name of ['readv', 'writev']) {
    nativeVector[name] = fs[name + 'Sync'];
    delete fs[name + 'Sync'];
  }
  const writevArgs = (fd, buffers, position) => [fd, buffers.map((b) => base64Of(viewBytes(b))), position];
  const readvArgs = (fd, buffers, position) => [fd, buffers.map((b) => b.byteLength), position];
  // Copies what readv read into its buffers, and gives the bytes read.
  function readvInto(buffers, parts) {
    let total = 0;
    parts.forEach((part, i) => {
      total += readInto({ buffer: buffers[i], offset: 0 }, part);
    });
    return total;
  }
  fs.readvSync = function () {
    const [fd, buffers, position] = vectorArgs.apply(null, arguments);
    return readvInto(buffers, check(nativeVector.readv.apply(fs, readvArgs(fd, buffers, position))));
  };
  fs.writevSync = function () {
    const [fd, buffers, position] = vectorArgs.apply(null, arguments);
    return check(nativeVector.writev.apply(fs, writevArgs(fd, buffers, position)));
  };
  // readv and writev call back with (err, bytes, buffers).
  fs.readv = function () {
    const [args, cb] = withCallback(arguments);
    const [fd, buffers, position] = vectorArgs.apply(null, args);
    runAsync('readv', readvArgs(fd, buffers, position), (err, n) => (err ? cb(err) : cb(null, n, buffers)),
      (parts) => readvInto(buffers, parts));
  };
  fs.writev = function () {
    const [args, cb] = withCallback(arguments);
    const [fd, buffers, position] = vectorArgs.apply(null, args);
    runAsync('writev', writevArgs(fd, buffers, position), (err, n) => (err ? cb(err) : cb(null, n, buffers)));
  };

  // fs.Dir, from opendir: the directory's entries, read when it is opened.
  function dirClosed() {
    return codedError(Error, 'ERR_DIR_CLOSED', 'Directory handle was closed');
  }
  class Dir {
    constructor(path, entries) {
      this._path = path;
      this._entries = entries;
      this._next = 0; // the next entry to give, so reading doesn't shift the array
      this._closed = false;
    }
    get path() { return this._path; }
    readSync() {
      if (this._closed) throw dirClosed();
      return this._next < this._entries.length ? this._entries[this._next++] : null;
    }
    read(callback) {
      if (typeof callback !== 'function') {
        return new Promise((resolve) => resolve(this.readSync()));
      }
      let entry;
      try {
        entry = this.readSync();
      } catch (e) {
        process.nextTick(callback, e);
        return;
      }
      process.nextTick(callback, null, entry);
    }
    closeSync() {
      if (this._closed) throw dirClosed();
      this._closed = true;
      this._entries = [];
    }
    close(callback) {
      if (typeof callback !== 'function') {
        return new Promise((resolve) => resolve(this.closeSync()));
      }
      try {
        this.closeSync();
      } catch (e) {
        process.nextTick(callback, e);
        return;
      }
      process.nextTick(callback, null);
    }
    async * entries() {
      try {
        for (;;) {
          const entry = await this.read();
          if (entry === null) break;
          yield entry;
        }
      } finally {
        if (!this._closed) await this.close();
      }
    }
    [Symbol.asyncIterator]() {
      return this.entries();
    }
  }
  fs.Dir = Dir;
  // Node's opendirSync names neither the path nor scandir in its error; its callback form
  // names the path.
  function opendirError(e, withPath) {
    if (e && e.syscall === 'scandir') {
      e.syscall = 'opendir';
      e.message = e.message.replace(/, scandir '.*'$/, withPath ? ", opendir '" + e.path + "'" : ', opendir');
      if (!withPath) delete e.path;
    }
    return e;
  }
  const dirPath = (path) => (typeof path === 'string' ? path : String(path));
  const isRecursive = (options) => !!(options && typeof options === 'object' && options.recursive);
  // With recursive, the entries of each directory follow, breadth first, as in Node.
  // The entries with their names in the encoding options ask for, once the walk that needs
  // the names as they are is done.
  function encodedDirents(entries, options) {
    const encoding = encodingOf(options);
    if (!encoding || isUtf8(encoding)) return entries;
    return entries.map((e) => {
      const name = encoding === 'buffer' ? Buffer.from(e.name) : Buffer.from(e.name).toString(encoding);
      return new Dirent(name, e[kType], e.parentPath);
    });
  }
  fs.opendirSync = function (path, options) {
    let entries;
    try {
      entries = fs.readdirSync(path, { withFileTypes: true });
      if (isRecursive(options)) {
        const p = pathModule();
        for (let i = 0; i < entries.length; i++) {
          if (entries[i].isDirectory()) {
            // One at a time, since a large directory has more entries than a call takes arguments.
            for (const entry of fs.readdirSync(p.join(entries[i].parentPath, entries[i].name), { withFileTypes: true })) {
              entries.push(entry);
            }
          }
        }
      }
    } catch (e) {
      throw opendirError(e, false);
    }
    return new Dir(dirPath(path), encodedDirents(entries, options));
  };
  fs.opendir = function (path, options, callback) {
    const [args, cb] = withCallback(arguments);
    encodingOf(args[1]); // a bad encoding throws now, as in Node, not in the callback
    const recursive = isRecursive(args[1]);
    fs.readdir(args[0], { withFileTypes: true }, (err, entries) => {
      if (err) return cb(opendirError(err, true));
      // Each subdirectory is read off the thread in turn.
      const more = (i) => {
        while (i < entries.length && !(recursive && entries[i].isDirectory())) i++;
        if (i === entries.length) return cb(null, new Dir(dirPath(args[0]), encodedDirents(entries, args[1])));
        fs.readdir(pathModule().join(entries[i].parentPath, entries[i].name), { withFileTypes: true }, (e, inner) => {
          if (e) return cb(opendirError(e, true));
          for (const entry of inner) entries.push(entry);
          more(i + 1);
        });
      };
      more(0);
    });
  };

  // fs.cp: copies a file, or with recursive a directory, as Node's cp does.
  function cpError(code, prefix, errnoCode, errno, message, path) {
    const e = new Error(prefix + ': cp returned ' + errnoCode + ' (' + message + ') ' + path);
    e.name = 'SystemError';
    e.code = code;
    e.errno = errno;
    e.syscall = 'cp';
    e.path = path;
    e.info = { message, path, syscall: 'cp', errno, code: errnoCode };
    return e;
  }
  function cpOptions(options) {
    const opts = Object.assign({ dereference: false, errorOnExist: false, force: true,
      preserveTimestamps: false, recursive: false, verbatimSymlinks: false, mode: 0 }, options);
    for (const key of ['dereference', 'errorOnExist', 'force', 'preserveTimestamps', 'recursive', 'verbatimSymlinks']) {
      if (typeof opts[key] !== 'boolean') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.' + key + '" property must be of type boolean. Received ' + receivedType(opts[key]));
      }
    }
    if (opts.filter !== undefined && typeof opts.filter !== 'function') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.filter" property must be of type function. Received ' + receivedType(opts.filter));
    }
    return opts;
  }
  const pathModule = () => globalThis.__path_module || (typeof require === 'function' ? require('path') : null);

  // cp's steps, written once as a generator that yields each filesystem call it needs as
  // [name, ...args]. cpSync runs the calls synchronously; cp runs them off the thread, and
  // awaits a filter that returns a promise.
  // A path made absolute as the filesystem takes it: from the working directory where
  // relative paths start there, and otherwise from the root.
  function fsResolve(p) {
    const path = pathModule();
    return cwdRelative || path.isAbsolute(p) ? path.resolve(p) : path.resolve(path.sep, p);
  }
  function* cpSteps(src, dest, opts) {
    const path = pathModule();
    const stat = opts.dereference ? 'stat' : 'lstat';
    const srcStat = yield [stat, src];
    const destStat = yield [stat + 'OrNothing', dest];
    checkPair(srcStat, destStat, src, dest);
    if (srcStat.isDirectory()) {
      const from = fsResolve(src);
      const to = fsResolve(dest);
      // The separator is added only where from doesn't already end with one, as the root does.
      if (to === from || to.startsWith(from.endsWith(path.sep) ? from : from + path.sep)) {
        throw cpError('ERR_FS_CP_EINVAL', 'Invalid src or dest', 'EINVAL', 22,
          'cannot copy ' + src + ' to a subdirectory of self ' + dest, dest);
      }
      // A parent of dest reached through a link may still be src, so each existing one is
      // compared with src by inode, as Node does.
      if (srcStat.ino) {
        for (let parent = path.dirname(to); ; parent = path.dirname(parent)) {
          const parentStat = yield ['statOrNothing', parent];
          if (parentStat && parentStat.ino === srcStat.ino && parentStat.dev === srcStat.dev) {
            throw cpError('ERR_FS_CP_EINVAL', 'Invalid src or dest', 'EINVAL', 22,
              'cannot copy ' + src + ' to a subdirectory of self ' + dest, dest);
          }
          if (parent === path.dirname(parent)) break;
        }
      }
    }
    if (opts.filter && !(yield ['filter', src, dest])) return;
    // Like Node's, cp makes the destination's missing parent directories.
    yield ['mkdirParents', path.dirname(dest)];
    yield* copyEntry(srcStat, destStat, src, dest, opts);
  }
  // Refuses a copy onto the same file, or of a directory onto a non-directory or the other way
  // round, as Node checks the top pair and each pair inside a directory.
  function checkPair(srcStat, destStat, src, dest) {
    if (!destStat) return;
    // The paths are compared too, for a filesystem that reports no inode numbers.
    if (fsResolve(src) === fsResolve(dest) ||
        (srcStat.ino && srcStat.ino === destStat.ino && srcStat.dev === destStat.dev)) {
      throw cpError('ERR_FS_CP_EINVAL', 'Invalid src or dest', 'EINVAL', 22, 'src and dest cannot be the same', dest);
    }
    if (srcStat.isDirectory() && !destStat.isDirectory()) {
      throw cpError('ERR_FS_CP_DIR_TO_NON_DIR', 'Cannot overwrite non-directory with directory', 'EISDIR', 21,
        'cannot overwrite non-directory ' + dest + ' with directory ' + src, dest);
    }
    if (!srcStat.isDirectory() && destStat.isDirectory()) {
      throw cpError('ERR_FS_CP_NON_DIR_TO_DIR', 'Cannot overwrite directory with non-directory', 'ENOTDIR', 20,
        'cannot overwrite directory ' + dest + ' with non-directory ' + src, dest);
    }
  }
  function* copyEntry(srcStat, destStat, src, dest, opts) {
    const stat = opts.dereference ? 'stat' : 'lstat';
    if (srcStat.isSymbolicLink()) {
      // A link is copied as a link. Its relative target is made absolute, as from the source's
      // directory, unless verbatimSymlinks asks for it as it is.
      const path = pathModule();
      let target = yield ['readlink', src];
      if (!opts.verbatimSymlinks && !path.isAbsolute(target)) {
        // With a root, a relative src is from the root, not the working directory.
        const dir = cwdRelative || path.isAbsolute(src) ? path.dirname(src) : path.join(path.sep, path.dirname(src));
        target = path.resolve(dir, target);
      }
      // Only a link in the way is replaced. Anything else stays, and the symlink fails with
      // EEXIST, as in Node, so copying a link onto its own target can't remove that target.
      if (destStat && destStat.isSymbolicLink()) yield ['unlink', dest];
      yield ['symlink', target, dest];
      return;
    }
    if (srcStat.isDirectory()) {
      if (!opts.recursive) {
        // Node's errno here is EINVAL from cpSync and EISDIR from cp.
        throw cpError('ERR_FS_EISDIR', 'Path is a directory', 'EISDIR', opts.async ? 21 : 22, src + ' is a directory (not copied)', src);
      }
      // The directory gets its mode after its contents, so a read-only one can be filled.
      if (!destStat) yield ['mkdir', dest];
      const path = pathModule();
      for (const name of yield ['readdir', src]) {
        const from = path.join(src, name);
        const to = path.join(dest, name);
        // A rejected directory is skipped whole.
        if (opts.filter && !(yield ['filter', from, to])) continue;
        const fromStat = yield [stat, from];
        const toStat = yield [stat + 'OrNothing', to];
        checkPair(fromStat, toStat, from, to);
        yield* copyEntry(fromStat, toStat, from, to, opts);
      }
      if (!destStat) yield ['chmod', dest, srcStat.mode & 0o7777];
      return;
    }
    if (destStat) {
      if (!opts.force) {
        if (opts.errorOnExist) {
          throw cpError('ERR_FS_CP_EEXIST', 'Target already exists', 'EEXIST', 17, dest + ' already exists', dest);
        }
        return;
      }
      yield ['unlink', dest];
    }
    yield ['copyFile', src, dest, opts.mode];
    if (opts.preserveTimestamps) yield ['utimes', dest, srcStat.atime, srcStat.mtime];
    yield ['chmod', dest, srcStat.mode & 0o7777];
  }
  // A stat that gives undefined for a missing path.
  const orNothing = (stat) => (p) => {
    try {
      return stat(p);
    } catch (e) {
      if (e.code === 'ENOENT') return undefined;
      throw e;
    }
  };
  const cpSyncCalls = {
    stat: (p) => fs.statSync(p), lstat: (p) => fs.lstatSync(p),
    statOrNothing: orNothing((p) => fs.statSync(p)), lstatOrNothing: orNothing((p) => fs.lstatSync(p)),
    readdir: (p) => fs.readdirSync(p), mkdir: (p, o) => fs.mkdirSync(p, o), chmod: (p, m) => fs.chmodSync(p, m),
    mkdirParents: (p) => { if (!fs.existsSync(p)) fs.mkdirSync(p, { recursive: true }); },
    readlink: (p) => fs.readlinkSync(p), symlink: (target, p) => fs.symlinkSync(target, p),
    unlink: (p) => fs.unlinkSync(p), copyFile: (a, b, m) => fs.copyFileSync(a, b, m),
    utimes: (p, a, m) => fs.utimesSync(p, a, m)
  };
  fs.cpSync = function (src, dest, options) {
    // Buffer and URL paths become strings, for the path helpers.
    src = pathString(src);
    dest = pathString(dest);
    const opts = cpOptions(options);
    const steps = cpSteps(src, dest, opts);
    let step = steps.next();
    while (!step.done) {
      const [name, ...args] = step.value;
      let value;
      try {
        if (name === 'filter') {
          value = opts.filter(...args);
          if (value && typeof value.then === 'function') {
            throw codedError(TypeError, 'ERR_INVALID_RETURN_VALUE',
              'Expected boolean to be returned from the "filter" function but got an instance of Promise.');
          }
        } else {
          value = cpSyncCalls[name](...args);
        }
      } catch (e) {
        step = steps.throw(e);
        continue;
      }
      step = steps.next(value);
    }
  };
  // A filter's promise rejected with a falsy reason fails the copy too, as in Node.
  function falsyRejection(reason) {
    const e = codedError(Error, 'ERR_FALSY_VALUE_REJECTION', 'Promise was rejected with falsy value');
    e.reason = reason;
    return e;
  }
  const orNothingAsync = (stat) => (p, cb) => stat(p, (err, s) => (err && err.code === 'ENOENT' ? cb(null, undefined) : cb(err, s)));
  const cpAsyncCalls = {
    stat: (p, cb) => fs.stat(p, cb), lstat: (p, cb) => fs.lstat(p, cb),
    statOrNothing: orNothingAsync((p, cb) => fs.stat(p, cb)), lstatOrNothing: orNothingAsync((p, cb) => fs.lstat(p, cb)),
    readdir: (p, cb) => fs.readdir(p, cb), mkdir: (...args) => fs.mkdir(...args), chmod: (p, m, cb) => fs.chmod(p, m, cb),
    readlink: (p, cb) => fs.readlink(p, cb), symlink: (target, p, cb) => fs.symlink(target, p, cb),
    mkdirParents: (p, cb) => fs.access(p, (missing) => (missing ? fs.mkdir(p, { recursive: true }, cb) : cb(null))),
    unlink: (p, cb) => fs.unlink(p, cb), copyFile: (a, b, m, cb) => fs.copyFile(a, b, m, cb),
    utimes: (p, a, m, cb) => fs.utimes(p, a, m, cb)
  };
  fs.cp = function (src, dest, options, callback) {
    src = pathString(src);
    dest = pathString(dest);
    if (typeof options === 'function') {
      callback = options;
      options = undefined;
    }
    checkCallback(callback);
    const opts = Object.assign(cpOptions(options), { async: true });
    const steps = cpSteps(src, dest, opts);
    const advance = (err, value) => {
      let step;
      try {
        step = err ? steps.throw(err) : steps.next(value);
      } catch (e) {
        callback(e);
        return;
      }
      if (step.done) {
        callback(null);
        return;
      }
      const [name, ...args] = step.value;
      if (name === 'filter') {
        let answer;
        try {
          answer = opts.filter(...args);
        } catch (e) {
          advance(e || falsyRejection(e));
          return;
        }
        Promise.resolve(answer).then((keep) => advance(null, keep), (e) => advance(e || falsyRejection(e)));
        return;
      }
      cpAsyncCalls[name](...args, advance);
    };
    advance(null);
  };

  // fs.openAsBlob(path[, { type }]): a Blob of the file's contents, read when it is opened.
  fs.openAsBlob = function (path, options) {
    const type = options && typeof options === 'object' && options.type !== undefined ? options.type : '';
    if (typeof type !== 'string') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.type" property must be of type string. Received ' + receivedType(type));
    }
    let contents;
    try {
      contents = fs.readFileSync(path);
    } catch (e) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', 'Unable to open file as blob');
    }
    return Promise.resolve(new Blob([contents], { type }));
  };

  // fs.watchFile and fs.unwatchFile: poll a file's stats, and call the listeners with the new
  // and old stats when they change. A missing file has zeroed stats.
  const eventEmitter = () => {
    const events = globalThis.__events_module;
    return events && (events.EventEmitter || events);
  };
  function zeroStats() {
    return new Stats(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0);
  }
  const zeroFor = (bigint) => (bigint ? toBigIntStats(zeroStats()) : zeroStats());
  function statOrZero(path, bigint) {
    let s;
    try {
      s = fs.statSync(path);
    } catch (e) {
      s = zeroStats();
    }
    return bigint ? toBigIntStats(s) : s;
  }
  // BigInt stats are compared to the nanosecond, since their *Ms fields are whole.
  function sameStats(a, b) {
    if (typeof a.mtimeNs === 'bigint' && (a.mtimeNs !== b.mtimeNs || a.ctimeNs !== b.ctimeNs)) return false;
    return a.mtimeMs === b.mtimeMs && a.ctimeMs === b.ctimeMs && a.size === b.size && a.mode === b.mode &&
      a.ino === b.ino && a.dev === b.dev && a.uid === b.uid && a.gid === b.gid;
  }
  const statWatchers = new Map();
  // Watchers are found by absolute path, so another spelling of the same path finds one.
  const watchKey = (filename) => (pathModule() ? fsResolve(String(pathString(filename))) : String(filename));
  // The path a watcher polls: on a filesystem where relative paths start at the working
  // directory, made absolute once, so a later process.chdir doesn't change what is watched.
  const cwdRelative = !!fs.__cwdRelative;
  delete fs.__cwdRelative;
  const watchTarget = (filename) => {
    const p = pathString(filename);
    return cwdRelative && typeof p === 'string' && pathModule() ? pathModule().resolve(p) : p;
  };
  fs.watchFile = function (filename, options, listener) {
    if (typeof options === 'function') {
      listener = options;
      options = undefined;
    }
    if (typeof listener !== 'function') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "listener" argument must be of type function. Received ' + receivedType(listener));
    }
    const opts = Object.assign({ interval: 5007, persistent: true }, options);
    const key = watchKey(filename);
    filename = watchTarget(filename);
    let watcher = statWatchers.get(key);
    if (!watcher) {
      const EventEmitter = eventEmitter();
      watcher = new EventEmitter();
      const bigint = !!opts.bigint;
      let prev = statOrZero(filename, bigint);
      // Node calls the listeners once at the start for a path that doesn't exist.
      if (!fs.existsSync(filename)) process.nextTick(() => watcher.emit('change', zeroFor(bigint), zeroFor(bigint)));
      watcher._timer = setInterval(() => {
        const curr = statOrZero(filename, bigint);
        if (!sameStats(curr, prev)) {
          const old = prev;
          prev = curr;
          watcher.emit('change', curr, old);
        }
      }, opts.interval);
      if (!opts.persistent && watcher._timer.unref) watcher._timer.unref();
      watcher.ref = function () { if (this._timer.ref) this._timer.ref(); return this; };
      watcher.unref = function () { if (this._timer.unref) this._timer.unref(); return this; };
      watcher.stop = function () {
        clearInterval(this._timer);
        statWatchers.delete(key);
        process.nextTick(() => this.emit('stop'));
      };
      statWatchers.set(key, watcher);
    }
    watcher.addListener('change', listener);
    return watcher;
  };
  fs.unwatchFile = function (filename, listener) {
    const key = watchKey(filename);
    const watcher = statWatchers.get(key);
    if (!watcher) return;
    if (typeof listener === 'function') watcher.removeListener('change', listener);
    else watcher.removeAllListeners('change');
    if (watcher.listenerCount('change') === 0) watcher.stop();
  };

  // fs.watch: an FSWatcher that polls a file, or a directory's entries, every 100 ms and
  // reports 'rename' when one appears or goes and 'change' when one changes.
  function snapshot(target, recursive) {
    const path = pathModule();
    const entries = new Map();
    const stat = fs.statSync(target);
    if (!stat.isDirectory()) {
      entries.set(path.basename(String(target)), stat);
      return entries;
    }
    // Each directory is entered once, so links that lead back into the tree don't loop.
    const visited = new Set();
    const enter = (s) => {
      if (!s.ino) return true;
      const key = s.dev + ':' + s.ino;
      if (visited.has(key)) return false;
      visited.add(key);
      return true;
    };
    enter(stat);
    const walk = (dir, prefix) => {
      let names;
      try {
        names = fs.readdirSync(dir);
      } catch (e) {
        return;
      }
      for (const name of names) {
        const rel = prefix ? path.join(prefix, name) : name;
        let s;
        try {
          s = fs.statSync(path.join(dir, name));
        } catch (e) {
          continue;
        }
        entries.set(rel, s);
        if (recursive && s.isDirectory() && enter(s)) walk(path.join(dir, name), rel);
      }
    };
    walk(target, '');
    return entries;
  }
  fs.watch = function (filename, options, listener) {
    if (typeof options === 'function') {
      listener = options;
      options = undefined;
    }
    if (typeof options === 'string') options = { encoding: options };
    const opts = Object.assign({ persistent: true, recursive: false }, options);
    // A Buffer or URL path becomes a string, for the path helpers, and a relative one is
    // fixed against the working directory now.
    filename = watchTarget(filename);
    let entries;
    try {
      entries = snapshot(filename, opts.recursive);
    } catch (e) {
      if (e && e.syscall) {
        e.syscall = 'watch';
        e.message = e.message.replace(/, stat '/, ", watch '");
      }
      throw e;
    }
    // Filenames come in the encoding asked for: a string, or a Buffer for 'buffer'.
    const encoding = encodingOf(opts.encoding || null);
    const named = (name) => (encoding === 'buffer' ? Buffer.from(name)
      : encoding && !isUtf8(encoding) ? Buffer.from(name).toString(encoding) : name);
    const EventEmitter = eventEmitter();
    const watcher = new EventEmitter();
    if (typeof listener === 'function') watcher.on('change', listener);
    // The watched path itself appearing or going is reported too, by its name.
    let present = true;
    const ownName = pathModule().basename(filename);
    const timer = setInterval(() => {
      let next;
      try {
        next = snapshot(filename, opts.recursive);
        if (!present) watcher.emit('change', 'rename', named(ownName));
        present = true;
      } catch (e) {
        next = new Map();
        if (present && !entries.has(ownName)) watcher.emit('change', 'rename', named(ownName));
        present = false;
      }
      for (const [name, stat] of next) {
        const old = entries.get(name);
        if (!old) watcher.emit('change', 'rename', named(name));
        else if (!sameStats(old, stat)) watcher.emit('change', 'change', named(name));
      }
      for (const name of entries.keys()) {
        if (!next.has(name)) watcher.emit('change', 'rename', named(name));
      }
      entries = next;
    }, 100);
    if (!opts.persistent && timer.unref) timer.unref();
    let closed = false;
    let onAbort = null;
    watcher.close = function () {
      if (closed) return;
      closed = true;
      clearInterval(timer);
      if (onAbort) opts.signal.removeEventListener('abort', onAbort);
      process.nextTick(() => watcher.emit('close'));
    };
    watcher.ref = function () { if (timer.ref) timer.ref(); return watcher; };
    watcher.unref = function () { if (timer.unref) timer.unref(); return watcher; };
    if (opts.signal) {
      if (opts.signal.aborted) process.nextTick(() => watcher.close());
      else {
        onAbort = () => watcher.close();
        opts.signal.addEventListener('abort', onAbort, { once: true });
      }
    }
    return watcher;
  };

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
    rename: ['oldPath', 'newPath'], copyFile: ['src', 'dest'],
    // symlink's target is left for symlinkArgs, which keeps a Buffer's bytes.
    symlinkSync: [null, 'path'], symlink: [null, 'path'], readlinkSync: ['path'], readlink: ['path'],
    linkSync: ['existingPath', 'newPath'], link: ['existingPath', 'newPath'], mkdtempSync: ['prefix'], mkdtemp: ['prefix'],
    truncateSync: ['path'], truncate: ['path'], chmodSync: ['path'], chmod: ['path'],
    chownSync: ['path'], chown: ['path'], utimesSync: ['path'], utimes: ['path'],
    statfsSync: ['path'], statfs: ['path'], opendirSync: ['path'], opendir: ['path'],
    cpSync: ['src', 'dest'], cp: ['src', 'dest'], openAsBlob: ['path'], watch: ['filename'],
    watchFile: ['filename']
  };
  for (const name of Object.keys(PATH_ARGUMENTS)) {
    const original = fs[name];
    if (typeof original !== 'function') continue;
    const names = PATH_ARGUMENTS[name];
    // Buffer and file: URL paths become strings here, checked as Node checks them, so every
    // call below takes them.
    const asPaths = (args) => {
      args = Array.prototype.slice.call(args);
      names.forEach((argName, i) => {
        if (argName === null || typeof args[i] === 'number') return; // left as is, or a descriptor
        args[i] = nativePath(args[i], argName);
      });
      return args;
    };
    fs[name] = function () {
      return original.apply(this, asPaths(arguments));
    };
    // realpath.native and realpathSync.native are checked the same way.
    if (typeof original.native === 'function') {
      const native = original.native;
      fs[name].native = function () {
        return native.apply(this, asPaths(arguments));
      };
    }
  }
  // A path as the native layer takes it: a string, checked as Node checks it, or, for a
  // Buffer whose bytes aren't UTF-8, { __pathBytes: base64 }, since a string would lose them.
  function nativePath(given, name) {
    const text = pathString(given);
    checkPath(text, name);
    if (ArrayBuffer.isView(given) && hasBuffer()) {
      const bytes = viewBytes(given);
      if (!Buffer.from(text).equals(bytes)) return { __pathBytes: base64Of(bytes) };
    }
    return text;
  }
  const _existsSync = fs.existsSync;
  fs.existsSync = function (path) {
    try {
      path = nativePath(path, 'path');
    } catch (e) {
      return false;
    }
    return _existsSync.call(this, path);
  };
})();
