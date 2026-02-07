// repl - REPL module
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;

  // REPL Mode constants
  const REPL_MODE_SLOPPY = Symbol('repl-sloppy');
  const REPL_MODE_STRICT = Symbol('repl-strict');

  /**
   * Recoverable error for multi-line input.
   */
  class Recoverable extends SyntaxError {
    constructor(err) {
      super(err.message);
      this.name = 'Recoverable';
      this.err = err;
    }
  }

  /**
   * REPLServer class - A REPL server instance.
   */
  class REPLServer extends EventEmitter {
    constructor(options) {
      super();

      options = options || {};

      this.prompt = options.prompt !== undefined ? options.prompt : '> ';
      this.input = options.input || (typeof process !== 'undefined' ? process.stdin : null);
      this.output = options.output || (typeof process !== 'undefined' ? process.stdout : null);
      this.terminal = options.terminal !== undefined ? options.terminal : true;
      this.useColors = options.useColors !== undefined ? options.useColors : true;
      this.useGlobal = options.useGlobal !== undefined ? options.useGlobal : false;
      this.ignoreUndefined = options.ignoreUndefined !== undefined ? options.ignoreUndefined : false;
      this.replMode = options.replMode || REPL_MODE_SLOPPY;
      this.breakEvalOnSigint = options.breakEvalOnSigint || false;
      this.preview = options.preview !== undefined ? options.preview : true;

      this._eval = options.eval || this._defaultEval.bind(this);
      this._writer = options.writer || this._defaultWriter.bind(this);
      
      this.commands = new Map();
      this.context = {};
      this._buffer = '';
      this._running = false;
      
      // Register default commands
      this._registerDefaultCommands();
      
      // Setup context
      this.resetContext();
    }

    /**
     * Default eval function.
     */
    _defaultEval(cmd, context, filename, callback) {
      let result;
      try {
        // Check if using global context
        if (this.useGlobal) {
          result = eval(cmd);
        } else {
          // Create a function with the context as scope
          const contextKeys = Object.keys(context);
          const contextValues = contextKeys.map(k => context[k]);
          
          const fn = new Function(...contextKeys, `return eval(${JSON.stringify(cmd)})`);
          result = fn.apply(null, contextValues);
        }
        callback(null, result);
      } catch (e) {
        // Check for recoverable syntax errors
        if (e instanceof SyntaxError && this._isRecoverable(e, cmd)) {
          callback(new Recoverable(e));
        } else {
          callback(e);
        }
      }
    }

    /**
     * Check if a syntax error is recoverable (incomplete input).
     */
    _isRecoverable(error, code) {
      const msg = error.message;
      
      // Common patterns for incomplete input
      if (msg.includes('Unexpected end of input')) return true;
      if (msg.includes('Unexpected token')) {
        // Check for unclosed brackets/braces/parens
        const opens = (code.match(/[{[(]/g) || []).length;
        const closes = (code.match(/[}\])]/g) || []).length;
        if (opens > closes) return true;
      }
      if (msg.includes('Invalid or unexpected token')) {
        // Could be an unclosed string
        const singleQuotes = (code.match(/'/g) || []).length;
        const doubleQuotes = (code.match(/"/g) || []).length;
        const backticks = (code.match(/`/g) || []).length;
        if (singleQuotes % 2 !== 0 || doubleQuotes % 2 !== 0 || backticks % 2 !== 0) {
          return true;
        }
      }
      
      return false;
    }

    /**
     * Default writer function.
     */
    _defaultWriter(output) {
      if (output === undefined && this.ignoreUndefined) {
        return '';
      }
      
      // Use util.inspect if available
      if (global.__util_module && global.__util_module.inspect) {
        return global.__util_module.inspect(output, {
          colors: this.useColors,
          showProxy: true
        });
      }
      
      // Fallback
      if (output === undefined) return 'undefined';
      if (output === null) return 'null';
      if (typeof output === 'string') return output;
      
      try {
        return JSON.stringify(output, null, 2);
      } catch (e) {
        return String(output);
      }
    }

    /**
     * Register default REPL commands.
     */
    _registerDefaultCommands() {
      this.defineCommand('help', {
        help: 'Print this help message',
        action: () => {
          this._write('REPL commands:\n');
          for (const [name, cmd] of this.commands) {
            this._write(`.${name}\t${cmd.help || ''}\n`);
          }
          this.displayPrompt();
        }
      });

      this.defineCommand('break', {
        help: 'Clear the current multi-line input',
        action: () => {
          this._buffer = '';
          this._write('\n');
          this.displayPrompt();
        }
      });

      this.defineCommand('clear', {
        help: 'Clear the REPL context',
        action: () => {
          this.resetContext();
          this._write('Context cleared.\n');
          this.displayPrompt();
        }
      });

      this.defineCommand('exit', {
        help: 'Exit the REPL',
        action: () => {
          this.close();
        }
      });

      this.defineCommand('save', {
        help: 'Save the current REPL session to a file',
        action: (filename) => {
          // Would need fs access
          this._write('Save functionality requires fs module.\n');
          this.displayPrompt();
        }
      });

      this.defineCommand('load', {
        help: 'Load a file into the REPL session',
        action: (filename) => {
          // Would need fs access
          this._write('Load functionality requires fs module.\n');
          this.displayPrompt();
        }
      });

      this.defineCommand('editor', {
        help: 'Enter multi-line editor mode',
        action: () => {
          this._write('Editor mode (Ctrl+D to finish):\n');
          this._editorMode = true;
          this._editorBuffer = '';
        }
      });
    }

    /**
     * Write output.
     */
    _write(text) {
      if (this.output && this.output.write) {
        this.output.write(text);
      }
    }

    /**
     * Display the prompt.
     */
    displayPrompt(preserveCursor) {
      const prompt = this._buffer.length > 0 ? '... ' : this.prompt;
      this._write(prompt);
    }

    /**
     * Reset the REPL context.
     */
    resetContext() {
      this.context = this.useGlobal ? global : Object.create(null);
      
      // Add some default context properties
      if (!this.useGlobal) {
        this.context.global = this.context;
        this.context.console = global.console;
        this.context.require = global.require;
        this.context.module = global.module;
        this.context.exports = global.exports;
      }
      
      this.emit('reset', this.context);
    }

    /**
     * Define a REPL command.
     */
    defineCommand(keyword, cmd) {
      if (typeof cmd === 'function') {
        cmd = { action: cmd };
      }
      this.commands.set(keyword, cmd);
    }

    /**
     * Setup the REPL to read from input.
     */
    setupHistory(historyPath, callback) {
      // History setup would need fs access
      if (callback) callback(null, this);
    }

    /**
     * Close the REPL.
     */
    close() {
      this._running = false;
      this.emit('exit');
    }

    /**
     * Process a line of input.
     */
    _processLine(line) {
      // Handle editor mode
      if (this._editorMode) {
        if (line === '\x04' || line === null) { // Ctrl+D
          this._editorMode = false;
          line = this._editorBuffer;
          this._editorBuffer = '';
        } else {
          this._editorBuffer += line + '\n';
          return;
        }
      }

      // Check for REPL commands
      if (line.startsWith('.')) {
        const parts = line.slice(1).split(/\s+/);
        const cmdName = parts[0];
        const cmdArgs = parts.slice(1).join(' ');
        
        const cmd = this.commands.get(cmdName);
        if (cmd && cmd.action) {
          cmd.action.call(this, cmdArgs);
          return;
        }
      }

      // Accumulate multi-line input
      this._buffer += line + '\n';
      
      // Try to evaluate
      const code = this._buffer;
      
      this._eval(code, this.context, 'repl', (err, result) => {
        if (err) {
          if (err instanceof Recoverable) {
            // Need more input
            this.displayPrompt();
            return;
          }
          
          // Display error
          this._buffer = '';
          if (this.useColors) {
            this._write('\x1b[31m' + (err.stack || err.message || String(err)) + '\x1b[0m\n');
          } else {
            this._write((err.stack || err.message || String(err)) + '\n');
          }
        } else {
          this._buffer = '';
          const output = this._writer(result);
          if (output) {
            this._write(output + '\n');
          }
        }
        
        this.displayPrompt();
      });
    }

    /**
     * Start the REPL.
     */
    start() {
      this._running = true;
      this.displayPrompt();
      
      if (this.input && this.input.on) {
        this.input.setEncoding && this.input.setEncoding('utf8');
        
        let lineBuffer = '';
        this.input.on('data', (chunk) => {
          if (!this._running) return;
          
          lineBuffer += chunk;
          const lines = lineBuffer.split('\n');
          
          // Keep the last incomplete line in buffer
          lineBuffer = lines.pop() || '';
          
          for (const line of lines) {
            this._processLine(line);
          }
        });
        
        this.input.on('end', () => {
          this.close();
        });
      }
      
      return this;
    }
  }

  /**
   * Start a REPL.
   */
  function start(options) {
    if (typeof options === 'string') {
      options = { prompt: options };
    }
    
    const repl = new REPLServer(options);
    return repl.start();
  }

  // Export
  const replModule = {
    start,
    REPLServer,
    Recoverable,
    REPL_MODE_SLOPPY,
    REPL_MODE_STRICT,
    // Deprecated but still exported
    builtinModules: []
  };

  global.__repl_module = replModule;

})(globalThis);
