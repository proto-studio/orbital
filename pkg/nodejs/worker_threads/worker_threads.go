// Package worker_threads implements Node's worker_threads with real,
// isolate-backed workers.
//
// Each Worker runs on its own goroutine with its own V8 isolate + runtime +
// event loop (created by the provider bound to the module). Parent and worker never share V8
// values; messages are serialized to JSON inside the owning isolate and handed
// across goroutines as plain strings over Go channels, then re-parsed and
// dispatched on the receiving runtime's event loop. This keeps every isolate
// single-threaded while providing real cross-thread messaging.
package worker_threads

import (
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	goruntime "proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed worker_threads.js
var workerThreadsJS string

// wtEvent is a worker->parent event carried over a Go channel.
type wtEvent struct {
	kind string // "online" | "message" | "error" | "exit"
	data string // JSON payload (message) or error text
	code int    // exit code
}

type worker struct {
	id         int
	target     string // absolute file path, or source when eval
	eval       bool
	workerData string // JSON
	toWorker   chan string
	fromWorker chan wtEvent
	terminate  chan struct{}
	termOnce   sync.Once
	resourceID uint64
}

// Close terminates the worker when its parent runtime is disposed.
func (wk *worker) Close() error {
	wk.termOnce.Do(func() { close(wk.terminate) })
	return nil
}

// WorkerThreads is the module instance for one runtime (parent or worker).
type WorkerThreads struct {
	rt         *goruntime.Runtime
	mu         sync.Mutex
	workers    map[int]*worker
	nextID     int
	dispatcher *v8.Value // parent-side JS event dispatcher
	provider   func() (*goruntime.Runtime, error)
}

// New creates a WorkerThreads module without a runtime provider: constructing a
// Worker throws because workers are unavailable.
func New() *WorkerThreads {
	return &WorkerThreads{workers: make(map[int]*worker)}
}

// NewWithProvider creates a WorkerThreads module whose workers run on runtimes
// built by provider. provider is called from the worker's goroutine, possibly
// after this module's runtime has been disposed, so it must not depend on that
// runtime; it should return a runtime configured as the module's owner intends.
func NewWithProvider(provider func() (*goruntime.Runtime, error)) *WorkerThreads {
	return &WorkerThreads{workers: make(map[int]*worker), provider: provider}
}

// Name returns the module name.
func (w *WorkerThreads) Name() string {
	return "worker_threads"
}

// Register wires the native bridges and runs the JS surface.
func (w *WorkerThreads) Register(rt *goruntime.Runtime) error {
	w.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Only expose the spawn bridge (and therefore a working Worker class) when a
	// provider is available. Without it, worker_threads.js leaves Worker as a
	// throwing stub.
	if w.provider != nil {
		if err := w.setFunc(iso, ctx, "__wt_spawn", w.spawnFunc); err != nil {
			return err
		}
		if err := w.setFunc(iso, ctx, "__wt_post", w.postFunc); err != nil {
			return err
		}
		if err := w.setFunc(iso, ctx, "__wt_terminate", w.terminateFunc); err != nil {
			return err
		}
		if err := w.setFunc(iso, ctx, "__wt_set_dispatcher", w.setDispatcherFunc); err != nil {
			return err
		}
	}

	if _, err := rt.RunScript(workerThreadsJS, "worker_threads.js"); err != nil {
		return err
	}
	return nil
}

func (w *WorkerThreads) setFunc(iso *v8.Isolate, ctx *v8.Context, name string, cb v8.FunctionCallback) error {
	tmpl, err := iso.NewFunctionTemplate(cb)
	if err != nil {
		return err
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		return err
	}
	return w.rt.SetGlobal(name, fn)
}

// ---- parent side ---------------------------------------------------------

func (w *WorkerThreads) setDispatcherFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) > 0 && args[0] != nil && args[0].IsFunction() {
		w.dispatcher = args[0]
	}
	return nil
}

