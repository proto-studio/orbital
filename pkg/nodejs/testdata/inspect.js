// Runs unchanged in Node and in Orbital; TestBinaryStdio_MatchesNode compares what it writes to
// stdout and stderr with binary_stdio.node.json. Covers how console.log and util.inspect print
// values: ArrayBuffers, DataViews, how deep they print nested values, and how they break long
// output across lines.
const u = require('util');
const show = (...values) => { for (const v of values) { console.log(v); console.log(u.inspect(v)); } };

// ArrayBuffers and DataViews, which go across lines when they don't fit in 80 columns.
const dv = new DataView(new ArrayBuffer(4), 1, 2);
show(dv, new ArrayBuffer(2), new ArrayBuffer(0), new ArrayBuffer(10), new ArrayBuffer(101), new SharedArrayBuffer(1),
  new DataView(new ArrayBuffer(0)), { d: dv }, [new ArrayBuffer(1)], { a: { b: { c: dv } } },
  Object.assign(new DataView(new ArrayBuffer(1)), { x: 1 }));
class Bytes extends ArrayBuffer {}
show(new Bytes(1));
console.log(u.inspect(new ArrayBuffer(5), { maxArrayLength: 2 }), u.inspect({ d: dv }, { depth: 0 }));
console.log(u.inspect(dv, { breakLength: Infinity }));
console.log(u.inspect(new ArrayBuffer(1), { compact: false }), u.inspect(new ArrayBuffer(3), { maxArrayLength: null }));
const bare = new ArrayBuffer(1);
Object.setPrototypeOf(bare, null);
console.log(u.inspect(bare));
// A custom inspector replaces the description, which would throw reading this toStringTag.
const hooked = new ArrayBuffer(2);
Object.defineProperty(hooked, Symbol.toStringTag, { get() { throw new Error('read the tag'); } });
hooked[u.inspect.custom] = () => 'hooked';
console.log(u.inspect(hooked), u.inspect({ hooked }));
// Own properties that shadow the fields shown: an accessor isn't called, and one that isn't
// enumerable is named in brackets. sorted sorts every entry.
const shadowed = new ArrayBuffer(1);
Object.defineProperty(shadowed, 'byteLength', { get() { throw new Error('called the getter'); } });
const relabeled = new ArrayBuffer(1);
Object.defineProperty(relabeled, 'byteLength', { value: 99 });
const rebuffered = new DataView(new ArrayBuffer(1));
Object.defineProperty(rebuffered, 'buffer', { get() { return 1; }, set(v) {} });
show(shadowed, relabeled, rebuffered);
console.log(u.inspect(Object.assign(new ArrayBuffer(0), { z: 1, a: 2 }), { sorted: true }));
console.log(u.inspect(Object.assign(new DataView(new ArrayBuffer(1)), { z: 1, a: 2 }), { sorted: true }));
// %j is JSON.stringify, which prints an ArrayBuffer, a DataView and an Error as {} plus their
// enumerable properties.
const looped = { a: 1 };
looped.self = looped;
console.log('%j %j %j %j', new ArrayBuffer(2), dv, { list: [1, 'a'] }, Object.assign(new Error('e'), { code: 'E' }));
console.log('%j %j', looped, undefined);

// console.log prints values nested three objects deep, and cuts the ones past them short.
const err = new Error('deep');
err.stack = 'Error: deep\n    at f (file.js:1:1)';
err.code = 'E_DEEP';
console.log({ a: { b: { c: 1 } } }, { a: { b: { c: { d: 1 } } } }, [[[[1]]]]);
const plain = new RangeError('plain');
plain.stack = 'RangeError: plain\n    at g (file.js:2:2)';
show({ a: { b: { c: err } } }, { a: { b: { c: plain } } });

// Output longer than breakLength (80) goes one entry per line, and arrays of more than six short
// entries go in columns: numbers aligned right, anything else left.
const range = (n, f = (i) => i) => Array.from({ length: n }, (_, i) => f(i));
show(range(30), { a: 'a'.repeat(20), b: 'b'.repeat(20), c: 'c'.repeat(20), d: 'd'.repeat(20) },
  new Uint16Array(101), range(101, (i) => i * 3), range(200), range(120, (i) => 'v' + i),
  ['apple', 'banana', 'cherry', 'date', 'elderberry', 'fig', 'grape', 'honeydew'],
  range(26, (i) => String.fromCharCode(97 + i).repeat((i % 5) + 1)),
  [1, -2, 3.5, 100000, NaN, 7, 8], [true, false, null, undefined, 1, 'two', 3],
  range(10, (i) => ({ id: i, name: 'n' + i })), ['x'.repeat(30), 'y'.repeat(30), 'z'.repeat(30)],
  new Float64Array([1.5, -2.25, 3, 4, 5, 6, 7, 8.125]), new Uint8Array(200),
  ['一二三', '四五六', 'seven', 'eight', 'nine', 'ten', '十一']);
