// The parts of Node's util.inspect that the console and util modules both need: quoting strings
// and object keys, printing Buffers, typed arrays, ArrayBuffers and DataViews, and fitting a
// value's entries onto lines. They share one copy, and each loads it itself, since an embedder
// can register either one without the other. Evaluates to { quote, key, view, binary, reduce }.
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

  // Brand checks: each getter throws for anything but its own kind of object.
  const getter = (proto, name) => Object.getOwnPropertyDescriptor(proto, name).get;
  const brand = (get) => (value) => {
    try {
      get.call(value);
      return true;
    } catch (e) {
      return false;
    }
  };
  const isArrayBuffer = brand(getter(ArrayBuffer.prototype, 'byteLength'));
  const isSharedArrayBuffer = typeof SharedArrayBuffer === 'function'
    ? brand(getter(SharedArrayBuffer.prototype, 'byteLength'))
    : () => false;
  const isDataView = brand(getter(DataView.prototype, 'byteLength'));

  // The name util.inspect puts before an object's brace: the constructor's name, the kind of
  // object in brackets when its toStringTag differs, or [Kind: null prototype] without one.
  function prefix(value, fallback) {
    let ctor = null;
    for (let obj = value; obj !== null && ctor === null; obj = Object.getPrototypeOf(obj)) {
      const desc = Object.getOwnPropertyDescriptor(obj, 'constructor');
      if (desc && typeof desc.value === 'function' && desc.value.name !== '') ctor = desc.value.name;
    }
    const tag = value[Symbol.toStringTag];
    if (ctor === null) return `[${fallback}: null prototype]`;
    if (typeof tag === 'string' && tag !== '' && tag !== ctor) return `${ctor} [${tag}]`;
    return ctor;
  }

  // binary describes an ArrayBuffer, SharedArrayBuffer or DataView the way util.inspect prints
  // one, as { name, entries, keys }: name goes before the brace (and in brackets in place of the
  // value past the depth limit), entries come first between the braces, and keys are properties
  // to print next, before the value's own keys. An ArrayBuffer's entries show at most
  // maxArrayLength bytes. It answers null for anything else.
  function binary(value, maxArrayLength = 100) {
    if (value === null || typeof value !== 'object') return null;
    if (isDataView(value)) {
      // .buffer goes last, as it's the one that isn't a number.
      return { name: prefix(value, 'DataView'), entries: [], keys: ['byteLength', 'byteOffset', 'buffer'] };
    }
    const shared = !isArrayBuffer(value);
    if (shared && !isSharedArrayBuffer(value)) return null;
    let bytes;
    try {
      bytes = new Uint8Array(value);
    } catch (e) {
      // Only a detached ArrayBuffer can't be viewed.
      bytes = null;
    }
    let contents = '(detached)';
    if (bytes !== null) {
      const hex = [];
      for (let i = 0; i < Math.min(maxArrayLength, bytes.length); i++) hex.push(bytes[i].toString(16).padStart(2, '0'));
      const extra = bytes.length - maxArrayLength;
      contents = '[Uint8Contents]: <' + hex.join(' ') + (extra > 0 ? ` ... ${extra} more byte${extra > 1 ? 's' : ''}` : '') + '>';
    }
    return {
      name: prefix(value, shared ? 'SharedArrayBuffer' : 'ArrayBuffer'),
      entries: [contents],
      keys: ['byteLength']
    };
  }

  // Node's colors are SGR escapes like \x1b[33m, which take no room on screen.
  const COLORS = /\u001b\[\d\d?m/g;
  const width = (s) => s.replace(COLORS, '').length;

  // reduce joins a value's entries between its braces as util.inspect does. They go on one line
  // when none spans lines, the value holds fewer than compact levels of objects (nested), and the
  // line, started level objects deep, fits in breakLength. Otherwise each entry goes on its own
  // line, indented two spaces, along with any lines it spans.
  function reduce(output, open, close, { level = 0, nested = 0, breakLength = 80, compact = 3 } = {}) {
    if (typeof compact === 'number' && compact >= 1 && nested < compact) {
      // Each level indents two columns. Node allows 10 more characters, and counts each
      // entry's comma and space, and the commas once more before it adds up the entries.
      let total = output.length * 2 + level * 2 + open.length + 10;
      let fits = total + output.length <= breakLength;
      for (let i = 0; fits && i < output.length; i++) {
        total += width(output[i]);
        fits = total <= breakLength && !output[i].includes('\n');
      }
      if (fits) return `${open} ${output.join(', ')} ${close}`;
    }
    return `${open}\n  ${output.map((entry) => entry.replace(/\n/g, '\n  ')).join(',\n  ')}\n${close}`;
  }

  return { quote, key, view, binary, reduce };
})()
