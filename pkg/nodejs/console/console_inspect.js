// The parts of Node's util.inspect that console.log needs from JavaScript: quoting strings and
// keys, and printing Buffers and typed arrays. Keys are read here too, because a key holding a
// NUL can't cross into Go as a string. Quoting comes from inspectjs, which console.go binds to
// inspectQuote. Evaluates to an object of functions.
(function () {
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
    quote: inspectQuote.quote,
    key: inspectQuote.key,
    get: (obj, k) => obj[k],
    view
  };
})()
