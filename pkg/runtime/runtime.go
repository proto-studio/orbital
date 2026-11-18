// Package runtime provides the JavaScript runtime environment.
package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"proto.zip/studio/orbital/pkg/v8"
)

// Runtime represents a JavaScript runtime environment.
type Runtime struct {
	unhandledRejections string // Config.UnhandledRejections
	noWarnings          bool   // Config.NoWarnings
	threadPool          *threadPool
	isolates            *isolateLimit
	sockets             *handleLimit
	files               *handleLimit
	socketsClosed       bool  // SocketFactory and HTTPClient refuse everything, so OpenSocket counts nothing
	filesClosed         bool  // Filesystem refuses everything, so OpenFile counts nothing
	cryptoMemoryLimit   int64 // Config.CryptoMemoryLimit
	memoryBudget        *MemoryBudget
	outOfMemory         atomic.Bool

	isolate           *v8.Isolate
	context           *v8.Context
	eventLoop         *EventLoop
	modules           map[string]Module
	nativeModules     map[string]*v8.Value // User-registered native modules
	filesystem        Filesystem
	fsLimit           *isolateMemoryLimit // the isolate's limit on a MemoryFilesystem, if fs is one
	unlimitFS         func()              // removes fsLimit from the filesystem
	systemInfo        SystemInfo
	httpClient        HTTPClient
	environment       Environment
	dnsResolver       Resolver
	socketFactory     SocketFactory
	clientTLSConfig   *tls.Config
	processSpawner    ProcessSpawner
	processSignaler   ProcessSignaler
	documentRoot      string
	resourceTracker   *ResourceTracker
	execController    *ExecutionController
	disposed          bool
	mu                sync.Mutex
	uncaught          uncaughtHandlers
	fatal             atomic.Pointer[UncaughtError]
	sites             []*v8.ExceptionPlace // same goroutine: where the exceptions the handlers have were thrown
	madePlace         *v8.ExceptionPlace   // same goroutine: see placeOf in installUncaught
	exited            atomic.Pointer[ExitError]
	tracksRejections  bool
	rejectionsPending bool     // only touched on the goroutine running JavaScript
	fatalSettled      bool     // same goroutine: UncaughtError has discarded the promise queue
	mainBody          func()   // same goroutine: what the runMain native function runs next
	processExitHooks  []func() // same goroutine
}

// Module represents a Node.js module that can be registered with the runtime.
type Module interface {
	// Name returns the module name (e.g., "console", "fs", "path")
	Name() string
	// Register sets up the module in the given runtime context
	Register(rt *Runtime) error
}

