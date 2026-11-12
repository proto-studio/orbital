// The parts of Node's util.inspect that console.log needs from JavaScript: quoting strings and
// keys, and printing Buffers and typed arrays. Keys are read here too, because a key holding a
// NUL can't cross into Go as a string. Evaluates to an object of functions.
(function () {
  const META = [
    '\\x00', '\\x01', '\\x02', '\\x03', '\\x04', '\\x05', '\\x06', '\\x07',
    '\\b', '\\t', '\\n', '\\x0B', '\\f', '\\r', '\\x0E', '\\x0F',
    '\\x10', '\\x11', '\\x12', '\\x13', '\\x14', '\\x15', '\\x16', '\\x17',
    '\\x18', '\\x19', '\\x1A', '\\x1B', '\\x1C', '\\x1D', '\\x1E', '\\x1F'
  ];
  function quote(str) {
    let q = "'";
    if (str.includes("'")) {
      if (!str.includes('"')) q = '"';
      else if (!str.includes('`') && !str.includes('${')) q = '`';
    }
    let out = '';
    for (let i = 0; i < str.length; i++) {
      const code = str.charCodeAt(i);
      if (code < 0x20) out += META[code];
      else if (code === 0x7f) out += '\\x7F';
      else if (code === 0x5c) out += '\\\\';
      else if (str[i] === q) out += '\\' + q;
      else if (code >= 0xd800 && code <= 0xdfff) {
        const next = str.charCodeAt(i + 1);
        if (code <= 0xdbff && next >= 0xdc00 && next <= 0xdfff) {
          out += str[i] + str[i + 1];
          i++;
        } else {
          out += '\\u' + code.toString(16);
        }
      } else out += str[i];
    }
    return q + out + q;
  }
  function view(value) {
    if (!ArrayBuffer.isView(value) || value instanceof DataView) return null;
    if (typeof Buffer === 'function' && Buffer.isBuffer(value)) {
      const hex = [];
      for (let i = 0; i < Math.min(value.length, 50); i++) hex.push(value[i].toString(16).padStart(2, '0'));
      const extra = value.length - 50;
      return '<Buffer ' + hex.join(' ') + (extra > 0 ? ` ... ${extra} more byte${extra > 1 ? 's' : ''}` : '') + '>';
    }
    const name = value.constructor.name;
    if (value.length === 0) return `${name}(0) []`;
    const items = Array.from(value.subarray(0, 100), String);
    const extra = value.length - 100;
    return `${name}(${value.length}) [ ${items.join(', ')}${extra > 0 ? `, ... ${extra} more item${extra > 1 ? 's' : ''}` : ''} ]`;
  }
  return {
    quote,
    key: (k) => (/^[a-zA-Z_][a-zA-Z_0-9]*$/.test(k) ? k : quote(k)),
    get: (obj, k) => obj[k],
    view
  };
})()
