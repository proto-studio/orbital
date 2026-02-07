// node:test - Test runner
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module ? global.__events_module.EventEmitter : class EventEmitter {
    constructor() { this._events = {}; }
    on(e, fn) { (this._events[e] = this._events[e] || []).push(fn); return this; }
    emit(e, ...a) { (this._events[e] || []).forEach(fn => fn(...a)); return true; }
  };

  // Test result counts
  let passed = 0;
  let failed = 0;
  let skipped = 0;
  let todos = 0;

  // Current test context stack
  const contextStack = [];
  let testIdCounter = 0;

  /**
   * TestContext class - Context passed to test functions.
   */
  class TestContext {
    constructor(name, options = {}) {
      this.name = name;
      this.testId = ++testIdCounter;
      this.signal = options.signal || null;
      this.diagnostic = [];
      this._plan = null;
      this._assertionCount = 0;
    }

    /**
     * Set a test plan (number of assertions expected).
     */
    plan(count) {
      this._plan = count;
    }

    /**
     * Add a diagnostic message.
     */
    diagnostic(message) {
      this.diagnostic.push(message);
      console.log(`# ${message}`);
    }

    /**
     * Run a subtest.
     */
    test(name, options, fn) {
      return test(name, options, fn);
    }

    /**
     * Mark assertions for todo tests.
     */
    todo(message) {
      console.log(`# TODO: ${message}`);
    }

    /**
     * Skip this test.
     */
    skip(message) {
      throw new SkipError(message);
    }

    /**
     * Get before hooks for this context.
     */
    before(fn) {
      return before(fn);
    }

    /**
     * Get after hooks for this context.
     */
    after(fn) {
      return after(fn);
    }

    /**
     * Get beforeEach hooks for this context.
     */
    beforeEach(fn) {
      return beforeEach(fn);
    }

    /**
     * Get afterEach hooks for this context.
     */
    afterEach(fn) {
      return afterEach(fn);
    }
  }

  /**
   * Skip error to indicate test should be skipped.
   */
  class SkipError extends Error {
    constructor(message) {
      super(message || 'Test skipped');
      this.name = 'SkipError';
    }
  }

  // Hooks storage
  const hooks = {
    before: [],
    after: [],
    beforeEach: [],
    afterEach: []
  };

  /**
   * Register a before hook.
   */
  function before(fn) {
    hooks.before.push(fn);
  }

  /**
   * Register an after hook.
   */
  function after(fn) {
    hooks.after.push(fn);
  }

  /**
   * Register a beforeEach hook.
   */
  function beforeEach(fn) {
    hooks.beforeEach.push(fn);
  }

  /**
   * Register an afterEach hook.
   */
  function afterEach(fn) {
    hooks.afterEach.push(fn);
  }

  /**
   * Run hooks of a specific type.
   */
  async function runHooks(type, context) {
    for (const fn of hooks[type]) {
      await fn(context);
    }
  }

  /**
   * Format test name with context.
   */
  function formatTestName(name) {
    const names = contextStack.map(c => c.name);
    names.push(name);
    return names.join(' > ');
  }

  /**
   * Main test function.
   */
  async function test(name, options, fn) {
    // Handle overloads
    if (typeof options === 'function') {
      fn = options;
      options = {};
    }
    
    if (typeof name === 'function') {
      fn = name;
      name = fn.name || '<anonymous>';
      options = {};
    }

    options = options || {};

    // Check for skip/todo
    if (options.skip) {
      skipped++;
      console.log(`ok ${passed + failed + skipped + todos} - ${formatTestName(name)} # SKIP${typeof options.skip === 'string' ? ' ' + options.skip : ''}`);
      return;
    }

    if (options.todo) {
      todos++;
      console.log(`not ok ${passed + failed + skipped + todos} - ${formatTestName(name)} # TODO${typeof options.todo === 'string' ? ' ' + options.todo : ''}`);
      return;
    }

    const context = new TestContext(name, options);
    contextStack.push(context);

    const startTime = Date.now();

    try {
      // Run beforeEach hooks
      await runHooks('beforeEach', context);

      // Run the test
      const result = fn(context);
      if (result && typeof result.then === 'function') {
        await result;
      }

      // Check plan
      if (context._plan !== null && context._assertionCount !== context._plan) {
        throw new Error(`Expected ${context._plan} assertions, but got ${context._assertionCount}`);
      }

      // Run afterEach hooks
      await runHooks('afterEach', context);

      passed++;
      const duration = Date.now() - startTime;
      console.log(`ok ${passed + failed + skipped + todos} - ${formatTestName(name)} (${duration}ms)`);

    } catch (err) {
      if (err instanceof SkipError) {
        skipped++;
        console.log(`ok ${passed + failed + skipped + todos} - ${formatTestName(name)} # SKIP ${err.message}`);
      } else {
        failed++;
        console.log(`not ok ${passed + failed + skipped + todos} - ${formatTestName(name)}`);
        console.log(`  ---`);
        console.log(`  error: ${err.message}`);
        if (err.stack) {
          console.log(`  stack: |`);
          err.stack.split('\n').forEach(line => console.log(`    ${line}`));
        }
        console.log(`  ...`);
      }

      // Run afterEach hooks even on failure
      try {
        await runHooks('afterEach', context);
      } catch (hookErr) {
        console.log(`# afterEach hook error: ${hookErr.message}`);
      }
    }

    contextStack.pop();
  }

  /**
   * Describe a test suite (alias for test with subtests).
   */
  async function describe(name, options, fn) {
    if (typeof options === 'function') {
      fn = options;
      options = {};
    }

    const context = new TestContext(name, options);
    contextStack.push(context);

    try {
      // Run before hooks
      await runHooks('before', context);

      // Run suite
      const result = fn(context);
      if (result && typeof result.then === 'function') {
        await result;
      }

      // Run after hooks
      await runHooks('after', context);
    } finally {
      contextStack.pop();
    }
  }

  /**
   * Define a single test case (alias for test).
   */
  function it(name, options, fn) {
    return test(name, options, fn);
  }

  /**
   * Mock function creation.
   */
  function mock(target, methodName, options = {}) {
    const original = target ? target[methodName] : null;
    const calls = [];
    let implementation = options.implementation;

    const mockFn = function(...args) {
      const callInfo = {
        arguments: args,
        this: this,
        target: mockFn
      };
      calls.push(callInfo);

      if (implementation) {
        try {
          callInfo.result = implementation.apply(this, args);
          return callInfo.result;
        } catch (e) {
          callInfo.error = e;
          throw e;
        }
      }

      if (original) {
        try {
          callInfo.result = original.apply(this, args);
          return callInfo.result;
        } catch (e) {
          callInfo.error = e;
          throw e;
        }
      }
    };

    mockFn.mock = {
      calls,
      callCount() { return calls.length; },
      mockImplementation(fn) {
        implementation = fn;
        return mockFn;
      },
      mockImplementationOnce(fn) {
        const originalImpl = implementation;
        implementation = function(...args) {
          implementation = originalImpl;
          return fn.apply(this, args);
        };
        return mockFn;
      },
      restore() {
        if (target && methodName && original) {
          target[methodName] = original;
        }
      },
      resetCalls() {
        calls.length = 0;
      }
    };

    if (target && methodName) {
      target[methodName] = mockFn;
    }

    return mockFn;
  }

  /**
   * Create a mock timer module (simplified).
   */
  mock.timers = {
    enable(options = {}) {
      // Simplified timer mocking
      console.log('# Timer mocking enabled (limited support)');
    },
    reset() {
      console.log('# Timer mock reset');
    },
    tick(ms) {
      console.log(`# Timer mock tick: ${ms}ms`);
    }
  };

  /**
   * Run all registered tests.
   */
  async function run(options = {}) {
    console.log('TAP version 14');
    
    // Tests are run as they are defined
    // This function exists for API compatibility
    
    return {
      passed,
      failed,
      skipped,
      todo: todos,
      total: passed + failed + skipped + todos
    };
  }

  /**
   * Print test summary.
   */
  function printSummary() {
    const total = passed + failed + skipped + todos;
    console.log('');
    console.log(`1..${total}`);
    console.log(`# tests ${total}`);
    console.log(`# pass ${passed}`);
    console.log(`# fail ${failed}`);
    console.log(`# skip ${skipped}`);
    console.log(`# todo ${todos}`);
  }

  // Export
  const testModule = {
    test,
    describe,
    it,
    before,
    after,
    beforeEach,
    afterEach,
    mock,
    run,
    TestContext,
    skip: (name, fn) => test(name, { skip: true }, fn),
    todo: (name, fn) => test(name, { todo: true }, fn),
    only: (name, fn) => test(name, fn), // Would need filter support
  };

  // Also export as default
  testModule.default = test;

  global.__test_module = testModule;

  // Register cleanup to print summary
  if (typeof process !== 'undefined' && process.on) {
    process.on('exit', printSummary);
  }

})(globalThis);
