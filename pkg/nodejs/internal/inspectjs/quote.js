// Quoting strings and object keys the way Node's util.inspect does. The console and util modules
// share it, since an embedder can register either one without the other. Evaluates to
// { quote, key }.
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

  return { quote, key };
})()
