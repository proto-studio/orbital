// Runs unchanged in Node and in Orbital; TestBinaryStdio_MatchesNode compares what it writes to
// stdout and stderr with binary_stdio.node.json. Covers how console.log and util.inspect print
// values: ArrayBuffers, DataViews, and how deep they print nested values.
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
