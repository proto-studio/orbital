// readline - Line reading module
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;

  /**
   * Interface for reading lines from a Readable stream.
   */
  class Interface extends EventEmitter {
    constructor(options) {
      super();
      
      this.input = options.input || process.stdin;
      this.output = options.output || process.stdout;
      this.terminal = options.terminal !== undefined ? options.terminal : (this.output && this.output.isTTY);
      this.prompt = options.prompt || '> ';
      this.historySize = options.historySize || 30;
      this.crlfDelay = options.crlfDelay || 100;
      this.escapeCodeTimeout = options.escapeCodeTimeout || 500;
      
      this.history = [];
      this.historyIndex = -1;
      this.line = '';
      this.cursor = 0;
      this.closed = false;
      this._paused = false;
      
      this._completer = options.completer || null;
      this._tabCompleter = options.tabCompleter || null;
      
      if (this.input && this.input.on) {
        this._setupInput();
      }
    }

    _setupInput() {
      this.input.setEncoding && this.input.setEncoding('utf8');
      
      let buffer = '';
      
      this.input.on('data', (chunk) => {
        if (this.closed || this._paused) return;
        
        buffer += chunk;
        const lines = buffer.split(/\r?\n/);
        buffer = lines.pop() || '';
        
        for (const line of lines) {
          this.emit('line', line);
          this._addHistory(line);
        }
      });
      
      this.input.on('end', () => {
        if (buffer.length > 0) {
          this.emit('line', buffer);
        }
        this.close();
      });
      
      this.input.on('close', () => {
        this.close();
      });
    }

    _addHistory(line) {
      if (line && line.trim() && this.historySize > 0) {
        // Don't add duplicates
        if (this.history[0] !== line) {
          this.history.unshift(line);
          if (this.history.length > this.historySize) {
            this.history.pop();
          }
        }
      }
      this.historyIndex = -1;
    }

    /**
     * Set the prompt.
     */
    setPrompt(prompt) {
      this.prompt = prompt;
    }

    /**
     * Get the current prompt.
     */
    getPrompt() {
      return this.prompt;
    }

    /**
     * Display the prompt.
     */
    promptFunc() {
      if (this.output && this.output.write) {
        this.output.write(this.prompt);
      }
    }

    /**
     * Write data to the output stream.
     */
    write(data, key) {
      if (this.output && this.output.write) {
        this.output.write(data);
      }
    }

    /**
     * Pause the input stream.
     */
    pause() {
      this._paused = true;
      if (this.input && this.input.pause) {
        this.input.pause();
      }
      return this;
    }

    /**
     * Resume the input stream.
     */
    resume() {
      this._paused = false;
      if (this.input && this.input.resume) {
        this.input.resume();
      }
      return this;
    }

    /**
     * Close the interface.
     */
    close() {
      if (this.closed) return;
      this.closed = true;
      this.emit('close');
      
      if (this.input && this.input.removeAllListeners) {
        this.input.removeAllListeners('data');
        this.input.removeAllListeners('end');
        this.input.removeAllListeners('close');
      }
    }

    /**
     * Ask a question and get the answer.
     */
    question(query, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      
      if (this.output && this.output.write) {
        this.output.write(query);
      }
      
      if (callback) {
        this.once('line', callback);
      } else {
        return new Promise((resolve) => {
          this.once('line', resolve);
        });
      }
    }

    /**
     * Get cursor position.
     */
    getCursorPos() {
      return { rows: 0, cols: this.cursor };
    }

    /**
     * Async iterator support.
     */
    [Symbol.asyncIterator]() {
      const self = this;
      const lines = [];
      let resolveNext = null;
      let done = false;

      this.on('line', (line) => {
        if (resolveNext) {
          resolveNext({ done: false, value: line });
          resolveNext = null;
        } else {
          lines.push(line);
        }
      });

      this.on('close', () => {
        done = true;
        if (resolveNext) {
          resolveNext({ done: true, value: undefined });
        }
      });

      return {
        next() {
          if (lines.length > 0) {
            return Promise.resolve({ done: false, value: lines.shift() });
          }
          if (done) {
            return Promise.resolve({ done: true, value: undefined });
          }
          return new Promise((resolve) => {
            resolveNext = resolve;
          });
        }
      };
    }
  }

  /**
   * Create a readline interface.
   */
  function createInterface(options) {
    if (options.input !== undefined && options.output !== undefined) {
      // Options object
      return new Interface(options);
    }
    
    // Legacy: (input, output, completer, terminal)
    return new Interface({
      input: arguments[0],
      output: arguments[1],
      completer: arguments[2],
      terminal: arguments[3]
    });
  }

  /**
   * Clear the line at cursor.
   */
  function clearLine(stream, dir, callback) {
    if (stream && stream.write) {
      const codes = {
        '-1': '\x1b[1K',  // Clear left
        '0': '\x1b[2K',   // Clear entire line
        '1': '\x1b[0K'    // Clear right
      };
      stream.write(codes[String(dir)] || '\x1b[2K');
    }
    if (callback) {
      setImmediate(callback);
    }
  }

  /**
   * Clear screen from cursor down.
   */
  function clearScreenDown(stream, callback) {
    if (stream && stream.write) {
      stream.write('\x1b[0J');
    }
    if (callback) {
      setImmediate(callback);
    }
  }

  /**
   * Move cursor to position.
   */
  function cursorTo(stream, x, y, callback) {
    if (typeof y === 'function') {
      callback = y;
      y = undefined;
    }
    
    if (stream && stream.write) {
      if (y !== undefined) {
        stream.write(`\x1b[${y + 1};${x + 1}H`);
      } else {
        stream.write(`\x1b[${x + 1}G`);
      }
    }
    
    if (callback) {
      setImmediate(callback);
    }
  }

  /**
   * Move cursor relative to current position.
   */
  function moveCursor(stream, dx, dy, callback) {
    if (stream && stream.write) {
      let code = '';
      if (dx > 0) code += `\x1b[${dx}C`;
      else if (dx < 0) code += `\x1b[${-dx}D`;
      if (dy > 0) code += `\x1b[${dy}B`;
      else if (dy < 0) code += `\x1b[${-dy}A`;
      if (code) stream.write(code);
    }
    if (callback) {
      setImmediate(callback);
    }
  }

  /**
   * Emits a key event (for testing).
   */
  function emitKeypressEvents(stream, interface_) {
    // This is a no-op stub for compatibility
    // Real keypress events require terminal raw mode
  }

  const readline = {
    Interface,
    createInterface,
    clearLine,
    clearScreenDown,
    cursorTo,
    moveCursor,
    emitKeypressEvents
  };

  // Promises API
  readline.promises = {
    Interface: class PromiseInterface extends Interface {
      question(query, options) {
        if (typeof options === 'object' && options.signal) {
          const signal = options.signal;
          if (signal.aborted) {
            return Promise.reject(new DOMException('The operation was aborted', 'AbortError'));
          }
        }
        return super.question(query, options);
      }
    },
    
    createInterface: function(options) {
      return new readline.promises.Interface(options);
    }
  };

  // Export
  global.__readline_module = readline;
  global.__readline_promises_module = readline.promises;

})(globalThis);
