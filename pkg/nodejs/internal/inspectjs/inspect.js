// The parts of Node's util.inspect that the console and util modules both need: quoting strings
// and object keys, and printing Buffers and typed arrays. They share one copy, and each loads it
// itself, since an embedder can register either one without the other. Evaluates to
// { quote, key, view }.
(function () {
  // META[c] is how util.inspect writes control character c (0x00-0x1F) inside a quoted string:
  // \b, \t, \n, \f and \r for those five, and \xNN for the rest.
  const META = [
    '\\x00', '\\x01', '\\x02', '\\x03', '\\x04', '\\x05', '\\x06', '\\x07',
    '\\b', '\\t', '\\n', '\\x0B', '\\f', '\\r', '\\x0E', '\\x0F',
    '\\x10', '\\x11', '\\x12', '\\x13', '\\x14', '\\x15', '\\x16', '\\x17',
    '\\x18', '\\x19', '\\x1A', '\\x1B', '\\x1C', '\\x1D', '\\x1E', '\\x1F'
  ];

  // Quotes a string: single quotes unless the string holds one, control characters, DEL,
  // backslashes and lone surrogates escaped.
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
      // 0x7F is DEL.
      else if (code === 0x7f) out += '\\x7F';
      // 0x5C is a backslash.
      else if (code === 0x5c) out += '\\\\';
      else if (str[i] === q) out += '\\' + q;
      // 0xD800-0xDFFF are UTF-16 surrogates: a high one (to 0xDBFF) followed by a low one
      // (0xDC00-0xDFFF) is a pair and prints as is, and any other is escaped.
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

  // A key prints bare only when it is an identifier.
  const key = (k) => (/^[a-zA-Z_][a-zA-Z_0-9]*$/.test(k) ? k : quote(k));

  // One element of a typed array: a BigInt keeps its n suffix, and negative zero prints as -0.
  const element = (v) => (typeof v === 'bigint' ? v + 'n' : Object.is(v, -0) ? '-0' : String(v));

  // view prints a Buffer as <Buffer 01 02> and any other typed array as Name(length) [ ... ],
  // showing at most 50 bytes or maxArrayLength elements. It answers null for anything else,
  // including a DataView.
  function view(value, maxArrayLength = 100) {
    if (!ArrayBuffer.isView(value) || value instanceof DataView) return null;
    if (typeof Buffer === 'function' && Buffer.isBuffer(value)) {
      const hex = [];
      for (let i = 0; i < Math.min(value.length, 50); i++) hex.push(value[i].toString(16).padStart(2, '0'));
      const extra = value.length - 50;
      return '<Buffer ' + hex.join(' ') + (extra > 0 ? ` ... ${extra} more byte${extra > 1 ? 's' : ''}` : '') + '>';
    }
    const name = value.constructor.name;
    if (value.length === 0) return `${name}(0) []`;
    const items = Array.from(value.subarray(0, maxArrayLength), element);
    const extra = value.length - maxArrayLength;
    return `${name}(${value.length}) [ ${items.join(', ')}${extra > 0 ? `, ... ${extra} more item${extra > 1 ? 's' : ''}` : ''} ]`;
  }

  return { quote, key, view };
})()