// Config contains configuration options for creating a runtime.
//
// Every field that grants access to the host is closed when nil: a zero Config gives
// scripts no filesystem, no network and no processes, and they see the errors Node
// reports when the OS refuses those operations. Set a field to grant that access, for
// example NewLocalFilesystem(root), NewRealSocketFactory() or NewRealProcessSpawner().
// The orbital CLI sets the real implementations, as Node behaves.
type Config struct {
	// EnableConsole enables the console global object
	EnableConsole bool
	// EnableTimers enables setTimeout, setInterval, etc.
	EnableTimers bool
	// Filesystem is the filesystem implementation to use.
	// If nil, NewNoOpFilesystem() refuses every operation with EACCES, and require and
	// import of files fail as they do in Node for a file it can't read.
	Filesystem Filesystem
	// SystemInfo is the system information provider.
	// If nil, NewSandboxedSystemInfo(nil) reports a made-up host instead of this one.
	SystemInfo SystemInfo
	// HTTPClient is the HTTP client for fetch, and for node:http and node:https when
	// it is set to anything but the real client. It then sees every such request.
	// If nil, fetch fails as it does in Node without network access: a name the
	// DNSResolver doesn't resolve fails with ENOTFOUND, and connecting fails with EPERM.
	// node:http and node:https then connect through SocketFactory, as with the real
	// client.
	HTTPClient HTTPClient
	// Environment is the environment variable provider.
	// If nil, NewSandboxedEnvironment(nil) starts empty and exposes nothing of the
	// host's environment.
	Environment Environment
	// DNSResolver resolves names for node:dns and for everything that connects by name.
	// If nil, NewNoOpResolver() resolves only localhost, as a host without network
	// access does.
	DNSResolver Resolver
	// SocketFactory creates TCP/UDP sockets.
	// If nil, NewNoOpSocketFactory() refuses every connect, listen and bind with EPERM.
	SocketFactory SocketFactory
	// ClientTLSConfig, when non-nil, is the authoritative client TLS configuration
	// for node:tls, node:https and node:http2: JavaScript options such as rejectUnauthorized,
	// ca, cert, servername and checkServerIdentity, and
	// NODE_TLS_REJECT_UNAUTHORIZED, are ignored so guest code cannot weaken it. Set
	// &tls.Config{} to lock verification to the system roots. When nil, those
	// JavaScript options apply as they do in Node, and certificates are verified
	// against the system roots by default.
	ClientTLSConfig *tls.Config
	// MaxWorkers is how many workers and loader realms (from module.register) scripts
	// may have running at once, across the runtime and everything it starts, nested
	// workers included. Each is a V8 isolate with a heap of its own. One more fails
	// with Node's ERR_WORKER_INIT_FAILED. 0 allows none; a negative value means no
	// limit, which the orbital CLI uses, as Node has none.
	MaxWorkers int
	// MaxOpenSockets is how many sockets scripts may have open at once: TCP
	// connections and listeners (including TLS, HTTP and HTTP/2 connections), UDP
	// sockets, and requests through HTTPClient. One more fails with EMFILE, as at the
	// OS's own limit. 0 allows none, so an embedder that grants network access sets
	// it too; a negative value means no limit beyond the OS's, which the orbital CLI
	// uses. A runtime's workers and loader realms count against the same cap. It
	// doesn't apply while SocketFactory and HTTPClient are both nil, since they then
	// refuse every connection with EPERM and nothing opens.
	MaxOpenSockets int
	// MaxOpenFiles is how many files scripts may have open at once through fs: file
	// descriptors from fs.open and FileHandles, streams, directories being read, and
	// the files calls like readFile, writeFile and copyFile open while they run. One
	// more fails with EMFILE. 0 allows none; a negative value means no limit beyond
	// the OS's, which the orbital CLI uses. Loading modules and worker entry files
	// doesn't count. A runtime's workers and loader realms count against the same cap.
	// It doesn't apply to a NoOpFilesystem, the default, which opens nothing.
	MaxOpenFiles int
	// ProcessSpawner spawns child processes.
	// If nil, NewNoOpProcessSpawner() refuses every spawn with EPERM.
	ProcessSpawner ProcessSpawner
	// ProcessSignaler sends the signals of process.kill().
	// If nil, every signal is refused with EPERM: process.kill() can reach any process
	// the host process may signal, including the host process itself, so a library
	// embedder opts in. The orbital CLI uses NewRealProcessSignaler, as Node behaves.
	ProcessSignaler ProcessSignaler
	// DocumentRoot is the root directory for sandboxing.
	// When set, paths like process.cwd() are relative to this root.
	DocumentRoot string
	// Timeout is the maximum execution time. 0 means no timeout.
	Timeout time.Duration
	// IsolateOptions are create-time V8 isolate options (heap limits, etc.).
	// Heap limits cannot be changed after the isolate is created.
	IsolateOptions v8.IsolateOptions
	// UnhandledRejections is what an unhandled promise rejection does, as with Node's
	// --unhandled-rejections: "throw" (the default when empty), "strict", "warn", "none" or
	// "warn-with-error-code". See UnhandledRejectionModes.
	UnhandledRejections string
	// NoWarnings stops process warnings from being printed, as Node's --no-warnings does.
	// The 'warning' event is still emitted.
	NoWarnings bool
	// ThreadPoolSize is how many tasks run at once on the runtime's thread pool,
	// which runs the callback forms of node:crypto functions (randomBytes,
	// generateKeyPair, pbkdf2, scrypt, sign and the rest), as Node runs them on
	// libuv's threadpool. Further tasks wait their turn. 0 means
	// DefaultThreadPoolSize (4), Node's default UV_THREADPOOL_SIZE.
	ThreadPoolSize int
	// ThreadPoolQueueSize is how many more tasks may wait for the thread pool once
	// ThreadPoolSize are running. Each waiting task holds its inputs, so when the
	// queue is full, starting more work waits for a task to leave it, blocking the
	// JavaScript thread, rather than letting the queue grow without bound. Every
	// task still runs and calls back. 0 means none wait: new work waits for a
	// running task to finish. A negative value means no limit, as in Node; the
	// orbital CLI uses that.
	ThreadPoolQueueSize int
	// MemoryBudget, when set, is the memory the runtime and every worker and loader
	// realm it starts may use together: their JavaScript heaps, and the contents of
	// their ArrayBuffers and Buffers. Workers and loader realms built from this Config
	// share it, as does every other runtime given the same budget, so give each
	// runtime you want limited on its own a budget of its own.
	//
	// Each heap is limited by IsolateOptions as well. With a budget, a heap starts
	// small and grows from the budget as it needs, so a runtime that doesn't use its
	// whole heap leaves the rest to the others. Going over the budget looks to
	// scripts like running out of memory: an ArrayBuffer or Buffer that doesn't fit
	// throws RangeError ("Array buffer allocation failed"), a worker whose heap can't
	// grow stops with ERR_WORKER_OUT_OF_MEMORY, and a runtime whose heap can't grow
	// stops, with Run returning an *OutOfMemoryError. A worker the budget has no room
	// to start fails with ERR_WORKER_INIT_FAILED.
	//
	// nil means no limit beyond each heap's own, as in Node. Whether or not one is set,
	// a heap that reaches its limit stops its runtime rather than the process.
	MemoryBudget *MemoryBudget
	// CryptoMemoryLimit, when positive, is the most memory in bytes one key
	// derivation (pbkdf2, scrypt, hkdf, and WebCrypto's PBKDF2 and HKDF) may
	// use: scrypt's workspace, whatever maxmem the script passes, plus the
	// derived key on its way to JavaScript, counted as six times its length
	// (the key, its base64 form and that form's copies, and the Buffer), plus
	// the inputs as they're copied and decoded. An scrypt call over it fails as one
	// over its own maxmem does (ERR_CRYPTO_INVALID_SCRYPT_PARAMS); a pbkdf2 or
	// hkdf key too long for it is ERR_OUT_OF_RANGE. 0 means no limit beyond
	// the script's own, as in Node.
	CryptoMemoryLimit int64
}

