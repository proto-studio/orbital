// The parts of Node's util.inspect that the console and util modules both need: quoting strings
// and object keys, printing Buffers, typed arrays, ArrayBuffers, DataViews, Maps, Sets, Dates
// and RegExps, and laying a value's entries out on lines. They share one copy, and each loads it itself, since an embedder
// can register either one without the other. Evaluates to a function that takes the native
// helpers inspectjs.go makes, ownNonIndexKeys, and returns { context, quote, string, key,
// keys, extraKeys, numbersAt, primitive, numeric, stringSpec, more, view, snapshot, finish, binary,
// collection, scalar, isDate, field, cut, entry, tracker, reduce, objectPrefix, arrayPrefix }.
//
// Layout follows a context from context(options), which, like Node's, holds the options and
// currentDepth: the level of the object whose entries were formatted last. A value's level is
// how many objects it sits inside, so a top-level value is at 0. Before formatting the entries
// of an object, array, Map, Set or Error, the caller sets currentDepth to its level; reduce
// then knows how many levels of objects the value holds. Lines that a value spans are indented
// relative to the value itself: reduce indents an entry's lines along with the entry.
(function (ownNonIndexKeys) {
  // META[c] is how util.inspect writes control character c (0x00-0x1F) inside a quoted string:
  // \b, \t, \n, \f and \r for those five, and \xNN for the rest.
  const META = [
    '\\x00', '\\x01', '\\x02', '\\x03', '\\x04', '\\x05', '\\x06', '\\x07',
    '\\b', '\\t', '\\n', '\\x0B', '\\f', '\\r', '\\x0E', '\\x0F',
    '\\x10', '\\x11', '\\x12', '\\x13', '\\x14', '\\x15', '\\x16', '\\x17',
    '\\x18', '\\x19', '\\x1A', '\\x1B', '\\x1C', '\\x1D', '\\x1E', '\\x1F'
  ];

  // escape is how util.inspect writes a control character (0x00-0x1F, or DEL and the C1 controls
  // 0x7F-0x9F), a backslash or a single quote where it escapes them.
  function escape(char) {
    const code = char.charCodeAt(0);
    if (code < 0x20) return META[code];
    if (code === 0x5c || code === 0x27) return '\\' + char;
    return '\\x' + code.toString(16).toUpperCase();
  }
  const ESCAPED = /[\x00-\x1f\x27\x5c\x7f-\x9f]/g;

  // Quotes a string: single quotes unless the string holds one, control characters, DEL, C1
  // controls, backslashes and lone surrogates escaped.
  function quote(str) {
    let q = "'";
    if (str.includes("'")) {
      if (!str.includes('"')) q = '"';
      else if (!str.includes('`') && !str.includes('${')) q = '`';
    }
    let out = '';
    for (let i = 0; i < str.length; i++) {
      const code = str.charCodeAt(i);
      // 0x5C is a backslash.
      if (code < 0x20 || (code >= 0x7f && code <= 0x9f) || code === 0x5c) out += escape(str[i]);
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
  function context({ breakLength = 80, compact = 3, depth = 2, maxArrayLength = 100, maxStringLength = 10000 } = {}) {
    return {
      breakLength,
      compact,
      // How many levels of objects print in full, which also bounds how far prototype chains
      // without a constructor are described (see constructorName).
      depth: depth === null ? Infinity : depth,
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

  // A key prints bare only when it is an identifier. A symbol key prints in brackets as
  // [Symbol(description)], with its control characters, backslashes and quotes escaped.
  const key = (k) => {
    if (typeof k === 'symbol') return `[${Symbol.prototype.toString.call(k).replace(ESCAPED, escape)}]`;
    return /^[a-zA-Z_][a-zA-Z_0-9]*$/.test(k) ? k : quote(k);
  };

  // symbols lists value's own enumerable symbol keys, which util.inspect prints after its other
  // keys, as an array does after its elements.
  function symbols(value) {
    const all = Object.getOwnPropertySymbols(value);
    return all.length === 0 ? all : all.filter((s) => Object.prototype.propertyIsEnumerable.call(value, s));
  }

  // keys lists the keys util.inspect prints for value: its own enumerable string keys, then its
  // symbols. For arrays and typed arrays, whose elements are keys too, see extraKeys.
  function keys(value) {
    const names = Object.keys(value);
    const syms = symbols(value);
    return syms.length === 0 ? names : names.concat(syms);
  }

  // extraKeys lists the keys util.inspect prints after an array's or typed array's elements:
  // its own enumerable keys other than indices, strings then symbols. ownNonIndexKeys skips the
  // indices without walking them, as Object.keys would.
  function extraKeys(value) {
    // The native helper answers nothing when listing throws, like a proxy's trap can.
    const found = ownNonIndexKeys(value);
    return Array.isArray(found) ? found : [];
  }

  // numbersAt reports whether value's elements from start up to end are all numbers or
  // BigInts. When Node lays an array out in columns, it aligns them on the right when the
  // elements at the positions of all its entries are, counting the entries for left-out items
  // and keys; see reduce.
  function numbersAt(value, start, end) {
    for (let i = start; i < end; i++) {
      if (typeof value[i] !== 'number' && typeof value[i] !== 'bigint') return false;
    }
    return true;
  }

  // A primitive other than a string, as util.inspect prints one: a BigInt keeps its n suffix,
  // and negative zero prints as -0.
  const primitive = (v) => (typeof v === 'bigint' ? v + 'n' : Object.is(v, -0) ? '-0' : String(v));

  // numeric is how util.format prints value for %d, %i or %f (spec): Number(value), parseInt or
  // parseFloat of it, printed as primitive prints a number. %d and %i print a BigInt as it is,
  // and all three print a symbol, which none of those take, as NaN.
  function numeric(spec, value) {
    if (typeof value === 'symbol') return 'NaN';
    if (typeof value === 'bigint' && spec !== 'f') return primitive(value);
    return primitive(spec === 'd' ? Number(value) : spec === 'i' ? parseInt(value) : parseFloat(value));
  }

  // builtIns names the constructors of the global object, like Node's builtInObjects: global
  // names that start with a capital. Read on first use, once every module has added its globals.
  let builtIns = null;

  // hasBuiltInToString reports whether value, an object, converts to a string as its kind always
  // does, rather than through its own toString or Symbol.toPrimitive: Node's hasBuiltInToString.
  // A value that throws while it's looked at, like a proxy with a throwing trap, counts as
  // built in.
  function hasBuiltInToString(value) {
    try {
      if (typeof value[Symbol.toPrimitive] === 'function') return false;
      if (typeof value.toString !== 'function') return true;
      if (Object.prototype.hasOwnProperty.call(value, 'toString')) return false;
      let pointer = value;
      do {
        pointer = Object.getPrototypeOf(pointer);
      } while (!Object.prototype.hasOwnProperty.call(pointer, 'toString'));
      const desc = Object.getOwnPropertyDescriptor(pointer, 'constructor');
      if (builtIns === null) {
        builtIns = new Set(Object.getOwnPropertyNames(globalThis).filter((name) => /^[A-Z][a-zA-Z0-9]+$/.test(name)));
      }
      return desc !== undefined && typeof desc.value === 'function' && builtIns.has(desc.value.name);
    } catch (e) {
      return true;
    }
  }

  // stringSpec is how util.format prints value for %s: a number or BigInt as primitive prints
  // it, with -0 and a BigInt's n, and anything but an object String(value), as is an object with
  // its own way of becoming a string. It answers null for any other object, which %s inspects
  // with a depth of 0.
  function stringSpec(value) {
    if (typeof value === 'number' || typeof value === 'bigint') return primitive(value);
    if (typeof value !== 'object' || value === null || !hasBuiltInToString(value)) return String(value);
    return null;
  }

  // more is the entry that stands for the items an array or collection leaves out.
  const more = (n) => `... ${n} more item${n > 1 ? 's' : ''}`;

  // view prints a Buffer as <Buffer 01 02> and any other typed array at level as
  // Name(length) [ ... ], showing at most 50 bytes or maxArrayLength elements, then fields, the
  // entries the caller formatted for its extraKeys, with ref, the value's <ref *N> when one of
  // them holds it, first. It answers null for anything else, including a DataView. fields and
  // ref may be functions, called once the elements are read: formatting a field may change the
  // elements, or detach them, and Node shows them as they were. Callers that can't pass
  // functions use its two halves, snapshot and finish.
  function view(value, ctx = context(), level = 0, fields = [], ref = '') {
    const shot = snapshot(value, ctx, level);
    if (shot === null) return null;
    const list = typeof fields === 'function' ? fields() : fields;
    return finish(shot, ctx, level, list, typeof ref === 'function' ? ref() : ref);
  }

  // snapshot reads what view shows of a typed array's or Buffer's elements, or answers null for
  // anything else.
  function snapshot(value, ctx = context(), level = 0) {
    if (!ArrayBuffer.isView(value) || value instanceof DataView) return null;
    // Read through %TypedArray%.prototype, which a typed array without a prototype lacks.
    const length = typedArrayLength.call(value);
    if (typeof Buffer === 'function' && Buffer.isBuffer(value)) {
      const hex = [];
      for (let i = 0; i < Math.min(length, 50); i++) hex.push(value[i].toString(16).padStart(2, '0'));
      const extra = length - 50;
      return { buffer: true, length, text: hex.join(' ') + (extra > 0 ? ` ... ${extra} more byte${extra > 1 ? 's' : ''}` : '') };
    }
    // An empty one formats no entries, so it doesn't count as a level of them.
    if (length > 0) ctx.currentDepth = level;
    const shown = Math.min(Math.max(0, ctx.maxArrayLength), length);
    // Read by index, as Node does: subarray would look up the array's constructor, which may be
    // a getter, and fails on a detached array even for no elements.
    const output = [];
    for (let i = 0; i < shown; i++) output.push(primitive(value[i]));
    if (length > shown) output.push(more(length - shown));
    // Named through the prototype chain's descriptors, as Node does, so no getter runs.
    return { buffer: false, value, name: prefix(value, typedArrayTag.call(value), `(${length})`, ctx.depth - level), length, shown, output };
  }

  // finish prints what snapshot read, followed by fields, with mark (a <ref *N>, or '') first.
  function finish(shot, ctx, level, fields = [], mark = '') {
    const ref = mark === '' ? '' : mark + ' ';
    if (shot.buffer) {
      // Node lists a Buffer's keys on the same line, after a comma when it has bytes.
      const text = shot.text + (fields.length > 0 ? (shot.length > 0 ? ', ' : '') + fields.join(', ') : '');
      return `${ref}<Buffer ${text}>`;
    }
    if (shot.length === 0 && fields.length === 0) return `${ref}${shot.name} []`;
    const output = shot.output;
    // Appended one by one: spreading them into push would pass one argument each.
    for (let i = 0; i < fields.length; i++) output.push(fields[i]);
    return reduce(ctx, output, `${shot.name} [`, ']', level,
      { array: true, numbers: true, value: shot.value, shown: shot.shown, ref: mark });
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
  const TypedArrayPrototype = Object.getPrototypeOf(Uint8Array.prototype);
  const typedArrayTag = getter(TypedArrayPrototype, Symbol.toStringTag);
  const typedArrayLength = getter(TypedArrayPrototype, 'length');
  const isDataView = (value) => ArrayBuffer.isView(value) && typedArrayTag.call(value) === undefined;
  // Whether value may be a Ctor: it inherits from Ctor, or from no Object of this realm, like one
  // with a null prototype or from another realm. It spares ordinary objects the brand checks,
  // which throw for them, and are slow when they do. A proxy whose getPrototypeOf trap throws is
  // no Ctor.
  const mayBe = (value, Ctor) => {
    try {
      return value instanceof Ctor || !(value instanceof Object);
    } catch (e) {
      return false;
    }
  };
  const mayBeBuffer = (value) => mayBe(value, ArrayBuffer) ||
    (typeof SharedArrayBuffer === 'function' && mayBe(value, SharedArrayBuffer));

  // constructorName is the name util.inspect gives value's constructor: that of the first
  // named constructor on its prototype chain that value is an instance of. Without one, it's null
  // when value has no prototype, and otherwise, as Node's getConstructorName has it, the kind of
  // value with its prototype described in angle brackets: Object <[Object: null prototype] {}>.
  // budget is how many more levels deep the value may print; past it, the prototype is
  // <Complex prototype>. A proxy whose traps throw while it's looked at is named Object, as
  // Node, which inspects a proxy's target, would name a plain one.
  function constructorName(value, budget = Infinity) {
    let first;
    try {
      for (let obj = value; obj !== null; obj = Object.getPrototypeOf(obj)) {
        const desc = Object.getOwnPropertyDescriptor(obj, 'constructor');
        if (desc !== undefined && typeof desc.value === 'function' && desc.value.name !== '' &&
            isInstance(value, desc.value)) {
          return String(desc.value.name);
        }
        if (obj === value) first = Object.getPrototypeOf(obj);
      }
      if (first === null) return null;
      // Node asks V8 for the kind, which knows how value was made; Array or Object covers the
      // values that get here.
      const kind = Array.isArray(value) ? 'Array' : 'Object';
      if (budget < 0) return `${kind} <Complex prototype>`;
      const inner = constructorName(first, budget - 1);
      if (inner !== null) return `${kind} <${inner}>`;
      // Node inspects the prototype with a depth of -1: its braces when it has no keys, and
      // otherwise its name alone.
      const proto = keys(first).length === 0 ? `${objectPrefix(first)}{}` : cutLabel(first, objectFallback(first, tagOf(first)));
      return `${kind} <${proto}>`;
    } catch (e) {
      return 'Object';
    }
  }
  const isInstance = (value, ctor) => {
    try {
      return value instanceof ctor;
    } catch (e) {
      return false;
    }
  };

  // tagOf is value's toStringTag as util.inspect shows it next to the constructor's name: a
  // string, unless it's an own enumerable property, which is printed among the keys instead.
  // One that throws is left out.
  function tagOf(value) {
    let tag;
    try {
      tag = value[Symbol.toStringTag];
    } catch (e) {
      return '';
    }
    try {
      if (typeof tag !== 'string' || (tag !== '' && Object.prototype.propertyIsEnumerable.call(value, Symbol.toStringTag))) {
        return '';
      }
    } catch (e) {
      return '';
    }
    return tag;
  }

  // label is Node's getPrefix without its trailing space: the constructor's name and size, the
  // toStringTag in brackets when it differs, or [fallback: null prototype] without a
  // constructor.
  function label(ctor, tag, fallback, size = '') {
    if (ctor === null) {
      return `[${fallback}${size}: null prototype]` + (tag !== '' && fallback !== tag ? ` [${tag}]` : '');
    }
    return tag !== '' && ctor !== tag ? `${ctor}${size} [${tag}]` : ctor + size;
  }

  // The name util.inspect puts before the brace of value, a kind of object named fallback: its
  // constructor's name and size, the kind in brackets when its toStringTag differs, or
  // [fallback: null prototype] without a constructor. budget is as for constructorName.
  function prefix(value, fallback, size = '', budget = Infinity) {
    return label(constructorName(value, budget), tagOf(value), fallback, size);
  }

  // objectFallback is the kind Node names an object without a constructor after: V8's name for
  // its constructor, which for an object made without one is its own toStringTag when that's a
  // string data property, and otherwise Object. When it matches the tag shown, it's Object.
  function objectFallback(value, tag) {
    let desc;
    try {
      desc = Object.getOwnPropertyDescriptor(value, Symbol.toStringTag);
    } catch (e) {
      return 'Object';
    }
    const own = desc !== undefined && typeof desc.value === 'string' ? desc.value : '';
    return own === '' || own === tag ? 'Object' : own;
  }

  // objectPrefix is what util.inspect puts before a plain object's brace, with a space after it:
  // nothing for an Object without a toStringTag to show, the class of an instance, or
  // [Object: null prototype] without a prototype. budget is as for constructorName.
  function objectPrefix(value, budget = Infinity) {
    const ctor = constructorName(value, budget);
    const tag = tagOf(value);
    if (ctor === 'Object' && tag === '') return '';
    return label(ctor, tag, ctor === null ? objectFallback(value, tag) : 'Object') + ' ';
  }

  // arrayPrefix is what util.inspect puts before an array's bracket, with a space after it:
  // nothing for a plain Array, or the name and length of a subclass, a tagged array or one
  // without a prototype. budget is as for constructorName.
  function arrayPrefix(value, budget = Infinity) {
    const ctor = constructorName(value, budget);
    const tag = tagOf(value);
    if (ctor === 'Array' && tag === '') return '';
    return label(ctor, tag, 'Array', `(${value.length})`) + ' ';
  }

  // cutLabel is how util.inspect names value, a kind of object named fallback, past the depth
  // limit: its constructor's name in brackets, or [fallback: null prototype]. Past the limit, a
  // prototype without a constructor is <Complex prototype>.
  function cutLabel(value, fallback) {
    const ctor = constructorName(value, -1);
    const text = label(ctor, tagOf(value), fallback);
    return ctor === null ? text : `[${text}]`;
  }

  // binary describes an ArrayBuffer, SharedArrayBuffer or DataView the way util.inspect prints
  // one, as { name, entries, keys }: name goes before the brace (and in brackets in place of the
  // value past the depth limit), entries come first between the braces, and keys are properties
  // to print next, before the value's own keys. An ArrayBuffer's entries show at most
  // maxArrayLength bytes, or all of them when it's null. It answers null for anything else.
  function binary(value, maxArrayLength = 100, budget = Infinity) {
    if (value === null || typeof value !== 'object') return null;
    if (maxArrayLength === null) maxArrayLength = Infinity;
    if (isDataView(value)) {
      // .buffer goes last, as it's the one that isn't a number.
      return { name: prefix(value, 'DataView', '', budget), entries: [], keys: ['byteLength', 'byteOffset', 'buffer'] };
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
      name: prefix(value, shared ? 'SharedArrayBuffer' : 'ArrayBuffer', '', budget),
      entries: [contents],
      keys: ['byteLength']
    };
  }

  const mapSize = getter(Map.prototype, 'size');
  const setSize = getter(Set.prototype, 'size');
  // A brand check for Ctor, run only on values that may be one.
  const kind = (Ctor, isBrand) => (value) => mayBe(value, Ctor) && isBrand(value);
  const isMap = kind(Map, brand(mapSize));
  const isSet = kind(Set, brand(setSize));
  const isDate = kind(Date, brand(Date.prototype.getTime));
  // RegExp.prototype.global throws for anything but a RegExp, and answers undefined for
  // RegExp.prototype itself, which isn't one.
  const regExpGlobal = getter(RegExp.prototype, 'global');
  const isRegExp = kind(RegExp, (value) => {
    try {
      return regExpGlobal.call(value) !== undefined;
    } catch (e) {
      return false;
    }
  });

  // collection describes a Map or Set the way util.inspect prints one, as { name, map, entries,
  // more }: name goes before the brace, entries are what to format, at most maxArrayLength of
  // them (all of them when it's null), keys and values taking turns for a Map, and more is how
  // many were left out. It answers null for anything else.
  function collection(value, maxArrayLength = 100, budget = Infinity) {
    if (value === null || typeof value !== 'object') return null;
    const map = isMap(value);
    if (!map && !isSet(value)) return null;
    if (maxArrayLength === null) maxArrayLength = Infinity;
    const size = map ? mapSize.call(value) : setSize.call(value);
    const shown = Math.min(Math.max(0, maxArrayLength), size);
    const entries = [];
    // The prototype's iterators, as a Map or Set may override its own.
    const iterator = map ? Map.prototype.entries.call(value) : Set.prototype.values.call(value);
    for (let i = 0; i < shown; i++) {
      const next = iterator.next();
      if (next.done) break;
      if (map) entries.push(next.value[0], next.value[1]);
      else entries.push(next.value);
    }
    return { name: prefix(value, map ? 'Map' : 'Set', `(${size})`, budget), map, entries, more: size - shown };
  }

  // scalar is how util.inspect prints a Date or RegExp, before any properties of its own: an
  // invalid Date as Invalid Date, and a subclass's name first. It answers null for anything else.
  function scalar(value, budget = Infinity) {
    if (value === null || typeof value !== 'object') return null;
    let text;
    let fallback;
    if (isDate(value)) {
      const time = Date.prototype.getTime.call(value);
      text = Number.isNaN(time) ? 'Invalid Date' : Date.prototype.toISOString.call(value);
      fallback = 'Date';
    } else if (isRegExp(value)) {
      // toString reads source and flags as properties, which a RegExp without a prototype
      // doesn't have. Like Node, copy it first: new RegExp takes them from its internal state.
      const plain = Object.getPrototypeOf(value) === null ? new RegExp(value) : value;
      text = RegExp.prototype.toString.call(plain);
      fallback = 'RegExp';
    } else {
      return null;
    }
    const name = prefix(value, fallback, '', budget);
    return name === fallback ? text : `${name} ${text}`;
  }

  // cut names value in brackets, as util.inspect prints it past the depth limit, or answers null
  // when Node prints it in full anyway: when it's empty, a RegExp, which leaves its properties
  // out, a Date without properties or a Buffer. Errors are left to the caller. A value that
  // throws when looked at, like a proxy with a throwing trap, is [Object].
  function cut(value) {
    try {
      return cutName(value);
    } catch (e) {
      return '[Object]';
    }
  }

  function cutName(value) {
    // Read first, so a proxy whose getPrototypeOf trap throws is [Object] even when empty.
    const proto = Object.getPrototypeOf(value);
    // Only the name is needed, so no bytes are read.
    const bin = binary(value, 0, -1);
    if (bin !== null) return `[${bin.name}]`;
    // An array's or typed array's keys are its elements, so they're never listed in full.
    if (Array.isArray(value)) return value.length === 0 && extraKeys(value).length === 0 ? null : cutLabel(value, 'Array');
    if (ArrayBuffer.isView(value)) {
      const buffer = typeof Buffer === 'function' && Buffer.isBuffer(value);
      const empty = typedArrayLength.call(value) === 0 && extraKeys(value).length === 0;
      return buffer || empty ? null : cutLabel(value, typedArrayTag.call(value));
    }
    const bare = keys(value).length === 0;
    if (isMap(value)) return mapSize.call(value) === 0 && bare ? null : cutLabel(value, 'Map');
    if (isSet(value)) return setSize.call(value) === 0 && bare ? null : cutLabel(value, 'Set');
    if (isRegExp(value)) return null;
    if (isDate(value)) return bare ? null : cutLabel(value, 'Date');
    if (bare) return null;
    return cutLabel(value, proto === null ? objectFallback(value, tagOf(value)) : 'Object');
  }

  // field describes property k of value as util.inspect prints one of the keys binary lists:
  // { name, value } with the value to format, or { name, label } for an accessor, which isn't
  // called. An own property that isn't enumerable is named in brackets. A key value doesn't
  // own, like byteLength from its prototype, is read as usual.
  function field(value, k) {
    const desc = Object.getOwnPropertyDescriptor(value, k);
    if (desc === undefined) return { name: key(k), value: value[k] };
    const name = desc.enumerable ? key(k) : `[${typeof k === 'symbol' ? key(k) : k}]`;
    if ('value' in desc) return { name, value: desc.value };
    return { name, label: desc.get === undefined ? '[Setter]' : desc.set === undefined ? '[Getter]' : '[Getter/Setter]' };
  }

  // Node's colors are SGR escapes like \x1b[33m, which take no room on screen.
  const COLORS = /\u001b\[\d\d?m/g;
  const colorless = (s) => s.replace(COLORS, '');

  // How many columns each code point takes, as ICU's getStringWidth, which Node uses, counts
  // them. WIDTHS lists where the width changes: each entry is how far on from the last one the
  // change is, in base 36, followed by the width from there.
  // BEGIN generated widths
  // From ICU 75.1 (Unicode 15.1) in Node v20.18.1, by gen_widths.js.
  const WIDTHS =
    '00,w1,2n0,x1,gw0,341,7n0,71,7b0,191,10,11,10,21,10,21,10,11,1k0,61,a0,b1,10,11,1a0,l1,g0,' +
    '11,2t0,81,10,61,20,21,10,41,x0,11,10,11,u0,r1,2j0,b1,1m0,91,90,11,o0,41,10,91,10,31,10,51,' +
    '170,31,1g0,21,60,81,160,1l1,1j0,11,10,11,40,81,40,11,30,71,a0,21,t0,11,1m0,11,40,41,80,11,' +
    'k0,21,q0,11,20,21,1l0,11,40,21,40,21,20,31,30,11,u0,21,30,11,b0,21,1l0,11,40,51,10,21,40,' +
    '11,k0,21,m0,61,10,11,1m0,11,20,11,10,41,80,11,70,21,b0,21,u0,11,1p0,11,c0,11,1e0,11,30,11,' +
    '1j0,11,10,31,50,31,10,41,70,21,b0,21,t0,11,1m0,11,20,11,60,11,50,21,k0,21,s0,21,1l0,21,40,' +
    '41,80,11,k0,21,t0,11,200,11,70,31,10,11,2i0,11,20,71,c0,81,2q0,11,20,91,b0,71,210,21,r0,11,' +
    '10,11,10,11,1j0,e1,10,51,10,21,50,b1,10,101,90,11,2u0,41,10,61,10,21,20,21,p0,21,40,31,g0,' +
    '41,d0,11,20,21,60,11,f0,11,2q2,2o1,e50,31,qa0,31,t0,21,u0,21,u0,21,1s0,21,10,71,80,11,20,' +
    'b1,90,11,190,51,390,21,y0,11,3a0,31,40,21,90,11,60,31,630,21,20,11,1m0,11,10,71,10,11,10,' +
    '11,20,81,60,a1,20,11,1c0,v1,1d0,41,1c0,11,10,51,10,11,50,11,140,91,c0,21,w0,41,20,21,10,31,' +
    '1k0,11,10,21,30,11,10,31,1m0,81,20,21,480,31,10,d1,10,71,40,11,60,11,30,21,5i0,1s1,ej0,51,' +
    'q0,51,1d0,51,10,a1,2o0,x1,fd2,21,d2,21,5a2,41,32,11,22,11,eh2,21,l2,21,1e2,c1,172,11,j2,11,' +
    'd2,11,82,21,h2,21,52,21,82,11,52,11,l2,11,72,21,12,11,42,11,22,11,72,11,42,21,s2,11,z2,11,' +
    '12,11,42,31,12,11,1p2,31,o2,11,e2,11,nv2,21,1f2,11,42,11,bd0,31,3x0,11,2o0,w1,3k2,q1,12,' +
    '2h1,c2,5y1,q2,271,22,2e1,22,2v1,52,171,12,2m1,12,2c1,b2,1c1,12,141,82,5f41,1s2,h3h1,32,1j1,' +
    'bs0,41,10,a1,w0,21,280,21,7k0,11,30,11,40,11,p0,21,50,11,470,21,q0,i1,d0,11,120,81,p0,b1,' +
    'e2,t1,30,31,1c0,11,20,41,20,21,130,11,1v0,61,20,21,20,21,c0,11,80,11,1b0,11,1f0,11,10,31,' +
    '20,21,50,21,10,11,160,21,80,11,6m0,11,20,11,40,11,i2,8mc1,6l82,e81,u0,11,kh0,g2,a1,60,g2,' +
    'z1,12,j1,12,41,430,11,12,2o1,3j2,71,i0,31,e90,11,6a0,11,450,51,1ae0,31,10,21,50,41,140,31,' +
    '40,11,4l0,21,fx0,41,ar0,21,280,31,1y0,b1,1d0,41,3f0,11,1i0,f1,150,11,20,21,a0,31,1d0,41,20,' +
    '21,20,11,40,11,a0,11,1e0,31,100,51,10,81,1q0,11,c0,21,1g0,91,a0,41,20,11,2n0,31,20,11,10,' +
    '21,60,11,20,11,4d0,11,30,81,l0,21,1l0,21,30,11,110,71,30,51,5f0,81,20,31,10,11,n0,11,2c0,' +
    '61,10,11,40,21,10,21,6m0,41,60,21,10,21,r0,21,2d0,81,20,11,10,21,2y0,11,10,11,20,61,10,11,' +
    '2t0,31,20,41,10,51,770,91,10,21,740,21,10,11,40,11,400,41,20,21,40,11,w0,a1,140,61,20,41,' +
    '80,11,90,61,20,31,1a0,d1,10,21,ba0,71,10,61,10,11,2a0,m1,20,71,10,21,10,21,3e0,61,30,11,10,' +
    '21,10,71,10,11,200,21,30,11,10,11,9n0,21,b0,21,1g0,51,50,11,10,11,44t0,h1,60,f1,asa0,51,' +
    '1n0,71,t40,11,1r0,41,252,51,b2,21,e2,4qg1,82,ye1,162,91,6w72,41,12,71,12,21,12,831,f2,11,' +
    't2,31,22,11,e2,41,82,b01,1wh0,21,10,41,3mk0,1a1,20,n1,f40,31,90,g1,20,71,u0,41,440,31,1iz0,' +
    '1j1,40,1e1,80,11,e0,11,m0,51,10,f1,11s0,71,10,h1,20,71,10,21,10,51,2s0,11,4g0,71,af0,11,' +
    '1p0,41,e40,41,rk0,71,310,71,1bt2,11,5m2,11,5a2,11,22,a1,232,t1,d2,181,42,91,72,21,e2,61,' +
    '4a2,x1,c2,91,12,1y1,12,m1,c2,171,42,51,c2,h1,32,11,32,1z1,12,11,12,571,22,1r1,d2,41,12,o1,' +
    'i2,11,q2,21,d2,11,2e2,2d1,1c2,1y1,62,11,32,31,22,31,42,41,b2,21,72,91,6b2,c1,42,11,7v2,1b1,' +
    '12,a1,12,551,342,d1,32,91,72,1a1,12,71,82,e1,42,91,72,91,zr2,1eke1,22,1eke1,e1oj0,11,u0,' +
    '2o1,3k0,6o1';
  // END generated widths
  const widthStarts = [];
  const widthValues = [];
  {
    let point = 0;
    for (const change of WIDTHS.split(',')) {
      point += parseInt(change.slice(0, -1), 36);
      widthStarts.push(point);
      widthValues.push(Number(change.slice(-1)));
    }
  }
  // columns is how many columns code point takes: the width of the last change at or before it.
  function columns(point) {
    let lo = 0;
    let hi = widthStarts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (widthStarts[mid] <= point) lo = mid;
      else hi = mid - 1;
    }
    return widthValues[lo];
  }

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
        n += columns(char.codePointAt(0));
      }
      break;
    }
    return n;
  }

  // tracker follows the objects being formatted, each inside the one before, to find those that
  // hold themselves, as Node does. visit(value) starts formatting value, or, when value is being
  // formatted already, answers [Circular *N], numbering value the first time. leave() finishes
  // the value visited last, and answers <ref *N> when it held itself (or '' when not), which
  // goes before the value: callers call it after formatting its entries and before laying them
  // out, passing what it answers to reduce as ref. Numbers count from 1 again once the
  // outermost value is done.
  function tracker() {
    const seen = [];
    let circular = null;
    return {
      visit(value) {
        if (!seen.includes(value)) {
          seen.push(value);
          return null;
        }
        if (circular === null) circular = new Map();
        let index = circular.get(value);
        if (index === undefined) {
          index = circular.size + 1;
          circular.set(value, index);
        }
        return `[Circular *${index}]`;
      },
      leave() {
        const value = seen.pop();
        const index = circular === null ? undefined : circular.get(value);
        if (seen.length === 0) circular = null;
        return index === undefined ? '' : `<ref *${index}>`;
      }
    };
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
    if (typeof numbers === 'function') numbers = numbers();
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

  // entry joins an object's key, already quoted, with its formatted value. With compact: true,
  // Node indents a value's lines three spaces in from the key's, rather than two, and starts the
  // value on a line of its own when it's wider than breakLength; but not for an array's or typed
  // array's keys (array), which stay on the key's line.
  function entry(ctx, name, value, array = false) {
    if (ctx.compact !== true || array) return `${name}: ${value}`;
    const nested = value.replace(/\n/g, '\n ');
    return width(value) > ctx.breakLength ? `${name}:\n ${nested}` : `${name}: ${nested}`;
  }

  // Indents the lines an entry spans along with it.
  const indent = (entry) => entry.replace(/\n/g, '\n  ');

  // reduce joins the entries of a value at level between open and close as util.inspect does.
  // base, like an Error's stack, goes before open, and ref, a value's <ref *N>, before both.
  // Arrays' entries are grouped in columns, with numbers aligned right: numbers says whether the
  // shown elements of the array, value, are all numbers or BigInts, and, only when they're
  // grouped, the elements from shown up to the number of entries are checked too, as Node does.
  // The entries go on one line when none spans lines, the value holds fewer than compact levels
  // of objects, and the line fits in breakLength. Otherwise each goes on its own line, indented
  // two spaces.
  function reduce(ctx, output, open, close, level,
    { base = '', array = false, numbers = false, value = undefined, shown = 0, ref = '' } = {}) {
    if (ref !== '') {
      // Node puts it in the base, which counts toward the line's length, except with compact:
      // true, where it goes in the brace.
      if (ctx.compact === true) open = `${ref} ${open}`;
      else base = base === '' ? ref : `${ref} ${base}`;
    }
    const before = base ? `${base} ` : '';
    if (ctx.compact !== true) {
      if (typeof ctx.compact === 'number' && ctx.compact >= 1) {
        const entries = output.length;
        if (array && entries > 6) {
          // Asked only once the entries do go in columns, as Node reads elements past the
          // shown ones only then.
          const right = () => numbers && (value === undefined || numbersAt(value, shown, output.length));
          output = group(ctx, output, level, right);
        }
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

  return {
    context, quote, string, key, keys, extraKeys, numbersAt, primitive, numeric, stringSpec, more, view, snapshot,
    finish, binary, collection, scalar, isDate, field, cut, entry, tracker, reduce, objectPrefix, arrayPrefix
  };
})