// An object holding three levels of objects goes across lines even when it would fit.
show({ first: [1, 2, 3], second: { inner: [1, 2, 3] } }, { a: { b: { c: {} } } },
  [[1, 2, [3, 4, [5, 6]]], { k: [7, 8] }], { x: range(30), y: 'short' },
  { arr: range(40, (i) => i * 7), deep: { deeper: { deepest: [1, 2] } } },
  { buf: Buffer.alloc(60), view: new DataView(new ArrayBuffer(3)) },
  { 'key with spaces': 'value', 'another-key': 'another value that is long', third: 'third value' });
// A long string holding newlines is split after each into quoted pieces joined with +.
const lines = 'line one\nline two\nline three is a bit longer than the others\n' + 'z'.repeat(50);
show({ s: lines }, [lines, 'short']);
console.log(u.inspect(lines));
// An Error's properties go on one line after a stack with no frames, when they fit.
const short = new Error('short');
short.stack = 'Error: short';
short.code = 'E_SHORT';
const long = new Error('long');
long.stack = 'Error: long';
long.info = 'i'.repeat(70);
show(short, long, { nested: short, list: [long] });
// Each line printed inside a console.group is indented.
console.group('group');
console.log({ a: 'a'.repeat(20), b: 'b'.repeat(20), c: 'c'.repeat(20), d: 'd'.repeat(20) });
console.groupEnd();
console.dir({ s: lines });
// breakLength, compact, maxArrayLength and maxStringLength change the layout.
console.log(u.inspect(range(30), { breakLength: 40 }), u.inspect(range(30), { compact: 1 }));
console.log(u.inspect({ a: 'a'.repeat(30), b: 'b'.repeat(30) }, { breakLength: Infinity }));
console.log(u.inspect({ a: 1, b: { c: 2 } }, { compact: false }), u.inspect({ a: { b: { c: 1 } } }, { compact: 1 }));
console.log(u.inspect(range(8), { maxArrayLength: 3 }), u.inspect(new Set([1, 2, 3, 4]), { maxArrayLength: 2 }));
console.log(u.inspect(new Map([['alpha', { x: 1 }], ['beta', 'b'.repeat(70)]])), u.inspect(new Set(range(20, (i) => 'item' + i))));
console.log(u.inspect('abcdef', { maxStringLength: 3 }), u.inspect([1, 2], { maxArrayLength: 1 }));
// Past the depth limit a Date with properties is named, even one that holds itself, and %j
// prints every element of a long array.
const dated = new Date(0);
dated.self = dated;
show({ a: { b: { c: dated } } });
console.log(u.inspect({ a: { b: { c: /x/ } } }));
console.log('%j', range(101, () => 0));

// Maps, Sets, Dates, RegExps, BigInts, negative zero and symbols, at the top level and nested.
class MyMap extends Map {}
class MySet extends Set {}
class MyDate extends Date {}
class MyRegExp extends RegExp {}
const keyed = new Map([['a', 1]]);
keyed.extra = true;
const marked = /x/g;
marked.note = 'n';
const stamped = new Date(86400000);
stamped.tz = 'UTC';
const bareMap = Object.setPrototypeOf(new Map([[1, 2]]), null);
const bareSet = Object.setPrototypeOf(new Set(), null);
// Without a prototype a RegExp has no source or flags properties; with an own source, that's shown.
const bareRegExp = Object.setPrototypeOf(/hi\/x/g, null);
const ownSource = /x/m;
Object.defineProperty(ownSource, 'source', { value: 'own' });
show(new Map([['a', 1], ['b', { c: [1, 2] }]]), new Set([1, 'two', [3]]), new Map(), new Set(),
  new Date(Date.UTC(2024, 0, 1)), new Date(NaN), /a\/b/gi, new RegExp(''), 1n, -12345678901234567890n, -0, 0,
  Symbol('sym'), Symbol(), new MyMap([[1, 2]]), new MySet([1]), new MyDate(0), new MyRegExp('r', 'y'),
  keyed, marked, stamped, bareMap, bareSet, bareRegExp, ownSource,
  { map: new Map([[{ k: 1 }, new Set([2])]]), set: new Set([new Map()]), date: new Date(0), re: /z/m },
  [1n, -0, 0, Symbol.iterator, new Date(0), /q/], { big: 2n, neg: -0, sym: Symbol('s') },
  new Map([[1n, -0]]), new Set([-0, 3n]), { a: { b: { c: new Map([[1, 2]]), d: new Set([1]), e: new Map() } } },
  { a: { b: { c: marked, d: stamped, e: /plain/, f: new Date(0) } } },
  new Map(range(120, (i) => [i, 'v'])), new Set(range(30)),
  new Map([['long', 'l'.repeat(60)], ['other', 'o'.repeat(30)]]));