// DefaultConfig returns a configuration with the console and timers enabled. Like a zero
// Config, it gives scripts no filesystem, no network and no processes.
func DefaultConfig() *Config {
	return &Config{
		EnableConsole: true,
		EnableTimers:  true,
	}
}

// New creates a new JavaScript runtime.
func New(cfg *Config) (*Runtime, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if !validUnhandledRejections(cfg.UnhandledRejections) {
		return nil, fmt.Errorf("invalid value for UnhandledRejections: %q", cfg.UnhandledRejections)
	}

	isolateOptions := cfg.IsolateOptions
	if cfg.MemoryBudget != nil {
		isolateOptions.MemoryBudget = cfg.MemoryBudget
	}
	iso, err := v8.NewIsolateWithOptions(isolateOptions)
	if errors.Is(err, v8.ErrSetupOutOfMemory) {
		// Its heap was too small for V8 to set it up.
		return nil, &OutOfMemoryError{}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create isolate: %w", err)
	}
	// setupFailed is what New returns for a setup step that failed: running out of
	// memory, when the heap ran out, as it can at any step for a small heap.
	setupFailed := func(err error) error {
		if iso.OutOfMemory() {
			return &OutOfMemoryError{}
		}
		return err
	}
	ctx, err := iso.NewContext()
	if err != nil {
		err = setupFailed(fmt.Errorf("failed to create context: %w", err))
		iso.Dispose()
		return nil, err
	}

	// Node.js exposes the global object under the name `global` in addition to
	// the standard `globalThis`. Many npm packages (and their bundled deps)
	// reference `global` directly, so alias it here for compatibility.
	if _, err := ctx.RunScript("if (typeof global === 'undefined') { globalThis.global = globalThis; }", "<bootstrap:global>"); err != nil {
		err = setupFailed(fmt.Errorf("failed to set up global alias: %w", err))
		ctx.Dispose()
		iso.Dispose()
		return nil, err
	}

	// Every field that grants access to the host refuses it when nil.
	fs := cfg.Filesystem
	if fs == nil {
		fs = NewNoOpFilesystem()
	}

	// A memory filesystem's contents count against the isolate's memory limit.
	var fsLimit *isolateMemoryLimit
	var unlimitFS func()
	if limited, ok := fs.(memoryLimited); ok {
		fsLimit = newIsolateMemoryLimit(iso, cfg.IsolateOptions)
		unlimitFS = limited.limitMemory(fsLimit)
	}

	sysInfo := cfg.SystemInfo
	if sysInfo == nil {
		sysInfo = NewSandboxedSystemInfo(nil)
	}
	env := cfg.Environment
	if env == nil {
		env = NewSandboxedEnvironment(nil)
	}
	dnsResolver := cfg.DNSResolver
	if dnsResolver == nil {
		dnsResolver = NewNoOpResolver()
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &refusingHTTPClient{resolver: dnsResolver}
	}
	socketFactory := cfg.SocketFactory
	if socketFactory == nil {
		socketFactory = NewNoOpSocketFactory()
	}
	processSignaler := cfg.ProcessSignaler
	if processSignaler == nil {
		processSignaler = NewNoOpProcessSignaler()
	}
	processSpawner := cfg.ProcessSpawner
	if processSpawner == nil {
		processSpawner = NewNoOpProcessSpawner()
	}

	// Implementations that refuse everything never open a socket or file, so there is
	// nothing to count, and the runtime fails as Node does when the OS refuses access, not
	// with EMFILE. Only this runtime skips the count: the limits themselves stay as
	// configured, since workers and loader realms with access of their own share them.
	_, filesClosed := fs.(*NoOpFilesystem)
	_, closedSockets := socketFactory.(*NoOpSocketFactory)
	_, closedClient := httpClient.(*refusingHTTPClient)
	socketsClosed := closedSockets && closedClient

	// Create resource tracker
	resourceTracker := NewResourceTracker()

	// Create execution controller with timeout
	execController := NewExecutionController(cfg.Timeout)

	// Create event loop with execution context
	eventLoop := NewEventLoopWithContext(execController.Context())

	rt := &Runtime{
		isolate:         iso,
		context:         ctx,
		eventLoop:       eventLoop,
		modules:         make(map[string]Module),
		nativeModules:   make(map[string]*v8.Value),
		filesystem:      fs,
		fsLimit:         fsLimit,
		unlimitFS:       unlimitFS,
		systemInfo:      sysInfo,
		httpClient:      httpClient,
		environment:     env,
		dnsResolver:     dnsResolver,
		socketFactory:   socketFactory,
		clientTLSConfig: cfg.ClientTLSConfig,
		processSpawner:  processSpawner,
		processSignaler: processSignaler,
		documentRoot:    cfg.DocumentRoot,
		resourceTracker: resourceTracker,
		execController:  execController,

		unhandledRejections: cfg.UnhandledRejections,
		noWarnings:          cfg.NoWarnings,
		threadPool:          newThreadPool(cfg.ThreadPoolSize, cfg.ThreadPoolQueueSize, execController.Done()),
		isolates:            &isolateLimit{max: cfg.MaxWorkers},
		sockets:             &handleLimit{max: cfg.MaxOpenSockets},
		files:               &handleLimit{max: cfg.MaxOpenFiles},
		socketsClosed:       socketsClosed,
		filesClosed:         filesClosed,
		cryptoMemoryLimit:   cfg.CryptoMemoryLimit,
		memoryBudget:        isolateOptions.MemoryBudget,
	}
	rt.watchMemory()

	// Set up kill callback to stop the event loop, cancel tasks, and terminate
	// any in-flight V8 JavaScript so runaway scripts / OOM recoveries stop
	// without disposing the whole process.
	execController.OnKill(func() {
		iso.TerminateExecution()
		rt.eventLoop.CancelAllTimers()
		rt.eventLoop.ClearAllMicrotasks()
		rt.eventLoop.Stop()
	})

	if err := rt.installUncaught(); err != nil {
		err = setupFailed(fmt.Errorf("failed to set up exception handling: %w", err))
		if unlimitFS != nil {
			unlimitFS()
		}
		ctx.Dispose()
		iso.Dispose()
		return nil, err
	}

	return rt, nil
}

