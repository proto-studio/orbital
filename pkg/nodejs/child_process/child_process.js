// child_process - Child process spawning
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const internal = global.__child_process_internal;

  /**
   * ChildProcess class - Represents a spawned child process.
   */
  class ChildProcess extends EventEmitter {
    constructor() {
      super();
      this._id = null;
      this._pid = null;
      this._exitCode = null;
      this._signalCode = null;
      this._killed = false;
      this._connected = true;
      this._spawnfile = null;
      this._spawnargs = null;
      
      this.stdin = null;
      this.stdout = null;
      this.stderr = null;
      this.stdio = [null, null, null];
    }

    get pid() { return this._pid; }
    get exitCode() { return this._exitCode; }
    get signalCode() { return this._signalCode; }
    get killed() { return this._killed; }
    get connected() { return this._connected; }
    get spawnfile() { return this._spawnfile; }
    get spawnargs() { return this._spawnargs; }

    /**
     * Kill the child process.
     */
    kill(signal) {
      if (this._killed || this._id === null) {
        return false;
      }

      signal = signal || 'SIGTERM';
      const sigNum = signalToNumber(signal);
      
      const success = internal.kill(this._id, sigNum);
      if (success) {
        this._killed = true;
      }
      return success;
    }

    /**
     * Send a message to the child (for IPC).
     */
    send(message, sendHandle, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }

      // IPC not fully implemented
      if (callback) {
        process.nextTick(() => callback(new Error('IPC not supported')));
      }
      return false;
    }

    /**
     * Disconnect IPC channel.
     */
    disconnect() {
      this._connected = false;
      this.emit('disconnect');
    }

    /**
     * Unreference for event loop.
     */
    unref() {
      if (this._id !== null) {
        internal.unref(this._id);
      }
      return this;
    }

    /**
     * Reference for event loop.
     */
    ref() {
      if (this._id !== null) {
        internal.ref(this._id);
      }
      return this;
    }

    /**
     * Internal: Set up streams after spawn.
     */
    _setupStreams() {
      // Create readable streams for stdout/stderr
      this.stdout = new ProcessReadableStream(this._id, 'stdout');
      this.stderr = new ProcessReadableStream(this._id, 'stderr');
      this.stdin = new ProcessWritableStream(this._id);
      
      this.stdio = [this.stdin, this.stdout, this.stderr];
    }

    /**
     * Internal: Start waiting for exit.
     */
    _waitForExit() {
      internal.wait(this._id, (err, exitCode, signalCode) => {
        this._exitCode = exitCode;
        this._signalCode = signalCode;
        
        // Close streams
        if (this.stdout) this.stdout.push(null);
        if (this.stderr) this.stderr.push(null);
        if (this.stdin) this.stdin.end();
        
        this.emit('exit', exitCode, signalCode);
        this.emit('close', exitCode, signalCode);
      });
    }
  }

  /**
   * Readable stream for process output.
   */
  class ProcessReadableStream extends EventEmitter {
    constructor(processId, type) {
      super();
      this._processId = processId;
      this._type = type;
      this._flowing = false;
      this._buffer = [];
      this._ended = false;

      this._startReading();
    }

    _startReading() {
      if (this._ended) return;

      internal.read(this._processId, this._type, (err, data) => {
        if (err || data === null) {
          this._ended = true;
          this.emit('end');
          return;
        }

        const buf = Buffer.from(data, 'binary');
        
        if (this._flowing) {
          this.emit('data', buf);
        } else {
          this._buffer.push(buf);
        }

        this._startReading();
      });
    }

    read(size) {
      if (this._buffer.length === 0) return null;
      return this._buffer.shift();
    }

    push(data) {
      if (data === null) {
        this._ended = true;
        this.emit('end');
        return;
      }
      this._buffer.push(data);
      if (this._flowing) {
        this.emit('data', data);
      }
    }

    resume() {
      this._flowing = true;
      while (this._buffer.length > 0) {
        this.emit('data', this._buffer.shift());
      }
      return this;
    }

    pause() {
      this._flowing = false;
      return this;
    }

    pipe(destination, options) {
      this.on('data', (chunk) => destination.write(chunk));
      this.on('end', () => {
        if (!options || options.end !== false) {
          destination.end();
        }
      });
      this.resume();
      return destination;
    }

    setEncoding(encoding) {
      return this;
    }
  }

  /**
   * Writable stream for process input.
   */
  class ProcessWritableStream extends EventEmitter {
    constructor(processId) {
      super();
      this._processId = processId;
      this._ended = false;
      this._writable = true;
    }

    get writable() { return this._writable; }

    write(chunk, encoding, callback) {
      if (typeof encoding === 'function') {
        callback = encoding;
        encoding = undefined;
      }

      if (this._ended) {
        if (callback) callback(new Error('Stream ended'));
        return false;
      }

      const data = Buffer.isBuffer(chunk) ? chunk.toString('binary') : chunk;
      
      internal.write(this._processId, data, (err) => {
        if (err) {
          this.emit('error', new Error(err));
          if (callback) callback(new Error(err));
        } else {
          this.emit('drain');
          if (callback) callback();
        }
      });

      return true;
    }

    end(chunk, encoding, callback) {
      if (typeof chunk === 'function') {
        callback = chunk;
        chunk = undefined;
      }

      if (chunk) {
        this.write(chunk, encoding);
      }

      this._ended = true;
      this._writable = false;

      internal.closeStdin(this._processId);

      if (callback) this.once('finish', callback);
      this.emit('finish');

      return this;
    }

    destroy(error) {
      this._ended = true;
      this._writable = false;
      if (error) this.emit('error', error);
      return this;
    }
  }

  /**
   * Signal name to number mapping.
   */
  function signalToNumber(signal) {
    if (typeof signal === 'number') return signal;
    const signals = {
      'SIGHUP': 1, 'SIGINT': 2, 'SIGQUIT': 3, 'SIGILL': 4,
      'SIGTRAP': 5, 'SIGABRT': 6, 'SIGFPE': 8, 'SIGKILL': 9,
      'SIGBUS': 10, 'SIGSEGV': 11, 'SIGSYS': 12, 'SIGPIPE': 13,
      'SIGALRM': 14, 'SIGTERM': 15, 'SIGURG': 16, 'SIGSTOP': 17,
      'SIGTSTP': 18, 'SIGCONT': 19, 'SIGCHLD': 20, 'SIGTTIN': 21,
      'SIGTTOU': 22, 'SIGIO': 23, 'SIGXCPU': 24, 'SIGXFSZ': 25,
      'SIGVTALRM': 26, 'SIGPROF': 27, 'SIGWINCH': 28, 'SIGUSR1': 30,
      'SIGUSR2': 31
    };
    return signals[signal] || 15;
  }

  /**
   * Spawn a child process.
   */
  function spawn(command, args, options) {
    if (Array.isArray(args)) {
      // spawn(command, args, options)
    } else if (args && typeof args === 'object') {
      // spawn(command, options)
      options = args;
      args = [];
    } else {
      args = [];
    }

    options = options || {};

    const child = new ChildProcess();
    child._spawnfile = command;
    child._spawnargs = [command, ...args];

    const spawnOptions = {
      cwd: options.cwd || process.cwd(),
      env: options.env ? Object.entries(options.env).map(([k, v]) => `${k}=${v}`) : null,
      shell: options.shell || false,
      detached: options.detached || false
    };

    child._id = internal.spawn(command, args, JSON.stringify(spawnOptions), (err, pid) => {
      if (err) {
        process.nextTick(() => {
          child.emit('error', new Error(err));
        });
        return;
      }

      child._pid = pid;
      child._setupStreams();
      child._waitForExit();

      process.nextTick(() => {
        child.emit('spawn');
      });
    });

    return child;
  }

  /**
   * Execute a command in a shell.
   */
  function exec(command, options, callback) {
    if (typeof options === 'function') {
      callback = options;
      options = {};
    }

    options = options || {};

    const execOptions = {
      cwd: options.cwd || process.cwd(),
      env: options.env ? Object.entries(options.env).map(([k, v]) => `${k}=${v}`) : null,
      timeout: options.timeout || 0,
      maxBuffer: options.maxBuffer || 1024 * 1024,
      encoding: options.encoding || 'utf8'
    };

    const child = new ChildProcess();
    child._spawnfile = '/bin/sh';
    child._spawnargs = ['/bin/sh', '-c', command];

    internal.exec(command, JSON.stringify(execOptions), (err, stdout, stderr, exitCode) => {
      if (callback) {
        const stdoutBuf = stdout ? Buffer.from(stdout, 'binary') : Buffer.alloc(0);
        const stderrBuf = stderr ? Buffer.from(stderr, 'binary') : Buffer.alloc(0);

        const stdoutStr = execOptions.encoding === 'buffer' ? stdoutBuf : stdoutBuf.toString(execOptions.encoding);
        const stderrStr = execOptions.encoding === 'buffer' ? stderrBuf : stderrBuf.toString(execOptions.encoding);

        if (err) {
          const error = new Error(err);
          error.code = exitCode;
          error.killed = false;
          error.stdout = stdoutStr;
          error.stderr = stderrStr;
          callback(error, stdoutStr, stderrStr);
        } else if (exitCode !== 0) {
          const error = new Error(`Command failed: ${command}`);
          error.code = exitCode;
          error.killed = false;
          error.stdout = stdoutStr;
          error.stderr = stderrStr;
          callback(error, stdoutStr, stderrStr);
        } else {
          callback(null, stdoutStr, stderrStr);
        }
      }

      child._exitCode = exitCode;
      child.emit('exit', exitCode, null);
      child.emit('close', exitCode, null);
    });

    return child;
  }

  /**
   * Execute a file directly.
   */
  function execFile(file, args, options, callback) {
    if (typeof args === 'function') {
      callback = args;
      args = [];
      options = {};
    } else if (typeof options === 'function') {
      callback = options;
      options = {};
    }

    if (!Array.isArray(args)) {
      options = args;
      args = [];
    }

    options = options || {};

    const execOptions = {
      cwd: options.cwd || process.cwd(),
      env: options.env ? Object.entries(options.env).map(([k, v]) => `${k}=${v}`) : null,
      timeout: options.timeout || 0,
      maxBuffer: options.maxBuffer || 1024 * 1024,
      encoding: options.encoding || 'utf8'
    };

    const child = new ChildProcess();
    child._spawnfile = file;
    child._spawnargs = [file, ...args];

    internal.execFile(file, args, JSON.stringify(execOptions), (err, stdout, stderr, exitCode) => {
      if (callback) {
        const stdoutBuf = stdout ? Buffer.from(stdout, 'binary') : Buffer.alloc(0);
        const stderrBuf = stderr ? Buffer.from(stderr, 'binary') : Buffer.alloc(0);

        const stdoutStr = execOptions.encoding === 'buffer' ? stdoutBuf : stdoutBuf.toString(execOptions.encoding);
        const stderrStr = execOptions.encoding === 'buffer' ? stderrBuf : stderrBuf.toString(execOptions.encoding);

        if (err) {
          const error = new Error(err);
          error.code = exitCode;
          callback(error, stdoutStr, stderrStr);
        } else if (exitCode !== 0) {
          const error = new Error(`Command failed: ${file}`);
          error.code = exitCode;
          callback(error, stdoutStr, stderrStr);
        } else {
          callback(null, stdoutStr, stderrStr);
        }
      }

      child._exitCode = exitCode;
      child.emit('exit', exitCode, null);
      child.emit('close', exitCode, null);
    });

    return child;
  }

  /**
   * Fork a new Node.js process (spawns gnode).
   */
  function fork(modulePath, args, options) {
    if (!Array.isArray(args)) {
      options = args;
      args = [];
    }

    options = options || {};
    
    // Get the gnode executable path
    const gnodePath = options.execPath || process.execPath || 'gnode';
    const execArgs = options.execArgv || [];

    const spawnArgs = [...execArgs, modulePath, ...args];
    
    const child = spawn(gnodePath, spawnArgs, {
      ...options,
      stdio: options.stdio || ['pipe', 'pipe', 'pipe', 'ipc']
    });

    return child;
  }

  /**
   * Synchronous exec.
   */
  function execSync(command, options) {
    options = options || {};
    
    const execOptions = {
      cwd: options.cwd || process.cwd(),
      env: options.env ? Object.entries(options.env).map(([k, v]) => `${k}=${v}`) : null,
      timeout: options.timeout || 0,
      encoding: options.encoding || 'utf8'
    };

    const result = internal.execSync(command, JSON.stringify(execOptions));
    
    if (result.error) {
      const error = new Error(result.error);
      error.status = result.exitCode;
      error.stdout = result.stdout ? Buffer.from(result.stdout, 'binary') : Buffer.alloc(0);
      error.stderr = result.stderr ? Buffer.from(result.stderr, 'binary') : Buffer.alloc(0);
      throw error;
    }

    const stdout = result.stdout ? Buffer.from(result.stdout, 'binary') : Buffer.alloc(0);
    
    if (execOptions.encoding === 'buffer') {
      return stdout;
    }
    return stdout.toString(execOptions.encoding);
  }

  /**
   * Synchronous execFile.
   */
  function execFileSync(file, args, options) {
    if (!Array.isArray(args)) {
      options = args;
      args = [];
    }

    options = options || {};

    const execOptions = {
      cwd: options.cwd || process.cwd(),
      env: options.env ? Object.entries(options.env).map(([k, v]) => `${k}=${v}`) : null,
      timeout: options.timeout || 0,
      encoding: options.encoding || 'utf8'
    };

    const result = internal.execFileSync(file, args, JSON.stringify(execOptions));

    if (result.error) {
      const error = new Error(result.error);
      error.status = result.exitCode;
      error.stdout = result.stdout ? Buffer.from(result.stdout, 'binary') : Buffer.alloc(0);
      error.stderr = result.stderr ? Buffer.from(result.stderr, 'binary') : Buffer.alloc(0);
      throw error;
    }

    const stdout = result.stdout ? Buffer.from(result.stdout, 'binary') : Buffer.alloc(0);

    if (execOptions.encoding === 'buffer') {
      return stdout;
    }
    return stdout.toString(execOptions.encoding);
  }

  /**
   * Synchronous spawn.
   */
  function spawnSync(command, args, options) {
    if (!Array.isArray(args)) {
      options = args;
      args = [];
    }

    options = options || {};

    const spawnOptions = {
      cwd: options.cwd || process.cwd(),
      env: options.env ? Object.entries(options.env).map(([k, v]) => `${k}=${v}`) : null,
      shell: options.shell || false,
      timeout: options.timeout || 0,
      encoding: options.encoding || 'utf8'
    };

    const result = internal.spawnSync(command, args, JSON.stringify(spawnOptions));

    return {
      pid: result.pid || 0,
      output: [null, result.stdout, result.stderr],
      stdout: result.stdout ? Buffer.from(result.stdout, 'binary') : Buffer.alloc(0),
      stderr: result.stderr ? Buffer.from(result.stderr, 'binary') : Buffer.alloc(0),
      status: result.exitCode,
      signal: result.signal || null,
      error: result.error ? new Error(result.error) : undefined
    };
  }

  // Export module
  const child_process = {
    ChildProcess,
    spawn,
    exec,
    execFile,
    fork,
    execSync,
    execFileSync,
    spawnSync
  };

  global.__child_process_module = child_process;

})(globalThis);