console.log('%s %d %i %f', 5n, 5n, 5n, 5n);
// %f rounds a BigInt to the nearest number, which may be Infinity.
console.log('%f %f %f', 9007199254740993n, 10n ** 400n, -(10n ** 400n));
console.log('%s %d %i %f %o %O', -0, -0, -0, -0, -0, -0);
console.log('%d %i %i %s', -1.5, -0.5, 2.7, Symbol('fmt'));
console.log(u.inspect(new Map([[1, 2], [3, 4]]), { maxArrayLength: 1 }), u.inspect(new Map([[1, { a: 1 }]]), { depth: 0 }));
console.log(u.inspect(new Map([['k', 'v']]), { sorted: true }), u.format('%o', new Set([1])));
// Past the depth limit, an empty Set with properties, even one that holds itself, and a RegExp
// with properties are named.
const emptySet = new Set();
emptySet.self = emptySet;
show({ a: { b: { emptySet, marked } } });
// compact: true indents a nested value three spaces in from its key, and starts it on its own line
// when it's wider than breakLength.
const wide = { aaaaaaaaaa: 'x'.repeat(30), bbbbbbbbbb: { cccccccccc: 'y'.repeat(30), dddddddddd: 'z'.repeat(30),
  e: { ffffffffff: 'w'.repeat(40), g: [1, 2] } } };
console.log(u.inspect(wide, { compact: true }));
console.log(u.inspect([wide, 'q'.repeat(60)], { compact: true }));
console.log(u.inspect(new Map([['k'.repeat(40), wide]]), { compact: true }));
console.log(u.inspect({ a: 1, b: { c: 2 } }, { compact: true }), u.inspect({ k: 'v'.repeat(90) }, { compact: true }));
// getters: accessors print as labels, and with getters: true (or 'get' or 'set', for those
// without and with a setter) also show what the getter returns.
const accessors = {
  get a() { return 1; }, get b() { return { x: 1 }; }, set c(v) {}, get d() { return 'str'; }, set d(v) {},
  get e() { throw new Error('boom'); }, get f() { return null; }
};
for (const getters of [false, true, 'get', 'set']) console.log(u.inspect(accessors, { getters }));
console.log(accessors);
// %d is Number of the value and %i parseInt of it, which reads a number's text, so 2 ** 70
// (1.1805916207174113e+21) is 1. A BigInt keeps its n, and a symbol is NaN.
const numbers = ['%i|%d|%d|%i|%f', 2 ** 70, '42', 42n, '1e3', '1.5x'];
const conversions = ['%d %i %f %d %i %d %i %f', 1.5, -1.5, '-0', '', ' 7 ', null, true, Symbol('n')];
const objects = ['%d %i %f %d %i', { valueOf: () => 3, toString: () => '4' }, { valueOf: () => 3, toString: () => '4' },
  { toString: () => '2.5' }, [5], 1e-7];
