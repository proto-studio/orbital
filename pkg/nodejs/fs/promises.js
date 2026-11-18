// fs/promises - Promise-based file system API
(function(global) {
  'use strict';

  const fs = global.__fs_module;

  // Helper to promisify callback-based functions
  function promisify(fn) {
    return function(...args) {
      return new Promise((resolve, reject) => {
        fn(...args, (err, result) => {
          if (err) {
            reject(new Error(err));
          } else {
            resolve(result);
          }
        });
      });
    };
  }

  // Runs a synchronous operation as a promise.
  function attempt(operation) {
    return new Promise((resolve) => resolve(operation()));
  }

  // viaCallback with its first argument from first(), which may throw to reject.
  function viaCallbackWith(first, name, ...args) {
    return new Promise((resolve, reject) => {
      fs[name](first(), ...args, (err, value) => (err ? reject(err) : resolve(value)));
    });
  }

  // Runs fs[name], a callback form, which does its work off the JavaScript thread, as a
  // promise of its result. A bad argument it throws rejects the promise.
  function viaCallback(name, ...args) {
    return new Promise((resolve, reject) => {
      fs[name](...args, (err, value) => (err ? reject(err) : resolve(value)));
    });
  }

  // What writeFile and appendFile write to: a path, or the descriptor of a FileHandle. Unlike
  // the callback forms, these refuse a bare descriptor, as Node's do.
  function target(path) {
    if (typeof path === 'number') {
      const e = new TypeError('The "path" argument must be of type string or an instance of Buffer or URL. Received type number (' + path + ')');
      e.code = 'ERR_INVALID_ARG_TYPE';
      throw e;
    }
    if (path && typeof path === 'object' && handles.has(path)) return path.fd;
    return path;
  }
  const handles = new WeakSet();

  // A FileHandle over a real descriptor, so it shares the file offset and byte handling of
  // fs.readSync and fs.writeSync.
  function fileHandle(fd, path) {
    const readAll = () => {
      const chunks = [];
      for (;;) {
        const chunk = Buffer.alloc(65536);
        const n = fs.readSync(fd, chunk, 0, chunk.length, null);
        if (n === 0) return Buffer.concat(chunks);
        chunks.push(chunk.subarray(0, n));
      }
    };
    const encodingOf = (options) => (typeof options === 'string' ? options : options && options.encoding);
    let closing = null;
    const handle = {
      fd,
      path,

      // read(buffer[, offset[, length[, position]]]), read(buffer, options) or read(options).
      // The read is queued on the descriptor before this returns, so a close made after it
      // waits for it.
      read(buffer, offset, length, position) {
        return new Promise((resolve, reject) => {
          if (!ArrayBuffer.isView(buffer)) {
            ({ buffer = Buffer.alloc(16384), offset, length, position } = buffer || {});
          } else if (offset !== null && typeof offset === 'object') {
            ({ offset, length, position } = offset);
          }
          const start = offset === undefined || offset === null ? 0 : offset;
          const count = length === undefined || length === null ? buffer.byteLength - start : length;
          fs.read(fd, buffer, start, count, position === undefined ? null : position,
            (err, bytesRead) => (err ? reject(err) : resolve({ bytesRead, buffer })));
        });
      },

      write(data, ...args) {
        return new Promise((resolve, reject) => {
          fs.write(fd, data, ...args, (err, bytesWritten) => (err ? reject(err) : resolve({ bytesWritten, buffer: data })));
        });
      },

      // A second close while one is under way gets the same promise, so it waits too.
      close() {
        if (closing) return closing;
        handle.fd = -1;
        closing = viaCallback('close', fd);
        return closing;
      },

      stat() {
        return viaCallback('fstat', fd);
      },

      truncate(len) {
        return viaCallback('ftruncate', fd, len);
      },

      sync() {
        return viaCallback('fsync', fd);
      },

      datasync() {
        return viaCallback('fdatasync', fd);
      },

      chmod(mode) {
        return viaCallback('fchmod', fd, mode);
      },

      chown(uid, gid) {
        return viaCallback('fchown', fd, uid, gid);
      },

      utimes(atime, mtime) {
        return viaCallback('futimes', fd, atime, mtime);
      },

      readv(buffers, position) {
        return new Promise((resolve, reject) => {
          fs.readv(fd, buffers, position, (err, bytesRead) => (err ? reject(err) : resolve({ bytesRead, buffers })));
        });
      },

      writev(buffers, position) {
        return new Promise((resolve, reject) => {
          fs.writev(fd, buffers, position, (err, bytesWritten) => (err ? reject(err) : resolve({ bytesWritten, buffers })));
        });
      },

      // These two work from the current offset, unlike their fs.promises namesakes.
      readFile(options) {
        return attempt(() => {
          const contents = readAll();
          const encoding = encodingOf(options);
          return encoding ? contents.toString(encoding) : contents;
        });
      },

      writeFile(data, options) {
        return attempt(() => {
          const encoding = encodingOf(options);
          fs.writeSync(fd, typeof data === 'string' ? Buffer.from(data, encoding || 'utf8') : data);
        });
      },
    };
    handles.add(handle);
    return handle;
  }

  // Promise-based versions of fs functions
  const fsPromises = {
    // These run off the JavaScript thread, through the callback forms.
    readFile: (path, options) => viaCallback('readFile', path, options),
    // The write is queued before these return, so a FileHandle closed right after waits for it.
    writeFile: (path, data, options) => viaCallbackWith(() => target(path), 'writeFile', data, options),
    appendFile: (path, data, options) => viaCallbackWith(() => target(path), 'appendFile', data, options),
    unlink: (path) => viaCallback('unlink', path),
    rename: (oldPath, newPath) => viaCallback('rename', oldPath, newPath),
    copyFile: (src, dest, mode) => viaCallback('copyFile', src, dest, mode),
    mkdir: (path, options) => viaCallback('mkdir', path, options),
    rmdir: (path, options) => viaCallback('rmdir', path, options),
    rm: (path, options) => viaCallback('rm', path, options),
    readdir: (path, options) => viaCallback('readdir', path, options),
    stat: (path, options) => viaCallback('stat', path),
    lstat: (path, options) => viaCallback('lstat', path),

    // Check access permissions
    access: function(path, mode) {
      return viaCallback('access', path, mode);
    },

    truncate: function(path, len) {
      return viaCallback('truncate', path, len);
    },

    symlink: (target, path, type) => viaCallback('symlink', target, path, type),
    readlink: (path, options) => viaCallback('readlink', path, options),
    link: (existingPath, newPath) => viaCallback('link', existingPath, newPath),
    // Node's promise realpath is the native one, which names realpath in its errors.
    realpath: (path, options) => new Promise((resolve, reject) => {
      fs.realpath.native(path, options, (err, value) => (err ? reject(err) : resolve(value)));
    }),

    chmod: function(path, mode) {
      return viaCallback('chmod', path, mode);
    },

    chown: function(path, uid, gid) {
      return viaCallback('chown', path, uid, gid);
    },

    utimes: function(path, atime, mtime) {
      return viaCallback('utimes', path, atime, mtime);
    },

    statfs: function(path, options) {
      return viaCallback('statfs', path, options);
    },

    opendir: function(path, options) {
      return viaCallback('opendir', path, options);
    },

    cp: function(src, dest, options) {
      return new Promise((resolve, reject) => {
        // A filter's promise rejected with a falsy reason rejects this with that reason, as
        // Node's does; the callback form gets an error instead.
        fs.cp(src, dest, options || {}, (err) => {
          if (!err) resolve();
          else reject(err.code === 'ERR_FALSY_VALUE_REJECTION' && 'reason' in err ? err.reason : err);
        });
      });
    },

    // Watch for file changes (stub)
    watch: function(filename, options) {
      throw new Error('watch not supported');
    },

    // Open file handle
    open: function(path, flags, mode) {
      return new Promise((resolve, reject) => {
        try {
          const openFlags = flags === undefined || flags === null ? 'r' : flags;
          resolve(fileHandle(fs.openSync(path, openFlags, mode), path));
        } catch (e) {
          reject(e);
        }
      });
    },

    mkdtemp: (prefix, options) => viaCallback('mkdtemp', prefix, options),

    // The same object as fs.constants, as in Node.
    constants: fs.constants
  };

  // Export
  global.__fs_promises_module = fsPromises;
  if (fs && !fs.promises) {
    Object.defineProperty(fs, 'promises', { configurable: true, enumerable: true, value: fsPromises });
  }

})(globalThis);
