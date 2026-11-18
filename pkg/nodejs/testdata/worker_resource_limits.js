// Prints worker_threads.resourceLimits as scripts see it, as JSON: on the main thread, in
// workers given resourceLimits (before they start, once online, inside, and after exit),
// and for a worker that reaches its heap limit. Fields the script didn't set depend on the
// engine and the machine, so for those only their type is recorded.
const { Worker, resourceLimits } = require('worker_threads');
const report = typeof done === 'function' ? done : (v) => console.log(JSON.stringify(v));

function shape(limits, given) {
  const out = {};
  for (const key of Object.keys(limits)) {
    out[key] = given && typeof given[key] === 'number' && given[key] > 0 ? limits[key] : typeof limits[key];
  }
  return out;
}

const inner = `
const { resourceLimits, parentPort } = require('worker_threads');
parentPort.postMessage(resourceLimits);
`;

async function run(given) {
  const w = new Worker(inner, { eval: true, resourceLimits: given });
  const result = { constructed: w.resourceLimits };
  await new Promise((resolve) => w.once('online', resolve));
  result.online = shape(w.resourceLimits, given);
  const message = await new Promise((resolve) => w.once('message', resolve));
  result.inside = shape(message, given);
  result.sameInside = JSON.stringify(message) === JSON.stringify(w.resourceLimits);
  result.exitCode = await new Promise((resolve) => w.once('exit', resolve));
  result.afterExit = w.resourceLimits;
  return result;
}

(async () => {
  const out = { main: resourceLimits };
  out.lowered = await run({ maxOldGenerationSizeMb: 16, maxYoungGenerationSizeMb: 4, codeRangeSizeMb: 8, stackSizeMb: 2 });
  out.oldOnly = await run({ maxOldGenerationSizeMb: 24 });
  out.ignored = await run({ maxOldGenerationSizeMb: '16', maxYoungGenerationSizeMb: -1, codeRangeSizeMb: 0 });
  out.none = await run(undefined);

  const w = new Worker('const a = []; for (;;) a.push({ x: Math.random(), y: [1, 2, 3] });', {
    eval: true,
    resourceLimits: { maxOldGenerationSizeMb: 8 },
  });
  const events = [];
  w.on('error', (e) => events.push({
    event: 'error', name: e.name, code: e.code, message: e.message, keys: Object.keys(e),
    limits: w.resourceLimits,
  }));
  w.on('exit', (code) => {
    events.push({ event: 'exit', code, limits: w.resourceLimits });
    out.outOfMemory = events;
    report(out);
  });
})();