for (const args of [numbers, conversions, objects]) {
  console.log(...args);
  console.log(u.format(...args));
  console.log(u.formatWithOptions({}, ...args));
}
// Own enumerable symbol keys print after the other keys, in brackets with their descriptions
// escaped; ones that aren't enumerable are left out. An object holding only symbols isn't empty.
const sym = Symbol('k');
const symErr = new Error('sym');
symErr.stack = 'Error: sym\n    at h (file.js:3:3)';
symErr[sym] = 1;
show({ x: 2, [Symbol('k')]: 1 }, { [Symbol()]: 1, [Symbol.iterator]: 2, [Symbol.for('f')]: 3 },
  { [Symbol('a\nb\'\\\x85')]: Symbol('v') }, Object.defineProperty({ a: 1 }, Symbol('hidden'), { value: 1 }),
  Object.assign([1, 2], { [sym]: 3 }), Object.assign(new Map([[1, 2]]), { [sym]: 1 }),
  Object.assign(new Set(), { [sym]: 1 }), Object.assign(new Date(0), { [sym]: 1 }), Object.assign(/x/, { [sym]: 1 }),
  Object.assign(new ArrayBuffer(1), { [sym]: 1 }), symErr, { get [sym]() { return 1; } },
  { a: { b: { c: { [sym]: 1 }, d: Object.assign(new Set(), { [sym]: 1 }), e: Object.assign(new Date(0), { [sym]: 1 }) } } },
  ['\x85\x7f\x9f']);
console.log(u.inspect({ b: 1, [Symbol('z')]: 1, a: 2, a0: 3, A: 1, [Symbol('a')]: 2 }, { sorted: true }));
console.log(u.inspect(Object.assign([3, 1], { [Symbol('b')]: 1, [Symbol('a')]: 2 }), { sorted: true }));
console.log(u.inspect(new Map([[2, 1], [1, 2]]), { sorted: true }), u.inspect(new Set([2, 1]), { sorted: true }));
// A value that holds itself is labeled <ref *N> where it starts, and [Circular *N] where it
// appears inside itself, numbered in the order they're found. Checked before the depth limit.
{
  const o = { x: 2 }; o.self = o;
  const a = [1]; a.push(a);
  const p = { name: 'p' }; const c = { parent: p }; p.child = c; p.kids = [c, c];
  const m = new Map(); m.set('me', m); m.set(m, 1);
  const s = new Set(); s.add(s);
  const two = { a: {}, b: {} }; two.a.up = two; two.b.up = two.b; two.a.side = two.b;
  const d = new Date(0); d.self = d;
  const e = new Error('loop'); e.stack = 'Error: loop\n    at f (file.js:1:1)'; e.self = e;
  const deep = { l1: { l2: { l3: {} } } }; deep.l1.l2.l3.back = deep;
  const nest = { inner: {} }; nest.inner.me = nest.inner;
  const long = { aaaaaaaaaaaaaaaaaaaa: 1, bbbbbbbbbbbbbbbbbbbbbbb: 2, cccccccccccccccccc: 3 }; long.self = long;
  const rx = /r/; rx.self = rx;
  const ab = new ArrayBuffer(1); ab.self = ab;
  show(o, a, p, m, s, two, d, e, deep, nest, long, rx, ab, [o, o], { o1: o, o2: o });
  console.log(o, o);
  console.log('%o %O', o, a);
  console.log(u.format('%s', o), u.inspect(o, { compact: true }), u.inspect(long, { compact: false }));
  console.log(u.inspect(o, { depth: 0 }));
  const top = { a: { b: {} } }; top.a.b.c = top; top.a.b.d = [top.a];
  show(top);
  // A custom inspector's replacement that holds the value formats it afresh, and one that answers
  // the value itself leaves it to be formatted as usual.
  const wrapper = { [u.inspect.custom]() { return { wrapped: this }; } };
  const itself = { a: 1, [u.inspect.custom]() { return this; } };
  console.log(u.inspect(wrapper), u.inspect(itself));
}
// Columns are padded by how many columns each entry takes on screen, which Node measures with
// ICU's East Asian Width data: wide and full-width characters and emoji take two, and marks,
// controls and format characters none.
show(range(10, (i) => '\u{1F680}'.repeat(i % 3) + 'ab'.repeat(i % 4)),
  range(10, (i) => ['\u{1FA70}', '⌚', '☔', '\u{1F004}', '　', 'Ａ', '±', '́e', '­', '​'][i] + 'x'.repeat(i)),
  range(9, (i) => ['\u{1F3FB}', 'é', 'ᅠ', '\u{E0001}', '\u{20000}', '\u{30000}', '⺀', '\u{1F1E6}', '\u{16FE4}'][i] + 'yy'.repeat(i % 3)),
  range(8, (i) => '\u{1F468}‍\u{1F469}'.repeat(i % 2) + '-'.repeat(i)));
