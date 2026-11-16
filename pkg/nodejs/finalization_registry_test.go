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

// Per-task semantics, as in Node 20 run with --expose-gc: a cleanup callback can
// unregister the others with its token; each registry's ticks and promise jobs
// run before the next registry's cleanup; an exception stops the batch and is
// reported before its ticks; and cleanup left by the last callback runs before
// 'beforeExit'.
func TestFinalizationRegistry_TasksMatchNode(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `const out = {};
// (a) unregister from a cleanup callback cancels the others with the token.
const a = [];
const token = {};
const regA = new FinalizationRegistry((held) => { a.push(held, regA.unregister(token)); });
(() => { regA.register({}, 'a1', token); regA.register({}, 'a2', token); })();
// (b) two registries: each one's ticks and promise jobs run before the next one's cleanup.
const b = [];
const mk = (name) => new FinalizationRegistry(() => {
  b.push('cleanup ' + name);
  process.nextTick(() => b.push('tick ' + name));
  Promise.resolve().then(() => b.push('promise ' + name));
});
const r1 = mk('A'), r2 = mk('B');
(() => { r1.register({}, 1); r2.register({}, 2); })();
// (c) an exception stops the batch; its ticks run before the next cleanup.
const c = [];
process.on('uncaughtException', (e) => c.push('error ' + e.message));
const regC = new FinalizationRegistry((held) => {
  c.push('cleanup');
  process.nextTick(() => c.push('tick'));
  Promise.resolve().then(() => c.push('promise'));
  throw new Error('x');
});
(() => { regC.register({}, 1); regC.register({}, 2); })();
setTimeout(() => {
  gc();
  setTimeout(() => {
    out.a = a;
    // b's registries may be cleaned in either order; check the grouping.
    const first = b[0] && b[0].slice(-1);
    const second = first === 'A' ? 'B' : 'A';
    out.b = b.join() === ['cleanup', 'tick', 'promise'].map((s) => s + ' ' + first).concat(['cleanup', 'tick', 'promise'].map((s) => s + ' ' + second)).join();
    out.c = c;
    // (d) the last callback, an I/O completion, collects; cleanup runs before 'beforeExit'.
    const d = [];
    const regD = new FinalizationRegistry(() => d.push('cleanup'));
    (() => { regD.register({}, 1); })();
    process.once('beforeExit', () => { d.push('beforeExit'); out.d = d; done(out); });
    require('crypto').randomBytes(4, () => gc());
  }, 30);
}, 10);
`)
	want := map[string]any{
		"a": []any{"a2", true},
		"b": true,
		"c": []any{"cleanup", "error x", "tick", "promise", "cleanup", "error x", "tick", "promise"},
		"d": []any{"cleanup", "beforeExit"},
	}
	wantJSON(t, got, want)
}

// A rejection a cleanup task leaves unhandled is reported before the next
// registry's cleanup runs, as in Node 20 (run with --expose-gc).
func TestFinalizationRegistry_RejectionsBetweenTasks(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const order = [];
process.on('unhandledRejection', (r) => order.push('unhandled ' + r));
const mk = (name) => new FinalizationRegistry(() => { order.push('cleanup ' + name); Promise.reject(name); });
const r1 = mk('A'), r2 = mk('B');
(() => { r1.register({}, 1); r2.register({}, 2); })();
setTimeout(() => {
  gc();
  setTimeout(() => {
    const x = order[0].slice(-1), y = x === 'A' ? 'B' : 'A';
    done({ ok: order.join() === ['cleanup ' + x, 'unhandled ' + x, 'cleanup ' + y, 'unhandled ' + y].join(), order });
  }, 30);
}, 10);
`)
	if got["ok"] != true {
		t.Errorf("order = %v, want each cleanup followed by its unhandledRejection", got["order"])
	}
}

// Cleanup runs before the loop waits: an I/O callback that collects while a
// later timer keeps the loop alive gets its cleanup before that timer; and an
// unhandledRejection listener's tick runs before the next registry's cleanup.
// Both as in Node 20 run with --expose-gc.
func TestFinalizationRegistry_BeforeWaitingMatchesNode(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `const out = {};
// (e) an I/O callback collects while a later timer keeps the loop alive: cleanup runs first.
const e = [];
const regE = new FinalizationRegistry(() => e.push('cleanup'));
// (f) an unhandledRejection listener's tick runs before the next registry's cleanup.
const f = [];
process.on('unhandledRejection', (r) => { f.push('unhandled ' + r); process.nextTick(() => f.push('tick ' + r)); });
const mk = (name) => new FinalizationRegistry(() => { f.push('cleanup ' + name); Promise.reject(name); });
const f1 = mk('A'), f2 = mk('B');
(() => { f1.register({}, 1); f2.register({}, 2); })();
setTimeout(() => {
  gc();
  setTimeout(() => {
    const x = f[0].slice(-1), y = x === 'A' ? 'B' : 'A';
    out.f = f.join() === ['cleanup ' + x, 'unhandled ' + x, 'tick ' + x, 'cleanup ' + y, 'unhandled ' + y, 'tick ' + y].join() || f;
    (() => { regE.register({}, 1); })();
    require('crypto').randomBytes(4, () => { e.push('io'); gc(); });
    setTimeout(() => { e.push('timer'); out.e = e; done(out); }, 40);
  }, 30);
}, 10);
`)
	want := map[string]any{"f": true, "e": []any{"io", "cleanup", "timer"}}
	wantJSON(t, got, want)
}
