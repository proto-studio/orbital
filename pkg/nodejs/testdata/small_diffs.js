// Runs unchanged in Node and in Orbital; TestBinaryStdio_MatchesNode compares what it writes to
// stdout and stderr with binary_stdio.node.json. Covers console and util.inspect output for
// Buffers, keys and strings, fs.writeSync to descriptors 1 and 2, and Buffer.from(arrayBuffer).
const fs = require('fs');
let n; try { n = fs.writeSync(1, 'to-stdout\n'); } catch (e) { console.log('writeSync1 threw', e.message); }
console.log('writeSync1 returned', n);
const ab = new ArrayBuffer(4); const b = Buffer.from(ab); new Uint8Array(ab)[0] = 7; console.log('shared', b[0]);
const b2 = Buffer.from(ab, 1, 2); new Uint8Array(ab)[1] = 9; console.log('shared offset', b2[0], b2.length);
console.log(Buffer.from([1, 0, 255]));
console.log([Buffer.from([1])]);
console.log({ buf: Buffer.from('hi') });
console.log({ 'a\0b': 1 });
console.log(require('util').inspect({ 'a\0b': 'c\0d' }));
console.log(new Uint8Array([1, 2]));
const ab2 = new ArrayBuffer(8);
for (const args of [[2], [2, 3], [8], [9], [2, 7], [0, 0]]) { try { const x = Buffer.from(ab2, ...args); console.log('range', args, x.length, x.byteOffset); } catch (e) { console.log('range', args, e.name, e.code, e.message); } }
fs.writeSync(2, Buffer.from([0x41, 0, 0x42, 0x0a]));
const u = require('util');
const cases = [Buffer.from([1, 0]), Buffer.alloc(0), Buffer.alloc(53, 1), Buffer.alloc(51), new Uint8Array([1, 2]),
  { 'a-b': 1, 'a\0b': 'c\0d' }, { $x: 1, '1a': 2, ok: 3 }, "it's", 'both \' and "', 'all \' " `', 'tab\there\x7f\x1b',
  'back\\slash', 'lone \ud800 x', 'pair 😀', ['a\0', { k: Buffer.from('hi') }]];
for (const c of cases) { console.log(c); console.log(u.inspect(c)); }
