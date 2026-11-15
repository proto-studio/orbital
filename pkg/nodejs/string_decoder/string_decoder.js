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
      // Base64 encodes 3 bytes at a time. Bytes short of a group wait for the next write.
      this.base64Rest = new Uint8Array(0);
      // The start of a UTF-8 character whose other bytes have not arrived yet.
      this.utf8Rest = new Uint8Array(0);
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
      if (this.base64Rest.length) {
        const rest = btoa(String.fromCharCode.apply(null, this.base64Rest));
        r += this.encoding === 'base64url' ? toBase64Url(rest) : rest;
        this.base64Rest = new Uint8Array(0);
      }
      if (this.utf8Rest.length) {
        // What is left is an unfinished or invalid character, replaced as Node does.
        r += utf8Decoder.decode(this.utf8Rest);
        this.utf8Rest = new Uint8Array(0);
      }
      // As in Node, a lone UTF-16 byte at the end is dropped.
      this.lastNeed = 0;
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
        case 'base64url':
          return toBase64Url(this._decodeBase64(bytes));
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
      if (this.utf8Rest.length) {
        const joined = new Uint8Array(this.utf8Rest.length + bytes.length);
        joined.set(this.utf8Rest, 0);
        joined.set(bytes, this.utf8Rest.length);
        bytes = joined;
      }
      // Keep back a character that has started but not finished: a lead byte within the last
      // three bytes, followed only by continuation bytes, and too few of them.
      let keep = 0;
      for (let back = 1; back <= Math.min(3, bytes.length); back++) {
        const b = bytes[bytes.length - back];
        if ((b & 0xc0) === 0x80) continue;
        // Only a valid lead byte starts a character worth waiting for.
        const len = b >= 0xf0 && b <= 0xf4 ? 4 : b >= 0xe0 && b <= 0xef ? 3 : b >= 0xc2 && b <= 0xdf ? 2 : 1;
        if (len > back) keep = back;
        break;
      }
      this.utf8Rest = bytes.slice(bytes.length - keep);
      // The rest decodes on its own, with invalid bytes replaced as Node replaces them.
      return utf8Decoder.decode(bytes.subarray(0, bytes.length - keep));
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
      // Base64 works in groups of 3 bytes -> 4 characters. A partial group would be padded
      // in the middle of the output, so it waits for more bytes or for end().
      if (this.base64Rest.length) {
        const joined = new Uint8Array(this.base64Rest.length + bytes.length);
        joined.set(this.base64Rest, 0);
        joined.set(bytes, this.base64Rest.length);
        bytes = joined;
      }
      const whole = bytes.length - (bytes.length % 3);
      this.base64Rest = bytes.slice(whole);
      let str = '';
      for (let i = 0; i < whole; i++) {
        str += String.fromCharCode(bytes[i]);
      }
      return str ? btoa(str) : '';
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
  const utf8Decoder = new TextDecoder('utf-8');

  function toBase64Url(str) {
    return str.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

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
      case 'base64url':
        return 'base64url';
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
