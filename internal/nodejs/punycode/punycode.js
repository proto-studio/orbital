// punycode - Punycode encoding (DEPRECATED)
(function(global) {
  'use strict';

  // Punycode constants
  const base = 36;
  const tMin = 1;
  const tMax = 26;
  const skew = 38;
  const damp = 700;
  const initialBias = 72;
  const initialN = 128;
  const delimiter = '-';
  const maxInt = 2147483647;

  // Helper functions
  function adapt(delta, numPoints, firstTime) {
    let k = 0;
    delta = firstTime ? Math.floor(delta / damp) : delta >> 1;
    delta += Math.floor(delta / numPoints);
    for (; delta > (base - tMin) * tMax >> 1; k += base) {
      delta = Math.floor(delta / (base - tMin));
    }
    return Math.floor(k + (base - tMin + 1) * delta / (delta + skew));
  }

  function basicToDigit(codePoint) {
    if (codePoint - 0x30 < 0x0a) return codePoint - 0x16;
    if (codePoint - 0x41 < 0x1a) return codePoint - 0x41;
    if (codePoint - 0x61 < 0x1a) return codePoint - 0x61;
    return base;
  }

  function digitToBasic(digit, flag) {
    return digit + 22 + 75 * (digit < 26) - ((flag !== 0) << 5);
  }

  /**
   * Decode a Punycode string.
   */
  function decode(input) {
    const output = [];
    const inputLength = input.length;
    let i = 0;
    let n = initialN;
    let bias = initialBias;
    let basic = input.lastIndexOf(delimiter);
    
    if (basic < 0) basic = 0;
    
    for (let j = 0; j < basic; ++j) {
      output.push(input.charCodeAt(j));
    }

    let index = basic > 0 ? basic + 1 : 0;
    
    while (index < inputLength) {
      const oldi = i;
      let w = 1;
      
      for (let k = base; ; k += base) {
        if (index >= inputLength) throw new RangeError('Invalid input');
        
        const digit = basicToDigit(input.charCodeAt(index++));
        
        if (digit >= base || digit > Math.floor((maxInt - i) / w)) {
          throw new RangeError('Overflow');
        }
        
        i += digit * w;
        const t = k <= bias ? tMin : (k >= bias + tMax ? tMax : k - bias);
        
        if (digit < t) break;
        
        if (w > Math.floor(maxInt / (base - t))) {
          throw new RangeError('Overflow');
        }
        
        w *= base - t;
      }

      const out = output.length + 1;
      bias = adapt(i - oldi, out, oldi === 0);
      
      if (Math.floor(i / out) > maxInt - n) {
        throw new RangeError('Overflow');
      }
      
      n += Math.floor(i / out);
      i %= out;
      output.splice(i++, 0, n);
    }

    return String.fromCodePoint(...output);
  }

  /**
   * Encode a string to Punycode.
   */
  function encode(input) {
    const output = [];
    const inputArray = [...input];
    const inputLength = inputArray.length;
    
    let n = initialN;
    let delta = 0;
    let bias = initialBias;

    // Handle basic code points
    for (const char of inputArray) {
      const codePoint = char.codePointAt(0);
      if (codePoint < 0x80) {
        output.push(String.fromCharCode(codePoint));
      }
    }

    const basicLength = output.length;
    let handledCPCount = basicLength;

    if (basicLength > 0) {
      output.push(delimiter);
    }

    while (handledCPCount < inputLength) {
      let m = maxInt;
      
      for (const char of inputArray) {
        const codePoint = char.codePointAt(0);
        if (codePoint >= n && codePoint < m) {
          m = codePoint;
        }
      }

      if (m - n > Math.floor((maxInt - delta) / (handledCPCount + 1))) {
        throw new RangeError('Overflow');
      }

      delta += (m - n) * (handledCPCount + 1);
      n = m;

      for (const char of inputArray) {
        const codePoint = char.codePointAt(0);
        
        if (codePoint < n) {
          if (++delta > maxInt) throw new RangeError('Overflow');
        }

        if (codePoint === n) {
          let q = delta;
          
          for (let k = base; ; k += base) {
            const t = k <= bias ? tMin : (k >= bias + tMax ? tMax : k - bias);
            
            if (q < t) break;
            
            output.push(String.fromCharCode(digitToBasic(t + (q - t) % (base - t), 0)));
            q = Math.floor((q - t) / (base - t));
          }

          output.push(String.fromCharCode(digitToBasic(q, 0)));
          bias = adapt(delta, handledCPCount + 1, handledCPCount === basicLength);
          delta = 0;
          ++handledCPCount;
        }
      }

      ++delta;
      ++n;
    }

    return output.join('');
  }

  /**
   * Convert a domain name to ASCII (Punycode).
   */
  function toASCII(input) {
    return input.split('.').map(label => {
      if (/[^\x00-\x7F]/.test(label)) {
        return 'xn--' + encode(label);
      }
      return label;
    }).join('.');
  }

  /**
   * Convert an ASCII domain name to Unicode.
   */
  function toUnicode(input) {
    return input.split('.').map(label => {
      if (label.toLowerCase().startsWith('xn--')) {
        return decode(label.slice(4));
      }
      return label;
    }).join('.');
  }

  const punycode = {
    decode,
    encode,
    toASCII,
    toUnicode,
    ucs2: {
      decode: function(string) {
        const output = [];
        let counter = 0;
        const length = string.length;
        while (counter < length) {
          const value = string.charCodeAt(counter++);
          if (value >= 0xD800 && value <= 0xDBFF && counter < length) {
            const extra = string.charCodeAt(counter++);
            if ((extra & 0xFC00) === 0xDC00) {
              output.push(((value & 0x3FF) << 10) + (extra & 0x3FF) + 0x10000);
            } else {
              output.push(value);
              counter--;
            }
          } else {
            output.push(value);
          }
        }
        return output;
      },
      encode: function(array) {
        return String.fromCodePoint(...array);
      }
    },
    version: '2.1.0'
  };

  // Export
  global.__punycode_module = punycode;

})(globalThis);
