// dgram - UDP/Datagram sockets
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module;
  const net = global.__net_module;
  const internal = global.__dgram_internal;

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  function toBuffer(msg) {
    if (Buffer.isBuffer(msg)) return msg;
    if (typeof msg === 'string') return Buffer.from(msg);
    if (ArrayBuffer.isView(msg)) return Buffer.from(msg.buffer, msg.byteOffset, msg.byteLength);
    throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "buffer" argument must be of type string or an instance of Buffer, TypedArray, or DataView.');
  }

  // Applies the offset and length of the six-argument send form.
  function sliceMessage(msg, offset, length) {
    if (Array.isArray(msg)) return msg;
    if (typeof offset !== 'number' || typeof length !== 'number') return msg;
    return toBuffer(msg).subarray(offset, offset + length);
  }

  // Messages cross into Go as base64, so binary data and the byte count reported to
  // the callback are preserved.
  function messageToBase64(msg) {
    const buf = Array.isArray(msg) ? Buffer.concat(msg.map(toBuffer)) : toBuffer(msg);
    return buf.toString('base64');
  }

  // Errors from socket implementations are prefixed with their code ("EACCES: ...").
  function socketError(message, syscall, address, port) {
    const error = new Error(message);
    const match = /^([A-Z][A-Z0-9_]+):/.exec(message);
    if (match) error.code = match[1];
    if (syscall) {
      if (!match && /address already in use/.test(message)) error.code = 'EADDRINUSE';
      else if (!match && /permission denied/.test(message)) error.code = 'EACCES';
      else if (!match && /no such host/.test(message)) error.code = 'ENOTFOUND';
      if (error.code && !match) {
        error.message = `${syscall} ${error.code} ${address}${port ? `:${port}` : ''}`;
      }
      error.syscall = syscall;
      if (address) error.address = address;
      if (port) error.port = port;
    }
    return error;
  }

  function validatePort(port) {
    const valid = (typeof port === 'number' || (typeof port === 'string' && port.trim() !== '')) &&
      +port === (+port >>> 0) && +port <= 0xFFFF;
    if (!valid) {
      throw codedError(RangeError, 'ERR_SOCKET_BAD_PORT', `Port should be >= 0 and < 65536. Received ${String(port)}.`);
    }
    return +port;
  }

  function validateTTL(ttl) {
    if (typeof ttl !== 'number') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "ttl" argument must be of type number.');
    }
  }

  /**
   * Socket class - Represents a UDP socket.
   */
  class Socket extends EventEmitter {
    constructor(options, callback) {
      super();

      const type = typeof options === 'string' ? options : options && options.type;
      if (type !== 'udp4' && type !== 'udp6') {
        throw codedError(TypeError, 'ERR_SOCKET_BAD_TYPE', 'Bad socket type specified. Valid types are: udp4, udp6');
      }
      options = typeof options === 'object' && options ? options : {};

      this.type = type;
      this._id = internal.createSocket(type);
      this._bound = false;
      this._binding = false;
      this._closed = false;
      this._receiving = false;
      this._reuseAddr = !!options.reuseAddr;
      this._recvBufferSize = options.recvBufferSize;
      this._sendBufferSize = options.sendBufferSize;
      this._remote = null;
      this._connecting = false;
      this._lookup = options.lookup;
      this._sendQueue = 0;

      if (callback) this.on('message', callback);

      if (options.signal) {
        const onAbort = () => {
          const err = codedError(Error, 'ABORT_ERR', 'The operation was aborted');
          err.name = 'AbortError';
          this.emit('error', err);
          this.close();
        };
        if (options.signal.aborted) process.nextTick(onAbort);
        else options.signal.addEventListener('abort', onAbort, { once: true });
      }
    }

    _requireRunning() {
      if (this._closed) {
        throw codedError(Error, 'ERR_SOCKET_DGRAM_NOT_RUNNING', 'Not running');
      }
    }

    /**
     * Bind the socket to an address and port.
     */
    bind(port, address, callback) {
      this._requireRunning();
      if (this._bound || this._binding) {
        throw codedError(Error, 'ERR_SOCKET_ALREADY_BOUND', 'Socket is already bound');
      }

      if (port !== null && typeof port === 'object') {
        // bind(options, callback)
        callback = address;
        address = port.address;
        port = port.port;
      } else if (typeof port === 'function') {
        callback = port;
        port = 0;
        address = undefined;
      }

      if (typeof address === 'function') {
        callback = address;
        address = undefined;
      }

      port = port === undefined || port === null ? 0 : validatePort(port);
      address = address || (this.type === 'udp6' ? '::' : '0.0.0.0');

      if (typeof callback === 'function') this.once('listening', callback);

      this._binding = true;
      internal.bind(this._id, address, port, (err) => {
        this._binding = false;
        if (this._closed) return;
        if (err) {
          this.emit('error', socketError(err, 'bind', address, port));
          return;
        }

        this._bound = true;
        this._applyBufferSizes();
        this.emit('listening');
        this._startReceiving();
      });

      return this;
    }

    _applyBufferSizes() {
      if (this._recvBufferSize) internal.setOption(this._id, 'recvBuffer', this._recvBufferSize);
      if (this._sendBufferSize) internal.setOption(this._id, 'sendBuffer', this._sendBufferSize);
    }

    _startReceiving() {
      if (this._closed || this._receiving) return;
      this._receiving = true;

      internal.receive(this._id, (err, msg, rinfo) => {
        this._receiving = false;
        if (this._closed) return;
        if (err) {
          this.emit('error', socketError(err, 'recvmsg'));
          return;
        }

        // A connected socket only hears from its peer.
        const remote = this._remote;
        if (!remote || (remote.address === rinfo.address && remote.port === rinfo.port)) {
          this.emit('message', Buffer.from(msg, 'base64'), rinfo);
        }

        if (!this._closed) this._startReceiving();
      });
    }

    /**
     * Send a message.
     */
    send(msg, offset, length, port, address, callback) {
      // Same overload rule as Node: arguments four and five only exist in the
      // (msg, offset, length, port[, address][, callback]) forms. Otherwise this is
      // (msg, port[, address][, callback]).
      const connected = !!this._remote;
      if (!connected) {
        if (address || (port && typeof port !== 'function')) {
          msg = sliceMessage(msg, offset, length);
        } else {
          callback = port;
          port = offset;
          address = length;
        }
      } else {
        if (typeof length === 'number') {
          msg = sliceMessage(msg, offset, length);
          if (typeof port === 'function') {
            callback = port;
            port = null;
          }
        } else {
          callback = offset;
        }
        if (port || address) {
          throw codedError(Error, 'ERR_SOCKET_DGRAM_IS_CONNECTED', 'Already connected');
        }
      }

      if (typeof address === 'function') {
        callback = address;
        address = undefined;
      }

      this._requireRunning();

      let destination;
      if (connected) {
        destination = { host: this._remote.host, port: this._remote.port };
      } else {
        if (port === undefined || port === null) {
          throw codedError(RangeError, 'ERR_SOCKET_BAD_PORT', 'Port should be > 0 and < 65536. Received undefined.');
        }
        const p = validatePort(port);
        if (p === 0) {
          throw codedError(RangeError, 'ERR_SOCKET_BAD_PORT', 'Port should be > 0 and < 65536. Received 0.');
        }
        destination = { host: address || (this.type === 'udp6' ? '::1' : '127.0.0.1'), port: p };
      }

      if (address !== undefined && address !== null && typeof address !== 'string') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "address" argument must be of type string or falsy.');
      }

      const encoded = messageToBase64(msg);
      const wasBound = this._bound;
      this._sendQueue++;

      const family = this.type === 'udp6' ? 6 : 4;
      internal.send(this._id, encoded, destination.host, destination.port, family, (err, bytes) => {
        this._sendQueue--;
        if (err) {
          const error = socketError(err, 'send', destination.host, destination.port);
          if (typeof callback === 'function') callback(error);
          else this.emit('error', error);
          return;
        }

        // A successful first send may have bound an ephemeral port. Replies to it
        // are delivered as for an explicit bind, as in Node.
        if (!wasBound && !this._bound && !this._closed && internal.address(this._id)) {
          this._bound = true;
          this._applyBufferSizes();
          this._startReceiving();
        }

        if (typeof callback === 'function') callback(null, bytes);
      });

      return this;
    }

    /**
     * Connect to a remote address, which then filters received packets.
     */
    connect(port, address, callback) {
      port = validatePort(port);
      if (port === 0) {
        throw codedError(RangeError, 'ERR_SOCKET_BAD_PORT', 'Port should be > 0 and < 65536. Received 0.');
      }
      if (typeof address === 'function') {
        callback = address;
        address = undefined;
      }
      this._requireRunning();
      if (this._remote || this._connecting) {
        throw codedError(Error, 'ERR_SOCKET_DGRAM_IS_CONNECTED', 'Already connected');
      }
      const host = address || (this.type === 'udp6' ? '::1' : '127.0.0.1');
      const fail = (err) => {
        this._connecting = false;
        process.nextTick(() => {
          if (typeof callback === 'function') callback(err);
          else this.emit('error', err);
        });
      };

      this._connecting = true;
      const start = () => {
        const family = this.type === 'udp6' ? 6 : 4;
        internal.resolve(host, family, (err, ip) => {
          if (this._closed) return;
          if (err) {
            const error = socketError(err, 'getaddrinfo', host);
            error.message = `getaddrinfo ${error.code} ${host}`;
            error.hostname = host;
            delete error.address;
            fail(error);
            return;
          }
          this._connecting = false;
          this._remote = { host, address: ip, port };
          if (typeof callback === 'function') callback();
          this.emit('connect');
        });
      };

      if (this._bound) {
        start();
      } else {
        this.once('listening', start);
        this.bind(0);
      }
      return this;
    }

    /**
     * Disconnect from remote address.
     */
    disconnect() {
      this._requireRunning();
      if (!this._remote) {
        throw codedError(Error, 'ERR_SOCKET_DGRAM_NOT_CONNECTED', 'Not connected');
      }
      this._remote = null;
    }

    /**
     * Get remote address.
     */
    remoteAddress() {
      this._requireRunning();
      if (!this._remote) {
        throw codedError(Error, 'ERR_SOCKET_DGRAM_NOT_CONNECTED', 'Not connected');
      }
      const { address, port } = this._remote;
      return { address, family: net.isIP(address) === 6 ? 'IPv6' : 'IPv4', port };
    }

    /**
     * Close the socket.
     */
    close(callback) {
      this._requireRunning();
      if (typeof callback === 'function') this.once('close', callback);
      this._closed = true;
      this._bound = false;
      internal.close(this._id);
      process.nextTick(() => this.emit('close'));
      return this;
    }

    [Symbol.asyncDispose]() {
      if (this._closed) return Promise.resolve();
      return new Promise((resolve) => this.close(resolve));
    }

    /**
     * Get the socket address.
     */
    address() {
      this._requireRunning();
      const info = this._bound ? internal.address(this._id) : null;
      if (!info) {
        throw codedError(Error, 'EBADF', 'getsockname EBADF');
      }
      return info;
    }

    _setOption(syscall, name, ...args) {
      this._requireRunning();
      const code = internal.setOption(this._id, name, ...args);
      if (code) {
        const err = codedError(Error, code, `${syscall} ${code}`);
        err.syscall = syscall;
        throw err;
      }
    }

    setBroadcast(flag) {
      this._requireRunning();
      internal.setBroadcast(this._id, !!flag);
    }

    setTTL(ttl) {
      validateTTL(ttl);
      this._setOption('setTTL', 'ttl', ttl);
      return ttl;
    }

    setMulticastTTL(ttl) {
      validateTTL(ttl);
      this._requireRunning();
      internal.setMulticastTTL(this._id, ttl);
      return ttl;
    }

    setMulticastLoopback(flag) {
      this._setOption('setMulticastLoopback', 'multicastLoopback', flag ? 1 : 0);
      return flag;
    }

    setMulticastInterface(interfaceAddress) {
      if (typeof interfaceAddress !== 'string') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "interfaceAddress" argument must be of type string.');
      }
      this._setOption('setMulticastInterface', 'multicastInterface', interfaceAddress);
    }

    addMembership(multicastAddress, interfaceAddress) {
      this._requireRunning();
      if (!multicastAddress) {
        throw codedError(TypeError, 'ERR_MISSING_ARGS', 'The "multicastAddress" argument must be specified');
      }
      internal.addMembership(this._id, multicastAddress, interfaceAddress || '');
    }

    dropMembership(multicastAddress, interfaceAddress) {
      this._requireRunning();
      if (!multicastAddress) {
        throw codedError(TypeError, 'ERR_MISSING_ARGS', 'The "multicastAddress" argument must be specified');
      }
      internal.dropMembership(this._id, multicastAddress, interfaceAddress || '');
    }

    addSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress) {
      this._setOption('addSourceSpecificMembership', 'joinSource', sourceAddress, groupAddress, interfaceAddress || '');
    }

    dropSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress) {
      this._setOption('dropSourceSpecificMembership', 'leaveSource', sourceAddress, groupAddress, interfaceAddress || '');
    }

    setRecvBufferSize(size) {
      this._bufferSize('recvBuffer', size);
      this._recvBufferSize = size;
    }

    setSendBufferSize(size) {
      this._bufferSize('sendBuffer', size);
      this._sendBufferSize = size;
    }

    _bufferSize(name, size) {
      this._requireRunning();
      if (!this._bound) {
        throw codedError(Error, 'ERR_SOCKET_BUFFER_SIZE', 'Could not get or set buffer size');
      }
      const code = internal.setOption(this._id, name, size);
      if (code) {
        throw codedError(Error, 'ERR_SOCKET_BUFFER_SIZE', 'Could not get or set buffer size');
      }
    }

    getRecvBufferSize() {
      this._requireBufferReady();
      return this._recvBufferSize || 65536;
    }

    getSendBufferSize() {
      this._requireBufferReady();
      return this._sendBufferSize || 65536;
    }

    _requireBufferReady() {
      this._requireRunning();
      if (!this._bound) {
        throw codedError(Error, 'ERR_SOCKET_BUFFER_SIZE', 'Could not get or set buffer size');
      }
    }

    getSendQueueSize() {
      return 0;
    }

    getSendQueueCount() {
      return this._sendQueue;
    }

    ref() {
      if (!this._closed) internal.ref(this._id);
      return this;
    }

    unref() {
      if (!this._closed) internal.unref(this._id);
      return this;
    }
  }

  function createSocket(options, callback) {
    return new Socket(options, callback);
  }

  global.__dgram_module = { Socket, createSocket };

})(globalThis);
