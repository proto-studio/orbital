// assert - Assertion testing module
(function(global) {
  'use strict';

  /**
   * AssertionError class for assertion failures.
   */
  class AssertionError extends Error {
    constructor(options) {
      const message = options.message || 
        `Expected ${formatValue(options.expected)} but got ${formatValue(options.actual)}`;
      super(message);
      this.name = 'AssertionError';
      this.actual = options.actual;
      this.expected = options.expected;
      this.operator = options.operator;
      this.generatedMessage = !options.message;
      this.code = 'ERR_ASSERTION';
      
      if (Error.captureStackTrace) {
        Error.captureStackTrace(this, options.stackStartFn || this.constructor);
      }
    }
  }

  /**
   * Format a value for display in error messages.
   */
  function formatValue(val) {
    if (val === null) return 'null';
    if (val === undefined) return 'undefined';
    if (typeof val === 'string') return JSON.stringify(val);
    if (typeof val === 'number' || typeof val === 'boolean') return String(val);
    if (typeof val === 'function') return val.name || '[Function]';
    if (typeof val === 'symbol') return val.toString();
    if (Array.isArray(val)) {
      return '[' + val.map(formatValue).join(', ') + ']';
    }
    if (val instanceof Date) return val.toISOString();
    if (val instanceof RegExp) return val.toString();
    if (val instanceof Error) return val.toString();
    try {
      return JSON.stringify(val);
    } catch (e) {
      return Object.prototype.toString.call(val);
    }
  }

  /**
   * Deep equality check.
   */
  function deepEqual(actual, expected, strict) {
    if (actual === expected) return true;
    
    if (actual === null || expected === null) return false;
    if (actual === undefined || expected === undefined) return false;

    // Type checking for strict mode
    if (strict && typeof actual !== typeof expected) return false;

    // Handle Date
    if (actual instanceof Date && expected instanceof Date) {
      return actual.getTime() === expected.getTime();
    }

    // Handle RegExp
    if (actual instanceof RegExp && expected instanceof RegExp) {
      return actual.toString() === expected.toString();
    }

    // Handle Error
    if (actual instanceof Error && expected instanceof Error) {
      return actual.message === expected.message && actual.name === expected.name;
    }

    // Handle arrays
    if (Array.isArray(actual) && Array.isArray(expected)) {
      if (actual.length !== expected.length) return false;
      for (let i = 0; i < actual.length; i++) {
        if (!deepEqual(actual[i], expected[i], strict)) return false;
      }
      return true;
    }

    // Handle objects
    if (typeof actual === 'object' && typeof expected === 'object') {
      const actualKeys = Object.keys(actual);
      const expectedKeys = Object.keys(expected);
      
      if (actualKeys.length !== expectedKeys.length) return false;
      
      for (const key of actualKeys) {
        if (!Object.prototype.hasOwnProperty.call(expected, key)) return false;
        if (!deepEqual(actual[key], expected[key], strict)) return false;
      }
      return true;
    }

    // Non-strict equality for primitives
    if (!strict) {
      return actual == expected;
    }

    return false;
  }

  /**
   * Check if value matches expected error.
   */
  function matchError(actual, expected) {
    if (expected === undefined) return true;
    
    if (expected instanceof RegExp) {
      return expected.test(actual.message);
    }
    
    if (typeof expected === 'function') {
      if (expected.prototype instanceof Error || expected === Error) {
        return actual instanceof expected;
      }
      // Validation function
      return expected(actual) === true;
    }
    
    if (typeof expected === 'object') {
      for (const key of Object.keys(expected)) {
        if (!deepEqual(actual[key], expected[key], true)) {
          return false;
        }
      }
      return true;
    }
    
    return false;
  }

  /**
   * Main assert function - tests if value is truthy.
   */
  function assert(value, message) {
    if (!value) {
      throw new AssertionError({
        message: message || 'The expression evaluated to a falsy value',
        actual: value,
        expected: true,
        operator: '==',
        stackStartFn: assert
      });
    }
  }

  // Add methods to assert
  Object.assign(assert, {
    AssertionError,

    /**
     * Test strict equality (===).
     */
    strictEqual: function(actual, expected, message) {
      if (actual !== expected) {
        throw new AssertionError({
          message,
          actual,
          expected,
          operator: 'strictEqual',
          stackStartFn: assert.strictEqual
        });
      }
    },

    /**
     * Test strict inequality (!==).
     */
    notStrictEqual: function(actual, expected, message) {
      if (actual === expected) {
        throw new AssertionError({
          message: message || `Expected ${formatValue(actual)} !== ${formatValue(expected)}`,
          actual,
          expected,
          operator: 'notStrictEqual',
          stackStartFn: assert.notStrictEqual
        });
      }
    },

    /**
     * Test loose equality (==).
     */
    equal: function(actual, expected, message) {
      if (actual != expected) {
        throw new AssertionError({
          message,
          actual,
          expected,
          operator: '==',
          stackStartFn: assert.equal
        });
      }
    },

    /**
     * Test loose inequality (!=).
     */
    notEqual: function(actual, expected, message) {
      if (actual == expected) {
        throw new AssertionError({
          message: message || `Expected ${formatValue(actual)} != ${formatValue(expected)}`,
          actual,
          expected,
          operator: '!=',
          stackStartFn: assert.notEqual
        });
      }
    },

    /**
     * Test deep strict equality.
     */
    deepStrictEqual: function(actual, expected, message) {
      if (!deepEqual(actual, expected, true)) {
        throw new AssertionError({
          message,
          actual,
          expected,
          operator: 'deepStrictEqual',
          stackStartFn: assert.deepStrictEqual
        });
      }
    },

    /**
     * Test deep strict inequality.
     */
    notDeepStrictEqual: function(actual, expected, message) {
      if (deepEqual(actual, expected, true)) {
        throw new AssertionError({
          message: message || 'Values should not be deeply strictly equal',
          actual,
          expected,
          operator: 'notDeepStrictEqual',
          stackStartFn: assert.notDeepStrictEqual
        });
      }
    },

    /**
     * Test deep equality (non-strict).
     */
    deepEqual: function(actual, expected, message) {
      if (!deepEqual(actual, expected, false)) {
        throw new AssertionError({
          message,
          actual,
          expected,
          operator: 'deepEqual',
          stackStartFn: assert.deepEqual
        });
      }
    },

    /**
     * Test deep inequality (non-strict).
     */
    notDeepEqual: function(actual, expected, message) {
      if (deepEqual(actual, expected, false)) {
        throw new AssertionError({
          message: message || 'Values should not be deeply equal',
          actual,
          expected,
          operator: 'notDeepEqual',
          stackStartFn: assert.notDeepEqual
        });
      }
    },

    /**
     * Test if value is truthy.
     */
    ok: function(value, message) {
      if (!value) {
        throw new AssertionError({
          message: message || 'The expression evaluated to a falsy value',
          actual: value,
          expected: true,
          operator: 'ok',
          stackStartFn: assert.ok
        });
      }
    },

    /**
     * Always fail.
     */
    fail: function(message) {
      throw new AssertionError({
        message: message || 'Failed',
        actual: undefined,
        expected: undefined,
        operator: 'fail',
        stackStartFn: assert.fail
      });
    },

    /**
     * Test if a function throws.
     */
    throws: function(fn, expected, message) {
      if (typeof fn !== 'function') {
        throw new TypeError('First argument must be a function');
      }

      let thrown = false;
      let actual;

      try {
        fn();
      } catch (e) {
        thrown = true;
        actual = e;
      }

      if (!thrown) {
        throw new AssertionError({
          message: message || 'Missing expected exception',
          actual: undefined,
          expected: expected,
          operator: 'throws',
          stackStartFn: assert.throws
        });
      }

      if (expected !== undefined && !matchError(actual, expected)) {
        throw new AssertionError({
          message: message || `The error did not match: ${actual}`,
          actual,
          expected,
          operator: 'throws',
          stackStartFn: assert.throws
        });
      }
    },

    /**
     * Test if a function does not throw.
     */
    doesNotThrow: function(fn, expected, message) {
      if (typeof fn !== 'function') {
        throw new TypeError('First argument must be a function');
      }

      try {
        fn();
      } catch (e) {
        if (expected === undefined || matchError(e, expected)) {
          throw new AssertionError({
            message: message || `Got unwanted exception: ${e.message}`,
            actual: e,
            expected: undefined,
            operator: 'doesNotThrow',
            stackStartFn: assert.doesNotThrow
          });
        }
      }
    },

    /**
     * Test if async function rejects.
     */
    rejects: async function(asyncFn, expected, message) {
      let thrown = false;
      let actual;

      try {
        const promise = typeof asyncFn === 'function' ? asyncFn() : asyncFn;
        await promise;
      } catch (e) {
        thrown = true;
        actual = e;
      }

      if (!thrown) {
        throw new AssertionError({
          message: message || 'Missing expected rejection',
          actual: undefined,
          expected,
          operator: 'rejects',
          stackStartFn: assert.rejects
        });
      }

      if (expected !== undefined && !matchError(actual, expected)) {
        throw new AssertionError({
          message: message || `The rejection did not match: ${actual}`,
          actual,
          expected,
          operator: 'rejects',
          stackStartFn: assert.rejects
        });
      }
    },

    /**
     * Test if async function does not reject.
     */
    doesNotReject: async function(asyncFn, expected, message) {
      try {
        const promise = typeof asyncFn === 'function' ? asyncFn() : asyncFn;
        await promise;
      } catch (e) {
        if (expected === undefined || matchError(e, expected)) {
          throw new AssertionError({
            message: message || `Got unwanted rejection: ${e.message}`,
            actual: e,
            expected: undefined,
            operator: 'doesNotReject',
            stackStartFn: assert.doesNotReject
          });
        }
      }
    },

    /**
     * Test if regexp matches string.
     */
    match: function(string, regexp, message) {
      if (!(regexp instanceof RegExp)) {
        throw new TypeError('Second argument must be a RegExp');
      }
      if (!regexp.test(string)) {
        throw new AssertionError({
          message: message || `The input did not match the regular expression ${regexp}`,
          actual: string,
          expected: regexp,
          operator: 'match',
          stackStartFn: assert.match
        });
      }
    },

    /**
     * Test if regexp does not match string.
     */
    doesNotMatch: function(string, regexp, message) {
      if (!(regexp instanceof RegExp)) {
        throw new TypeError('Second argument must be a RegExp');
      }
      if (regexp.test(string)) {
        throw new AssertionError({
          message: message || `The input was expected not to match ${regexp}`,
          actual: string,
          expected: regexp,
          operator: 'doesNotMatch',
          stackStartFn: assert.doesNotMatch
        });
      }
    },

    /**
     * Check if value is an instance of expected.
     */
    ifError: function(value) {
      if (value !== null && value !== undefined) {
        throw value instanceof Error ? value : new AssertionError({
          message: `ifError got unwanted exception: ${value}`,
          actual: value,
          expected: null,
          operator: 'ifError',
          stackStartFn: assert.ifError
        });
      }
    }
  });

  // Strict mode - all methods use strict equality
  const strict = Object.assign(function(value, message) {
    assert.ok(value, message);
  }, assert);

  strict.equal = assert.strictEqual;
  strict.notEqual = assert.notStrictEqual;
  strict.deepEqual = assert.deepStrictEqual;
  strict.notDeepEqual = assert.notDeepStrictEqual;

  assert.strict = strict;

  // Export
  global.__assert_module = assert;
  global.__assert_strict_module = strict;

})(globalThis);
