// dgram - UDP/Datagram sockets
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const internal = global.__dgram_internal;

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
          this.emit('error', new Error(err));
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
      // Handle overloads
      if (typeof offset === 'number' && typeof length === 'number') {
        // send(msg, offset, length, port, address, callback)
        if (Buffer.isBuffer(msg)) {
          msg = msg.slice(offset, offset + length);
        }
      } else if (typeof offset === 'number') {
        // send(msg, port, address, callback)
        callback = address;
        address = length;
        port = offset;
      } else {
        // send(msg, port, address, callback)
        callback = length;
        address = port;
        port = offset;
      }

      if (typeof address === 'function') {
        callback = address;
        address = undefined;
      }

      // Default address
      address = address || (this._type === 'udp6' ? '::1' : '127.0.0.1');

      // Convert message to string
      let msgStr;
      if (Buffer.isBuffer(msg)) {
        msgStr = msg.toString('binary');
      } else if (typeof msg === 'string') {
        msgStr = msg;
      } else if (Array.isArray(msg)) {
        // Array of buffers
        msgStr = Buffer.concat(msg.map(m => Buffer.isBuffer(m) ? m : Buffer.from(m))).toString('binary');
      } else {
        msgStr = String(msg);
      }

      internal.send(this._id, msgStr, address, port, (err, bytes) => {
        if (err) {
          if (callback) callback(new Error(err));
          else this.emit('error', new Error(err));
          return;
        }

        if (callback) callback(null, bytes);
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
