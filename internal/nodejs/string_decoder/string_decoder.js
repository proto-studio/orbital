// string_decoder - Decoding buffers to strings
(function(global) {
  'use strict';

  /**
   * StringDecoder provides an interface for decoding Buffer objects into strings
   * while preserving multi-byte characters that may be split across chunks.
   */
  class StringDecoder {
    /**
     * Creates a new StringDecoder instance.
     * @param {string} encoding - The character encoding to use (default: 'utf8')
     */
    constructor(encoding) {
      this.encoding = normalizeEncoding(encoding);
      this.lastChar = new Uint8Array(4); // Max 4 bytes for UTF-8
      this.lastNeed = 0;
      this.lastTotal = 0;
    }

    /**
     * Returns any remaining input stored in the internal buffer as a string.
     * @returns {string}
     */
    end(buf) {
      let r = '';
      if (buf && buf.length) {
        r = this.write(buf);
      }
      if (this.lastNeed) {
        // We have an incomplete character
        const total = this.lastTotal;
        const need = this.lastNeed;
        // Return replacement character for incomplete sequence
        r += '\ufffd';
        this.lastNeed = 0;
      }
      return r;
    }

    /**
     * Writes the buffer to the decoder and returns the decoded string.
     * @param {Buffer|Uint8Array|string} buf - The buffer to decode
     * @returns {string}
     */
    write(buf) {
      if (!buf || buf.length === 0) {
        return '';
      }

      // Convert to Uint8Array if needed
      let bytes;
      if (typeof buf === 'string') {
        bytes = new TextEncoder().encode(buf);
      } else if (buf instanceof Uint8Array) {
        bytes = buf;
      } else if (buf.buffer) {
        bytes = new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
      } else {
        bytes = new Uint8Array(buf);
      }

      switch (this.encoding) {
        case 'utf8':
        case 'utf-8':
          return this._decodeUtf8(bytes);
        case 'utf16le':
        case 'utf-16le':
        case 'ucs2':
        case 'ucs-2':
          return this._decodeUtf16le(bytes);
        case 'base64':
          return this._decodeBase64(bytes);
        case 'latin1':
        case 'binary':
          return this._decodeLatin1(bytes);
        case 'ascii':
          return this._decodeAscii(bytes);
        case 'hex':
          return this._decodeHex(bytes);
        default:
          // Default to UTF-8
          return this._decodeUtf8(bytes);
      }
    }

    /**
     * Decode UTF-8 bytes, handling incomplete multi-byte sequences.
     */
    _decodeUtf8(bytes) {
      let result = '';
      let i = 0;

      // Handle any incomplete character from previous chunk
      if (this.lastNeed > 0) {
        const needed = Math.min(bytes.length, this.lastNeed);
        for (let j = 0; j < needed; j++) {
          this.lastChar[this.lastTotal - this.lastNeed + j] = bytes[j];
          this.lastNeed--;
        }
        i = needed;

        if (this.lastNeed === 0) {
          // Complete character - decode it
          const charBytes = this.lastChar.slice(0, this.lastTotal);
          try {
            result += new TextDecoder('utf-8', { fatal: true }).decode(charBytes);
          } catch (e) {
            result += '\ufffd'; // Replacement character
          }
        }
      }

      // Process remaining bytes
      while (i < bytes.length) {
        const byte = bytes[i];

        // Determine character length from first byte
        let charLen;
        if (byte < 0x80) {
          charLen = 1;
        } else if ((byte & 0xe0) === 0xc0) {
          charLen = 2;
        } else if ((byte & 0xf0) === 0xe0) {
          charLen = 3;
        } else if ((byte & 0xf8) === 0xf0) {
          charLen = 4;
        } else {
          // Invalid start byte - emit replacement character
          result += '\ufffd';
          i++;
          continue;
        }

        const remaining = bytes.length - i;
        if (remaining >= charLen) {
          // Complete character
          const charBytes = bytes.slice(i, i + charLen);
          try {
            result += new TextDecoder('utf-8', { fatal: true }).decode(charBytes);
          } catch (e) {
            result += '\ufffd';
          }
          i += charLen;
        } else {
          // Incomplete character - save for next chunk
          this.lastTotal = charLen;
          this.lastNeed = charLen - remaining;
          for (let j = 0; j < remaining; j++) {
            this.lastChar[j] = bytes[i + j];
          }
          break;
        }
      }

      return result;
    }

    /**
     * Decode UTF-16LE bytes.
     */
    _decodeUtf16le(bytes) {
      let result = '';
      let i = 0;

      // Handle incomplete character from previous chunk
      if (this.lastNeed > 0) {
        if (bytes.length > 0) {
          this.lastChar[1] = bytes[0];
          result += String.fromCharCode(this.lastChar[0] | (this.lastChar[1] << 8));
          i = 1;
          this.lastNeed = 0;
        }
      }

      // Process pairs of bytes
      while (i < bytes.length - 1) {
        result += String.fromCharCode(bytes[i] | (bytes[i + 1] << 8));
        i += 2;
      }

      // Save incomplete byte
      if (i < bytes.length) {
        this.lastChar[0] = bytes[i];
        this.lastNeed = 1;
        this.lastTotal = 2;
      }

      return result;
    }

    /**
     * Decode Base64 bytes.
     */
    _decodeBase64(bytes) {
      // Base64 works in groups of 3 bytes -> 4 characters
      let str = '';
      for (let i = 0; i < bytes.length; i++) {
        str += String.fromCharCode(bytes[i]);
      }
      return btoa(str);
    }

    /**
     * Decode Latin-1 (ISO-8859-1) bytes.
     */
    _decodeLatin1(bytes) {
      let result = '';
      for (let i = 0; i < bytes.length; i++) {
        result += String.fromCharCode(bytes[i]);
      }
      return result;
    }

    /**
     * Decode ASCII bytes (7-bit).
     */
    _decodeAscii(bytes) {
      let result = '';
      for (let i = 0; i < bytes.length; i++) {
        result += String.fromCharCode(bytes[i] & 0x7f);
      }
      return result;
    }

    /**
     * Decode bytes as hex string.
     */
    _decodeHex(bytes) {
      let result = '';
      for (let i = 0; i < bytes.length; i++) {
        result += bytes[i].toString(16).padStart(2, '0');
      }
      return result;
    }

    /**
     * The encoding being used.
     */
    get [Symbol.toStringTag]() {
      return 'StringDecoder';
    }
  }

  /**
   * Normalize encoding name to standard form.
   */
  function normalizeEncoding(enc) {
    if (!enc) return 'utf8';
    
    const encoding = enc.toLowerCase().replace(/[^a-z0-9]/g, '');
    
    switch (encoding) {
      case 'utf8':
      case 'utf-8':
        return 'utf8';
      case 'utf16le':
      case 'utf-16le':
      case 'ucs2':
      case 'ucs-2':
        return 'utf16le';
      case 'base64':
        return 'base64';
      case 'latin1':
      case 'binary':
        return 'latin1';
      case 'ascii':
        return 'ascii';
      case 'hex':
        return 'hex';
      default:
        return 'utf8';
    }
  }

  // Export
  global.__string_decoder_module = {
    StringDecoder: StringDecoder
  };

})(globalThis);
