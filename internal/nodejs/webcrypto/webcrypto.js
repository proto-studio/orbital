// crypto/webcrypto - Web Crypto API
(function(global) {
  'use strict';

  // Get the existing crypto module
  const cryptoModule = global.__crypto_module;

  /**
   * CryptoKey class representing a cryptographic key.
   */
  class CryptoKey {
    constructor(type, extractable, algorithm, usages) {
      this._type = type;
      this._extractable = extractable;
      this._algorithm = algorithm;
      this._usages = usages;
      this._keyData = null;
    }

    get type() { return this._type; }
    get extractable() { return this._extractable; }
    get algorithm() { return this._algorithm; }
    get usages() { return this._usages.slice(); }

    get [Symbol.toStringTag]() {
      return 'CryptoKey';
    }
  }

  /**
   * SubtleCrypto interface for cryptographic operations.
   */
  class SubtleCrypto {
    /**
     * Generate a digest (hash) of the given data.
     */
    async digest(algorithm, data) {
      const algoName = typeof algorithm === 'string' ? algorithm : algorithm.name;
      const normalizedAlgo = algoName.toUpperCase().replace('-', '');
      
      // Convert data to Uint8Array if needed
      let bytes;
      if (data instanceof ArrayBuffer) {
        bytes = new Uint8Array(data);
      } else if (ArrayBuffer.isView(data)) {
        bytes = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
      } else {
        throw new TypeError('Data must be BufferSource');
      }

      // Use crypto module hash function
      const hashName = normalizedAlgo.toLowerCase();
      if (!['md5', 'sha1', 'sha256', 'sha384', 'sha512'].includes(hashName)) {
        throw new DOMException(`Unsupported algorithm: ${algoName}`, 'NotSupportedError');
      }

      // Convert bytes to string for hashing
      let dataStr = '';
      for (let i = 0; i < bytes.length; i++) {
        dataStr += String.fromCharCode(bytes[i]);
      }
      
      // Use createHash API
      const hash = cryptoModule.createHash(hashName);
      hash.update(dataStr);
      const hashResult = hash.digest('hex');
      
      if (!hashResult) {
        throw new DOMException('Hash operation failed', 'OperationError');
      }

      // Convert hex result to ArrayBuffer
      const hexBytes = hashResult.match(/.{2}/g) || [];
      const result = new Uint8Array(hexBytes.length);
      for (let i = 0; i < hexBytes.length; i++) {
        result[i] = parseInt(hexBytes[i], 16);
      }
      
      return result.buffer;
    }

    /**
     * Generate a cryptographic key or key pair.
     */
    async generateKey(algorithm, extractable, keyUsages) {
      const algoName = typeof algorithm === 'string' ? algorithm : algorithm.name;
      
      switch (algoName.toUpperCase()) {
        case 'AES-GCM':
        case 'AES-CBC':
        case 'AES-CTR': {
          const length = algorithm.length || 256;
          const keyData = new Uint8Array(length / 8);
          crypto.getRandomValues(keyData);
          
          const key = new CryptoKey('secret', extractable, { name: algoName, length }, keyUsages);
          key._keyData = keyData;
          return key;
        }
        
        case 'HMAC': {
          const hashName = algorithm.hash ? (typeof algorithm.hash === 'string' ? algorithm.hash : algorithm.hash.name) : 'SHA-256';
          const length = algorithm.length || 256;
          const keyData = new Uint8Array(length / 8);
          crypto.getRandomValues(keyData);
          
          const key = new CryptoKey('secret', extractable, { name: 'HMAC', hash: { name: hashName }, length }, keyUsages);
          key._keyData = keyData;
          return key;
        }
        
        case 'RSA-OAEP':
        case 'RSASSA-PKCS1-V1_5':
        case 'RSA-PSS': {
          // RSA key generation is complex - return a placeholder
          throw new DOMException('RSA key generation not yet supported', 'NotSupportedError');
        }
        
        case 'ECDSA':
        case 'ECDH': {
          throw new DOMException('EC key generation not yet supported', 'NotSupportedError');
        }
        
        default:
          throw new DOMException(`Unsupported algorithm: ${algoName}`, 'NotSupportedError');
      }
    }

    /**
     * Import a key from external data.
     */
    async importKey(format, keyData, algorithm, extractable, keyUsages) {
      const algoName = typeof algorithm === 'string' ? algorithm : algorithm.name;
      
      let rawKeyData;
      if (format === 'raw') {
        if (keyData instanceof ArrayBuffer) {
          rawKeyData = new Uint8Array(keyData);
        } else if (ArrayBuffer.isView(keyData)) {
          rawKeyData = new Uint8Array(keyData.buffer, keyData.byteOffset, keyData.byteLength);
        } else {
          throw new TypeError('Key data must be BufferSource for raw format');
        }
      } else if (format === 'jwk') {
        // JWK format
        if (keyData.k) {
          // Symmetric key
          const base64 = keyData.k.replace(/-/g, '+').replace(/_/g, '/');
          const binary = atob(base64);
          rawKeyData = new Uint8Array(binary.length);
          for (let i = 0; i < binary.length; i++) {
            rawKeyData[i] = binary.charCodeAt(i);
          }
        } else {
          throw new DOMException('Unsupported JWK format', 'NotSupportedError');
        }
      } else {
        throw new DOMException(`Unsupported format: ${format}`, 'NotSupportedError');
      }

      const key = new CryptoKey('secret', extractable, algorithm, keyUsages);
      key._keyData = rawKeyData;
      return key;
    }

    /**
     * Export a key to external data.
     */
    async exportKey(format, key) {
      if (!key.extractable) {
        throw new DOMException('Key is not extractable', 'InvalidAccessError');
      }

      if (format === 'raw') {
        return key._keyData.buffer.slice(0);
      } else if (format === 'jwk') {
        // Export as JWK
        let binary = '';
        for (let i = 0; i < key._keyData.length; i++) {
          binary += String.fromCharCode(key._keyData[i]);
        }
        const base64 = btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
        
        return {
          kty: 'oct',
          k: base64,
          alg: key.algorithm.name,
          ext: key.extractable,
          key_ops: key.usages
        };
      } else {
        throw new DOMException(`Unsupported format: ${format}`, 'NotSupportedError');
      }
    }

    /**
     * Encrypt data using a key.
     */
    async encrypt(algorithm, key, data) {
      // Basic XOR encryption for demo (real implementation would use proper algorithms)
      throw new DOMException('Encryption not yet fully supported', 'NotSupportedError');
    }

    /**
     * Decrypt data using a key.
     */
    async decrypt(algorithm, key, data) {
      throw new DOMException('Decryption not yet fully supported', 'NotSupportedError');
    }

    /**
     * Sign data using a key.
     */
    async sign(algorithm, key, data) {
      const algoName = typeof algorithm === 'string' ? algorithm : algorithm.name;
      
      if (algoName.toUpperCase() === 'HMAC') {
        // HMAC signing using existing crypto module
        let bytes;
        if (data instanceof ArrayBuffer) {
          bytes = new Uint8Array(data);
        } else if (ArrayBuffer.isView(data)) {
          bytes = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
        } else {
          throw new TypeError('Data must be BufferSource');
        }

        const hashAlgo = key.algorithm.hash.name.toLowerCase().replace('-', '');
        
        // Convert bytes to string
        let dataStr = '';
        for (let i = 0; i < bytes.length; i++) {
          dataStr += String.fromCharCode(bytes[i]);
        }
        
        let keyStr = '';
        for (let i = 0; i < key._keyData.length; i++) {
          keyStr += String.fromCharCode(key._keyData[i]);
        }
        
        // Use createHmac API
        const hmac = cryptoModule.createHmac(hashAlgo, keyStr);
        hmac.update(dataStr);
        const hmacResult = hmac.digest('hex');
        
        if (!hmacResult) {
          throw new DOMException('HMAC operation failed', 'OperationError');
        }

        // Convert hex to ArrayBuffer
        const hexBytes = hmacResult.match(/.{2}/g) || [];
        const result = new Uint8Array(hexBytes.length);
        for (let i = 0; i < hexBytes.length; i++) {
          result[i] = parseInt(hexBytes[i], 16);
        }
        
        return result.buffer;
      }
      
      throw new DOMException(`Unsupported algorithm: ${algoName}`, 'NotSupportedError');
    }

    /**
     * Verify a signature.
     */
    async verify(algorithm, key, signature, data) {
      const algoName = typeof algorithm === 'string' ? algorithm : algorithm.name;
      
      if (algoName.toUpperCase() === 'HMAC') {
        const expectedSig = await this.sign(algorithm, key, data);
        const expectedBytes = new Uint8Array(expectedSig);
        
        let sigBytes;
        if (signature instanceof ArrayBuffer) {
          sigBytes = new Uint8Array(signature);
        } else if (ArrayBuffer.isView(signature)) {
          sigBytes = new Uint8Array(signature.buffer, signature.byteOffset, signature.byteLength);
        } else {
          throw new TypeError('Signature must be BufferSource');
        }

        if (expectedBytes.length !== sigBytes.length) {
          return false;
        }
        
        for (let i = 0; i < expectedBytes.length; i++) {
          if (expectedBytes[i] !== sigBytes[i]) {
            return false;
          }
        }
        
        return true;
      }
      
      throw new DOMException(`Unsupported algorithm: ${algoName}`, 'NotSupportedError');
    }

    /**
     * Derive bits from a key.
     */
    async deriveBits(algorithm, baseKey, length) {
      throw new DOMException('deriveBits not yet supported', 'NotSupportedError');
    }

    /**
     * Derive a key from another key.
     */
    async deriveKey(algorithm, baseKey, derivedKeyAlgorithm, extractable, keyUsages) {
      throw new DOMException('deriveKey not yet supported', 'NotSupportedError');
    }

    /**
     * Wrap a key for export.
     */
    async wrapKey(format, key, wrappingKey, wrapAlgorithm) {
      throw new DOMException('wrapKey not yet supported', 'NotSupportedError');
    }

    /**
     * Unwrap an imported key.
     */
    async unwrapKey(format, wrappedKey, unwrappingKey, unwrapAlgorithm, unwrappedKeyAlgorithm, extractable, keyUsages) {
      throw new DOMException('unwrapKey not yet supported', 'NotSupportedError');
    }

    get [Symbol.toStringTag]() {
      return 'SubtleCrypto';
    }
  }

  /**
   * Crypto interface (Web Crypto API).
   */
  const webcrypto = {
    subtle: new SubtleCrypto(),
    
    /**
     * Generate cryptographically secure random values.
     */
    getRandomValues: function(typedArray) {
      if (!(typedArray instanceof Int8Array ||
            typedArray instanceof Uint8Array ||
            typedArray instanceof Uint8ClampedArray ||
            typedArray instanceof Int16Array ||
            typedArray instanceof Uint16Array ||
            typedArray instanceof Int32Array ||
            typedArray instanceof Uint32Array ||
            typedArray instanceof BigInt64Array ||
            typedArray instanceof BigUint64Array)) {
        throw new TypeError('Expected TypedArray');
      }

      if (typedArray.byteLength > 65536) {
        throw new DOMException('Quota exceeded', 'QuotaExceededError');
      }

      // Use crypto module randomBytes function
      const buffer = cryptoModule.randomBytes(typedArray.byteLength);
      if (!buffer) {
        throw new DOMException('Random generation failed', 'OperationError');
      }

      // Copy buffer data to typedArray
      const view = new Uint8Array(typedArray.buffer, typedArray.byteOffset, typedArray.byteLength);
      for (let i = 0; i < buffer.length && i < view.length; i++) {
        view[i] = buffer[i];
      }

      return typedArray;
    },

    /**
     * Generate a random UUID.
     */
    randomUUID: function() {
      const bytes = new Uint8Array(16);
      this.getRandomValues(bytes);
      
      // Set version (4) and variant (RFC4122)
      bytes[6] = (bytes[6] & 0x0f) | 0x40;
      bytes[8] = (bytes[8] & 0x3f) | 0x80;
      
      const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
      return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
    },

    // Classes
    CryptoKey,
    SubtleCrypto
  };

  // Export
  global.__webcrypto_module = webcrypto;
  
  // Also set as global crypto if not already set
  if (typeof global.crypto === 'undefined') {
    global.crypto = webcrypto;
  } else {
    // Merge with existing crypto
    global.crypto.subtle = webcrypto.subtle;
    global.crypto.getRandomValues = webcrypto.getRandomValues;
    global.crypto.randomUUID = webcrypto.randomUUID;
  }

})(globalThis);