// NoWarnings reports whether process warnings are kept off stderr (Config.NoWarnings).
func (rt *Runtime) NoWarnings() bool {
	return rt.noWarnings
}

// Filesystem returns the filesystem implementation for this runtime.
func (rt *Runtime) Filesystem() Filesystem {
	return rt.filesystem
}

// SystemInfo returns the system information provider for this runtime.
func (rt *Runtime) SystemInfo() SystemInfo {
	return rt.systemInfo
}

// HTTPClient returns the HTTP client for this runtime.
func (rt *Runtime) HTTPClient() HTTPClient {
	return rt.httpClient
}

// DNSResolver returns the DNS resolver for this runtime.
func (rt *Runtime) DNSResolver() Resolver {
	return rt.dnsResolver
}

// SocketFactory returns the socket factory for this runtime.
func (rt *Runtime) SocketFactory() SocketFactory {
	return rt.socketFactory
}

// ClientTLSConfig returns the embedder's authoritative client TLS configuration, or
// nil if none was set. Callers must Clone it before use.
func (rt *Runtime) ClientTLSConfig() *tls.Config {
	return rt.clientTLSConfig
}

// ProcessSpawner returns the process spawner for this runtime.
func (rt *Runtime) ProcessSpawner() ProcessSpawner {
	return rt.processSpawner
}

