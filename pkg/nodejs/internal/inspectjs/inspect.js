// The parts of Node's util.inspect that the console and util modules both need: quoting strings
// and object keys, printing Buffers, typed arrays, ArrayBuffers and DataViews, and laying a
// value's entries out on lines. They share one copy, and each loads it itself, since an embedder
// can register either one without the other. Evaluates to
// { context, quote, string, key, more, view, binary, field, cut, reduce }.
//
// Layout follows a context from context(options), which, like Node's, holds the options and
// currentDepth: the level of the object whose entries were formatted last. A value's level is
// how many objects it sits inside, so a top-level value is at 0. Before formatting the entries
// of an object, array, Map, Set or Error, the caller sets currentDepth to its level; reduce
// then knows how many levels of objects the value holds. Lines that a value spans are indented
// relative to the value itself: reduce indents an entry's lines along with the entry.
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

  // context makes a layout context from util.inspect options. A null maxArrayLength or
  // maxStringLength means no limit.
  function context({ breakLength = 80, compact = 3, maxArrayLength = 100, maxStringLength = 10000 } = {}) {
    return {
      breakLength,
      compact,
      maxArrayLength: maxArrayLength === null ? Infinity : maxArrayLength,
      maxStringLength: maxStringLength === null ? Infinity : maxStringLength,
      currentDepth: 0
    };
  }

  // string quotes a string at level as util.inspect does: cut to maxStringLength, and split
  // after each newline into quoted pieces joined with + when it's too long for its line.
  function string(ctx, str, level = 0) {
    let trailer = '';
    if (str.length > ctx.maxStringLength) {
      const remaining = str.length - ctx.maxStringLength;
      str = str.slice(0, ctx.maxStringLength);
      trailer = `... ${remaining} more character${remaining > 1 ? 's' : ''}`;
    }
    // Node leaves strings of 16 characters or fewer whole, and allows 4 columns for the
    // quotes, comma and space.
    if (ctx.compact !== true && str.length > 16 && str.length > ctx.breakLength - level * 2 - 4) {
      return str.split(/(?<=\n)/).map(quote).join(' +\n  ') + trailer;
    }
    return quote(str) + trailer;
  }

  // A key prints bare only when it is an identifier.
  const key = (k) => (/^[a-zA-Z_][a-zA-Z_0-9]*$/.test(k) ? k : quote(k));

  // One element of a typed array: a BigInt keeps its n suffix, and negative zero prints as -0.
  const element = (v) => (typeof v === 'bigint' ? v + 'n' : Object.is(v, -0) ? '-0' : String(v));

  // more is the entry that stands for the items an array or collection leaves out.
  const more = (n) => `... ${n} more item${n > 1 ? 's' : ''}`;

  // view prints a Buffer as <Buffer 01 02> and any other typed array at level as
  // Name(length) [ ... ], showing at most 50 bytes or maxArrayLength elements. It answers null
  // for anything else, including a DataView.
  function view(value, ctx = context(), level = 0) {
    if (!ArrayBuffer.isView(value) || value instanceof DataView) return null;
    if (typeof Buffer === 'function' && Buffer.isBuffer(value)) {
      const hex = [];
      for (let i = 0; i < Math.min(value.length, 50); i++) hex.push(value[i].toString(16).padStart(2, '0'));
      const extra = value.length - 50;
      return '<Buffer ' + hex.join(' ') + (extra > 0 ? ` ... ${extra} more byte${extra > 1 ? 's' : ''}` : '') + '>';
    }
    const name = value.constructor.name;
    if (value.length === 0) return `${name}(0) []`;
    ctx.currentDepth = level;
    const shown = Math.min(Math.max(0, ctx.maxArrayLength), value.length);
    const output = Array.from(value.subarray(0, shown), element);
    if (value.length > shown) output.push(more(value.length - shown));
    return reduce(ctx, output, `${name}(${value.length}) [`, ']', level, { array: true, numbers: true });
  }

  // Brand checks: each getter throws for anything but its own kind of object, so they only run
  // on objects that may be one.
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
  // %TypedArray%.prototype[Symbol.toStringTag] answers undefined, without throwing, for anything
  // but a typed array; ArrayBuffer.isView covers typed arrays and DataViews.
  const typedArrayTag = getter(Object.getPrototypeOf(Uint8Array.prototype), Symbol.toStringTag);
  const isDataView = (value) => ArrayBuffer.isView(value) && typedArrayTag.call(value) === undefined;
  // Whether value may be an ArrayBuffer or SharedArrayBuffer: it inherits from one, or from no
  // Object of this realm, like one with a null prototype or from another realm.
  // A proxy whose getPrototypeOf trap throws is none of them.
  const mayBeBuffer = (value) => {
    try {
      return value instanceof ArrayBuffer || !(value instanceof Object) ||
        (typeof SharedArrayBuffer === 'function' && value instanceof SharedArrayBuffer);
    } catch (e) {
      return false;
    }
  };

  // The name util.inspect puts before an object's brace: the constructor's name, the kind of
  // object in brackets when its toStringTag differs, or [Kind: null prototype] without one. A
  // toStringTag that throws is left out.
  function prefix(value, fallback) {
    let ctor = null;
    for (let obj = value; obj !== null && ctor === null; obj = Object.getPrototypeOf(obj)) {
      const desc = Object.getOwnPropertyDescriptor(obj, 'constructor');
      if (desc && typeof desc.value === 'function' && desc.value.name !== '') ctor = desc.value.name;
    }
    let tag;
    try {
      tag = value[Symbol.toStringTag];
    } catch (e) {
      tag = undefined;
    }
    if (ctor === null) return `[${fallback}: null prototype]`;
    if (typeof tag === 'string' && tag !== '' && tag !== ctor) return `${ctor} [${tag}]`;
    return ctor;
  }

  // binary describes an ArrayBuffer, SharedArrayBuffer or DataView the way util.inspect prints
  // one, as { name, entries, keys }: name goes before the brace (and in brackets in place of the
  // value past the depth limit), entries come first between the braces, and keys are properties
  // to print next, before the value's own keys. An ArrayBuffer's entries show at most
  // maxArrayLength bytes, or all of them when it's null. It answers null for anything else.
  function binary(value, maxArrayLength = 100) {
    if (value === null || typeof value !== 'object') return null;
    if (maxArrayLength === null) maxArrayLength = Infinity;
    if (isDataView(value)) {
      // .buffer goes last, as it's the one that isn't a number.
      return { name: prefix(value, 'DataView'), entries: [], keys: ['byteLength', 'byteOffset', 'buffer'] };
    }
    if (!mayBeBuffer(value)) return null;
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

  // cut names value in brackets, as util.inspect prints it past the depth limit, or answers null
  // when Node prints it in full anyway: when it's empty, a RegExp, a Date without properties or
  // a Buffer. Errors are left to the caller. A value that throws when looked at, like a proxy
  // with a throwing trap, is [Object].
  function cut(value) {
    try {
      return cutName(value);
    } catch (e) {
      return '[Object]';
    }
  }

  function cutName(value) {
    // Only the name is needed, so no bytes are read.
    const bin = binary(value, 0);
    if (bin !== null) return `[${bin.name}]`;
    if (Array.isArray(value)) return value.length === 0 ? null : '[Array]';
    if (value instanceof Map) return value.size === 0 ? null : '[Map]';
    if (value instanceof Set) return value.size === 0 ? null : '[Set]';
    if (value instanceof RegExp) return null;
    if (value instanceof Date) return Object.keys(value).length === 0 ? null : '[Date]';
    if (ArrayBuffer.isView(value)) {
      const buffer = typeof Buffer === 'function' && Buffer.isBuffer(value);
      return buffer || value.length === 0 ? null : `[${value.constructor.name}]`;
    }
    if (Object.keys(value).length === 0) return null;
    const proto = Object.getPrototypeOf(value);
    if (proto === null) return '[Object: null prototype]';
    const name = proto.constructor && proto.constructor.name;
    return `[${name || 'Object'}]`;
  }

  // field describes property k of value as util.inspect prints one of the keys binary lists:
  // { name, value } with the value to format, or { name, label } for an accessor, which isn't
  // called. An own property that isn't enumerable is named in brackets. A key value doesn't
  // own, like byteLength from its prototype, is read as usual.
  function field(value, k) {
    const desc = Object.getOwnPropertyDescriptor(value, k);
    if (desc === undefined) return { name: key(k), value: value[k] };
    const name = desc.enumerable ? key(k) : `[${k}]`;
    if ('value' in desc) return { name, value: desc.value };
    return { name, label: desc.get === undefined ? '[Setter]' : desc.set === undefined ? '[Getter]' : '[Getter/Setter]' };
  }

  // Node's colors are SGR escapes like \x1b[33m, which take no room on screen.
  const COLORS = /\u001b\[\d\d?m/g;
  const colorless = (s) => s.replace(COLORS, '');

  // Characters that take no columns: control characters, combining marks, zero-width spaces and
  // joiners, and variation selectors.
  const zeroWidth = (code) => code <= 0x1f || (code >= 0x7f && code <= 0x9f) ||
    (code >= 0x300 && code <= 0x36f) || (code >= 0x200b && code <= 0x200f) ||
    (code >= 0x20d0 && code <= 0x20ff) || (code >= 0xfe00 && code <= 0xfe0f) ||
    (code >= 0xfe20 && code <= 0xfe2f) || (code >= 0xe0100 && code <= 0xe01ef);
  // Characters that take two columns, from Unicode's East Asian Width data: Hangul, CJK, kana,
  // full-width forms and most emoji.
  const fullWidth = (code) => code >= 0x1100 && (code <= 0x115f || code === 0x2329 ||
    code === 0x232a || (code >= 0x2e80 && code <= 0x3247 && code !== 0x303f) ||
    (code >= 0x3250 && code <= 0x4dbf) || (code >= 0x4e00 && code <= 0xa4c6) ||
    (code >= 0xa960 && code <= 0xa97c) || (code >= 0xac00 && code <= 0xd7a3) ||
    (code >= 0xf900 && code <= 0xfaff) || (code >= 0xfe10 && code <= 0xfe19) ||
    (code >= 0xfe30 && code <= 0xfe6b) || (code >= 0xff01 && code <= 0xff60) ||
    (code >= 0xffe0 && code <= 0xffe6) || (code >= 0x1b000 && code <= 0x1b001) ||
    (code >= 0x1f200 && code <= 0x1f251) || (code >= 0x1f300 && code <= 0x1f64f) ||
    (code >= 0x20000 && code <= 0x3fffd));

  // width is how many columns s takes on screen.
  function width(s) {
    s = colorless(s);
    let n = 0;
    for (let i = 0; i < s.length; i++) {
      const code = s.charCodeAt(i);
      if (code < 0x7f) {
        n += code >= 0x20 ? 1 : 0;
        continue;
      }
      for (const char of s.slice(i).normalize('NFC')) {
        const point = char.codePointAt(0);
        n += fullWidth(point) ? 2 : zeroWidth(point) ? 0 : 1;
      }
      break;
    }
    return n;
  }

  // An entry takes a comma and a space after it.
  const SEPARATOR = 2;

  // group lays out the entries of an array at level in columns when they're short and similar in
  // length, as Node does for arrays with more than six entries. Numbers line up on the right
  // and anything else on the left. It answers the rows, or output unchanged when it doesn't
  // group them. numbers says whether the array's entries are all numbers or BigInts; callers
  // work it out from the values they formatted, so that getters aren't read twice.
  function group(ctx, output, level, numbers) {
    let totalLength = 0;
    let maxLength = 0;
    let outputLength = output.length;
    // The entry for items left out isn't grouped.
    if (ctx.maxArrayLength < output.length) outputLength--;
    const dataLen = new Array(outputLength);
    for (let i = 0; i < outputLength; i++) {
      const len = width(output[i]);
      dataLen[i] = len;
      totalLength += len + SEPARATOR;
      if (maxLength < len) maxLength = len;
    }
    const actualMax = maxLength + SEPARATOR;
    // Group only when three entries fit side by side, and none is longer than a fifth of the
    // rest together, which would leave wide gaps between the short ones.
    if (actualMax * 3 + level * 2 >= ctx.breakLength || (totalLength / actualMax <= 5 && maxLength > 6)) {
      return output;
    }
    // Node aims for a square, taking a character to be 2.5 times as tall as it is wide, with
    // more columns for short entries; within breakLength, compact * 4 and 15 columns.
    const averageBias = Math.sqrt(actualMax - totalLength / output.length);
    const biasedMax = Math.max(actualMax - 3 - averageBias, 1);
    const columns = Math.min(
      Math.round(Math.sqrt(2.5 * biasedMax * outputLength) / biasedMax),
      Math.floor((ctx.breakLength - level * 2) / actualMax),
      ctx.compact * 4,
      15
    );
    if (columns <= 1) return output;
    const maxLineLength = [];
    for (let i = 0; i < columns; i++) {
      let lineLength = 0;
      for (let j = i; j < output.length; j += columns) {
        if (dataLen[j] > lineLength) lineLength = dataLen[j];
      }
      maxLineLength.push(lineLength + SEPARATOR);
    }
    const tmp = [];
    for (let i = 0; i < outputLength; i += columns) {
      const max = Math.min(i + columns, outputLength);
      let str = '';
      let j = i;
      // Padding is counted in characters, so it adds what colors and wide characters take
      // beyond their columns.
      for (; j < max - 1; j++) {
        const padding = maxLineLength[j - i] + output[j].length - dataLen[j];
        str += numbers ? `${output[j]}, `.padStart(padding, ' ') : `${output[j]}, `.padEnd(padding, ' ');
      }
      if (numbers) {
        str += output[j].padStart(maxLineLength[j - i] + output[j].length - dataLen[j] - SEPARATOR, ' ');
      } else {
        str += output[j];
      }
      tmp.push(str);
    }
    if (ctx.maxArrayLength < output.length) tmp.push(output[outputLength]);
    return tmp;
  }

  // fits reports whether output fits on one line after start columns, with each entry's comma
  // and space, and its commas counted once more up front, as Node counts. It doesn't fit after a
  // base that spans lines.
  function fits(ctx, output, start, base) {
    let total = output.length + start;
    if (total + output.length > ctx.breakLength) return false;
    for (let i = 0; i < output.length; i++) {
      total += colorless(output[i]).length;
      if (total > ctx.breakLength) return false;
    }
    return base === '' || !base.includes('\n');
  }

  // Indents the lines an entry spans along with it.
  const indent = (entry) => entry.replace(/\n/g, '\n  ');

  // reduce joins the entries of a value at level between open and close as util.inspect does.
  // base, like an Error's stack, goes before open. Arrays' entries are grouped in columns, with
  // numbers (all numbers or BigInts) aligned right. The
  // entries go on one line when none spans lines, the value holds fewer than compact levels of
  // objects, and the line fits in breakLength. Otherwise each goes on its own line, indented
  // two spaces.
  function reduce(ctx, output, open, close, level, { base = '', array = false, numbers = false } = {}) {
    const before = base ? `${base} ` : '';
    if (ctx.compact !== true) {
      if (typeof ctx.compact === 'number' && ctx.compact >= 1) {
        const entries = output.length;
        if (array && entries > 6) output = group(ctx, output, level, numbers);
        // Grouped rows stay on lines of their own. Node allows 10 more columns.
        if (ctx.currentDepth - level < ctx.compact && entries === output.length &&
            fits(ctx, output, output.length + level * 2 + open.length + base.length + 10, base)) {
          const joined = output.join(', ');
          if (!joined.includes('\n')) return `${before}${open} ${joined} ${close}`;
        }
      }
      return `${before}${open}\n  ${output.map(indent).join(',\n  ')}\n${close}`;
    }
    // compact: true puts entries on one line when they fit, and otherwise wraps them after the
    // brace, the closing brace following the last.
    if (fits(ctx, output, 0, base)) return `${open}${base ? ` ${base}` : ''} ${output.join(', ')} ${close}`;
    const ln = base === '' && open.length === 1 ? ' ' : `${base ? ` ${base}` : ''}\n  `;
    return `${open}${ln}${output.map(indent).join(',\n  ')} ${close}`;
  }

  return { context, quote, string, key, more, view, binary, field, cut, reduce };
})()