// __wt_spawn(target, workerDataJson, isEval) -> id
func (w *WorkerThreads) spawnFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.NewInteger(0)
	}
	target := args[0].String()
	dataJSON := "null"
	if len(args) > 1 && args[1] != nil && !args[1].IsNull() && !args[1].IsUndefined() {
		dataJSON = args[1].String()
	}
	eval := len(args) > 2 && args[2] != nil && args[2].Boolean()

	w.mu.Lock()
	w.nextID++
	id := w.nextID
	wk := &worker{
		id:         id,
		target:     target,
		eval:       eval,
		workerData: dataJSON,
		toWorker:   make(chan string, 64),
		fromWorker: make(chan wtEvent, 64),
		terminate:  make(chan struct{}),
	}
	w.workers[id] = wk
	w.mu.Unlock()
	wk.resourceID = w.rt.TrackResource(wk)

	// Keep the parent event loop alive while the worker runs.
	w.rt.EventLoop().AddPendingWork()

	// Parent-side pump: deliver worker events onto the parent event loop.
	go func() {
		for ev := range wk.fromWorker {
			e := ev
			w.rt.EventLoop().EnqueueMicrotask(func() {
				w.dispatchToParent(id, e)
			})
			if e.kind == "exit" {
				w.mu.Lock()
				delete(w.workers, id)
				w.mu.Unlock()
				w.rt.UntrackResource(wk.resourceID)
				w.rt.EventLoop().DonePendingWork()
			}
		}
	}()

	// Worker goroutine.
	go func() {
		w.runWorker(wk)
		close(wk.fromWorker)
	}()

	return ctx.NewInteger(int64(id))
}

func (w *WorkerThreads) dispatchToParent(id int, e wtEvent) {
	if w.dispatcher == nil {
		return
	}
	ctx := w.rt.Context()
	idV := ctx.NewInteger(int64(id))
	kindV, _ := ctx.NewString(e.kind)
	dataV, _ := ctx.NewString(e.data)
	codeV := ctx.NewInteger(int64(e.code))
	w.rt.MakeCallback(w.dispatcher, nil, idV, kindV, dataV, codeV)
}

// __wt_post(id, msgJson)
func (w *WorkerThreads) postFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	id := int(args[0].Integer())
	msg := args[1].String()
	w.mu.Lock()
	wk := w.workers[id]
	w.mu.Unlock()
	if wk == nil {
		return nil
	}
	select {
	case wk.toWorker <- msg:
	case <-wk.terminate:
	}
	return nil
}

// __wt_terminate(id)
func (w *WorkerThreads) terminateFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	id := int(args[0].Integer())
	w.mu.Lock()
	wk := w.workers[id]
	w.mu.Unlock()
	if wk != nil {
		wk.Close()
	}
	return nil
}

// ---- worker side ---------------------------------------------------------

