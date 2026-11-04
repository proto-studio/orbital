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
