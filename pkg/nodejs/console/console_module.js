// require('console') / require('node:console').
//
// The default `console` global is implemented in Go and installed separately.
// This module adds the Console *class* so code can build its own console bound
// to specific streams (`new Console({ stdout, stderr })`) and require the
// module, matching Node's lib/internal/console/constructor.js. It is a real
// implementation (grouping, counting, timing, tables, inspection) backed by the
// core util module, not a set of no-op stubs. util is resolved lazily because
// it registers after console.
(function(global) {
  'use strict';

  function util() {
    return global.__util_module;
  }

  function format(args) {
    const u = util();
    if (u && typeof u.format === 'function') {
      return u.format.apply(u, args);
    }
    return args.map(String).join(' ');
  }

  function inspect(value, options) {
    const u = util();
    if (u && typeof u.inspect === 'function') {
      return u.inspect(value, options);
    }
    return String(value);
  }

  function now() {
    if (global.performance && typeof global.performance.now === 'function') {
      return global.performance.now();
    }
    return Date.now();
  }

  function formatDuration(ms) {
    if (ms >= 1000) return (ms / 1000).toFixed(3) + 's';
    return ms.toFixed(3) + 'ms';
  }

  // ---- string width / padding helpers (ANSI-aware) -------------------------

  const ANSI = /\u001b\[[0-9;]*m/g;

  function displayWidth(str) {
    return str.replace(ANSI, '').length;
  }

  function padCenter(str, width) {
    const w = displayWidth(str);
    if (w >= width) return str;
    const total = width - w;
    const left = Math.floor(total / 2);
    return ' '.repeat(left) + str + ' '.repeat(total - left);
  }

  // ---- console.table renderer ---------------------------------------------

  function renderTable(data, columns) {
    if (data === null || typeof data !== 'object') {
      return null;
    }

    const rows = [];
    const indexKeys = [];
    const isArray = Array.isArray(data);
    if (isArray) {
      for (let i = 0; i < data.length; i++) {
        indexKeys.push(String(i));
        rows.push(data[i]);
      }
    } else {
      for (const k of Object.keys(data)) {
        indexKeys.push(k);
        rows.push(data[k]);
      }
    }

    // Determine columns: union of own enumerable keys of object rows.
    const cols = [];
    let hasValuesColumn = false;
    for (const row of rows) {
      if (row !== null && typeof row === 'object' && !(row instanceof Date)) {
        for (const k of Object.keys(row)) {
          if (columns && columns.indexOf(k) === -1) continue;
          if (cols.indexOf(k) === -1) cols.push(k);
        }
      } else {
        hasValuesColumn = true;
      }
    }

    const header = ['(index)', ...cols];
    if (hasValuesColumn) header.push('Values');

    const cellInspect = (v) =>
      inspect(v, { depth: 0, colors: false, breakLength: Infinity });

    const body = rows.map((row, i) => {
      const line = [indexKeys[i]];
      for (const c of cols) {
        if (row !== null && typeof row === 'object' && Object.prototype.hasOwnProperty.call(row, c)) {
          line.push(cellInspect(row[c]));
        } else {
          line.push('');
        }
      }
      if (hasValuesColumn) {
        if (row === null || typeof row !== 'object' || row instanceof Date) {
          line.push(cellInspect(row));
        } else {
          line.push('');
        }
      }
      return line;
    });

    const widths = header.map((h, c) => {
      let w = displayWidth(h);
      for (const line of body) w = Math.max(w, displayWidth(line[c]));
      return w;
    });

    const bar = (l, m, r) =>
      l + widths.map((w) => '─'.repeat(w + 2)).join(m) + r;
    const rowLine = (cells) =>
      '│' + cells.map((cell, c) => ' ' + padCenter(cell, widths[c]) + ' ').join('│') + '│';

    const out = [];
    out.push(bar('┌', '┬', '┐'));
    out.push(rowLine(header));
    out.push(bar('├', '┼', '┤'));
    for (const line of body) out.push(rowLine(line));
    out.push(bar('└', '┴', '┘'));
    return out.join('\n');
  }

  class Console {
    constructor(options) {
      let stdout;
      let stderr;
      if (options && typeof options.write === 'function') {
        stdout = options;
        stderr = options;
      } else if (options) {
        stdout = options.stdout;
        stderr = options.stderr || options.stdout;
      }
      if (!stdout || typeof stdout.write !== 'function') {
        throw new TypeError('Console expects a writable stream (stdout)');
      }
      this._stdout = stdout;
      this._stderr = stderr && typeof stderr.write === 'function' ? stderr : stdout;
      this._groupIndent = '';
      this._times = new Map();
      this._counts = new Map();
      this._inspectOptions = (options && options.inspectOptions) || undefined;

      // Bind the common methods so they can be passed around detached, as Node
      // does (e.g. `const { log } = new Console(...)`).
      const methods = ['log', 'info', 'debug', 'warn', 'error', 'dir', 'trace'];
      for (const m of methods) this[m] = this[m].bind(this);
    }

    _writeTo(stream, str) {
      const indent = this._groupIndent;
      const text = indent ? indent + str.split('\n').join('\n' + indent) : str;
      stream.write(text + '\n');
    }

    log(...args) {
      this._writeTo(this._stdout, format(args));
    }

    info(...args) {
      this.log(...args);
    }

    debug(...args) {
      this.log(...args);
    }

    dir(obj, options) {
      this._writeTo(this._stdout, inspect(obj, options || this._inspectOptions));
    }

    warn(...args) {
      this._writeTo(this._stderr, format(args));
    }

    error(...args) {
      this._writeTo(this._stderr, format(args));
    }

    trace(...args) {
      const msg = args.length ? format(args) : '';
      const err = new Error(msg);
      err.name = 'Trace';
      this._writeTo(this._stderr, err.stack || ('Trace: ' + msg));
    }

    assert(condition, ...args) {
      if (!condition) {
        this._writeTo(
          this._stderr,
          'Assertion failed' + (args.length ? ': ' + format(args) : '')
        );
      }
    }

    table(data, columns) {
      const rendered = renderTable(data, columns);
      if (rendered === null) {
        this.log(data);
        return;
      }
      this._writeTo(this._stdout, rendered);
    }

    group(...args) {
      if (args.length) this.log(...args);
      this._groupIndent += '  ';
    }

    groupCollapsed(...args) {
      this.group(...args);
    }

    groupEnd() {
      this._groupIndent = this._groupIndent.slice(0, -2);
    }

    count(label = 'default') {
      const next = (this._counts.get(label) || 0) + 1;
      this._counts.set(label, next);
      this.log(`${label}: ${next}`);
    }

    countReset(label = 'default') {
      this._counts.delete(label);
    }

    time(label = 'default') {
      if (this._times.has(label)) {
        this.warn(`Warning: Label '${label}' already exists for console.time()`);
        return;
      }
      this._times.set(label, now());
    }

    timeEnd(label = 'default') {
      const start = this._times.get(label);
      if (start === undefined) {
        this.warn(`Warning: No such label '${label}' for console.timeEnd()`);
        return;
      }
      this._times.delete(label);
      this.log(`${label}: ${formatDuration(now() - start)}`);
    }

    timeLog(label = 'default', ...args) {
      const start = this._times.get(label);
      if (start === undefined) {
        this.warn(`Warning: No such label '${label}' for console.timeLog()`);
        return;
      }
      const prefix = `${label}: ${formatDuration(now() - start)}`;
      this.log(prefix, ...args);
    }

    dirxml(...args) {
      this.log(...args);
    }

    clear() {
      if (typeof this._stdout.isTTY === 'boolean' && this._stdout.isTTY) {
        this._stdout.write('\u001b[2J\u001b[0f');
      }
    }

    profile() {}
    profileEnd() {}
    timeStamp() {}
  }

  if (global.console) {
    global.console.Console = Console;
  }
  global.__console_module = global.console;
})(globalThis);