// An object or array without the usual constructor is named before its brace: a class's name,
// a toStringTag that isn't an own enumerable key in brackets, or [Kind: null prototype].
{
  const show = (...values) => { for (const v of values) { console.log(v); console.log(u.inspect(v)); } };
  class Foo { constructor() { this.a = 1; } }
  class Empty {}
  class MyArr extends Array {}
  class Bytes extends Uint8Array {}
  class MyMap extends Map {}
  const tag = (o, t, enumerable = false) => Object.defineProperty(o, Symbol.toStringTag, { value: t, enumerable });
  const fake = Object.create({ constructor: function Zed() {} });
  show(Object.create(null), Object.assign(Object.create(null), { a: 1 }), 'abc'.match(/(?<g>b)/).groups,
    new Foo(), new Empty(), new (class {})(), fake, Object.create({ inherited: 1 }),
    tag({ a: 1 }, 'T'), { a: 1, [Symbol.toStringTag]: 'Own' }, tag(new Foo(), 'Bar'), tag(new Foo(), 'Foo'),
    tag(Object.create(null), 'T'), tag(Object.create(null), 'T', true), Object.assign(Object.create(null), { [Symbol.toStringTag]: 5 }),
    Object.defineProperty(Object.create(null), Symbol.toStringTag, { get() { return 'G'; }, enumerable: true }),
    MyArr.from([1, 2]), new MyArr(), Object.setPrototypeOf([1, 2], null), tag([1], 'T'), new Bytes(2), new Bytes(0),
    Object.setPrototypeOf(new Uint8Array(2), null), new MyMap([[1, 2]]), Object.setPrototypeOf(new Map([[1, 2]]), null),
    tag(new Map(), 'M'), Object.setPrototypeOf(new Set([1]), null),
    { a: { b: { c: Object.create(null), d: Object.assign(Object.create(null), { x: 1 }), e: new Foo(), f: new Empty(),
      g: MyArr.from([1]), h: Object.setPrototypeOf([1], null), i: tag({ x: 1 }, 'T'), j: new MyMap([[1, 1]]),
      k: Object.setPrototypeOf(new Map([[1, 1]]), null), l: new Bytes(1), m: Object.setPrototypeOf(new Uint8Array(1), null),
      n: tag(Object.assign(Object.create(null), { x: 1 }), 'T', true), o: new MyArr() } } });
}
// An object whose prototype chain has no named constructor but doesn't end right away is named
// after its kind with its prototype described in angle brackets, <Complex prototype> past the
// depth limit.
{
  const np = Object.create(null);
  show(Object.create(np), Object.assign(Object.create(np), { a: 1 }), Object.create(Object.assign(Object.create(null), { x: 1 })),
    Object.create(Object.create(Object.create(null))), Object.create(Object.create(Object.create(Object.create(null)))),
    { a: { b: { c: Object.assign(Object.create(np), { z: 1 }), d: Object.create(Object.create(Object.create(Object.create(null)))) } } },
    Object.create(Object.setPrototypeOf({ q: 1 }, Object.create(null))), { a: Object.create(Object.create(Object.create(null))) });
  console.log(u.inspect(Object.create(Object.create(Object.create(null))), { depth: 0 }), u.inspect({ a: Object.create(np) }, { depth: null }));
}
// %s prints a number or BigInt as inspect does, with -0 and a BigInt's n, anything but an
// object with String(), as it does an object with its own toString or Symbol.toPrimitive, and
// inspects any other object with a depth of 0.
{
  class T { toString() { return 'custom'; } }
  class P { [Symbol.toPrimitive]() { return 'prim'; } }
  class Sub extends T {}
  const loop = { a: 1 }; loop.self = loop;
  const err = new Error('boom'); err.stack = 'Error: boom\n    at f (file.js:1:1)';
  const args = ['%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s', 10n, -0, 5, Symbol('s'), null, undefined, { a: { b: { c: 1 } } }, new T(), [1, [2, [3]]],
    new P(), new Sub(), Object.assign(new Date(0), { toString() { return 'date'; } })];
  const more = ['%s|%s|%s|%s', new Map([[1, { x: 1 }]]), loop, err, { k: 'v'.repeat(80), l: [1, 2] }];
  for (const a of [args, more]) { console.log(u.format(...a)); console.log(...a); console.log(u.formatWithOptions({ depth: 5 }, ...a)); }
  console.log('%s', -12345678901234567890n, 'tail', 3n);
}
