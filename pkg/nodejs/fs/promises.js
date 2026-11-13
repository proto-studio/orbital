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
    const handle = {
      fd,
      path,

      // read(buffer[, offset[, length[, position]]]), read(buffer, options) or read(options).
      read(buffer, offset, length, position) {
        return attempt(() => {
          if (!ArrayBuffer.isView(buffer)) {
            ({ buffer = Buffer.alloc(16384), offset, length, position } = buffer || {});
          } else if (offset !== null && typeof offset === 'object') {
            ({ offset, length, position } = offset);
          }
          const start = offset === undefined || offset === null ? 0 : offset;
          const count = length === undefined || length === null ? buffer.byteLength - start : length;
          const bytesRead = fs.readSync(fd, buffer, start, count, position === undefined ? null : position);
          return { bytesRead, buffer };
        });
      },

      write(data, ...args) {
        return attempt(() => ({ bytesWritten: fs.writeSync(fd, data, ...args), buffer: data }));
      },

      close() {
        return attempt(() => {
          if (handle.fd !== -1) {
            handle.fd = -1;
            fs.closeSync(fd);
          }
        });
      },

      stat() {
        return attempt(() => fs.fstatSync(fd));
      },

      truncate(len) {
        return fsPromises.truncate(path, len);
      },

      sync() {
        return Promise.resolve();
      },

      datasync() {
        return Promise.resolve();
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
    // Read file contents
    readFile: function(path, options) {
      return new Promise((resolve, reject) => {
        try {
          const result = fs.readFileSync(path, options);
          if (result === null || result === undefined) {
            reject(new Error(`ENOENT: no such file or directory, open '${path}'`));
          } else {
            resolve(result);
          }
        } catch (e) {
          reject(e);
        }
      });
    },

    // Write file contents
    writeFile: function(path, data, options) {
      return attempt(() => fs.writeFileSync(target(path), data, options));
    },

    // Append to file
    appendFile: function(path, data, options) {
      return attempt(() => fs.appendFileSync(target(path), data, options));
    },

    // Delete file
    unlink: function(path) {
      return new Promise((resolve, reject) => {
        try {
          fs.unlinkSync(path);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
    },

    // Rename file
    rename: function(oldPath, newPath) {
      return new Promise((resolve, reject) => {
        try {
          fs.renameSync(oldPath, newPath);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
    },

    // Copy file
    copyFile: function(src, dest, mode) {
      return new Promise((resolve, reject) => {
        try {
          fs.copyFileSync(src, dest, mode);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
    },

    // Create directory
    mkdir: function(path, options) {
      return new Promise((resolve, reject) => {
        try {
          fs.mkdirSync(path, options);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
    },

    // Remove directory
    rmdir: function(path, options) {
      return new Promise((resolve, reject) => {
        try {
          fs.rmdirSync(path, options);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
    },

    // Remove file or directory (recursive)
    rm: function(path, options) {
      return attempt(() => fs.rmSync(path, options));
    },

    // Read directory contents
    readdir: function(path, options) {
      return new Promise((resolve, reject) => {
        try {
          const result = fs.readdirSync(path, options);
          if (result === null || result === undefined) {
            reject(new Error(`ENOENT: no such file or directory, scandir '${path}'`));
          } else {
            resolve(result);
          }
        } catch (e) {
          reject(e);
        }
      });
    },

    // Get file stats
    stat: function(path, options) {
      return new Promise((resolve, reject) => {
        try {
          const result = fs.statSync(path, options);
          if (result === null || result === undefined) {
            reject(new Error(`ENOENT: no such file or directory, stat '${path}'`));
          } else {
            // Add methods to stat object
            result.isDirectory = function() { return this._isDirectory; };
            result.isFile = function() { return this._isFile; };
            result.isSymbolicLink = function() { return false; };
            result.isBlockDevice = function() { return false; };
            result.isCharacterDevice = function() { return false; };
            result.isFIFO = function() { return false; };
            result.isSocket = function() { return false; };
            resolve(result);
          }
        } catch (e) {
          reject(e);
        }
      });
    },

    // Get file stats (don't follow symlinks)
    lstat: function(path, options) {
      // For now, lstat behaves like stat
      return fsPromises.stat(path, options);
    },

    // Check access permissions
    access: function(path, mode) {
      return new Promise((resolve, reject) => {
        try {
          if (fs.existsSync(path)) {
            resolve();
          } else {
            reject(new Error(`ENOENT: no such file or directory, access '${path}'`));
          }
        } catch (e) {
          reject(e);
        }
      });
    },

    // Truncate file
    truncate: function(path, len) {
      return new Promise((resolve, reject) => {
        try {
          // Read, truncate, write back. Growing a file pads it with NUL bytes.
          const size = len || 0;
          const content = fs.existsSync(path) ? fs.readFileSync(path) : Buffer.alloc(0);
          const resized = Buffer.alloc(size);
          content.copy(resized, 0, 0, Math.min(size, content.length));
          fs.writeFileSync(path, resized);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
    },

    // Create symbolic link (stub - may not work in sandbox)
    symlink: function(target, path, type) {
      return Promise.reject(new Error('symlink not supported'));
    },

    // Read symbolic link (stub)
    readlink: function(path, options) {
      return Promise.reject(new Error('readlink not supported'));
    },

    // Get real path
    realpath: function(path, options) {
      return new Promise((resolve, reject) => {
        // For now, just return the path if it exists
        if (fs.existsSync(path)) {
          resolve(path);
        } else {
          reject(new Error(`ENOENT: no such file or directory, realpath '${path}'`));
        }
      });
    },

    // Change file mode (stub)
    chmod: function(path, mode) {
      return Promise.resolve(); // No-op in sandbox
    },

    // Change file owner (stub)
    chown: function(path, uid, gid) {
      return Promise.resolve(); // No-op in sandbox
    },

    // Update file timestamps (stub)
    utimes: function(path, atime, mtime) {
      return Promise.resolve(); // No-op in sandbox
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
          if (!fs.existsSync(path) && String(openFlags).startsWith('r')) {
            reject(fs.__makeFsError(`ENOENT: no such file or directory, open '${path}'`, 'ENOENT', path, 'open'));
            return;
          }
          resolve(fileHandle(fs.openSync(path, openFlags, mode), path));
        } catch (e) {
          reject(e);
        }
      });
    },

    // Make temporary directory
    mkdtemp: function(prefix, options) {
      return new Promise((resolve, reject) => {
        try {
          const suffix = Math.random().toString(36).substring(2, 8);
          const dir = prefix + suffix;
          fs.mkdirSync(dir, { recursive: true });
          resolve(dir);
        } catch (e) {
          reject(e);
        }
      });
    },

    // The same object as fs.constants, as in Node.
    constants: fs.constants
  };

  // Export
  global.__fs_promises_module = fsPromises;
  if (fs && !fs.promises) {
    Object.defineProperty(fs, 'promises', { configurable: true, enumerable: true, value: fsPromises });
  }

})(globalThis);