// ProcessSignaler returns what sends the signals of process.kill().
func (rt *Runtime) ProcessSignaler() ProcessSignaler {
	return rt.processSignaler
}

// DocumentRoot returns the document root for path sandboxing.
func (rt *Runtime) DocumentRoot() string {
	return rt.documentRoot
}

// Environment returns the environment variable provider for this runtime.
func (rt *Runtime) Environment() Environment {
	return rt.environment
}

// Isolate returns the underlying V8 isolate.
func (rt *Runtime) Isolate() *v8.Isolate {
	return rt.isolate
}

// Context returns the underlying V8 context.
func (rt *Runtime) Context() *v8.Context {
	return rt.context
}

// EventLoop returns the runtime's event loop.
func (rt *Runtime) EventLoop() *EventLoop {
	return rt.eventLoop
}

// RegisterModule registers a module with the runtime.
func (rt *Runtime) RegisterModule(mod Module) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	name := mod.Name()
	if _, exists := rt.modules[name]; exists {
		return fmt.Errorf("module %q already registered", name)
	}

	if err := mod.Register(rt); err != nil {
		return fmt.Errorf("failed to register module %q: %w", name, err)
	}

	rt.modules[name] = mod
	return nil
}

// GetModule returns a previously registered module by name.
func (rt *Runtime) GetModule(name string) (Module, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	mod, ok := rt.modules[name]
	return mod, ok
}

