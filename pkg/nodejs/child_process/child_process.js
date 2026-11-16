// child_process - Child process spawning
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const { Readable, Writable, Duplex } = global.__stream_module;
  const internal = global.__child_process_internal;

  const signals = internal.signals;
  const signalNames = {};
  // Where two names share a number, such as SIGABRT and SIGIOT, Node reports the first.
  for (const name of Object.keys(signals)) {
    if (!(signals[name] in signalNames)) signalNames[signals[name]] = name;
  }

  const kDefaultMaxBuffer = 1024 * 1024;
  // What internal.kill returns. These numbers are the same on every platform.
  const kEPERM = -1;
  const kESRCH = -3;
  // Node reports these from the 'error' event. It throws any other spawn failure.
  const kRuntimeSpawnErrors = new Set(['EACCES', 'EAGAIN', 'EMFILE', 'ENFILE', 'ENOENT']);

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  // How Node's error messages print a value.
  function inspected(value) {
    if (typeof value === 'string') {
      const escaped = value
        .replace(/[\x00-\x1f\x7f-\x9f]/g, (c) => `\\x${c.charCodeAt(0).toString(16).padStart(2, '0').toUpperCase()}`)
        .replace(/'/g, "\\'");
      return `'${escaped}'`;
    }
    return global.__util_module.inspect(value);
  }

  function received(value) {
    if (value === null || value === undefined) return `Received ${value}`;
    if (typeof value === 'function') return `Received function ${value.name}`;
    if (typeof value === 'object') {
      if (value.constructor && value.constructor.name) return `Received an instance of ${value.constructor.name}`;
      return `Received ${inspected(value)}`;
    }
    return `Received type ${typeof value} (${inspected(value)})`;
  }

  function invalidArgType(name, expected, value) {
    const kind = name.includes('.') ? 'property' : 'argument';
    return codedError(TypeError, 'ERR_INVALID_ARG_TYPE', `The "${name}" ${kind} must be of type ${expected}. ${received(value)}`);
  }

  function invalidArgValue(name, value, reason) {
    const kind = name.includes('.') ? 'property' : 'argument';
    return codedError(TypeError, 'ERR_INVALID_ARG_VALUE', `The ${kind} '${name}' ${reason || 'is invalid'}. Received ${inspected(value)}`);
  }

  // A process receives its strings as C strings, which end at the first NUL.
  function checkNoNul(value, name) {
    if (typeof value === 'string' && value.includes('\u0000')) {
      throw invalidArgValue(name, value, 'must be a string without null bytes');
    }
  }

  // An error for a failed system call, shaped as Node's are.
  function errnoError(desc, syscallName) {
    const detail = desc.detail ? ` (${desc.detail})` : '';
    const err = new Error(`${syscallName} ${desc.code}${detail}`);
    err.errno = desc.errno;
    err.code = desc.code;
    err.syscall = syscallName;
    return err;
  }

  // A failure to start or run a known file.
  function spawnFailure(desc, syscallName, file, spawnargs) {
    const err = errnoError(desc, `${syscallName} ${file}`);
    err.path = file;
    err.spawnargs = spawnargs.slice(1);
    return err;
  }

  function convertToValidSignal(signal) {
    if (typeof signal === 'number' && signalNames[signal]) return signal;
    if (typeof signal === 'string') {
      const number = signals[signal.toUpperCase()];
      if (number) return number;
    }
    throw codedError(TypeError, 'ERR_UNKNOWN_SIGNAL', `Unknown signal: ${signal}`);
  }

  function sanitizeKillSignal(killSignal) {
    if (typeof killSignal === 'string' || typeof killSignal === 'number') {
      return convertToValidSignal(killSignal);
    }
    if (killSignal !== null && killSignal !== undefined) {
      throw invalidArgType('options.killSignal', 'string or number', killSignal);
    }
    return undefined;
  }

  function validateTimeout(timeout) {
    if (timeout !== null && timeout !== undefined && !(Number.isInteger(timeout) && timeout >= 0)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        `The value of "timeout" is out of range. It must be an unsigned integer. Received ${inspected(timeout)}`);
    }
  }

  function validateMaxBuffer(maxBuffer) {
    if (maxBuffer !== null && maxBuffer !== undefined && !(typeof maxBuffer === 'number' && maxBuffer >= 0)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        `The value of "options.maxBuffer" is out of range. It must be a non-negative number. Received ${inspected(maxBuffer)}`);
    }
  }

  // Node's encoding option: 'buffer' or null mean Buffers.
  function outputEncoding(options, fallback) {
    const { encoding } = options;
    if (encoding === undefined) return fallback;
    return encoding === null ? 'buffer' : encoding;
  }

  // The input of the synchronous calls, as base64 for Go.
  function encodeInput(input, encoding) {
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
   * Turns the stdio option into one {type, fd?, input?} per descriptor, where type is
   * 'pipe', 'ignore' or 'fd'.
   */
  function validStdio(stdio, sync) {
    if (typeof stdio === 'string') {
      if (stdio !== 'pipe' && stdio !== 'overlapped' && stdio !== 'ignore' && stdio !== 'inherit') {
        throw invalidArgValue('stdio', stdio);
      }
      stdio = [stdio, stdio, stdio];
    } else if (!Array.isArray(stdio)) {
      throw invalidArgValue('stdio', stdio);
    }

    const entries = [];
    for (let i = 0; i < Math.max(stdio.length, 3); i++) {
      let entry = stdio[i];
      if (entry === null || entry === undefined) entry = i < 3 ? 'pipe' : 'ignore';

      if (entry === 'ignore') {
        entries.push({ type: 'ignore' });
      } else if (entry === 'pipe' || entry === 'overlapped') {
        entries.push({ type: 'pipe' });
      } else if (entry === 'inherit') {
        // Script has no way to learn of real descriptors above stdio.
        entries.push(i < 3 ? { type: 'fd', fd: i } : { type: 'ignore' });
      } else if (entry === 'ipc') {
        // There is no IPC channel, so there is nothing at this descriptor.
        entries.push({ type: 'ignore' });
      } else if (typeof entry === 'number') {
        // As in Node, a negative number asks for the default, a pipe.
        entries.push(entry < 0 ? { type: 'pipe' } : { type: 'fd', fd: entry });
      } else if (typeof entry.fd === 'number') {
        entries.push({ type: 'fd', fd: entry.fd });
      } else if (ArrayBuffer.isView(entry) || typeof entry === 'string') {
        if (!sync) {
          throw codedError(TypeError, 'ERR_INVALID_SYNC_FORK_INPUT',
            `Asynchronous forks do not support Buffer, TypedArray, DataView or string input: ${inspected(entry)}`);
        }
        entries.push({ type: 'pipe', input: entry });
      } else {
        throw invalidArgValue('stdio', stdio);
      }
    }
    return entries;
  }

  /**
   * Validates the arguments of every way to start a process and works out what to run.
   * spawnfile and spawnargs are what Node reports. command and args are what the native
   * side runs: the spawner sees a shell line as such, so its sandbox rules can treat it
   * as one.
   */
  function normalizeSpawnArguments(file, args, options) {
    if (typeof file !== 'string') throw invalidArgType('file', 'string', file);
    if (file.length === 0) throw invalidArgValue('file', file, 'cannot be empty');
    checkNoNul(file, 'file');

    if (Array.isArray(args)) {
      args = args.slice();
    } else if (args === null || args === undefined) {
      args = [];
    } else if (typeof args !== 'object') {
      throw invalidArgType('args', 'object', args);
    } else {
      options = args;
      args = [];
    }
    args.forEach((arg, i) => checkNoNul(arg, `args[${i}]`));

    if (options === undefined || options === null) {
      options = {};
    } else if (typeof options !== 'object') {
      throw invalidArgType('options', 'object', options);
    }

    let cwd = options.cwd;
    if (cwd !== undefined && cwd !== null) {
      if (typeof URL !== 'undefined' && cwd instanceof URL) cwd = decodeURIComponent(cwd.pathname);
      else if (ArrayBuffer.isView(cwd)) cwd = Buffer.from(cwd.buffer, cwd.byteOffset, cwd.byteLength).toString();
      if (typeof cwd !== 'string') throw invalidArgType('options.cwd', 'string or an instance of Buffer or URL', cwd);
      if (cwd.includes('\u0000')) {
        throw invalidArgValue('options.cwd', cwd, 'must be a string, Uint8Array, or URL without null bytes');
      }
    }

    // As in Node, a child with no env option gets process.env: the environment the script
    // sees, which an embedder may have set up, rather than the host process's own.
    const envSource = options.env !== undefined && options.env !== null ? options.env : process.env;
    const env = [];
    for (const key of Object.keys(envSource)) {
      const value = envSource[key];
      if (value === undefined) continue;
      checkNoNul(key, `options.env['${key}']`);
      checkNoNul(value, `options.env['${key}']`);
      env.push(`${key}=${value}`);
    }

    const { shell } = options;
    if (shell !== undefined && shell !== null && typeof shell !== 'boolean' && typeof shell !== 'string') {
      throw invalidArgType('options.shell', 'boolean or string', shell);
    }

    if (options.argv0 !== undefined && options.argv0 !== null && typeof options.argv0 !== 'string') {
      throw invalidArgType('options.argv0', 'string', options.argv0);
    }
    let spawnfile = file;
    let spawnargs = [typeof options.argv0 === 'string' ? options.argv0 : file, ...args];
    let command = file;
    let nativeShell = false;
    if (shell) {
      const line = [file, ...args].join(' ');
      spawnfile = typeof shell === 'string' ? shell : '/bin/sh';
      // As in Node, argv0 names the shell process.
      spawnargs = [typeof options.argv0 === 'string' ? options.argv0 : spawnfile, '-c', line];
      // A shell the script names is an ordinary program, and is checked as one.
      if (typeof shell === 'string') {
        command = shell;
        args = ['-c', line];
      } else {
        command = line;
        args = [];
        nativeShell = true;
      }
    }

    return {
      command,
      args,
      spawnfile,
      spawnargs,
      options,
      nativeOptions: {
        cwd: cwd || process.cwd(),
        env,
        shell: nativeShell,
        detached: !!options.detached,
        argv0: typeof options.argv0 === 'string' ? options.argv0 : ''
      }
    };
  }

  /**
   * ChildProcess class - Represents a spawned child process.
   */
  class ChildProcess extends EventEmitter {
    constructor() {
      super();
      this._id = null;
      this._pid = undefined;
      this._exitCode = null;
      this._signalCode = null;
      this._exited = false;
      this._killed = false;
      this._connected = true;
      this._spawnfile = null;
      this._spawnargs = null;

      this.stdin = null;
      this.stdout = null;
      this.stderr = null;
      this.stdio = [null, null, null];
      this._closesGot = 0;
      this._closesNeeded = 1;
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
      const number = signal === 0 ? 0 : convertToValidSignal(signal === undefined ? 'SIGTERM' : signal);
      if (this._id === null || this._exited) return false;

      const err = internal.kill(this._id, number);
      if (err === 0) {
        this._killed = true;
        return true;
      }
      // A process that has already gone is not an error.
      if (err !== kESRCH) {
        this.emit('error', errnoError({ errno: err, code: err === kEPERM ? 'EPERM' : 'UNKNOWN' }, 'kill'));
      }
      return false;
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
        // Node reads output from the start, so it sees a pipe close while the child runs.
        // Reading ahead here does the same, so the end of output can let the loop finish.
        for (const stream of this.stdio || []) {
          if (stream && stream.readable && !stream.readableFlowing) stream.read(0);
        }
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
     * Internal: Create the streams stdio asks for. A null id means the process did
     * not start.
     */
    _setupStreams(entries) {
      this.stdio = entries.map((entry, fd) => {
        if (entry.type !== 'pipe') return null;
        if (fd === 0) return new ProcessWritableStream(this._id, fd);
        if (fd < 3) return new ProcessReadableStream(this._id, fd);
        return new ProcessDuplexStream(this._id, fd);
      });
      [this.stdin, this.stdout, this.stderr] = this.stdio;

      // As in Node, 'close' waits for the exit and for all of stdio to close.
      for (const stream of this.stdio) {
        if (stream === null) continue;
        this._closesNeeded++;
        stream.once('close', () => this._maybeClose());
      }
    }

    _maybeClose() {
      this._closesGot++;
      if (this._closesGot === this._closesNeeded) {
        this.emit('close', this._exitCode, this._signalCode);
      }
    }

    // Output nobody is reading would otherwise keep 'close' from firing.
    _flushStdio() {
      for (const stream of this.stdio) {
        if (stream && stream.readable && !stream.readableFlowing) stream.resume();
      }
    }

    // The process could not start. As in Node this emits 'error' in place of 'exit'.
    _onStartFailure(err, errno) {
      this._exitCode = errno;
      if (this.stdin) this.stdin.destroy();
      this.emit('error', err);
      process.nextTick(() => this._flushStdio());
      this._maybeClose();
    }

    /**
     * Internal: Start waiting for exit.
     */
    _waitForExit() {
      internal.wait(this._id, (err, exitCode, signalNumber) => {
        this._exited = true;
        const signal = signalNumber ? (signalNames[signalNumber] || `SIG${signalNumber}`) : null;
        this._signalCode = signal;
        this._exitCode = signal ? null : exitCode;

        if (this.stdin) this.stdin.destroy();
        this.emit('exit', this._exitCode, this._signalCode);
        process.nextTick(() => this._flushStdio());
        this._maybeClose();
      });
    }
  }

  function readNative(stream) {
    if (stream._processId === null) {
      stream.push(null);
      return;
    }
    if (stream._reading) return;
    stream._reading = true;
    internal.read(stream._processId, stream._fd, (data) => {
      stream._reading = false;
      if (stream.destroyed) return;
      stream.push(data === null ? null : Buffer.from(data, 'base64'));
    });
  }

  function writeNative(stream, chunk, encoding, callback) {
    if (stream._processId === null) {
      callback(codedError(Error, 'ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
      return;
    }
    const buf = typeof chunk === 'string' ? Buffer.from(chunk, encoding) : chunk;
    internal.write(stream._processId, stream._fd, buf.toString('base64'), (message, code) => {
      // As in Node, a write handed over before the stream closed reports no error.
      if (!message || stream.destroyed) {
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

  /**
   * Readable stream for process output.
   */
  class ProcessReadableStream extends Readable {
    constructor(processId, fd) {
      super();
      this._processId = processId;
      this._fd = fd;
      this._reading = false;
    }

    _read() {
      readNative(this);
    }

    _destroy(err, callback) {
      if (this._processId !== null) internal.closeOutput(this._processId, this._fd);
      callback(err);
    }
  }

  /**
   * Writable stream for process input. The base class hands over one chunk at a time
   * and calls _final only after the last one, so writes stay in order and all arrive
   * before stdin closes.
   */
  class ProcessWritableStream extends Writable {
    constructor(processId, fd) {
      super();
      this._processId = processId;
      this._fd = fd;
    }

    _write(chunk, encoding, callback) {
      writeNative(this, chunk, encoding, callback);
    }

    _final(callback) {
      if (this._processId !== null) internal.closeInput(this._processId, this._fd);
      callback();
    }

    _destroy(err, callback) {
      if (this._processId !== null) internal.closeInput(this._processId, this._fd);
      callback(err);
    }
  }

  /**
   * Stream for a pipe above stdio, which carries data both ways.
   */
  class ProcessDuplexStream extends Duplex {
    constructor(processId, fd) {
      // As with Node's socket here, the end of the child's data ends the whole stream.
      super({ allowHalfOpen: false });
      this._processId = processId;
      this._fd = fd;
      this._reading = false;
    }

    _read() {
      readNative(this);
    }

    _write(chunk, encoding, callback) {
      writeNative(this, chunk, encoding, callback);
    }

    _final(callback) {
      if (this._processId !== null) internal.closeInput(this._processId, this._fd);
      callback();
    }

    _destroy(err, callback) {
      if (this._processId !== null) internal.closeOutput(this._processId, this._fd);
      callback(err);
    }
  }

  /**
   * Spawn a child process.
   */
  function spawn(file, args, options) {
    const normalized = normalizeSpawnArguments(file, args, options);
    const stdio = validStdio(normalized.options.stdio || 'pipe', false);

    // Options are checked before anything starts, so a bad one runs nothing.
    const killSignal = validateTimeoutAndSignal(normalized.options);

    const child = new ChildProcess();
    child._spawnfile = normalized.spawnfile;
    child._spawnargs = normalized.spawnargs;

    const started = internal.spawn(normalized.command, normalized.args,
      JSON.stringify({ ...normalized.nativeOptions, stdio }));

    if (started.code) {
      if (!kRuntimeSpawnErrors.has(started.code)) throw errnoError(started, 'spawn');
      child._setupStreams(stdio);
      const err = spawnFailure(started, 'spawn', child._spawnfile, child._spawnargs);
      process.nextTick(() => child._onStartFailure(err, started.errno));
      return child;
    }

    child._id = started.id;
    child._pid = started.pid;
    child._setupStreams(stdio);
    child._waitForExit();
    process.nextTick(() => child.emit('spawn'));
    watchTimeoutAndSignal(child, normalized.options, killSignal);

    return child;
  }

  // Checks spawn's timeout, killSignal and signal options, and returns the signal to kill with.
  function validateTimeoutAndSignal(options) {
    validateTimeout(options.timeout);
    const killSignal = sanitizeKillSignal(options.killSignal) || 'SIGTERM';
    const signal = options.signal;
    if (signal !== undefined && signal !== null && (typeof signal !== 'object' || !('aborted' in signal))) {
      throw invalidArgType('options.signal', 'AbortSignal', signal);
    }
    return killSignal;
  }

  // spawn's timeout and signal options: either kills the child with killSignal, and an abort
  // also emits an AbortError, as Node does.
  function watchTimeoutAndSignal(child, options, killSignal) {
    if (options.timeout > 0) {
      let timeoutId = setTimeout(() => {
        if (!timeoutId) return;
        timeoutId = null;
        try {
          child.kill(killSignal);
        } catch (err) {
          child.emit('error', err);
        }
      }, options.timeout);
      child.once('exit', () => {
        if (timeoutId) clearTimeout(timeoutId);
        timeoutId = null;
      });
    }
    const signal = options.signal;
    if (signal !== undefined && signal !== null) {
      const onAbort = () => {
        if (child.kill(killSignal)) {
          const err = new Error('The operation was aborted');
          err.name = 'AbortError';
          err.code = 'ABORT_ERR';
          if (signal.reason !== undefined) err.cause = signal.reason;
          child.emit('error', err);
        }
      };
      if (signal.aborted) {
        process.nextTick(onAbort);
      } else {
        signal.addEventListener('abort', onAbort, { once: true });
        child.once('exit', () => signal.removeEventListener('abort', onAbort));
      }
    }
  }

  // The (file[, args][, options][, callback]) forms of execFile and execFileSync.
  function normalizeExecFileArgs(file, args, options, callback) {
    if (Array.isArray(args)) {
      args = args.slice();
    } else if (args !== null && typeof args === 'object') {
      callback = options;
      options = args;
      args = null;
    } else if (typeof args === 'function') {
      callback = args;
      options = null;
      args = null;
    }

    if (args === null || args === undefined) args = [];

    if (typeof options === 'function') {
      callback = options;
      options = null;
    } else if (options !== null && options !== undefined && typeof options !== 'object') {
      throw invalidArgType('options', 'object', options);
    }

    if (callback !== null && callback !== undefined && typeof callback !== 'function') {
      throw invalidArgType('callback', 'function', callback);
    }

    return { file, args, options: options || {}, callback };
  }

  // exec and execSync run their command through a shell.
  function normalizeExecArgs(command, options, callback) {
    if (typeof command !== 'string') throw invalidArgType('command', 'string', command);
    checkNoNul(command, 'command');
    if (typeof options === 'function') {
      callback = options;
      options = undefined;
    }
    options = { ...options };
    options.shell = typeof options.shell === 'string' ? options.shell : true;
    return { file: command, options, callback };
  }

  /**
   * Execute a file directly, buffering its output for the callback.
   */
  function execFile(file, args, options, callback) {
    ({ file, args, options, callback } = normalizeExecFileArgs(file, args, options, callback));

    options = {
      encoding: 'utf8',
      timeout: 0,
      maxBuffer: kDefaultMaxBuffer,
      killSignal: 'SIGTERM',
      ...options
    };
    validateTimeout(options.timeout);
    validateMaxBuffer(options.maxBuffer);
    options.killSignal = sanitizeKillSignal(options.killSignal);

    const child = spawn(file, args, {
      cwd: options.cwd,
      env: options.env,
      shell: options.shell,
      detached: options.detached
    });

    const encoding = options.encoding && options.encoding !== 'buffer' && Buffer.isEncoding(options.encoding)
      ? options.encoding : null;
    const outputs = { stdout: [], stderr: [] };
    const lengths = { stdout: 0, stderr: 0 };
    let killed = false;
    let exited = false;
    let timeoutId;
    let ex = null;
    let cmd = file;

    function exitHandler(code, signal) {
      if (exited) return;
      exited = true;

      if (timeoutId) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }
      if (!callback) return;

      // As in Node, output is a string if the stream decodes, even when the caller set its
      // encoding after exec returned.
      const collect = (name) => (encoding || child[name].readableEncoding
        ? outputs[name].join('') : Buffer.concat(outputs[name]));
      const stdout = collect('stdout');
      const stderr = collect('stderr');

      if (!ex && code === 0 && signal === null) {
        callback(null, stdout, stderr);
        return;
      }

      if (args.length) cmd += ` ${args.join(' ')}`;

      if (!ex) {
        ex = new Error(`Command failed: ${cmd}\n${stderr}`);
        ex.code = code;
        ex.killed = child.killed || killed;
        ex.signal = signal;
      }
      ex.cmd = cmd;
      callback(ex, stdout, stderr);
    }

    function errorHandler(err) {
      ex = err;
      child.stdout.destroy();
      child.stderr.destroy();
      exitHandler();
    }

    function kill() {
      child.stdout.destroy();
      child.stderr.destroy();
      killed = true;
      try {
        child.kill(options.killSignal);
      } catch (err) {
        ex = err;
        exitHandler();
      }
    }

    if (options.timeout > 0) {
      timeoutId = setTimeout(() => {
        kill();
        timeoutId = null;
      }, options.timeout);
    }

    for (const name of ['stdout', 'stderr']) {
      if (encoding) child[name].setEncoding(encoding);
      child[name].on('data', (chunk) => {
        if (lengths[name] > options.maxBuffer) return;
        // maxBuffer counts bytes, and a decoded chunk's length counts characters.
        const streamEncoding = child[name].readableEncoding;
        const length = streamEncoding ? Buffer.byteLength(chunk, streamEncoding) : chunk.length;
        lengths[name] += length;
        if (lengths[name] > options.maxBuffer) {
          outputs[name].push(chunk.slice(0, options.maxBuffer - (lengths[name] - length)));
          ex = codedError(RangeError, 'ERR_CHILD_PROCESS_STDIO_MAXBUFFER', `${name} maxBuffer length exceeded`);
          kill();
        } else {
          outputs[name].push(chunk);
        }
      });
    }

    child.addListener('close', exitHandler);
    child.addListener('error', errorHandler);

    return child;
  }

  /**
   * Execute a command in a shell.
   */
  function exec(command, options, callback) {
    const normalized = normalizeExecArgs(command, options, callback);
    return execFile(normalized.file, normalized.options, normalized.callback);
  }

  // util.promisify(exec) resolves to {stdout, stderr}, and a rejection carries both too.
  function promisifiedExec(original) {
    return (...args) => {
      let child;
      const promise = new Promise((resolve, reject) => {
        child = original(...args, (err, stdout, stderr) => {
          if (err !== null) {
            err.stdout = stdout;
            err.stderr = stderr;
            reject(err);
          } else {
            resolve({ stdout, stderr });
          }
        });
      });
      promise.child = child;
      return promise;
    };
  }

  const kPromisifyCustom = Symbol.for('nodejs.util.promisify.custom');
  Object.defineProperty(exec, kPromisifyCustom, { enumerable: false, value: promisifiedExec(exec) });
  Object.defineProperty(execFile, kPromisifyCustom, { enumerable: false, value: promisifiedExec(execFile) });

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
   * Synchronous spawn.
   */
  function spawnSync(file, args, options) {
    const normalized = normalizeSpawnArguments(file, args, options);
    options = normalized.options;

    validateTimeout(options.timeout);
    const maxBuffer = options.maxBuffer === undefined ? kDefaultMaxBuffer : options.maxBuffer;
    validateMaxBuffer(maxBuffer);
    const killSignal = sanitizeKillSignal(options.killSignal) || signals.SIGTERM;

    const stdio = validStdio(options.stdio || 'pipe', true);
    const encoding = outputEncoding(options, 'buffer');
    const input = options.input !== undefined && options.input !== null ? options.input : stdio[0].input;

    const result = internal.spawnSync(normalized.command, normalized.args, JSON.stringify({
      ...normalized.nativeOptions,
      stdio: stdio.map(({ type, fd }) => ({ type, fd })),
      timeout: options.timeout || 0,
      maxBuffer: maxBuffer === null || maxBuffer === Infinity ? -1 : maxBuffer,
      killSignal,
      input: encodeInput(input, encoding)
    }));

    if (result.pid === undefined) {
      if (!result.code) throw new Error('child_process: spawn failed');
      return {
        error: spawnFailure(result, 'spawnSync', normalized.spawnfile, normalized.spawnargs),
        status: null,
        signal: null,
        output: null,
        pid: 0,
        stdout: null,
        stderr: null
      };
    }

    const output = stdio.map((entry, fd) => {
      if (fd === 0 || entry.type !== 'pipe') return null;
      const buf = Buffer.from(result.output[fd] || '', 'base64');
      return encoding === 'buffer' ? buf : buf.toString(encoding);
    });

    const ret = {
      status: result.status,
      signal: result.signal ? (signalNames[result.signal] || `SIG${result.signal}`) : null,
      output,
      pid: result.pid,
      stdout: output[1],
      stderr: output[2]
    };
    if (result.code) {
      ret.error = spawnFailure(result, 'spawnSync', normalized.spawnfile, normalized.spawnargs);
    }
    return ret;
  }

  // The error execSync and execFileSync throw for a result, if it calls for one.
  function checkExecSyncError(ret, command) {
    if (ret.error) return Object.assign(ret.error, ret);
    if (ret.status === 0) return undefined;
    let message = `Command failed: ${command}`;
    if (ret.stderr && ret.stderr.length > 0) message += `\n${ret.stderr.toString()}`;
    return Object.assign(new Error(message), ret);
  }

  /**
   * Synchronous exec.
   */
  function execSync(command, options) {
    const normalized = normalizeExecArgs(command, options, null);
    // Without a stdio option, the child's stderr also goes to this process's stderr.
    const inheritStderr = !normalized.options.stdio;

    const ret = spawnSync(normalized.file, normalized.options);
    if (inheritStderr && ret.stderr) process.stderr.write(ret.stderr);

    const err = checkExecSyncError(ret, command);
    if (err) throw err;
    return ret.stdout;
  }

  /**
   * Synchronous execFile.
   */
  function execFileSync(file, args, options) {
    const normalized = normalizeExecFileArgs(file, args, options);
    const inheritStderr = !normalized.options.stdio;

    const ret = spawnSync(normalized.file, normalized.args, normalized.options);
    if (inheritStderr && ret.stderr) process.stderr.write(ret.stderr);

    const err = checkExecSyncError(ret, [normalized.file, ...normalized.args].join(' '));
    if (err) throw err;
    return ret.stdout;
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
