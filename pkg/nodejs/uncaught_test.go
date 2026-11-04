package nodejs

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// runUncaught runs source and returns what it passed to note(), and Run's error.
func runUncaught(t *testing.T, source string) ([]string, error) {
	t.Helper()
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	prelude := `globalThis.__notes = []; globalThis.note = (...a) => __notes.push(a.join(' '));`
	if _, err := inst.Runtime.RunScript(prelude, "prelude.js"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run(source, "test.js")
	if inst.Runtime.IsKilled() {
		t.Fatalf("killed: %s", inst.Runtime.KillReason())
	}
	val, err := inst.Runtime.RunScript(`JSON.stringify(__notes)`, "notes.js")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	if err := json.Unmarshal([]byte(val.String()), &notes); err != nil {
		t.Fatal(err)
	}
	return notes, runErr
}

func supportsRejections(t *testing.T) {
	t.Helper()
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if !inst.Runtime.TracksRejections() {
		t.Skip("the linked V8 glue does not report promise rejections")
	}
}

const (
	netThrower = `
const net = require('net');
let client;
const server = net.createServer((socket) => {
  socket.on('data', () => { throw new Error('boom'); });
});
const cleanup = () => { server.close(); client.destroy(); };
server.listen(0, '127.0.0.1', () => {
  client = net.connect(server.address().port, '127.0.0.1', () => client.write('x'));
});
`
	httpThrower = `
const http = require('http');
const server = http.createServer(() => { throw new Error('boom'); });
const cleanup = () => { server.close(); server.closeAllConnections(); };
server.listen(0, '127.0.0.1', () => {
  http.get({ port: server.address().port, host: '127.0.0.1' }).on('error', () => {});
});
`
)

// The expectations in the tests below are what Node 20.18.1 does with the same script.

func TestUncaughtCallbackWithHandler(t *testing.T) {
	throwers := map[string]string{
		"setTimeout":     `const cleanup = () => {}; setTimeout(() => { throw new Error('boom'); }, 1);`,
		"setImmediate":   `const cleanup = () => {}; setImmediate(() => { throw new Error('boom'); });`,
		"nextTick":       `const cleanup = () => {}; process.nextTick(() => { throw new Error('boom'); });`,
		"queueMicrotask": `const cleanup = () => {}; queueMicrotask(() => { throw new Error('boom'); });`,
		"fs":             `const cleanup = () => {}; require('fs').stat('.', () => { throw new Error('boom'); });`,
		"socket":         netThrower,
		"http handler":   httpThrower,
	}
	for name, thrower := range throwers {
		t.Run(name, func(t *testing.T) {
			notes, err := runUncaught(t, `
process.on('uncaughtExceptionMonitor', (e, origin) => note('monitor', e.message, origin));
process.on('uncaughtException', (e, origin) => {
  note('handler', e.message, origin);
  cleanup();
  setTimeout(() => note('still running'), 1);
});
`+thrower)
			if err != nil {
				t.Fatalf("Run returned %v", err)
			}
			want := []string{"monitor boom uncaughtException", "handler boom uncaughtException", "still running"}
			if !reflect.DeepEqual(notes, want) {
				t.Errorf("notes = %q, want %q", notes, want)
			}
		})
	}
}

func TestUncaughtCallbackWithoutHandler(t *testing.T) {
	throwers := map[string]string{
		"setTimeout":   `setTimeout(() => { throw new Error('boom'); }, 1);`,
		"nextTick":     `process.nextTick(() => { throw new Error('boom'); });`,
		"socket":       netThrower,
		"http handler": httpThrower,
	}
	for name, thrower := range throwers {
		t.Run(name, func(t *testing.T) {
			notes, err := runUncaught(t, `
process.on('exit', (code) => note('exit', code, process.exitCode));
setTimeout(() => note('must not run'), 200);
`+thrower)
			var uncaught *runtime.UncaughtError
			if !errors.As(err, &uncaught) {
				t.Fatalf("Run returned %v, want an UncaughtError", err)
			}
			if uncaught.ExitCode != 1 || uncaught.Origin != "uncaughtException" {
				t.Errorf("got %+v", uncaught)
			}
			if !strings.Contains(uncaught.Message, "Error: boom") || !strings.Contains(uncaught.Message, "    at ") {
				t.Errorf("message is missing the stack: %q", uncaught.Message)
			}
			if want := []string{"exit 1 1"}; !reflect.DeepEqual(notes, want) {
				t.Errorf("notes = %q, want %q", notes, want)
			}
		})
	}
}

func TestUncaughtMainScript(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e, origin) => note(e.message, origin));
setTimeout(() => note('later'), 1);
throw new Error('main');
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"main uncaughtException", "later"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}

	notes, err = runUncaught(t, `process.on('exit', (code) => note('exit', code)); throw new TypeError('main');`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 1 || !strings.Contains(uncaught.Message, "TypeError: main") {
		t.Fatalf("Run returned %#v", err)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtMainScriptPassesTheThrownValue(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e) => note(e.code, e instanceof RangeError));
const e = new RangeError('main');
e.code = 'E_X';
throw e;
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"E_X true"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtPassesTheThrownValue(t *testing.T) {
	notes, err := runUncaught(t, `
const thrown = { not: 'an error' };
process.on('uncaughtException', (e) => note(e === thrown));
setTimeout(() => { throw thrown; }, 1);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"true"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtHandlerThrows(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('exit', () => note('must not run'));
process.on('uncaughtException', () => { note('handler'); throw new Error('second'); });
setTimeout(() => { throw new Error('first'); }, 1);
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 7 || !strings.Contains(uncaught.Message, "Error: second") {
		t.Fatalf("Run returned %#v", err)
	}
	if want := []string{"handler"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtExitListenerSetsCode(t *testing.T) {
	_, err := runUncaught(t, `
process.on('exit', () => { process.exitCode = 9; });
setTimeout(() => { throw { a: 1 }; }, 1);
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 9 || !strings.Contains(uncaught.Message, "a: 1") {
		t.Fatalf("Run returned %#v", err)
	}
}

func TestUncaughtIntervalKeepsRunning(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e) => note(e.message));
let n = 0;
const interval = setInterval(() => {
  if (++n === 2) clearInterval(interval);
  throw new Error('tick' + n);
}, 1);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"tick1", "tick2"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtCaptureCallback(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', () => note('must not run'));
process.setUncaughtExceptionCaptureCallback((e) => note('captured', e.message));
note(process.hasUncaughtExceptionCaptureCallback());
try {
  process.setUncaughtExceptionCaptureCallback(() => {});
} catch (e) {
  note(e.code);
}
setTimeout(() => { throw new Error('boom'); }, 1);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"true", "ERR_UNCAUGHT_EXCEPTION_CAPTURE_ALREADY_SET", "captured boom"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUnhandledRejectionWithHandler(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('unhandledRejection', (r, p) => note('unhandled', r.message || r, p instanceof Promise));
process.on('rejectionHandled', (p) => note('handled late', p instanceof Promise));
const late = Promise.reject(new Error('late'));
setTimeout(() => late.catch(() => {}), 1);
Promise.reject(5);
Promise.reject(new Error('caught')).catch(() => note('caught'));
(async () => { throw new Error('async'); })();
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"caught", "unhandled late true", "unhandled 5 true", "unhandled async true", "handled late true"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUnhandledRejectionWithoutHandler(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('exit', (code) => note('exit', code));
setTimeout(() => note('must not run'), 200);
(async () => { throw new Error('rejected'); })();
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) {
		t.Fatalf("Run returned %v", err)
	}
	if uncaught.ExitCode != 1 || uncaught.Origin != "unhandledRejection" || !strings.Contains(uncaught.Message, "Error: rejected") {
		t.Errorf("got %+v", uncaught)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUnhandledRejectionReachesUncaughtHandler(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e, origin) => note(e.code, e.message, origin));
Promise.reject(new Error('x'));
Promise.reject(42);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if len(notes) != 2 || notes[0] != " x unhandledRejection" ||
		!strings.HasPrefix(notes[1], "ERR_UNHANDLED_REJECTION This error originated") ||
		!strings.HasSuffix(notes[1], `The promise rejected with the reason "42". unhandledRejection`) {
		t.Errorf("notes = %q", notes)
	}
}

func TestUnhandledRejectionHandlerThrows(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('exit', (code) => note('exit', code));
process.on('unhandledRejection', () => { throw new Error('from handler'); });
Promise.reject(1);
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 1 || uncaught.Origin != "uncaughtException" ||
		!strings.Contains(uncaught.Message, "Error: from handler") {
		t.Fatalf("Run returned %#v", err)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestWorkerUncaughtReachesParent(t *testing.T) {
	notes, err := runUncaught(t, `
const { Worker } = require('worker_threads');
const worker = new Worker("setTimeout(() => { throw new Error('in worker'); }, 1)", { eval: true });
worker.on('error', (e) => note('error', String(e.message).includes('in worker')));
worker.on('exit', (code) => note('exit', code));
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"error true", "exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}
