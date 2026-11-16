// Runs unchanged in Node and in Orbital. readable_from.node.json is this script's output
// under Node v20.18.1; regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestReadableFrom_MatchesNode
const { Readable } = require('stream');
const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const errorOf = (e) => [e.name, e.code, e.message];

// Records the order 'data', 'end', 'close' and 'error' happen in, relative to a nextTick
// and a promise reaction queued right after the stream is made.
function order(make) {
  return new Promise((resolve) => {
    const seen = [];
    let r;
    try {
      r = make();
    } catch (e) {
      resolve(errorOf(e));
      return;
    }
    r.on('data', (d) => seen.push('data ' + (Buffer.isBuffer(d) ? 'buffer ' + d : typeof d + ' ' + d)));
    r.on('end', () => seen.push('end'));
    r.on('error', (e) => seen.push('error ' + e.code + ' ' + e.message));
    r.on('close', () => { seen.push('close'); setTimeout(() => resolve(seen), 5); });
    Promise.resolve().then(() => seen.push('promise'));
    process.nextTick(() => seen.push('tick'));
  });
}

(async () => {
  const out = {};
  out.array = await order(() => Readable.from(['a', 'b']));
  out.generator = await order(() => Readable.from((function* () { yield 1; yield 2; yield 3; })()));
  out.asyncGenerator = await order(() => Readable.from((async function* () { yield 'x'; yield 'y'; })()));
  out.promiseValues = await order(() => Readable.from([Promise.resolve('p1'), 'plain', Promise.resolve('p2')]));
  out.string = await order(() => Readable.from('abc'));
  out.buffer = await order(() => Readable.from(Buffer.from('buf')));
  out.stringNotObjectMode = await order(() => Readable.from('abc', { objectMode: false }));
  out.nullValue = await order(() => Readable.from(['a', null, 'b']));
  out.throwing = await order(() => Readable.from((function* () { yield 'ok'; throw new Error('gen failed'); })()));
  out.notIterable = await order(() => Readable.from(5));
  out.nullArg = await order(() => Readable.from(null));

  // Destroying calls the iterator's return, and an error goes to its throw first.
  const calls = [];
  const iterable = {
    [Symbol.iterator]() {
      let i = 0;
      return {
        next: () => ({ value: i++, done: false }),
        return: () => { calls.push('return'); return { done: true }; },
        throw: (e) => { calls.push('throw ' + e.message); return { done: false }; },
      };
    },
  };
  const r = Readable.from(iterable);
  r.on('error', () => {});
  await new Promise((resolve) => { r.on('close', resolve); r.destroy(new Error('stop')); });
  out.destroyCalls = calls;

  const consumed = [];
  for await (const v of Readable.from(new Set([1, 2, 3]))) consumed.push(v);
  out.forAwait = consumed;
  finish(out);
})();
