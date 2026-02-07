// Package runtime provides the JavaScript runtime environment.
package runtime

import (
	"fmt"
	"sync"

	"github.com/andrewcurioso/gnode/pkg/filesystem"
	"github.com/andrewcurioso/gnode/pkg/network"
	"github.com/andrewcurioso/gnode/pkg/system"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

// Runtime represents a JavaScript runtime environment.
type Runtime struct {
	isolate    *v8go.Isolate
	context    *v8go.Context
	eventLoop  *EventLoop
	modules    map[string]Module
	filesystem filesystem.Filesystem
	systemInfo system.SystemInfo
	httpClient network.HTTPClient
	mu         sync.Mutex
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
	Filesystem filesystem.Filesystem
	// SystemInfo is the system information provider.
	// If nil, real system information is used.
	SystemInfo system.SystemInfo
	// HTTPClient is the HTTP client for outbound requests.
	// If nil, a real HTTP client with no restrictions is used.
	HTTPClient network.HTTPClient
}

// DefaultConfig returns a configuration with common modules enabled.
func DefaultConfig() *Config {
	return &Config{
		EnableConsole: true,
		EnableTimers:  true,
		Filesystem:    nil, // Will default to unrestricted local filesystem
		SystemInfo:    nil, // Will default to real system info
		HTTPClient:    nil, // Will default to real HTTP client
	}
}

// New creates a new JavaScript runtime.
func New(cfg *Config) (*Runtime, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	iso, err := v8go.NewIsolate()
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
		fs = filesystem.NewLocalFilesystem("")
	}

	// Set up system info
	sysInfo := cfg.SystemInfo
	if sysInfo == nil {
		sysInfo = system.NewRealSystemInfo()
	}

	// Set up HTTP client
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = network.NewRealHTTPClient()
	}

	rt := &Runtime{
		isolate:    iso,
		context:    ctx,
		eventLoop:  NewEventLoop(),
		modules:    make(map[string]Module),
		filesystem: fs,
		systemInfo: sysInfo,
		httpClient: httpClient,
	}

	return rt, nil
}

// Filesystem returns the filesystem implementation for this runtime.
func (rt *Runtime) Filesystem() filesystem.Filesystem {
	return rt.filesystem
}

// SystemInfo returns the system information provider for this runtime.
func (rt *Runtime) SystemInfo() system.SystemInfo {
	return rt.systemInfo
}

// HTTPClient returns the HTTP client for this runtime.
func (rt *Runtime) HTTPClient() network.HTTPClient {
	return rt.httpClient
}

// Isolate returns the underlying V8 isolate.
func (rt *Runtime) Isolate() *v8go.Isolate {
	return rt.isolate
}

// Context returns the underlying V8 context.
func (rt *Runtime) Context() *v8go.Context {
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

// SetGlobal sets a global variable in the runtime context.
func (rt *Runtime) SetGlobal(name string, value *v8go.Value) error {
	global, err := rt.context.Global()
	if err != nil {
		return err
	}
	return global.Set(name, value)
}

// GetGlobal gets a global variable from the runtime context.
func (rt *Runtime) GetGlobal(name string) (*v8go.Value, error) {
	global, err := rt.context.Global()
	if err != nil {
		return nil, err
	}
	return global.Get(name)
}

// RunScript executes JavaScript code.
func (rt *Runtime) RunScript(source, origin string) (*v8go.Value, error) {
	return rt.context.RunScript(source, origin)
}

// Run executes JavaScript code and runs the event loop until completion.
func (rt *Runtime) Run(source, origin string) (*v8go.Value, error) {
	result, err := rt.RunScript(source, origin)
	if err != nil {
		return nil, err
	}

	// Run the event loop to process any pending async operations
	rt.eventLoop.Run()

	return result, nil
}

// Dispose releases all resources associated with the runtime.
func (rt *Runtime) Dispose() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.eventLoop.Stop()

	if rt.context != nil {
		rt.context.Dispose()
		rt.context = nil
	}

	if rt.isolate != nil {
		rt.isolate.Dispose()
		rt.isolate = nil
	}
}