// RegisterNativeModule registers a user-defined native module that can be
// required from JavaScript using require('moduleName') or imported via ESM.
// The value should be an object containing the module's exports.
func (rt *Runtime) RegisterNativeModule(name string, value *v8.Value) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if _, exists := rt.nativeModules[name]; exists {
		return fmt.Errorf("native module %q already registered", name)
	}

	rt.nativeModules[name] = value

	// Also set it as a global with __native_module_ prefix for the module loader
	global, err := rt.context.Global()
	if err != nil {
		return err
	}

	return global.Set("__native_module_"+name, value)
}

// GetNativeModule retrieves a registered native module by name.
func (rt *Runtime) GetNativeModule(name string) (*v8.Value, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	val, exists := rt.nativeModules[name]
	return val, exists
}

// NativeModuleNames returns a list of all registered native module names.
func (rt *Runtime) NativeModuleNames() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	names := make([]string, 0, len(rt.nativeModules))
	for name := range rt.nativeModules {
		names = append(names, name)
	}
	return names
}

// SetGlobal sets a global variable in the runtime context.
func (rt *Runtime) SetGlobal(name string, value *v8.Value) error {
	global, err := rt.context.Global()
	if err != nil {
		return err
	}
	return global.Set(name, value)
}

// GetGlobal gets a global variable from the runtime context.
func (rt *Runtime) GetGlobal(name string) (*v8.Value, error) {
	global, err := rt.context.Global()
	if err != nil {
		return nil, err
	}
	return global.Get(name)
}

// RunScript executes JavaScript code.
func (rt *Runtime) RunScript(source, origin string) (*v8.Value, error) {
	return rt.context.RunScript(source, origin)
}

// Run executes JavaScript code and runs the event loop until completion.
//
// An exception the script does not catch, in its main body or any later callback, goes to
// process.on('uncaughtException') as it does in Node, and execution then carries on. If no
// handler takes it, or a promise is left rejected with no 'unhandledRejection' handler, the
// runtime stops and Run returns an *UncaughtError. If the script calls process.exit(), the
// runtime runs the 'exit' listeners, stops, and Run returns an *ExitError. Either way the
// runtime cannot run anything further: calling Run again returns the same error without
// running the script.
func (rt *Runtime) Run(source, origin string) (*v8.Value, error) {
	if ended := rt.Ended(); ended != nil {
		return nil, ended
	}

	result, err := rt.runMainScript(source, origin)
	if ended := rt.Ended(); ended != nil {
		return nil, ended
	}
	if err != nil {
		return nil, err
	}

	// Run the event loop to process any pending async operations
	rt.RunEventLoop()

	if ended := rt.Ended(); ended != nil {
		return nil, ended
	}
	return result, nil
}

// RunEventLoop runs the event loop until it has no work left, as Node does at the end of a
// program: each time the loop empties, process emits 'beforeExit', and if a listener
// scheduled more work the loop runs again. It returns early if the runtime ends (an
// uncaught exception or process.exit(), neither of which emits 'beforeExit') or is killed.
func (rt *Runtime) RunEventLoop() {
	rt.RunEventLoopWhile(nil)
}

