// querystring - Query string parsing and formatting
(function(global) {
  'use strict';

  const querystring = {
    /**
     * Parse a query string into an object.
     * @param {string} str - The query string to parse
     * @param {string} sep - The separator (default: '&')
     * @param {string} eq - The assignment character (default: '=')
     * @param {object} options - Options object
     * @returns {object}
     */
    parse: function(str, sep, eq, options) {
      sep = sep || '&';
      eq = eq || '=';
      const obj = Object.create(null);

      if (typeof str !== 'string' || str.length === 0) {
        return obj;
      }

      const maxKeys = options && typeof options.maxKeys === 'number' ? options.maxKeys : 1000;
      const decodeFunction = options && options.decodeURIComponent || decodeURIComponent;

      const pairs = str.split(sep);
      const len = maxKeys > 0 ? Math.min(pairs.length, maxKeys) : pairs.length;

      for (let i = 0; i < len; i++) {
        const pair = pairs[i];
        const eqIdx = pair.indexOf(eq);
        
        let key, value;
        if (eqIdx < 0) {
          key = pair;
          value = '';
        } else {
          key = pair.substring(0, eqIdx);
          value = pair.substring(eqIdx + 1);
        }

        try {
          key = decodeFunction(key);
        } catch (e) {
          // Keep original if decode fails
        }
        
        try {
          value = decodeFunction(value);
        } catch (e) {
          // Keep original if decode fails
        }

        // Handle multiple values
        if (obj[key] === undefined) {
          obj[key] = value;
        } else if (Array.isArray(obj[key])) {
          obj[key].push(value);
        } else {
          obj[key] = [obj[key], value];
        }
      }

      return obj;
    },

    /**
     * Stringify an object into a query string.
     * @param {object} obj - The object to stringify
     * @param {string} sep - The separator (default: '&')
     * @param {string} eq - The assignment character (default: '=')
     * @param {object} options - Options object
     * @returns {string}
     */
    stringify: function(obj, sep, eq, options) {
      sep = sep || '&';
      eq = eq || '=';
      const encodeFunction = options && options.encodeURIComponent || encodeURIComponent;

      if (obj === null || typeof obj !== 'object') {
        return '';
      }

      const keys = Object.keys(obj);
      const pairs = [];

      for (let i = 0; i < keys.length; i++) {
        const key = keys[i];
        const value = obj[key];
        const encodedKey = encodeFunction(key);

        if (Array.isArray(value)) {
          for (let j = 0; j < value.length; j++) {
            pairs.push(encodedKey + eq + encodeFunction(String(value[j])));
          }
        } else {
          pairs.push(encodedKey + eq + encodeFunction(String(value)));
        }
      }

      return pairs.join(sep);
    },

    /**
     * Encode a string for use in a query string.
     * @param {string} str - The string to encode
     * @returns {string}
     */
    escape: function(str) {
      return encodeURIComponent(str);
    },

    /**
     * Decode a query string encoded string.
     * @param {string} str - The string to decode
     * @returns {string}
     */
    unescape: function(str) {
      return decodeURIComponent(str);
    },

    /**
     * Encode an object (alias for stringify).
     */
    encode: function(obj, sep, eq, options) {
      return querystring.stringify(obj, sep, eq, options);
    },

    /**
     * Decode a string (alias for parse).
     */
    decode: function(str, sep, eq, options) {
      return querystring.parse(str, sep, eq, options);
    }
  };

  // Export
  global.__querystring_module = querystring;

})(globalThis);
