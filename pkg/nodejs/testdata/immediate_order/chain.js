const log = globalThis.note || console.log;
// A chain of immediates interleaves with an interval, one immediate per turn.
let n = 0;
const iv = setInterval(() => { log('interval'); }, 1);
function next() {
  log('immediate ' + n);
  const end = Date.now() + 2; while (Date.now() < end);
  if (++n < 3) setImmediate(next);
  else clearInterval(iv);
}
setImmediate(next);
{ const end = Date.now() + 3; while (Date.now() < end); }
