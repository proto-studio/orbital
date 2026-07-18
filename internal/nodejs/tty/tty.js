// tty - terminal helpers. isatty() is backed by a real fstat on the fd (see
// tty.go). ReadStream/WriteStream provide the TTY stream surface libraries
// probe for (color depth, window size, raw mode); actual byte I/O for fd 1/2
// delegates to process.stdout/stderr.
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module;

  function isatty(fd) {
    try {
      return !!global.__tty_isatty(fd | 0);
    } catch (e) {
      return false;
    }
  }

  function streamForFd(fd) {
    if (fd === 1) return global.process && global.process.stdout;
    if (fd === 2) return global.process && global.process.stderr;
    return null;
  }

  class ReadStream extends EventEmitter {
    constructor(fd) {
      super();
      this.fd = fd == null ? 0 : fd;
      this.isTTY = true;
      this.isRaw = false;
    }
    setRawMode(mode) {
      this.isRaw = !!mode;
      return this;
    }
    resume() {
      return this;
    }
    pause() {
      return this;
    }
  }

  class WriteStream extends EventEmitter {
    constructor(fd) {
      super();
      this.fd = fd == null ? 1 : fd;
      this.isTTY = true;
      this.columns = 80;
      this.rows = 24;
    }
    write(chunk, encoding, cb) {
      const s = streamForFd(this.fd);
      if (s && typeof s.write === 'function') {
        return s.write(chunk, encoding, cb);
      }
      if (typeof encoding === 'function') cb = encoding;
      if (typeof cb === 'function') cb();
      return true;
    }
    getColorDepth() {
      return isatty(this.fd) ? 8 : 1;
    }
    hasColors(count) {
      const depth = this.getColorDepth();
      const colors = 1 << depth;
      if (typeof count === 'number') return colors >= count;
      return depth > 1;
    }
    getWindowSize() {
      return [this.columns, this.rows];
    }
    clearLine() {
      return true;
    }
    clearScreenDown() {
      return true;
    }
    cursorTo() {
      return true;
    }
    moveCursor() {
      return true;
    }
    end(chunk, encoding, cb) {
      if (chunk != null && typeof chunk !== 'function') this.write(chunk);
      if (typeof encoding === 'function') cb = encoding;
      if (typeof cb === 'function') cb();
      return this;
    }
  }

  global.__tty_module = {
    isatty,
    ReadStream,
    WriteStream
  };
})(globalThis);
