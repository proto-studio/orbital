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
      return new Promise((resolve, reject) => {
        try {
          fs.writeFileSync(path, data, options);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
    },

    // Append to file
    appendFile: function(path, data, options) {
      return new Promise((resolve, reject) => {
        try {
          fs.appendFileSync(path, data, options);
          resolve();
        } catch (e) {
          reject(e);
        }
      });
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
      return new Promise((resolve, reject) => {
        try {
          if (options && options.recursive) {
            fs.rmdirSync(path, { recursive: true });
          } else {
            fs.unlinkSync(path);
          }
          resolve();
        } catch (e) {
          reject(e);
        }
      });
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
          // Read, truncate, write back
          let content = '';
          if (fs.existsSync(path)) {
            content = fs.readFileSync(path, 'utf8') || '';
          }
          content = content.substring(0, len || 0);
          fs.writeFileSync(path, content);
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
      // Return a file handle object
      return new Promise((resolve, reject) => {
        try {
          if (!fs.existsSync(path) && !flags.includes('w') && !flags.includes('a')) {
            reject(new Error(`ENOENT: no such file or directory, open '${path}'`));
            return;
          }
          
          // Create a simple file handle
          const handle = {
            fd: Math.floor(Math.random() * 1000000),
            path: path,
            
            read: function(buffer, offset, length, position) {
              return fsPromises.readFile(path).then(data => {
                const bytes = typeof data === 'string' ? 
                  new TextEncoder().encode(data) : data;
                return { bytesRead: bytes.length, buffer: bytes };
              });
            },
            
            write: function(buffer, offset, length, position) {
              const data = typeof buffer === 'string' ? buffer : 
                new TextDecoder().decode(buffer);
              return fsPromises.writeFile(path, data).then(() => {
                return { bytesWritten: data.length, buffer };
              });
            },
            
            close: function() {
              return Promise.resolve();
            },
            
            stat: function() {
              return fsPromises.stat(path);
            },
            
            truncate: function(len) {
              return fsPromises.truncate(path, len);
            },
            
            sync: function() {
              return Promise.resolve();
            },
            
            datasync: function() {
              return Promise.resolve();
            },
            
            readFile: function(options) {
              return fsPromises.readFile(path, options);
            },
            
            writeFile: function(data, options) {
              return fsPromises.writeFile(path, data, options);
            }
          };
          
          resolve(handle);
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

    // Constants
    constants: {
      F_OK: 0,
      R_OK: 4,
      W_OK: 2,
      X_OK: 1,
      COPYFILE_EXCL: 1,
      COPYFILE_FICLONE: 2,
      COPYFILE_FICLONE_FORCE: 4
    }
  };

  // Export
  global.__fs_promises_module = fsPromises;

})(globalThis);
