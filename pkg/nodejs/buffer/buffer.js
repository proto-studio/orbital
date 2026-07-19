(function() {
	'use strict';

	// Polyfill TextEncoder/TextDecoder if not available
	if (typeof TextEncoder === 'undefined') {
		globalThis.TextEncoder = class TextEncoder {
			get encoding() { return 'utf-8'; }

			encode(str) {
				const bytes = [];
				for (let i = 0; i < str.length; i++) {
					let code = str.charCodeAt(i);
					if (code < 0x80) {
						bytes.push(code);
					} else if (code < 0x800) {
						bytes.push(0xc0 | (code >> 6));
						bytes.push(0x80 | (code & 0x3f));
					} else if (code >= 0xd800 && code < 0xdc00) {
						// Surrogate pair
						i++;
						const low = str.charCodeAt(i);
						code = 0x10000 + ((code - 0xd800) << 10) + (low - 0xdc00);
						bytes.push(0xf0 | (code >> 18));
						bytes.push(0x80 | ((code >> 12) & 0x3f));
						bytes.push(0x80 | ((code >> 6) & 0x3f));
						bytes.push(0x80 | (code & 0x3f));
					} else {
						bytes.push(0xe0 | (code >> 12));
						bytes.push(0x80 | ((code >> 6) & 0x3f));
						bytes.push(0x80 | (code & 0x3f));
					}
				}
				return new Uint8Array(bytes);
			}

			// encodeInto(source, destination): UTF-8-encode source into the
			// destination Uint8Array, writing only whole code points that fit.
			// Returns { read, written } (UTF-16 units consumed / bytes written).
			// React's streaming SSR renderer relies on this being present.
			encodeInto(str, dest) {
				const cap = dest.length;
				let read = 0;
				let written = 0;
				for (let i = 0; i < str.length; i++) {
					let code = str.charCodeAt(i);
					let units = 1;
					let cp = code;
					if (code >= 0xd800 && code < 0xdc00) {
						const low = str.charCodeAt(i + 1);
						if (low >= 0xdc00 && low < 0xe000) {
							cp = 0x10000 + ((code - 0xd800) << 10) + (low - 0xdc00);
							units = 2;
						} else {
							cp = 0xfffd; // unpaired high surrogate
						}
					} else if (code >= 0xdc00 && code < 0xe000) {
						cp = 0xfffd; // unpaired low surrogate
					}

					let needed;
					if (cp < 0x80) needed = 1;
					else if (cp < 0x800) needed = 2;
					else if (cp < 0x10000) needed = 3;
					else needed = 4;

					if (written + needed > cap) break;

					if (needed === 1) {
						dest[written++] = cp;
					} else if (needed === 2) {
						dest[written++] = 0xc0 | (cp >> 6);
						dest[written++] = 0x80 | (cp & 0x3f);
					} else if (needed === 3) {
						dest[written++] = 0xe0 | (cp >> 12);
						dest[written++] = 0x80 | ((cp >> 6) & 0x3f);
						dest[written++] = 0x80 | (cp & 0x3f);
					} else {
						dest[written++] = 0xf0 | (cp >> 18);
						dest[written++] = 0x80 | ((cp >> 12) & 0x3f);
						dest[written++] = 0x80 | ((cp >> 6) & 0x3f);
						dest[written++] = 0x80 | (cp & 0x3f);
					}

					i += units - 1;
					read += units;
				}
				return { read: read, written: written };
			}
		};
	}

	if (typeof TextDecoder === 'undefined') {
		globalThis.TextDecoder = class TextDecoder {
			constructor(encoding = 'utf-8') {
				this.encoding = encoding;
			}
			decode(bytes) {
				let str = '';
				for (let i = 0; i < bytes.length; i++) {
					const b = bytes[i];
					if (b < 0x80) {
						str += String.fromCharCode(b);
					} else if ((b & 0xe0) === 0xc0) {
						const b2 = bytes[++i];
						str += String.fromCharCode(((b & 0x1f) << 6) | (b2 & 0x3f));
					} else if ((b & 0xf0) === 0xe0) {
						const b2 = bytes[++i];
						const b3 = bytes[++i];
						str += String.fromCharCode(((b & 0x0f) << 12) | ((b2 & 0x3f) << 6) | (b3 & 0x3f));
					} else if ((b & 0xf8) === 0xf0) {
						const b2 = bytes[++i];
						const b3 = bytes[++i];
						const b4 = bytes[++i];
						const code = ((b & 0x07) << 18) | ((b2 & 0x3f) << 12) | ((b3 & 0x3f) << 6) | (b4 & 0x3f);
						// Convert to surrogate pair
						const offset = code - 0x10000;
						str += String.fromCharCode(0xd800 + (offset >> 10), 0xdc00 + (offset & 0x3ff));
					}
				}
				return str;
			}
		};
	}

	// Polyfill atob/btoa if not available
	if (typeof atob === 'undefined') {
		const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
		const lookup = new Uint8Array(256);
		for (let i = 0; i < chars.length; i++) {
			lookup[chars.charCodeAt(i)] = i;
		}

		globalThis.atob = function(input) {
			// Remove whitespace and padding
			input = input.replace(/[\s=]/g, '');
			const len = input.length;
			const outLen = (len * 3) >> 2;
			const bytes = new Uint8Array(outLen);

			let j = 0;
			for (let i = 0; i < len; i += 4) {
				const a = lookup[input.charCodeAt(i)];
				const b = lookup[input.charCodeAt(i + 1)];
				const c = lookup[input.charCodeAt(i + 2)];
				const d = lookup[input.charCodeAt(i + 3)];

				bytes[j++] = (a << 2) | (b >> 4);
				if (i + 2 < len) bytes[j++] = ((b & 0x0f) << 4) | (c >> 2);
				if (i + 3 < len) bytes[j++] = ((c & 0x03) << 6) | d;
			}

			let str = '';
			for (let i = 0; i < j; i++) {
				str += String.fromCharCode(bytes[i]);
			}
			return str;
		};

		globalThis.btoa = function(input) {
			let str = '';
			for (let i = 0; i < input.length; i += 3) {
				const a = input.charCodeAt(i);
				const b = input.charCodeAt(i + 1);
				const c = input.charCodeAt(i + 2);
				const n = (a << 16) | ((b || 0) << 8) | (c || 0);
				str += chars[(n >> 18) & 63];
				str += chars[(n >> 12) & 63];
				str += i + 1 < input.length ? chars[(n >> 6) & 63] : '=';
				str += i + 2 < input.length ? chars[n & 63] : '=';
			}
			return str;
		};
	}

	// Polyfill structuredClone (WHATWG global, Node >= 17) if not available. Many
	// modern libraries (e.g. jose deep-clones JWT claim sets with it) rely on it.
	if (typeof globalThis.structuredClone === 'undefined') {
		globalThis.structuredClone = function structuredClone(value) {
			const seen = new Map();

			function dataCloneError(val) {
				const err = new Error(
					"Failed to execute 'structuredClone': " +
						Object.prototype.toString.call(val) +
						' could not be cloned.'
				);
				err.name = 'DataCloneError';
				return err;
			}

			function clone(val) {
				if (val === null || typeof val !== 'object') {
					if (typeof val === 'function' || typeof val === 'symbol') {
						throw dataCloneError(val);
					}
					return val;
				}
				if (seen.has(val)) return seen.get(val);

				if (val instanceof Date) return new Date(val.getTime());
				if (val instanceof RegExp) return new RegExp(val.source, val.flags);
				if (typeof ArrayBuffer !== 'undefined' && val instanceof ArrayBuffer) {
					return val.slice(0);
				}
				if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(val)) {
					if (typeof DataView !== 'undefined' && val instanceof DataView) {
						return new DataView(val.buffer.slice(0), val.byteOffset, val.byteLength);
					}
					const TypedCtor = val.constructor;
					return new TypedCtor(val.buffer.slice(0), val.byteOffset, val.length);
				}
				if (val instanceof Map) {
					const out = new Map();
					seen.set(val, out);
					val.forEach((v, k) => out.set(clone(k), clone(v)));
					return out;
				}
				if (val instanceof Set) {
					const out = new Set();
					seen.set(val, out);
					val.forEach((v) => out.add(clone(v)));
					return out;
				}
				if (val instanceof Error) {
					const ErrCtor = typeof val.constructor === 'function' ? val.constructor : Error;
					const out = new ErrCtor(val.message);
					seen.set(val, out);
					if (val.stack !== undefined) out.stack = val.stack;
					if (val.cause !== undefined) out.cause = clone(val.cause);
					return out;
				}
				if (Array.isArray(val)) {
					const out = new Array(val.length);
					seen.set(val, out);
					for (let i = 0; i < val.length; i++) out[i] = clone(val[i]);
					return out;
				}

				const out = {};
				seen.set(val, out);
				for (const key of Object.keys(val)) {
					out[key] = clone(val[key]);
				}
				return out;
			}

			return clone(value);
		};
	}

	const encodings = ['utf8', 'utf-8', 'ascii', 'latin1', 'binary', 'hex', 'base64', 'base64url', 'ucs2', 'ucs-2', 'utf16le', 'utf-16le'];

	function normalizeEncoding(enc) {
		if (!enc) return 'utf8';
		enc = enc.toLowerCase();
		if (enc === 'utf-8') return 'utf8';
		if (enc === 'ucs2' || enc === 'ucs-2' || enc === 'utf-16le' || enc === 'utf16le') return 'utf16le';
		return enc;
	}

	function assertEncoding(encoding) {
		encoding = normalizeEncoding(encoding);
		if (!encodings.includes(encoding)) {
			throw new TypeError('Unknown encoding: ' + encoding);
		}
		return encoding;
	}

	// Helper to convert hex string to bytes
	function hexToBytes(hex) {
		const bytes = [];
		for (let i = 0; i < hex.length; i += 2) {
			bytes.push(parseInt(hex.substr(i, 2), 16));
		}
		return bytes;
	}

	// Helper to convert bytes to hex string
	function bytesToHex(bytes) {
		let hex = '';
		for (let i = 0; i < bytes.length; i++) {
			hex += bytes[i].toString(16).padStart(2, '0');
		}
		return hex;
	}

	// Helper to convert base64 to bytes
	function base64ToBytes(base64) {
		const binString = atob(base64);
		const bytes = new Uint8Array(binString.length);
		for (let i = 0; i < binString.length; i++) {
			bytes[i] = binString.charCodeAt(i);
		}
		return bytes;
	}

	// Helper to convert bytes to base64
	function bytesToBase64(bytes) {
		let binString = '';
		for (let i = 0; i < bytes.length; i++) {
			binString += String.fromCharCode(bytes[i]);
		}
		return btoa(binString);
	}

	// base64url (RFC 4648 §5): URL-safe alphabet, padding optional. Decoding
	// restores the standard alphabet + padding; encoding strips padding and maps
	// +/ to -_ .
	function base64UrlToBytes(str) {
		str = String(str).replace(/-/g, '+').replace(/_/g, '/');
		while (str.length % 4 !== 0) str += '=';
		return base64ToBytes(str);
	}

	function bytesToBase64Url(bytes) {
		return bytesToBase64(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
	}

	class Buffer extends Uint8Array {
		// Static methods

		static alloc(size, fill, encoding) {
			if (typeof size !== 'number' || size < 0) {
				throw new RangeError('The value of "size" is out of range');
			}
			const buf = new Buffer(size);
			if (fill !== undefined) {
				buf.fill(fill, 0, size, encoding);
			}
			return buf;
		}

		static allocUnsafe(size) {
			if (typeof size !== 'number' || size < 0) {
				throw new RangeError('The value of "size" is out of range');
			}
			return new Buffer(size);
		}

		static allocUnsafeSlow(size) {
			return Buffer.allocUnsafe(size);
		}

		static from(value, encodingOrOffset, length) {
			if (typeof value === 'string') {
				return Buffer.fromString(value, encodingOrOffset);
			}
			if (ArrayBuffer.isView(value) || value instanceof ArrayBuffer) {
				return Buffer.fromArrayBuffer(value, encodingOrOffset, length);
			}
			if (Array.isArray(value)) {
				return Buffer.fromArray(value);
			}
			if (typeof value === 'object' && value !== null) {
				if (typeof value.length === 'number') {
					return Buffer.fromArray(Array.from(value));
				}
				if (value.type === 'Buffer' && Array.isArray(value.data)) {
					return Buffer.fromArray(value.data);
				}
			}
			throw new TypeError('The first argument must be a string, Buffer, ArrayBuffer, Array, or array-like object');
		}

		static fromString(string, encoding) {
			encoding = assertEncoding(encoding);
			let bytes;

			switch (encoding) {
				case 'hex':
					bytes = hexToBytes(string);
					break;
				case 'base64':
					bytes = base64ToBytes(string);
					break;
				case 'base64url':
					bytes = base64UrlToBytes(string);
					break;
				case 'utf16le':
					bytes = [];
					for (let i = 0; i < string.length; i++) {
						const code = string.charCodeAt(i);
						bytes.push(code & 0xff);
						bytes.push(code >> 8);
					}
					break;
				case 'latin1':
				case 'binary':
				case 'ascii':
					bytes = [];
					for (let i = 0; i < string.length; i++) {
						bytes.push(string.charCodeAt(i) & 0xff);
					}
					break;
				case 'utf8':
				default:
					// Use TextEncoder for UTF-8
					const encoder = new TextEncoder();
					bytes = encoder.encode(string);
					break;
			}

			const buf = new Buffer(bytes.length);
			for (let i = 0; i < bytes.length; i++) {
				buf[i] = bytes[i];
			}
			return buf;
		}

		static fromArray(array) {
			const buf = new Buffer(array.length);
			for (let i = 0; i < array.length; i++) {
				buf[i] = array[i] & 0xff;
			}
			return buf;
		}

		static fromArrayBuffer(arrayBuffer, byteOffset, length) {
			if (arrayBuffer instanceof ArrayBuffer) {
				byteOffset = byteOffset || 0;
				length = length !== undefined ? length : arrayBuffer.byteLength - byteOffset;
				const view = new Uint8Array(arrayBuffer, byteOffset, length);
				const buf = new Buffer(view.length);
				for (let i = 0; i < view.length; i++) {
					buf[i] = view[i];
				}
				return buf;
			}
			// TypedArray or DataView
			const buf = new Buffer(arrayBuffer.length || arrayBuffer.byteLength);
			for (let i = 0; i < buf.length; i++) {
				buf[i] = arrayBuffer[i];
			}
			return buf;
		}

		static isBuffer(obj) {
			return obj instanceof Buffer;
		}

		static isEncoding(encoding) {
			return encodings.includes(normalizeEncoding(encoding));
		}

		static byteLength(string, encoding) {
			if (typeof string !== 'string') {
				if (ArrayBuffer.isView(string) || string instanceof ArrayBuffer) {
					return string.byteLength;
				}
				throw new TypeError('The "string" argument must be a string or Buffer');
			}
			encoding = normalizeEncoding(encoding);
			switch (encoding) {
				case 'hex':
					return string.length >>> 1;
				case 'base64':
					// Remove padding and calculate
					let len = string.length;
					if (string[len - 1] === '=') len--;
					if (string[len - 1] === '=') len--;
					return (len * 3) >>> 2;
				case 'base64url':
					// base64url is unpadded; every 4 chars decode to 3 bytes.
					return (string.length * 3) >>> 2;
				case 'utf16le':
					return string.length * 2;
				case 'latin1':
				case 'binary':
				case 'ascii':
					return string.length;
				case 'utf8':
				default:
					const encoder = new TextEncoder();
					return encoder.encode(string).length;
			}
		}

		static concat(list, totalLength) {
			if (!Array.isArray(list)) {
				throw new TypeError('The "list" argument must be an Array of Buffers');
			}
			if (list.length === 0) {
				return Buffer.alloc(0);
			}
			if (totalLength === undefined) {
				totalLength = 0;
				for (const buf of list) {
					totalLength += buf.length;
				}
			}
			const result = Buffer.alloc(totalLength);
			let offset = 0;
			for (const buf of list) {
				for (let i = 0; i < buf.length && offset < totalLength; i++) {
					result[offset++] = buf[i];
				}
			}
			return result;
		}

		static compare(buf1, buf2) {
			if (!Buffer.isBuffer(buf1) || !Buffer.isBuffer(buf2)) {
				throw new TypeError('Arguments must be Buffers');
			}
			if (buf1 === buf2) return 0;
			const len = Math.min(buf1.length, buf2.length);
			for (let i = 0; i < len; i++) {
				if (buf1[i] < buf2[i]) return -1;
				if (buf1[i] > buf2[i]) return 1;
			}
			if (buf1.length < buf2.length) return -1;
			if (buf1.length > buf2.length) return 1;
			return 0;
		}

		// Instance methods

		toString(encoding, start, end) {
			encoding = normalizeEncoding(encoding);
			start = start || 0;
			end = end !== undefined ? end : this.length;

			if (start < 0) start = 0;
			if (end > this.length) end = this.length;
			if (end <= start) return '';

			const slice = this.subarray(start, end);

			switch (encoding) {
				case 'hex':
					return bytesToHex(slice);
				case 'base64':
					return bytesToBase64(slice);
				case 'base64url':
					return bytesToBase64Url(slice);
				case 'utf16le':
					let str = '';
					for (let i = 0; i < slice.length - 1; i += 2) {
						str += String.fromCharCode(slice[i] | (slice[i + 1] << 8));
					}
					return str;
				case 'latin1':
				case 'binary':
				case 'ascii':
					let result = '';
					for (let i = 0; i < slice.length; i++) {
						result += String.fromCharCode(slice[i]);
					}
					return result;
				case 'utf8':
				default:
					const decoder = new TextDecoder('utf-8');
					return decoder.decode(slice);
			}
		}

		toJSON() {
			return {
				type: 'Buffer',
				data: Array.from(this)
			};
		}

		equals(otherBuffer) {
			if (!Buffer.isBuffer(otherBuffer)) {
				throw new TypeError('Argument must be a Buffer');
			}
			if (this === otherBuffer) return true;
			if (this.length !== otherBuffer.length) return false;
			for (let i = 0; i < this.length; i++) {
				if (this[i] !== otherBuffer[i]) return false;
			}
			return true;
		}

		compare(target, targetStart, targetEnd, sourceStart, sourceEnd) {
			if (!Buffer.isBuffer(target)) {
				throw new TypeError('Argument must be a Buffer');
			}
			targetStart = targetStart || 0;
			targetEnd = targetEnd !== undefined ? targetEnd : target.length;
			sourceStart = sourceStart || 0;
			sourceEnd = sourceEnd !== undefined ? sourceEnd : this.length;

			const source = this.subarray(sourceStart, sourceEnd);
			const dest = target.subarray(targetStart, targetEnd);

			const len = Math.min(source.length, dest.length);
			for (let i = 0; i < len; i++) {
				if (source[i] < dest[i]) return -1;
				if (source[i] > dest[i]) return 1;
			}
			if (source.length < dest.length) return -1;
			if (source.length > dest.length) return 1;
			return 0;
		}

		copy(target, targetStart, sourceStart, sourceEnd) {
			targetStart = targetStart || 0;
			sourceStart = sourceStart || 0;
			sourceEnd = sourceEnd !== undefined ? sourceEnd : this.length;

			if (sourceEnd > this.length) sourceEnd = this.length;
			if (targetStart >= target.length) return 0;

			let bytesToCopy = sourceEnd - sourceStart;
			if (targetStart + bytesToCopy > target.length) {
				bytesToCopy = target.length - targetStart;
			}

			for (let i = 0; i < bytesToCopy; i++) {
				target[targetStart + i] = this[sourceStart + i];
			}
			return bytesToCopy;
		}

		slice(start, end) {
			// Return a new Buffer that shares memory with original
			const slice = this.subarray(start, end);
			const buf = new Buffer(slice.length);
			for (let i = 0; i < slice.length; i++) {
				buf[i] = slice[i];
			}
			return buf;
		}

		write(string, offset, length, encoding) {
			if (typeof offset === 'string') {
				encoding = offset;
				offset = 0;
				length = this.length;
			} else if (typeof length === 'string') {
				encoding = length;
				length = this.length - offset;
			}
			offset = offset || 0;
			encoding = assertEncoding(encoding);

			const buf = Buffer.fromString(string, encoding);
			const bytesToWrite = Math.min(buf.length, length, this.length - offset);
			for (let i = 0; i < bytesToWrite; i++) {
				this[offset + i] = buf[i];
			}
			return bytesToWrite;
		}

		fill(value, offset, end, encoding) {
			offset = offset || 0;
			end = end !== undefined ? end : this.length;

			if (typeof value === 'string') {
				encoding = assertEncoding(encoding);
				if (value.length === 0) {
					value = 0;
				} else if (value.length === 1) {
					value = value.charCodeAt(0);
				} else {
					const buf = Buffer.fromString(value, encoding);
					for (let i = offset; i < end; i++) {
						this[i] = buf[(i - offset) % buf.length];
					}
					return this;
				}
			}

			for (let i = offset; i < end; i++) {
				this[i] = value & 0xff;
			}
			return this;
		}

		indexOf(value, byteOffset, encoding) {
			return this._indexOfImpl(value, byteOffset, encoding, true);
		}

		lastIndexOf(value, byteOffset, encoding) {
			return this._indexOfImpl(value, byteOffset, encoding, false);
		}

		_indexOfImpl(value, byteOffset, encoding, first) {
			if (typeof byteOffset === 'string') {
				encoding = byteOffset;
				byteOffset = first ? 0 : this.length;
			}
			byteOffset = byteOffset || (first ? 0 : this.length);

			let searchBuf;
			if (typeof value === 'number') {
				searchBuf = [value & 0xff];
			} else if (typeof value === 'string') {
				encoding = assertEncoding(encoding);
				searchBuf = Buffer.fromString(value, encoding);
			} else if (Buffer.isBuffer(value)) {
				searchBuf = value;
			} else {
				throw new TypeError('value must be a string, Buffer, or number');
			}

			if (searchBuf.length === 0) return -1;

			if (first) {
				for (let i = byteOffset; i <= this.length - searchBuf.length; i++) {
					let found = true;
					for (let j = 0; j < searchBuf.length; j++) {
						if (this[i + j] !== searchBuf[j]) {
							found = false;
							break;
						}
					}
					if (found) return i;
				}
			} else {
				for (let i = Math.min(byteOffset, this.length - searchBuf.length); i >= 0; i--) {
					let found = true;
					for (let j = 0; j < searchBuf.length; j++) {
						if (this[i + j] !== searchBuf[j]) {
							found = false;
							break;
						}
					}
					if (found) return i;
				}
			}
			return -1;
		}

		includes(value, byteOffset, encoding) {
			return this.indexOf(value, byteOffset, encoding) !== -1;
		}

		// Read methods
		readUInt8(offset) {
			offset = offset || 0;
			return this[offset];
		}

		readUInt16LE(offset) {
			offset = offset || 0;
			return this[offset] | (this[offset + 1] << 8);
		}

		readUInt16BE(offset) {
			offset = offset || 0;
			return (this[offset] << 8) | this[offset + 1];
		}

		readUInt32LE(offset) {
			offset = offset || 0;
			return (this[offset] | (this[offset + 1] << 8) | (this[offset + 2] << 16)) + (this[offset + 3] * 0x1000000);
		}

		readUInt32BE(offset) {
			offset = offset || 0;
			return (this[offset] * 0x1000000) + ((this[offset + 1] << 16) | (this[offset + 2] << 8) | this[offset + 3]);
		}

		readInt8(offset) {
			offset = offset || 0;
			const val = this[offset];
			return val & 0x80 ? val - 0x100 : val;
		}

		readInt16LE(offset) {
			offset = offset || 0;
			const val = this[offset] | (this[offset + 1] << 8);
			return val & 0x8000 ? val - 0x10000 : val;
		}

		readInt16BE(offset) {
			offset = offset || 0;
			const val = (this[offset] << 8) | this[offset + 1];
			return val & 0x8000 ? val - 0x10000 : val;
		}

		readInt32LE(offset) {
			offset = offset || 0;
			return this[offset] | (this[offset + 1] << 8) | (this[offset + 2] << 16) | (this[offset + 3] << 24);
		}

		readInt32BE(offset) {
			offset = offset || 0;
			return (this[offset] << 24) | (this[offset + 1] << 16) | (this[offset + 2] << 8) | this[offset + 3];
		}

		readFloatLE(offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 4);
			return view.getFloat32(0, true);
		}

		readFloatBE(offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 4);
			return view.getFloat32(0, false);
		}

		readDoubleLE(offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 8);
			return view.getFloat64(0, true);
		}

		readDoubleBE(offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 8);
			return view.getFloat64(0, false);
		}

		// Write methods
		writeUInt8(value, offset) {
			offset = offset || 0;
			this[offset] = value & 0xff;
			return offset + 1;
		}

		writeUInt16LE(value, offset) {
			offset = offset || 0;
			this[offset] = value & 0xff;
			this[offset + 1] = (value >> 8) & 0xff;
			return offset + 2;
		}

		writeUInt16BE(value, offset) {
			offset = offset || 0;
			this[offset] = (value >> 8) & 0xff;
			this[offset + 1] = value & 0xff;
			return offset + 2;
		}

		writeUInt32LE(value, offset) {
			offset = offset || 0;
			this[offset] = value & 0xff;
			this[offset + 1] = (value >> 8) & 0xff;
			this[offset + 2] = (value >> 16) & 0xff;
			this[offset + 3] = (value >>> 24) & 0xff;
			return offset + 4;
		}

		writeUInt32BE(value, offset) {
			offset = offset || 0;
			this[offset] = (value >>> 24) & 0xff;
			this[offset + 1] = (value >> 16) & 0xff;
			this[offset + 2] = (value >> 8) & 0xff;
			this[offset + 3] = value & 0xff;
			return offset + 4;
		}

		writeInt8(value, offset) {
			offset = offset || 0;
			if (value < 0) value = 0x100 + value;
			this[offset] = value & 0xff;
			return offset + 1;
		}

		writeInt16LE(value, offset) {
			offset = offset || 0;
			if (value < 0) value = 0x10000 + value;
			this[offset] = value & 0xff;
			this[offset + 1] = (value >> 8) & 0xff;
			return offset + 2;
		}

		writeInt16BE(value, offset) {
			offset = offset || 0;
			if (value < 0) value = 0x10000 + value;
			this[offset] = (value >> 8) & 0xff;
			this[offset + 1] = value & 0xff;
			return offset + 2;
		}

		writeInt32LE(value, offset) {
			offset = offset || 0;
			this[offset] = value & 0xff;
			this[offset + 1] = (value >> 8) & 0xff;
			this[offset + 2] = (value >> 16) & 0xff;
			this[offset + 3] = (value >> 24) & 0xff;
			return offset + 4;
		}

		writeInt32BE(value, offset) {
			offset = offset || 0;
			this[offset] = (value >> 24) & 0xff;
			this[offset + 1] = (value >> 16) & 0xff;
			this[offset + 2] = (value >> 8) & 0xff;
			this[offset + 3] = value & 0xff;
			return offset + 4;
		}

		writeFloatLE(value, offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 4);
			view.setFloat32(0, value, true);
			return offset + 4;
		}

		writeFloatBE(value, offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 4);
			view.setFloat32(0, value, false);
			return offset + 4;
		}

		writeDoubleLE(value, offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 8);
			view.setFloat64(0, value, true);
			return offset + 8;
		}

		writeDoubleBE(value, offset) {
			offset = offset || 0;
			const view = new DataView(this.buffer, this.byteOffset + offset, 8);
			view.setFloat64(0, value, false);
			return offset + 8;
		}

		swap16() {
			const len = this.length;
			if (len % 2 !== 0) {
				throw new RangeError('Buffer size must be a multiple of 16-bits');
			}
			for (let i = 0; i < len; i += 2) {
				const tmp = this[i];
				this[i] = this[i + 1];
				this[i + 1] = tmp;
			}
			return this;
		}

		swap32() {
			const len = this.length;
			if (len % 4 !== 0) {
				throw new RangeError('Buffer size must be a multiple of 32-bits');
			}
			for (let i = 0; i < len; i += 4) {
				const tmp0 = this[i];
				const tmp1 = this[i + 1];
				this[i] = this[i + 3];
				this[i + 1] = this[i + 2];
				this[i + 2] = tmp1;
				this[i + 3] = tmp0;
			}
			return this;
		}

		swap64() {
			const len = this.length;
			if (len % 8 !== 0) {
				throw new RangeError('Buffer size must be a multiple of 64-bits');
			}
			for (let i = 0; i < len; i += 8) {
				const tmp0 = this[i];
				const tmp1 = this[i + 1];
				const tmp2 = this[i + 2];
				const tmp3 = this[i + 3];
				this[i] = this[i + 7];
				this[i + 1] = this[i + 6];
				this[i + 2] = this[i + 5];
				this[i + 3] = this[i + 4];
				this[i + 4] = tmp3;
				this[i + 5] = tmp2;
				this[i + 6] = tmp1;
				this[i + 7] = tmp0;
			}
			return this;
		}
	}

	// Add poolSize property
	Buffer.poolSize = 8192;

	// Add constants
	Buffer.kMaxLength = 2147483647;

	// Node defines Buffer's static API (from/alloc/isBuffer/concat/...) as
	// enumerable own properties (plain assignments), whereas ES6 class `static`
	// members are non-enumerable. Packages such as safer-buffer copy the static
	// surface with `for (key in Buffer)` + hasOwnProperty and therefore silently
	// drop every non-enumerable static, which breaks iconv-lite at runtime
	// ("Buffer.isBuffer is not a function") and anything layered on it
	// (body-parser/express.json, etc.). Re-flag the statics as enumerable so the
	// runtime matches Node's observable shape.
	for (const name of Object.getOwnPropertyNames(Buffer)) {
		if (name === 'length' || name === 'name' || name === 'prototype') continue;
		const desc = Object.getOwnPropertyDescriptor(Buffer, name);
		if (desc && desc.configurable && desc.enumerable === false) {
			desc.enumerable = true;
			Object.defineProperty(Buffer, name, desc);
		}
	}

	return Buffer;
})()
