// dgram - UDP/Datagram sockets
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const internal = global.__dgram_internal;

  function toBuffer(msg) {
    if (Buffer.isBuffer(msg)) return msg;
    if (typeof msg === 'string') return Buffer.from(msg);
    if (ArrayBuffer.isView(msg)) return Buffer.from(msg.buffer, msg.byteOffset, msg.byteLength);
    return Buffer.from(String(msg));
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
  function socketError(message) {
    const error = new Error(message);
    const match = /^([A-Z][A-Z0-9_]+):/.exec(message);
    if (match) error.code = match[1];
    return error;
  }

  /**
   * Socket class - Represents a UDP socket.
   */
  class Socket extends EventEmitter {
    constructor(options, callback) {
      super();
      
      if (typeof options === 'string') {
        options = { type: options };
      }
      
      options = options || {};
      this._type = options.type || 'udp4';
      this._id = null;
      this._bound = false;
      this._address = null;
      this._port = null;
      this._reuseAddr = options.reuseAddr || false;
      this._ipv6Only = options.ipv6Only || false;
      this._recvBufferSize = options.recvBufferSize;
      this._sendBufferSize = options.sendBufferSize;

      if (callback) {
        this.on('message', callback);
      }

      // Create the socket
      this._id = internal.createSocket(this._type);
    }

    get type() { return this._type; }

    /**
     * Bind the socket to an address and port.
     */
    bind(port, address, callback) {
      if (typeof port === 'object') {
        // bind(options, callback)
        callback = address;
        address = port.address;
        port = port.port;
      }

      if (typeof address === 'function') {
        callback = address;
        address = undefined;
      }

      port = port || 0;
      address = address || (this._type === 'udp6' ? '::' : '0.0.0.0');

      if (callback) {
        this.once('listening', callback);
      }

      internal.bind(this._id, address, port, (err) => {
        if (err) {
          this.emit('error', socketError(err));
          return;
        }

        this._bound = true;
        
        // Get bound address
        const addrInfo = internal.address(this._id);
        if (addrInfo) {
          this._address = addrInfo.address;
          this._port = addrInfo.port;
        }

        this.emit('listening');

        // Start receiving messages
        this._startReceiving();
      });

      return this;
    }

    _startReceiving() {
      if (!this._bound) return;

      internal.receive(this._id, (err, msg, rinfo) => {
        if (err) {
          if (this._bound) {
            this.emit('error', new Error(err));
          }
          return;
        }

        if (msg === null) {
          // Socket closed
          return;
        }

        // Convert to Buffer
        const buf = Buffer.from(msg, 'binary');
        this.emit('message', buf, rinfo);

        // Continue receiving
        this._startReceiving();
      });
    }

    /**
     * Send a message.
     */
    send(msg, offset, length, port, address, callback) {
      // Same overload rule as Node: arguments four and five only exist in the
      // (msg, offset, length, port[, address][, callback]) forms. Otherwise this is
      // (msg, port[, address][, callback]).
      if (address || (port && typeof port !== 'function')) {
        msg = sliceMessage(msg, offset, length);
      } else {
        callback = port;
        port = offset;
        address = length;
      }

      if (typeof address === 'function') {
        callback = address;
        address = undefined;
      }

      // Default address
      address = address || (this._type === 'udp6' ? '::1' : '127.0.0.1');

      const wasBound = this._bound;

      internal.send(this._id, messageToBase64(msg), address, port, (err, bytes) => {
        if (err) {
          const error = socketError(err);
          if (typeof callback === 'function') callback(error);
          else this.emit('error', error);
          return;
        }

        // A successful first send may have bound an ephemeral port. Replies to it
        // are delivered as for an explicit bind, as in Node.
        if (!wasBound && !this._bound && this._id !== null && internal.address(this._id)) {
          this._bound = true;
          this._startReceiving();
        }

        if (typeof callback === 'function') callback(null, bytes);
      });

      return this;
    }

    /**
     * Close the socket.
     */
    close(callback) {
      if (callback) {
        this.once('close', callback);
      }

      this._bound = false;

      if (this._id !== null) {
        internal.close(this._id);
        this._id = null;
      }

      process.nextTick(() => this.emit('close'));

      return this;
    }

    /**
     * Get the socket address.
     */
    address() {
      if (!this._bound) return null;
      return internal.address(this._id);
    }

    /**
     * Set broadcast mode.
     */
    setBroadcast(flag) {
      internal.setBroadcast(this._id, flag);
      return this;
    }

    /**
     * Set multicast TTL.
     */
    setMulticastTTL(ttl) {
      internal.setMulticastTTL(this._id, ttl);
      return this;
    }

    /**
     * Set multicast loopback.
     */
    setMulticastLoopback(flag) {
      internal.setMulticastLoopback(this._id, flag);
      return this;
    }

    /**
     * Set multicast interface.
     */
    setMulticastInterface(interfaceAddress) {
      internal.setMulticastInterface(this._id, interfaceAddress);
      return this;
    }

    /**
     * Add multicast membership.
     */
    addMembership(multicastAddress, interfaceAddress) {
      internal.addMembership(this._id, multicastAddress, interfaceAddress || '');
      return this;
    }

    /**
     * Drop multicast membership.
     */
    dropMembership(multicastAddress, interfaceAddress) {
      internal.dropMembership(this._id, multicastAddress, interfaceAddress || '');
      return this;
    }

    /**
     * Add source-specific multicast membership.
     */
    addSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress) {
      // Not implemented
      return this;
    }

    /**
     * Drop source-specific multicast membership.
     */
    dropSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress) {
      // Not implemented
      return this;
    }

    /**
     * Set TTL.
     */
    setTTL(ttl) {
      internal.setTTL(this._id, ttl);
      return this;
    }

    /**
     * Set receive buffer size.
     */
    setRecvBufferSize(size) {
      internal.setRecvBufferSize(this._id, size);
      return this;
    }

    /**
     * Set send buffer size.
     */
    setSendBufferSize(size) {
      internal.setSendBufferSize(this._id, size);
      return this;
    }

    /**
     * Get receive buffer size.
     */
    getRecvBufferSize() {
      return internal.getRecvBufferSize(this._id);
    }

    /**
     * Get send buffer size.
     */
    getSendBufferSize() {
      return internal.getSendBufferSize(this._id);
    }

    /**
     * Connect to a remote address (filters received packets).
     */
    connect(port, address, callback) {
      if (typeof address === 'function') {
        callback = address;
        address = undefined;
      }

      address = address || (this._type === 'udp6' ? '::1' : '127.0.0.1');

      internal.connect(this._id, address, port, (err) => {
        if (err) {
          if (callback) callback(new Error(err));
          else this.emit('error', new Error(err));
          return;
        }

        this.emit('connect');
        if (callback) callback();
      });

      return this;
    }

    /**
     * Disconnect from remote address.
     */
    disconnect() {
      internal.disconnect(this._id);
      return this;
    }

    /**
     * Get remote address.
     */
    remoteAddress() {
      return internal.remoteAddress(this._id);
    }

    /**
     * Reference/unreference for event loop.
     */
    ref() {
      internal.ref(this._id);
      return this;
    }

    unref() {
      internal.unref(this._id);
      return this;
    }
  }

  /**
   * Create a UDP socket.
   */
  function createSocket(options, callback) {
    return new Socket(options, callback);
  }

  // Export module
  const dgram = {
    Socket,
    createSocket
  };

  global.__dgram_module = dgram;

})(globalThis);
