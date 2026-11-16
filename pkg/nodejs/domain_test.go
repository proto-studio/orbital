package nodejs

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A domain follows the work started inside it: timers, immediates, intervals, nextTick
// callbacks and event emitters created in it run with it active, and their uncaught
// exceptions reach its 'error' listeners. Expected values are what Node 20.18.1 prints.
func TestDomain_FollowsAsyncWork(t *testing.T) {
	notes, err := runUncaught(t, `
const domain = require('domain');
const EventEmitter = require('events');
const log = (...a) => note(a.join(' '));
const cases = {
  timer(next) {
    const d = domain.create();
    d.on('error', (e) => { log('caught', e.message, e.domain === d, e.domainThrown, process.domain === d); next(); });
    d.run(() => setTimeout(() => { throw new Error('in timer'); }, 1));
  },
  immediate(next) {
    const d = domain.create();
    d.on('error', (e) => { log('caught', e.message); next(); });
    d.run(() => setImmediate(() => { throw new Error('in immediate'); }));
  },
  tick(next) {
    const d = domain.create();
    d.on('error', (e) => { log('caught', e.message); next(); });
    d.run(() => process.nextTick(() => { log('active in tick', process.domain === d); throw new Error('in tick'); }));
  },
  interval(next) {
    const d = domain.create();
    let n = 0;
    const h = d.run(() => setInterval(() => { if (++n === 2) { clearInterval(h); throw new Error('in interval'); } }, 1));
    d.on('error', (e) => { log('caught', e.message, n); next(); });
  },
  nested(next) {
    const outer = domain.create(), inner = domain.create();
    outer.on('error', (e) => log('outer', e.message));
    inner.on('error', (e) => { log('inner', e.message); next(); });
    outer.run(() => inner.run(() => setTimeout(() => { log('active', process.domain === inner); throw new Error('nested'); }, 1)));
  },
  sync(next) {
    const d = domain.create();
    d.on('error', (e) => log('caught sync', e.message));
    try { d.run(() => { throw new Error('sync'); }); } catch (e) { log('run rethrew', e.message, process.domain === d); }
    d.exit();
    setTimeout(next, 1);
  },
  emitter(next) {
    const d = domain.create();
    let em;
    d.on('error', (e) => { log('caught', e.message, e.domainEmitter === em, e.domainThrown); next(); });
    d.run(() => { em = new EventEmitter(); });
    log('bound', em.domain === d);
    em.emit('error', new Error('emitted'));
  },
  emitterListener(next) {
    const d = domain.create();
    let em;
    d.on('error', (e) => { log('caught', e.message); next(); });
    d.run(() => { em = new EventEmitter(); });
    em.on('x', () => { log('active in listener', process.domain === d); });
    setTimeout(() => { em.emit('x'); setTimeout(() => em.emit('error', new Error('later')), 1); }, 1);
  },
  noHandler(next) {
    const d = domain.create();
    process.once('uncaughtException', (e) => { log('process caught', e.message, e.domain === d); next(); });
    d.run(() => setTimeout(() => { throw new Error('unhandled in domain'); }, 1));
  },
  bindIntercept(next) {
    const d = domain.create();
    let count = 0;
    d.on('error', (e) => { log('caught', e.message); if (++count === 2) next(); });
    const f = d.bind(() => { throw new Error('bound'); });
    setTimeout(f, 1);
    const g = d.intercept((v) => log('intercepted value', v));
    g(null, 5);
    setTimeout(() => g(new Error('intercepted error')), 2);
  },

};
const names = Object.keys(cases);
const run = (i) => { if (i === names.length) { note('done'); return; } log('== ' + names[i]); cases[names[i]](() => setTimeout(() => run(i + 1), 5)); };
run(0);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{
		"== timer",
		"caught in timer true true false",
		"== immediate",
		"caught in immediate",
		"== tick",
		"active in tick true",
		"caught in tick",
		"== interval",
		"caught in interval 2",
		"== nested",
		"active true",
		"inner nested",
		"== sync",
		"run rethrew sync true",
		"== emitter",
		"bound true",
		"caught emitted true false",
		"== emitterListener",
		"active in listener true",
		"caught later",
		"== noHandler",
		"process caught unhandled in domain false",
		"== bindIntercept",
		"intercepted value 5",
		"caught bound",
		"caught intercepted error",
		"done",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes =\n%s\nwant\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}
}

// As in Node, run() lets an exception through to its caller, and an uncaught one reaches
// the domain; a domain's handler that throws ends the program with code 7.
func TestDomain_RunAndHandlerThrows(t *testing.T) {
	notes, err := runUncaught(t, `
const d = require('domain').create();
d.on('error', (e) => note('domain caught', e.message, e.domainThrown));
d.run(() => { throw new Error('sync top'); });
note('not reached');
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"domain caught sync top true"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}

	_, err = runUncaught(t, `
const domain = require('domain');
const outer = domain.create(), inner = domain.create();
outer.on('error', () => note('outer'));
inner.on('error', (e) => { throw new Error('handler threw ' + e.message); });
outer.run(() => inner.run(() => setTimeout(() => { throw new Error('x'); }, 1)));
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 7 || !strings.Contains(uncaught.Message, "handler threw x") {
		t.Errorf("Run returned %v, want exit 7 with the handler's error", err)
	}
}

// The domain module and process.setUncaughtExceptionCaptureCallback exclude each other,
// with Node's error codes.
func TestDomain_CaptureCallbackExclusive(t *testing.T) {
	notes, err := runUncaught(t, `
try { process.setUncaughtExceptionCaptureCallback(() => {}); require('domain').create(); } catch (e) { note(e.code); }
process.setUncaughtExceptionCaptureCallback(null);
require('domain').create();
try { process.setUncaughtExceptionCaptureCallback(() => {}); } catch (e) { note(e.code); }
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"ERR_DOMAIN_CALLBACK_NOT_AVAILABLE", "ERR_DOMAIN_CANNOT_SET_UNCAUGHT_EXCEPTION_CAPTURE"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// With no domain listening, process 'uncaughtException' listeners run with no domain
// entered, and removing a domain's 'error' listeners lets exceptions through to them.
// Expected values are Node 20.18.1's.
func TestDomain_ProcessHandlerRunsOutsideDomain(t *testing.T) {
	notes, err := runUncaught(t, `
const domain = require('domain');
const d = domain.create();
process.once('uncaughtException', (e) => {
  note('process handler', e.message, process.domain === d, domain._stack.length);
  setTimeout(() => {
    note('later', domain._stack.length, String(domain.active));
    const d2 = domain.create();
    d2.on('error', () => note('domain got it'));
    process.once('uncaughtException', (e2) => note('process handler', e2.message));
    d2.run(() => { d2.removeAllListeners('error'); throw new Error('removed'); });
  }, 5);
});
d.run(() => { throw new Error('boom'); });
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"process handler boom false 0", "later 0 null", "process handler removed"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// Schedulers a program took before creating its first domain still follow domains, as
// Node's async hooks make them do.
func TestDomain_CachedSchedulers(t *testing.T) {
	notes, err := runUncaught(t, `
const { setTimeout: cachedTimeout, setImmediate: cachedImmediate } = require('timers');
const nextTick = process.nextTick;
const domain = require('domain');
const d = domain.create();
let n = 0;
d.on('error', (e) => { note('caught', e.message); if (++n === 3) note('done'); });
d.run(() => {
  cachedTimeout(() => { throw new Error('timeout'); }, 1);
  cachedImmediate(() => { throw new Error('immediate'); });
  nextTick(() => { throw new Error('tick'); });
});
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	// Which of the timer and the immediate runs first is up to timing, in Node too.
	sort.Strings(notes)
	want := []string{"caught immediate", "caught tick", "caught timeout", "done"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// A domain follows promise reactions registered while it is active, and a rejection made
// while it is active goes to its 'error' listeners rather than 'unhandledRejection'; with
// no listener it becomes an uncaught exception. Expected values are Node 20.18.1's.
func TestDomain_FollowsPromises(t *testing.T) {
	for name, tc := range map[string]struct {
		script string
		want   []string
	}{
		"then throws": {`
const d = require('domain').create();
d.on('error', (e) => note('caught', e.message, process.domain === d, e.domain === d));
process.on('unhandledRejection', (r) => note('unhandledRejection', r.message));
d.run(() => Promise.resolve().then(() => { note('active in then', process.domain === d); throw new Error('in then'); }));
`, []string{"active in then true", "caught in then false false"}},
		"rejected in run": {`
const d = require('domain').create();
d.on('error', (e) => note('caught', String(e)));
d.run(() => { Promise.reject(new Error('direct')); });
`, []string{"caught Error: direct"}},
		"catch runs inside": {`
const d = require('domain').create();
d.on('error', (e) => note('domain', e.message));
d.run(() => Promise.reject(new Error('c')).catch((e) => note('catch active', process.domain === d, e.message)));
`, []string{"catch active true c"}},
		"no error listener": {`
const d = require('domain').create();
process.on('unhandledRejection', (r) => note('unhandledRejection', r.message));
process.on('uncaughtException', (e, o) => note('uncaught', e.message, o));
d.run(() => Promise.reject(new Error('nolistener')));
`, []string{"uncaught nolistener uncaughtException"}},
		"after the reaction": {`
const d = require('domain').create();
d.on('error', (e) => note('domain caught', e.message));
process.on('unhandledRejection', (r) => note('unhandledRejection', r.message));
d.run(() => {
  Promise.resolve().then(() => { throw new Error('thrown'); }).catch(() => note('thrown caught by catch'));
});
Promise.resolve().then(() => note('unrelated sees domain', process.domain === d));
d.run(() => Promise.resolve().then(() => Promise.reject(new Error('adopted'))));
d.run(() => Promise.resolve().then(() => { throw new Error('plain throw'); }));
`, []string{"unrelated sees domain false", "thrown caught by catch", "domain caught plain throw", "domain caught adopted"}},
		"by promise, not reason": {`
const d = require('domain').create();
d.on('error', (e) => note('domain caught', e.message));
process.on('unhandledRejection', (r) => note('unhandledRejection', r.message));
const error = new Error('shared');
d.run(() => Promise.resolve().then(() => { throw error; }).catch(() => note('caught by catch')));
setTimeout(() => Promise.reject(error), 10);
d.run(() => Promise.resolve().then(() => ({ then(resolve, reject) { reject(new Error('thenable')); } })));
let p; d.run(() => { p = Promise.resolve().then(() => p); });
`, []string{"caught by catch", "domain caught Chaining cycle detected for promise #<Promise>", "domain caught thenable", "unhandledRejection shared"}},
		"outside any domain": {`
require('domain').create();
process.on('unhandledRejection', (r) => note('unhandledRejection', r.message, typeof Promise.prototype.then));
Promise.reject(new Error('plain'));
`, []string{"unhandledRejection plain function"}},
	} {
		t.Run(name, func(t *testing.T) {
			notes, err := runUncaught(t, tc.script)
			if err != nil {
				t.Fatalf("Run returned %v", err)
			}
			if !reflect.DeepEqual(notes, tc.want) {
				t.Errorf("notes = %q, want %q", notes, tc.want)
			}
		})
	}
}
