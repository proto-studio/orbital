package nodejs

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A long buffer of writes that complete synchronously must drain without recursing.
func TestWritable_SynchronousWritesDrainIteratively(t *testing.T) {
	const prelude = `
const { Writable } = require('stream');
const COUNT = 20000;
const run = (options, start) => new Promise((resolve, reject) => {
  let bytes = 0, order = true, callbacks = 0;
  const w = new Writable(options(
    (chunk) => { if (+chunk !== bytes) order = false; bytes++; },
  ));
  w.on('error', reject);
  w.on('finish', () => resolve({ bytes, order, callbacks, left: w.writableLength }));
  start(w);
  for (let i = 0; i < COUNT; i++) w.write(String(i), () => { callbacks++; });
  w.end();
});
const fail = (e) => done({ error: String(e) });
`
	want := map[string]any{"bytes": float64(20000), "order": true, "callbacks": float64(20000), "left": float64(0)}
	for name, body := range map[string]string{
		"corked": `
run((seen) => ({ write(c, e, cb) { seen(c); cb(); } }), (w) => w.cork()).then(done, fail);`,
		"behind an asynchronous write": `
let first = true;
run((seen) => ({ write(c, e, cb) {
  seen(c);
  if (first) { first = false; setImmediate(cb); } else cb();
} }), () => {}).then(done, fail);`,
		"after uncork": `
let bytes = 0;
const w = new Writable({ write(c, e, cb) { bytes++; cb(); } });
w.on('error', fail);
w.on('finish', () => done({ bytes, order: true, callbacks: 20000, left: w.writableLength }));
w.cork();
for (let i = 0; i < COUNT; i++) w.write('a');
w.uncork();
w.end();`,
	} {
		t.Run(name, func(t *testing.T) {
			wantJSON(t, runJS(t, &runtime.Config{}, prelude+body), want)
		})
	}

	// Data written from inside a synchronous _writev has nothing else to flush it.
	t.Run("written during a synchronous writev", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, `
const { Writable } = require('stream');
const batches = [];
const w = new Writable({
  writev(entries, cb) {
    batches.push(entries.map((e) => String(e.chunk)).join(''));
    if (batches.length === 1) w.write('c');
    cb();
  },
  write(chunk, e, cb) {
    batches.push(String(chunk));
    cb();
  },
});
w.on('finish', () => done({ batches, left: w.writableLength }));
w.cork();
w.write('a');
w.write('b');
w.uncork();
w.end();`)
		wantJSON(t, result, map[string]any{"batches": []any{"ab", "c"}, "left": float64(0)})
	})

	// A _write that throws after calling back must not leave the flush marked as running,
	// which would strand whatever is still buffered.
	t.Run("a throw from write does not stop later flushes", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, `
const { Writable } = require('stream');
const seen = [];
let thrown = 0;
const w = new Writable({ write(chunk, e, cb) {
  seen.push(String(chunk));
  cb();
  if (String(chunk) === 'a') throw new Error('boom');
} });
w.on('finish', () => done({ seen: seen.sort(), thrown }));
w.cork();
w.write('a');
w.write('b');
try { w.uncork(); } catch (e) { thrown++; }
w.write('c');
w.end();`)
		wantJSON(t, result, map[string]any{"seen": []any{"a", "b", "c"}, "thrown": float64(1)})
	})
}

// An exception thrown by _write, _writev or _transform propagates out of
// write(), end() or uncork() instead of becoming the stream's 'error'. A
// buffered write that throws later reaches 'uncaughtException'.
func TestWritable_WriteExceptionReachesCaller(t *testing.T) {
	result := runJS(t, &runtime.Config{}, `
const { Writable, Transform, Duplex } = require('stream');
const out = []; const log = (...a) => out.push(a.join(' '));
// 1 basic
{
  const w = new Writable({ write() { throw new Error('in write'); } });
  w.on('error', e => log('1 stream error', e.message));
  try { log('1 ret', w.write('a')); } catch (e) { log('1 caller caught', e.message); }
  log('1 state', w.writableLength, w.writableNeedDrain, w.destroyed);
  try { log('1 second write ret', w.write('b')); } catch (e) { log('1 second caught', e.message); }
  log('1 len', w.writableLength);
}
// 2 not implemented
{
  const w = new Writable();
  w.on('error', e => log('2 stream error', e.code));
  try { w.write('a'); log('2 no throw'); } catch (e) { log('2 caught', e.code); }
}
// 3 writev throw on uncork
{
  const w = new Writable({ write(c, e, cb) { cb(); }, writev() { throw new Error('in writev'); } });
  w.on('error', e => log('3 stream error', e.message));
  w.cork(); w.write('a'); w.write('b');
  try { w.uncork(); log('3 no throw'); } catch (e) { log('3 caught', e.message); }
}
// 4 transform throws
{
  const t = new Transform({ transform() { throw new Error('in transform'); } });
  t.on('error', e => log('4 stream error', e.message));
  try { t.write('a'); log('4 no throw'); } catch (e) { log('4 caught', e.message); }
}
// 5 throw after cb
{
  const w = new Writable({ write(c, e, cb) { cb(); throw new Error('after cb'); } });
  w.on('error', e => log('5 stream error', e.message));
  try { w.write('a'); log('5 no throw'); } catch (e) { log('5 caught', e.message); }
}
// 6 throw from buffered write processed asynchronously -> uncaught
{
  let n = 0;
  const w = new Writable({ write(c, e, cb) { if (n++ === 0) setTimeout(cb, 1); else throw new Error('buffered'); } });
  w.on('error', e => log('6 stream error', e.message));
  w.write('a'); w.write('b');
  process.on('uncaughtException', e => log('6 uncaught', e.message));
}
// 7 end with throwing write
{
  const w = new Writable({ write() { throw new Error('end write'); } });
  w.on('error', e => log('7 stream error', e.message));
  try { w.end('x'); log('7 no throw'); } catch (e) { log('7 caught', e.message); }
}
// 8 duplex
{
  const d = new Duplex({ read() {}, write() { throw new Error('dup'); } });
  try { d.write('a'); log('8 no throw'); } catch (e) { log('8 caught', e.message); }
}
setTimeout(() => done({ log: out }), 20);
`)
	// Captured from Node 20.18.1.
	wantJSON(t, result, map[string]any{"log": []any{
		"1 caller caught in write",
		"1 state 1 false false",
		"1 second write ret true",
		"1 len 2",
		"2 caught ERR_METHOD_NOT_IMPLEMENTED",
		"3 caught in writev",
		"4 caught in transform",
		"5 caught after cb",
		"7 caught end write",
		"8 caught dup",
		"6 uncaught buffered",
	}})
}
