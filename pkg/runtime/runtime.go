// Package runtime provides the JavaScript runtime environment.
package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"proto.zip/studio/orbital/pkg/v8"
)

// Runtime represents a JavaScript runtime environment.
type Runtime struct {
	isolate           *v8.Isolate
	context           *v8.Context
	eventLoop         *EventLoop
	modules           map[string]Module
	nativeModules     map[string]*v8.Value // User-registered native modules
	filesystem        Filesystem
	systemInfo        SystemInfo
	httpClient        HTTPClient
	environment       Environment
	dnsResolver       Resolver
	socketFactory     SocketFactory
	processSpawner    ProcessSpawner
	documentRoot      string
	resourceTracker   *ResourceTracker
	execController    *ExecutionController
	disposed          bool
	mu                sync.Mutex
}

// Module represents a Node.js module that can be registered with the runtime.
type Module interface {
	// Name returns the module name (e.g., "console", "fs", "path")
	Name() string
	// Register sets up the module in the given runtime context
	Register(rt *Runtime) error
}

// Config contains configuration options for creating a runtime.
type Config struct {
	// EnableConsole enables the console global object
	EnableConsole bool
	// EnableTimers enables setTimeout, setInterval, etc.
	EnableTimers bool
	// Filesystem is the filesystem implementation to use.
	// If nil, a local filesystem with no restrictions is used.
	Filesystem Filesystem
	// SystemInfo is the system information provider.
	// If nil, real system information is used.
	SystemInfo SystemInfo
	// HTTPClient is the HTTP client for outbound requests.
	// If nil, a real HTTP client with no restrictions is used.
	HTTPClient HTTPClient
	// Environment is the environment variable provider.
	// If nil, real system environment variables are used.
	Environment Environment
	// DNSResolver is the DNS resolver for network lookups.
	// If nil, the system's DNS resolver is used.
	DNSResolver Resolver
	// SocketFactory creates TCP/UDP sockets.
	// If nil, real sockets with no restrictions are used.
	SocketFactory SocketFactory
	// ProcessSpawner spawns child processes.
	// If nil, real process spawning with no restrictions is used.
	ProcessSpawner ProcessSpawner
	// DocumentRoot is the root directory for sandboxing.
	// When set, paths like process.cwd() are relative to this root.
	DocumentRoot string
	// Timeout is the maximum execution time. 0 means no timeout.
	Timeout time.Duration
}

// DefaultConfig returns a configuration with common modules enabled.
func DefaultConfig() *Config {
	return &Config{
		EnableConsole:  true,
		EnableTimers:   true,
		Filesystem:     nil, // Will default to unrestricted local filesystem
		SystemInfo:     nil, // Will default to real system info
		HTTPClient:     nil, // Will default to real HTTP client
		Environment:    nil, // Will default to real environment
		DNSResolver:    nil, // Will default to system DNS resolver
		SocketFactory:  nil, // Will default to real sockets
		ProcessSpawner: nil, // Will default to real process spawning
	}
}

// New creates a new JavaScript runtime.
func New(cfg *Config) (*Runtime, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	iso, err := v8.NewIsolate()
	if err != nil {
		return nil, fmt.Errorf("failed to create isolate: %w", err)
	}

	ctx, err := iso.NewContext()
	if err != nil {
		iso.Dispose()
		return nil, fmt.Errorf("failed to create context: %w", err)
	}

	// Set up filesystem
	fs := cfg.Filesystem
	if fs == nil {
		fs = NewLocalFilesystem("")
	}

	// Set up system info
	sysInfo := cfg.SystemInfo
	if sysInfo == nil {
		sysInfo = NewRealSystemInfo()
	}

	// Set up HTTP client
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = NewRealHTTPClient()
	}

	// Set up environment
	env := cfg.Environment
	if env == nil {
		env = NewRealEnvironment()
	}

	// Set up DNS resolver
	dnsResolver := cfg.DNSResolver
	if dnsResolver == nil {
		dnsResolver = NewRealResolver()
	}

	// Set up socket factory
	socketFactory := cfg.SocketFactory
	if socketFactory == nil {
		socketFactory = NewRealSocketFactory()
	}

	// Set up process spawner
	processSpawner := cfg.ProcessSpawner
	if processSpawner == nil {
		processSpawner = NewRealProcessSpawner()
	}

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
		systemInfo:      sysInfo,
		httpClient:      httpClient,
		environment:     env,
		dnsResolver:     dnsResolver,
		socketFactory:   socketFactory,
		processSpawner:  processSpawner,
		documentRoot:    cfg.DocumentRoot,
		resourceTracker: resourceTracker,
		execController:  execController,
	}

	// Set up kill callback to stop the event loop and cancel all tasks
	execController.OnKill(func() {
		rt.eventLoop.CancelAllTimers()
		rt.eventLoop.ClearAllMicrotasks()
		rt.eventLoop.Stop()
	})

	return rt, nil
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

// ProcessSpawner returns the process spawner for this runtime.
func (rt *Runtime) ProcessSpawner() ProcessSpawner {
	return rt.processSpawner
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
func (rt *Runtime) Run(source, origin string) (*v8.Value, error) {
	result, err := rt.RunScript(source, origin)
	if err != nil {
		return nil, err
	}

	// Run the event loop to process any pending async operations
	rt.eventLoop.Run()

	return result, nil
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
