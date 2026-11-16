// Runs unchanged in Node and in Orbital. timers_promisify.node.json is this script's output
// under Node v20.18.1; regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestTimers_PromisifyMatchesNode
const util = require('util');
const tp = require('timers/promises');
const timers = require('timers');
const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const errorOf = (e) => [e.name, e.code, e.message, e.cause === undefined ? 'no cause' : String(e.cause)];
const c = util.promisify.custom;

(async () => {
  const out = {};
  out.identity = [util.promisify(setTimeout) === tp.setTimeout, util.promisify(setImmediate) === tp.setImmediate,
    setTimeout[c] === tp.setTimeout, setImmediate[c] === tp.setImmediate, typeof setInterval[c],
    timers.promises === tp, util.promisify(timers.setTimeout) === tp.setTimeout,
    Object.keys(setTimeout).includes(c), setTimeout.name, setImmediate.name];

  const sleep = util.promisify(setTimeout);
  out.sleep = [await sleep(5, 'value'), await sleep(), await util.promisify(setImmediate)('iv')];

  const ac = new AbortController();
  const pending = tp.setTimeout(1000, 'x', { signal: ac.signal });
  ac.abort();
  out.abort = await pending.catch(errorOf);
  const reason = new AbortController();
  reason.abort('why');
  out.preAborted = [await tp.setImmediate('x', { signal: reason.signal }).catch(errorOf),
    await tp.setTimeout(1, 'x', { signal: reason.signal }).catch(errorOf)];
  const late = new AbortController();
  out.abortAfterResolve = await tp.setTimeout(1, 'done', { signal: late.signal });
  late.abort();

  out.validation = await Promise.all([
    tp.setTimeout('x'), tp.setTimeout(1, 1, 'nope'), tp.setTimeout(1, 1, null), tp.setTimeout(1, 1, { signal: 1 }),
    tp.setImmediate(1, { ref: 1 }), tp.setImmediate(1, []),
  ].map((p) => p.then(() => 'resolved', errorOf)));
  out.intervalValidation = await tp.setInterval('x').next().then(() => 'resolved', errorOf);

  const vals = [];
  for await (const v of tp.setInterval(5, 'iv')) {
    vals.push(v);
    if (vals.length === 3) break;
  }
  out.interval = vals;
  const stop = new AbortController();
  const it = tp.setInterval(5, 'tick', { signal: stop.signal });
  const first = await it.next();
  setTimeout(() => stop.abort('enough'), 1);
  out.intervalAbort = [first, await it.next().catch(errorOf)];

  out.scheduler = [await tp.scheduler.wait(1), await tp.scheduler.yield(),
    (() => { try { new tp.scheduler.constructor(); } catch (e) { return errorOf(e); } })(),
    (() => { try { tp.scheduler.wait.call({}, 1); } catch (e) { return errorOf(e); } })()];

  finish(out);
})();