func (w *WorkerThreads) runWorker(wk *worker) {
	wrt, err := w.provider()
	if err != nil {
		wk.fromWorker <- wtEvent{kind: "error", data: err.Error()}
		wk.fromWorker <- wtEvent{kind: "exit", code: 1}
		return
	}

	// The pump below may call wrt.Kill, so it must be finished before the isolate
	// is disposed. Deferred calls run in reverse: stop the pump, then dispose.
	defer wrt.Dispose()
	workerDone := make(chan struct{})
	pumpDone := make(chan struct{})
	pumpStarted := false
	defer func() {
		close(workerDone)
		if pumpStarted {
			<-pumpDone
		}
	}()

	// Worker-side native bridges.
	_ = w.setWorkerFunc(wrt, "__wt_worker_send", func(info *v8.FunctionCallbackInfo) *v8.Value {
		a := info.Args()
		if len(a) > 0 && a[0] != nil {
			select {
			case wk.fromWorker <- wtEvent{kind: "message", data: a[0].String()}:
			case <-wk.terminate:
			}
		}
		return nil
	})
	_ = w.setWorkerFunc(wrt, "__wt_worker_ref", func(info *v8.FunctionCallbackInfo) *v8.Value {
		a := info.Args()
		if len(a) > 0 && a[0] != nil && a[0].Boolean() {
			wrt.EventLoop().AddPendingWork()
		} else {
			wrt.EventLoop().DonePendingWork()
		}
		return nil
	})

	// Initialize parentPort / workerData inside the worker.
	initCode := fmt.Sprintf("__wt_init_worker(%s);", jsStringLiteral(wk.workerData))
	if _, err := wrt.RunScript(initCode, "worker_bootstrap.js"); err != nil {
		wk.fromWorker <- wtEvent{kind: "error", data: err.Error()}
		wk.fromWorker <- wtEvent{kind: "exit", code: 1}
		return
	}
	deliverFn, _ := wrt.GetGlobal("__wt_deliver")

	// Incoming-message pump + termination watcher.
	pumpStarted = true
	go func() {
		defer close(pumpDone)
		for {
			select {
			case <-workerDone:
				return
			case <-wk.terminate:
				wrt.Kill("terminated")
				return
			case msg, ok := <-wk.toWorker:
				if !ok {
					return
				}
				m := msg
				wrt.EventLoop().EnqueueMicrotask(func() {
					if deliverFn == nil || !deliverFn.IsFunction() {
						return
					}
					s, _ := wrt.Context().NewString(m)
					wrt.MakeCallback(deliverFn, nil, s)
				})
			}
		}
	}()

	wk.fromWorker <- wtEvent{kind: "online"}

	// Resolve and run the entry.
	source := wk.target
	origin := "[worker eval]"
	filename := "[worker eval]"
	dirname := "."
	if !wk.eval {
		// The entry is read through the configured filesystem, like any other file
		// the program opens, so an injected sandbox applies to it.
		data, readErr := wrt.Filesystem().ReadFile(wk.target)
		if readErr != nil {
			wk.fromWorker <- wtEvent{kind: "error", data: readErr.Error()}
			wk.fromWorker <- wtEvent{kind: "exit", code: 1}
			return
		}
		source = string(data)
		origin = wk.target
		filename = wk.target
		dirname = filepath.Dir(wk.target)
	}

	pathSetup := fmt.Sprintf(
		"globalThis.__filename=%s; globalThis.__dirname=%s; if(typeof module!=='undefined'){module.filename=%s;module.id=%s;}",
		jsStringLiteral(filename), jsStringLiteral(dirname),
		jsStringLiteral(filename), jsStringLiteral(filename),
	)
	_, _ = wrt.RunScript(pathSetup, "worker_paths.js")

	code := 0
	if _, runErr := wrt.Run(source, origin); runErr != nil {
		if !wrt.IsKilled() {
			wk.fromWorker <- wtEvent{kind: "error", data: runErr.Error()}
			code = 1
			// The 7 for a throwing handler is a whole process's exit code, and Node's
			// workers report 1 for it.
			var uncaught *goruntime.UncaughtError
			if errors.As(runErr, &uncaught) && !uncaught.HandlerThrew {
				code = uncaught.ExitCode
			}
		}
	}
	wk.fromWorker <- wtEvent{kind: "exit", code: code}
}

func (w *WorkerThreads) setWorkerFunc(wrt *goruntime.Runtime, name string, cb v8.FunctionCallback) error {
	iso := wrt.Isolate()
	ctx := wrt.Context()
	tmpl, err := iso.NewFunctionTemplate(cb)
	if err != nil {
		return err
	}
	fn, err := tmpl.GetFunction(ctx)
	if err != nil {
		return err
	}
	return wrt.SetGlobal(name, fn)
}

// jsStringLiteral returns a safe JS string literal (JSON-quoted) for src, so
// arbitrary paths / JSON payloads can be embedded into a RunScript snippet.
func jsStringLiteral(s string) string {
	var b []byte
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		case '\u2028':
			b = append(b, '\\', 'u', '2', '0', '2', '8')
		case '\u2029':
			b = append(b, '\\', 'u', '2', '0', '2', '9')
		default:
			if r < 0x20 {
				b = fmt.Appendf(b, "\\u%04x", r)
			} else {
				b = append(b, string(r)...)
			}
		}
	}
	b = append(b, '"')
	return string(b)
}
