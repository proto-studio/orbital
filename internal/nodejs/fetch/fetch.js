// fetch - Web Fetch API implementation
(function(global) {
  'use strict';

  /**
   * Headers class for HTTP headers.
   */
  class Headers {
    constructor(init) {
      this._headers = new Map();
      
      if (init) {
        if (init instanceof Headers) {
          init.forEach((value, name) => {
            this.append(name, value);
          });
        } else if (Array.isArray(init)) {
          for (const [name, value] of init) {
            this.append(name, value);
          }
        } else if (typeof init === 'object') {
          for (const name of Object.keys(init)) {
            this.append(name, init[name]);
          }
        }
      }
    }

    _normalizeName(name) {
      return String(name).toLowerCase();
    }

    append(name, value) {
      const key = this._normalizeName(name);
      const existing = this._headers.get(key);
      if (existing) {
        this._headers.set(key, existing + ', ' + value);
      } else {
        this._headers.set(key, String(value));
      }
    }

    delete(name) {
      this._headers.delete(this._normalizeName(name));
    }

    get(name) {
      return this._headers.get(this._normalizeName(name)) || null;
    }

    has(name) {
      return this._headers.has(this._normalizeName(name));
    }

    set(name, value) {
      this._headers.set(this._normalizeName(name), String(value));
    }

    entries() {
      return this._headers.entries();
    }

    keys() {
      return this._headers.keys();
    }

    values() {
      return this._headers.values();
    }

    forEach(callback, thisArg) {
      for (const [name, value] of this._headers) {
        callback.call(thisArg, value, name, this);
      }
    }

    [Symbol.iterator]() {
      return this._headers.entries();
    }

    get [Symbol.toStringTag]() {
      return 'Headers';
    }
  }

  /**
   * FormData class for multipart form data.
   */
  class FormData {
    constructor() {
      this._entries = [];
    }

    append(name, value, filename) {
      this._entries.push({ name: String(name), value, filename });
    }

    delete(name) {
      this._entries = this._entries.filter(e => e.name !== String(name));
    }

    get(name) {
      const entry = this._entries.find(e => e.name === String(name));
      return entry ? entry.value : null;
    }

    getAll(name) {
      return this._entries
        .filter(e => e.name === String(name))
        .map(e => e.value);
    }

    has(name) {
      return this._entries.some(e => e.name === String(name));
    }

    set(name, value, filename) {
      this.delete(name);
      this.append(name, value, filename);
    }

    entries() {
      return this._entries.map(e => [e.name, e.value])[Symbol.iterator]();
    }

    keys() {
      return this._entries.map(e => e.name)[Symbol.iterator]();
    }

    values() {
      return this._entries.map(e => e.value)[Symbol.iterator]();
    }

    forEach(callback, thisArg) {
      for (const entry of this._entries) {
        callback.call(thisArg, entry.value, entry.name, this);
      }
    }

    [Symbol.iterator]() {
      return this.entries();
    }

    get [Symbol.toStringTag]() {
      return 'FormData';
    }

    // Internal: Convert to string for request body
    _toString() {
      const boundary = '----FormData' + Math.random().toString(36).substr(2);
      let body = '';
      for (const entry of this._entries) {
        body += '--' + boundary + '\r\n';
        body += 'Content-Disposition: form-data; name="' + entry.name + '"';
        if (entry.filename) {
          body += '; filename="' + entry.filename + '"';
        }
        body += '\r\n\r\n';
        body += String(entry.value) + '\r\n';
      }
      body += '--' + boundary + '--\r\n';
      return { body, boundary };
    }
  }

  /**
   * Request class representing an HTTP request.
   */
  class Request {
    constructor(input, init) {
      init = init || {};
      
      if (input instanceof Request) {
        this._url = input.url;
        this._method = input.method;
        this._headers = new Headers(input.headers);
        this._body = input._body;
        this._mode = input.mode;
        this._credentials = input.credentials;
        this._cache = input.cache;
        this._redirect = input.redirect;
        this._referrer = input.referrer;
        this._integrity = input.integrity;
      } else {
        this._url = String(input);
        this._method = (init.method || 'GET').toUpperCase();
        this._headers = new Headers(init.headers);
        this._body = init.body;
        this._mode = init.mode || 'cors';
        this._credentials = init.credentials || 'same-origin';
        this._cache = init.cache || 'default';
        this._redirect = init.redirect || 'follow';
        this._referrer = init.referrer || 'about:client';
        this._integrity = init.integrity || '';
      }
      
      if (init.headers) {
        this._headers = new Headers(init.headers);
      }
      if (init.body !== undefined) {
        this._body = init.body;
      }
      if (init.method) {
        this._method = init.method.toUpperCase();
      }
      
      this._signal = init.signal || null;
      this._bodyUsed = false;
    }

    get url() { return this._url; }
    get method() { return this._method; }
    get headers() { return this._headers; }
    get mode() { return this._mode; }
    get credentials() { return this._credentials; }
    get cache() { return this._cache; }
    get redirect() { return this._redirect; }
    get referrer() { return this._referrer; }
    get integrity() { return this._integrity; }
    get signal() { return this._signal; }
    get bodyUsed() { return this._bodyUsed; }

    clone() {
      if (this._bodyUsed) {
        throw new TypeError('Cannot clone a Request whose body has been used');
      }
      return new Request(this);
    }

    async text() {
      this._bodyUsed = true;
      if (this._body === null || this._body === undefined) return '';
      return String(this._body);
    }

    async json() {
      const text = await this.text();
      return JSON.parse(text);
    }

    async arrayBuffer() {
      const text = await this.text();
      const encoder = new TextEncoder();
      return encoder.encode(text).buffer;
    }

    async blob() {
      const buffer = await this.arrayBuffer();
      return new Blob([buffer]);
    }

    async formData() {
      throw new TypeError('Request.formData() not supported');
    }

    get [Symbol.toStringTag]() {
      return 'Request';
    }
  }

  /**
   * Response class representing an HTTP response.
   */
  class Response {
    constructor(body, init) {
      init = init || {};
      
      this._body = body;
      this._status = init.status !== undefined ? init.status : 200;
      this._statusText = init.statusText || '';
      this._ok = this._status >= 200 && this._status < 300;
      this._headers = new Headers(init.headers);
      this._type = 'default';
      this._url = init.url || '';
      this._redirected = false;
      this._bodyUsed = false;
    }

    get status() { return this._status; }
    get statusText() { return this._statusText; }
    get ok() { return this._ok; }
    get headers() { return this._headers; }
    get type() { return this._type; }
    get url() { return this._url; }
    get redirected() { return this._redirected; }
    get bodyUsed() { return this._bodyUsed; }

    clone() {
      if (this._bodyUsed) {
        throw new TypeError('Cannot clone a Response whose body has been used');
      }
      return new Response(this._body, {
        status: this._status,
        statusText: this._statusText,
        headers: this._headers,
        url: this._url
      });
    }

    async text() {
      this._bodyUsed = true;
      if (this._body === null || this._body === undefined) return '';
      return String(this._body);
    }

    async json() {
      const text = await this.text();
      return JSON.parse(text);
    }

    async arrayBuffer() {
      const text = await this.text();
      const encoder = new TextEncoder();
      return encoder.encode(text).buffer;
    }

    async blob() {
      const buffer = await this.arrayBuffer();
      return new Blob([buffer]);
    }

    async formData() {
      throw new TypeError('Response.formData() not supported');
    }

    static error() {
      const response = new Response(null, { status: 0 });
      response._type = 'error';
      return response;
    }

    static redirect(url, status) {
      if (![301, 302, 303, 307, 308].includes(status)) {
        status = 302;
      }
      return new Response(null, {
        status,
        headers: { Location: url }
      });
    }

    static json(data, init) {
      init = init || {};
      const headers = new Headers(init.headers);
      if (!headers.has('content-type')) {
        headers.set('content-type', 'application/json');
      }
      return new Response(JSON.stringify(data), {
        ...init,
        headers
      });
    }

    get [Symbol.toStringTag]() {
      return 'Response';
    }
  }

  /**
   * Simple Blob implementation.
   */
  if (typeof global.Blob === 'undefined') {
    class Blob {
      constructor(parts, options) {
        this._parts = parts || [];
        this._type = options && options.type || '';
      }

      get size() {
        let size = 0;
        for (const part of this._parts) {
          if (typeof part === 'string') {
            size += new TextEncoder().encode(part).length;
          } else if (part instanceof ArrayBuffer) {
            size += part.byteLength;
          } else if (part instanceof Uint8Array) {
            size += part.byteLength;
          }
        }
        return size;
      }

      get type() {
        return this._type;
      }

      async text() {
        const decoder = new TextDecoder();
        let result = '';
        for (const part of this._parts) {
          if (typeof part === 'string') {
            result += part;
          } else if (part instanceof ArrayBuffer) {
            result += decoder.decode(part);
          } else if (part instanceof Uint8Array) {
            result += decoder.decode(part);
          }
        }
        return result;
      }

      async arrayBuffer() {
        const text = await this.text();
        return new TextEncoder().encode(text).buffer;
      }

      slice(start, end, type) {
        return new Blob(this._parts, { type: type || this._type });
      }
    }
    global.Blob = Blob;
  }

  /**
   * The fetch function.
   */
  async function fetch(input, init) {
    const request = input instanceof Request ? input : new Request(input, init);
    
    // Check if aborted
    if (request.signal && request.signal.aborted) {
      throw new DOMException('The operation was aborted', 'AbortError');
    }

    // Get body if any
    let body = null;
    if (request._body) {
      if (request._body instanceof FormData) {
        const { body: formBody, boundary } = request._body._toString();
        body = formBody;
        if (!request.headers.has('content-type')) {
          request.headers.set('content-type', 'multipart/form-data; boundary=' + boundary);
        }
      } else if (typeof request._body === 'string') {
        body = request._body;
      } else if (request._body instanceof URLSearchParams) {
        body = request._body.toString();
        if (!request.headers.has('content-type')) {
          request.headers.set('content-type', 'application/x-www-form-urlencoded');
        }
      } else {
        body = String(request._body);
      }
    }

    // Build headers object for Go
    const headersObj = {};
    request.headers.forEach((value, name) => {
      headersObj[name] = value;
    });

    return new Promise((resolve, reject) => {
      // Check abort signal
      if (request.signal) {
        request.signal.addEventListener('abort', () => {
          reject(new DOMException('The operation was aborted', 'AbortError'));
        }, { once: true });
      }

      // Call internal fetch
      if (typeof __http_fetch === 'function') {
        __http_fetch(
          request.url,
          request.method,
          JSON.stringify(headersObj),
          body || '',
          (error, statusCode, statusText, responseHeaders, responseBody) => {
            if (error) {
              reject(new TypeError('Failed to fetch: ' + error));
              return;
            }

            const respHeaders = responseHeaders ? JSON.parse(responseHeaders) : {};
            const response = new Response(responseBody, {
              status: statusCode,
              statusText: statusText,
              headers: respHeaders,
              url: request.url
            });
            
            resolve(response);
          }
        );
      } else {
        reject(new TypeError('fetch not available (HTTP client not configured)'));
      }
    });
  }

  // Export as globals
  global.fetch = fetch;
  global.Headers = Headers;
  global.Request = Request;
  global.Response = Response;
  global.FormData = FormData;

})(globalThis);
