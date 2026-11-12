// child_process - Child process spawning
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const { Readable, Writable } = global.__stream_module;
  const internal = global.__child_process_internal;

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  // Process output crosses from Go as base64, so every byte survives.
  function decodeOutput(encoded, encoding) {
    const buf = encoded ? Buffer.from(encoded, 'base64') : Buffer.alloc(0);
    return encoding === 'buffer' ? buf : buf.toString(encoding);
  }

  // Node's encoding option: 'buffer' or null mean Buffers.
  function outputEncoding(options, fallback) {
    const { encoding } = options;
    if (encoding === undefined) return fallback;
    return encoding === null ? 'buffer' : encoding;
  }

  // The input option of the synchronous calls, as base64 for Go.
  function encodeInput(options) {
    const { input, encoding } = options;
    if (input === undefined || input === null) return undefined;
    if (typeof input === 'string') {
      return Buffer.from(input, encoding && encoding !== 'buffer' ? encoding : 'utf8').toString('base64');
    }
    if (ArrayBuffer.isView(input)) {
      return Buffer.from(input.buffer, input.byteOffset, input.byteLength).toString('base64');
    }
    throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "options.stdio[0]" property must be of type string or an instance of Buffer, TypedArray, or DataView.');
  }

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
      this._closesGot = 0;
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
     * Internal: Create stdio. A null id means the process did not start.
     */
    _setupStreams() {
      this.stdin = new ProcessWritableStream(this._id);
      this.stdout = new ProcessReadableStream(this._id, 'stdout');
      this.stderr = new ProcessReadableStream(this._id, 'stderr');
      this.stdio = [this.stdin, this.stdout, this.stderr];

      // As in Node, 'close' waits for the exit and for all of stdio to close.
      for (const stream of this.stdio) stream.once('close', () => this._maybeClose());
    }

    _maybeClose() {
      this._closesGot++;
      if (this._closesGot === this.stdio.length + 1) {
        this.emit('close', this._exitCode, this._signalCode);
      }
    }

    // Output nobody is reading would otherwise keep 'close' from firing.
    _flushStdio() {
      if (!this.stdout.readableFlowing) this.stdout.resume();
      if (!this.stderr.readableFlowing) this.stderr.resume();
    }

    /**
     * Internal: Start waiting for exit.
     */
    _waitForExit() {
      internal.wait(this._id, (err, exitCode, signalCode) => {
        this._exitCode = exitCode;
        this._signalCode = signalCode;

        this.stdin.destroy();
        this.emit('exit', exitCode, signalCode);
        process.nextTick(() => this._flushStdio());
        this._maybeClose();
      });
    }
  }

  /**
   * Readable stream for process output.
   */
  class ProcessReadableStream extends Readable {
    constructor(processId, type) {
      super();
      this._processId = processId;
      this._type = type;
      this._reading = false;
    }

    _read() {
      if (this._processId === null) {
        this.push(null);
        return;
      }
      if (this._reading) return;
      this._reading = true;
      internal.read(this._processId, this._type, (data) => {
        this._reading = false;
        if (this.destroyed) return;
        this.push(data === null ? null : Buffer.from(data, 'base64'));
      });
    }

    _destroy(err, callback) {
      if (this._processId !== null) internal.closeOutput(this._processId, this._type);
      callback(err);
    }
  }

  /**
   * Writable stream for process input. The base class hands over one chunk at a time
   * and calls _final only after the last one, so writes stay in order and all arrive
   * before stdin closes.
   */
  class ProcessWritableStream extends Writable {
    constructor(processId) {
      super();
      this._processId = processId;
    }

    _write(chunk, encoding, callback) {
      if (this._processId === null) {
        callback(codedError(Error, 'ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
        return;
      }
      internal.write(this._processId, chunk.toString('base64'), (message, code) => {
        // As in Node, a write handed over before the stream closed reports no error.
        if (!message || this.destroyed) {
          callback();
          return;
        }
        const err = new Error(message);
        if (code) {
          err.code = code;
          err.syscall = 'write';
        }
        callback(err);
      });
    }

    _final(callback) {
      if (this._processId !== null) internal.closeStdin(this._processId);
      callback();
    }

    _destroy(err, callback) {
      if (this._processId !== null) internal.closeStdin(this._processId);
      callback(err);
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

    const started = internal.spawn(command, args, JSON.stringify(spawnOptions));
    if (started.error) {
      child._exitCode = -1;
      child._setupStreams();
      child.stdin.destroy();
      process.nextTick(() => {
        child.emit('error', new Error(started.error));
        child._flushStdio();
        child._maybeClose();
      });
      return child;
    }

    child._id = started.id;
    child._pid = started.pid;
    child._setupStreams();
    child._waitForExit();
    process.nextTick(() => child.emit('spawn'));

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
      encoding: outputEncoding(options, 'utf8')
    };

    const child = new ChildProcess();
    child._spawnfile = '/bin/sh';
    child._spawnargs = ['/bin/sh', '-c', command];

    internal.exec(command, JSON.stringify(execOptions), (err, stdout, stderr, exitCode) => {
      if (callback) {
        const stdoutStr = decodeOutput(stdout, execOptions.encoding);
        const stderrStr = decodeOutput(stderr, execOptions.encoding);

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
      encoding: outputEncoding(options, 'utf8')
    };

    const child = new ChildProcess();
    child._spawnfile = file;
    child._spawnargs = [file, ...args];

    internal.execFile(file, args, JSON.stringify(execOptions), (err, stdout, stderr, exitCode) => {
      if (callback) {
        const stdoutStr = decodeOutput(stdout, execOptions.encoding);
        const stderrStr = decodeOutput(stderr, execOptions.encoding);

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
      encoding: outputEncoding(options, 'buffer'),
      input: encodeInput(options)
    };

    const result = internal.execSync(command, JSON.stringify(execOptions));
    
    if (result.error) {
      const error = new Error(result.error);
      error.status = result.exitCode;
      error.stdout = decodeOutput(result.stdout, execOptions.encoding);
      error.stderr = decodeOutput(result.stderr, execOptions.encoding);
      throw error;
    }

    return decodeOutput(result.stdout, execOptions.encoding);
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
      encoding: outputEncoding(options, 'buffer'),
      input: encodeInput(options)
    };

    const result = internal.execFileSync(file, args, JSON.stringify(execOptions));

    if (result.error) {
      const error = new Error(result.error);
      error.status = result.exitCode;
      error.stdout = decodeOutput(result.stdout, execOptions.encoding);
      error.stderr = decodeOutput(result.stderr, execOptions.encoding);
      throw error;
    }

    return decodeOutput(result.stdout, execOptions.encoding);
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
      encoding: outputEncoding(options, 'buffer'),
      input: encodeInput(options)
    };

    const result = internal.spawnSync(command, args, JSON.stringify(spawnOptions));
    const stdout = decodeOutput(result.stdout, spawnOptions.encoding);
    const stderr = decodeOutput(result.stderr, spawnOptions.encoding);

    return {
      pid: result.pid || 0,
      output: [null, stdout, stderr],
      stdout,
      stderr,
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
