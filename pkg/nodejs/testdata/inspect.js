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
