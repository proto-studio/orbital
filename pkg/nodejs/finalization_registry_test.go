package nodejs

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// FinalizationRegistry cleanup callbacks behave as in Node 20 (run there
// with --expose-gc): a batch of callbacks runs, then their ticks, then their
// promise jobs; they run after the current phase's callbacks; an exception
// reaches 'uncaughtException'; and the constructor keeps Node's shape and
// errors. KeyObject can't be constructed from outside.
func TestFinalizationRegistry_MatchesNode(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const order = [];
const errors = [];
process.on('uncaughtException', (e) => errors.push(e.message));
const reg = new FinalizationRegistry((held) => {
  order.push('cleanup ' + held);
  process.nextTick(() => order.push('tick ' + held));
  Promise.resolve().then(() => order.push('promise ' + held));
  if (held === 'b') throw new Error('from cleanup');
});
(() => { reg.register({}, 'a'); reg.register({}, 'b'); })();
const shape = [];
try { FinalizationRegistry(() => {}); } catch (e) { shape.push(e.message); }
try { new FinalizationRegistry(5); } catch (e) { shape.push(e.message); }
class Sub extends FinalizationRegistry {}
const s = new Sub(() => {});
shape.push(s instanceof Sub, s instanceof FinalizationRegistry, FinalizationRegistry.prototype.constructor === FinalizationRegistry,
  FinalizationRegistry.name, FinalizationRegistry.length);
const { KeyObject } = require('crypto');
for (const args of [['secret', 1], [1]]) {
  try { new KeyObject(...args); shape.push('constructed'); } catch (e) { shape.push(e.code + ' ' + e.message); }
}
// A batch: every cleanup callback runs, then their ticks, then their promise jobs.
const batch = [];
const reg2 = new FinalizationRegistry((held) => {
  batch.push('cleanup');
  process.nextTick(() => batch.push('tick'));
  Promise.resolve().then(() => batch.push('promise'));
});
(() => { for (let i = 0; i < 3; i++) reg2.register({}, i); })();
// The phase finishes first: an immediate that collects runs before the cleanup, and so
// does the next immediate in the same batch.
const phase = [];
const reg3 = new FinalizationRegistry((held) => phase.push('cleanup ' + held));
setTimeout(() => {
  gc();
  setTimeout(() => {
    (() => { reg3.register({}, 'x'); })();
    setImmediate(() => { phase.push('i1'); gc(); });
    setImmediate(() => phase.push('i2'));
    setTimeout(() => {
      done({ order: order.filter((s) => s.endsWith(' a')), errors, shape, batch, phase });
    }, 30);
  }, 20);
}, 10);
`)
	want := map[string]any{
		"order":  []any{"cleanup a", "tick a", "promise a"},
		"errors": []any{"from cleanup"},
		"shape": []any{
			"Constructor FinalizationRegistry requires 'new'",
			"FinalizationRegistry: cleanup must be callable",
			true, true, true, "FinalizationRegistry", float64(1),
			`ERR_INVALID_ARG_TYPE The "handle" argument must be of type object. Received type number (1)`,
			"ERR_INVALID_ARG_VALUE The argument 'type' is invalid. Received 1",
		},
	}
	want["batch"] = []any{"cleanup", "cleanup", "cleanup", "tick", "tick", "tick", "promise", "promise", "promise"}
	want["phase"] = []any{"i1", "i2", "cleanup x"}
	wantJSON(t, got, want)
}