// RunEventLoopWhile is RunEventLoop for a caller waiting on something (Node's startup
// awaiting an --import): when the loop empties, 'beforeExit' is emitted only while
// waiting reports true, since a listener may be what settles it.
//
// The loop stops as soon as waiting reports false, even with work left (an --import's
// open handles don't hold back the entry).
func (rt *Runtime) RunEventLoopWhile(waiting func() bool) {
	for {
		if waiting == nil {
			rt.eventLoop.Run()
		} else if waiting() {
			rt.eventLoop.RunUntil(func() bool { return !waiting() })
		}
		if rt.Ended() != nil || rt.execController.IsDone() {
			return
		}
		if waiting != nil && !waiting() {
			return
		}
		rt.MakeCallback(rt.uncaught.beforeExit, nil)
		if rt.Ended() != nil {
			return
		}
		// A listener can leave a promise rejected, and the rejection handlers can schedule
		// work of their own, so they run before the loop's liveness decides what's next.
		rt.reportRejections()
		if rt.Ended() != nil {
			return
		}
		if !rt.eventLoop.isAlive() {
			// The listeners' ticks have run; this only clears what is left in Go's queues.
			rt.eventLoop.Run()
			return
		}
	}
}

// Kill immediately stops execution with the given reason.
// This will stop the event loop and trigger resource cleanup.
func (rt *Runtime) Kill(reason string) {
	rt.execController.Kill(reason)
}

// IsKilled returns true if Kill has been called.
func (rt *Runtime) IsKilled() bool {
	return rt.execController.IsKilled()
}

// KillReason returns the reason for killing, if killed.
func (rt *Runtime) KillReason() string {
	return rt.execController.KillReason()
}

// CryptoMemoryLimit is Config.CryptoMemoryLimit: the most memory one key
// derivation may use, or 0 for no limit beyond the script's own.
func (rt *Runtime) CryptoMemoryLimit() int64 {
	return rt.cryptoMemoryLimit
}

// IsDone returns true if execution should stop (killed or timed out).
func (rt *Runtime) IsDone() bool {
	return rt.execController.IsDone()
}

// ExecutionContext returns the execution context for cancellation checks.
func (rt *Runtime) ExecutionContext() context.Context {
	return rt.execController.Context()
}

// ResourceTracker returns the resource tracker for this runtime.
func (rt *Runtime) ResourceTracker() *ResourceTracker {
	return rt.resourceTracker
}

// TrackResource adds a resource (file, socket, etc.) to be tracked for cleanup.
// Returns a resource ID that can be used with UntrackResource.
func (rt *Runtime) TrackResource(resource CloseableResource) uint64 {
	return rt.resourceTracker.Track(resource)
}

// UntrackResource removes a resource from tracking.
// Call this when a resource is closed normally.
func (rt *Runtime) UntrackResource(id uint64) {
	rt.resourceTracker.Untrack(id)
}

// OnProcessExit registers fn to run on the JavaScript thread when script calls
// process.exit(), before the runtime ends. Modules use it for work that Node would already
// have finished by then. fn must not block for long.
func (rt *Runtime) OnProcessExit(fn func()) {
	rt.processExitHooks = append(rt.processExitHooks, fn)
}

// RunProcessExitHooks runs the functions registered with OnProcessExit.
func (rt *Runtime) RunProcessExitHooks() {
	for _, fn := range rt.processExitHooks {
		fn()
	}
}

// Dispose releases all resources associated with the runtime.
// This stops the event loop, closes all tracked resources, and disposes the V8 isolate.
func (rt *Runtime) Dispose() {
	rt.mu.Lock()
	if rt.disposed {
		rt.mu.Unlock()
		return
	}
	rt.disposed = true
	rt.mu.Unlock()

	// Kill execution if not already done
	if !rt.execController.IsKilled() {
		rt.execController.Kill("disposed")
	}

	// Stop the event loop
	rt.eventLoop.Stop()

	// Close all tracked resources (files, sockets, etc.)
	rt.resourceTracker.CloseAll()

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.unlimitFS != nil {
		rt.unlimitFS()
	}

	if rt.context != nil {
		rt.context.Dispose()
		rt.context = nil
	}

	if rt.isolate != nil {
		rt.isolate.Dispose()
		rt.isolate = nil
	}
}

// CloseableResource is any resource that can be closed.
type CloseableResource interface {
	Close() error
}
