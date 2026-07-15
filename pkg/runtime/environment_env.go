// Package environment provides interfaces for environment variable access.
package runtime

import (
	"os"
	"strings"
	"sync"
)

// Environment defines the interface for environment variable access.
// Implement this interface to customize how process.env behaves.
type Environment interface {
	// Get retrieves an environment variable by name.
	// Returns the value and true if found, empty string and false if not.
	Get(key string) (string, bool)

	// Set sets an environment variable.
	Set(key, value string) error

	// Unset removes an environment variable.
	Unset(key string) error

	// List returns all environment variables as key=value pairs.
	List() []string

	// All returns all environment variables as a map.
	All() map[string]string
}

// RealEnvironment provides access to the actual system environment.
type RealEnvironment struct{}

// NewRealEnvironment creates a new real environment.
func NewRealEnvironment() *RealEnvironment {
	return &RealEnvironment{}
}

// Get retrieves an environment variable from the real environment.
func (e *RealEnvironment) Get(key string) (string, bool) {
	return os.LookupEnv(key)
}

// Set sets an environment variable in the real environment.
func (e *RealEnvironment) Set(key, value string) error {
	return os.Setenv(key, value)
}

// Unset removes an environment variable from the real environment.
func (e *RealEnvironment) Unset(key string) error {
	return os.Unsetenv(key)
}

// List returns all environment variables as key=value pairs.
func (e *RealEnvironment) List() []string {
	return os.Environ()
}

// All returns all environment variables as a map.
func (e *RealEnvironment) All() map[string]string {
	result := make(map[string]string)
	for _, env := range os.Environ() {
		if idx := strings.Index(env, "="); idx != -1 {
			result[env[:idx]] = env[idx+1:]
		}
	}
	return result
}

// SandboxedEnvironment provides an isolated environment that doesn't
// affect or expose the real system environment.
type SandboxedEnvironment struct {
	mu   sync.RWMutex
	vars map[string]string
}

// NewSandboxedEnvironment creates a new sandboxed environment.
// If initial is nil, starts with an empty environment.
func NewSandboxedEnvironment(initial map[string]string) *SandboxedEnvironment {
	vars := make(map[string]string)
	if initial != nil {
		for k, v := range initial {
			vars[k] = v
		}
	}
	return &SandboxedEnvironment{
		vars: vars,
	}
}

// NewSandboxedEnvironmentWithDefaults creates a sandboxed environment
// with common safe defaults (PATH, HOME as /sandbox, etc.)
func NewSandboxedEnvironmentWithDefaults() *SandboxedEnvironment {
	return NewSandboxedEnvironment(map[string]string{
		"PATH":   "/usr/local/bin:/usr/bin:/bin",
		"HOME":   "/sandbox",
		"USER":   "sandbox",
		"SHELL":  "/bin/sh",
		"LANG":   "en_US.UTF-8",
		"LC_ALL": "en_US.UTF-8",
		"TZ":     "UTC",
	})
}

// Get retrieves an environment variable from the sandbox.
func (e *SandboxedEnvironment) Get(key string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	val, ok := e.vars[key]
	return val, ok
}

// Set sets an environment variable in the sandbox.
func (e *SandboxedEnvironment) Set(key, value string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vars[key] = value
	return nil
}

// Unset removes an environment variable from the sandbox.
func (e *SandboxedEnvironment) Unset(key string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.vars, key)
	return nil
}

// List returns all sandbox environment variables as key=value pairs.
func (e *SandboxedEnvironment) List() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]string, 0, len(e.vars))
	for k, v := range e.vars {
		result = append(result, k+"="+v)
	}
	return result
}

// All returns all sandbox environment variables as a map.
func (e *SandboxedEnvironment) All() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make(map[string]string, len(e.vars))
	for k, v := range e.vars {
		result[k] = v
	}
	return result
}

// FilteredEnvironment wraps another environment and filters variables
// based on an allow list or deny list.
type FilteredEnvironment struct {
	base      Environment
	allowList map[string]bool // If non-nil, only these keys are visible
	denyList  map[string]bool // If non-nil, these keys are hidden
}

// NewFilteredEnvironment creates a filtered environment.
// allowList and denyList are mutually exclusive - if allowList is set, denyList is ignored.
func NewFilteredEnvironment(base Environment, allowList, denyList []string) *FilteredEnvironment {
	f := &FilteredEnvironment{base: base}

	if len(allowList) > 0 {
		f.allowList = make(map[string]bool)
		for _, key := range allowList {
			f.allowList[key] = true
		}
	} else if len(denyList) > 0 {
		f.denyList = make(map[string]bool)
		for _, key := range denyList {
			f.denyList[key] = true
		}
	}

	return f
}

// isAllowed checks if a key is allowed by the filter rules.
func (e *FilteredEnvironment) isAllowed(key string) bool {
	if e.allowList != nil {
		return e.allowList[key]
	}
	if e.denyList != nil {
		return !e.denyList[key]
	}
	return true
}

// Get retrieves an environment variable if it's allowed by the filter.
func (e *FilteredEnvironment) Get(key string) (string, bool) {
	if !e.isAllowed(key) {
		return "", false
	}
	return e.base.Get(key)
}

// Set sets an environment variable if it's allowed by the filter.
func (e *FilteredEnvironment) Set(key, value string) error {
	if !e.isAllowed(key) {
		return nil // Silently ignore writes to filtered vars
	}
	return e.base.Set(key, value)
}

// Unset removes an environment variable if it's allowed by the filter.
func (e *FilteredEnvironment) Unset(key string) error {
	if !e.isAllowed(key) {
		return nil
	}
	return e.base.Unset(key)
}

// List returns all allowed environment variables as key=value pairs.
func (e *FilteredEnvironment) List() []string {
	all := e.base.List()
	result := make([]string, 0, len(all))
	for _, env := range all {
		if idx := strings.Index(env, "="); idx != -1 {
			if e.isAllowed(env[:idx]) {
				result = append(result, env)
			}
		}
	}
	return result
}

// All returns all allowed environment variables as a map.
func (e *FilteredEnvironment) All() map[string]string {
	all := e.base.All()
	result := make(map[string]string)
	for k, v := range all {
		if e.isAllowed(k) {
			result[k] = v
		}
	}
	return result
}

// ReadOnlyEnvironment wraps another environment and prevents modifications.
type ReadOnlyEnvironment struct {
	base Environment
}

// NewReadOnlyEnvironment creates a read-only wrapper around an environment.
func NewReadOnlyEnvironment(base Environment) *ReadOnlyEnvironment {
	return &ReadOnlyEnvironment{base: base}
}

// Get retrieves an environment variable from the underlying environment.
func (e *ReadOnlyEnvironment) Get(key string) (string, bool) {
	return e.base.Get(key)
}

// Set silently ignores modifications.
func (e *ReadOnlyEnvironment) Set(key, value string) error {
	return nil // Silently ignore
}

// Unset silently ignores modifications.
func (e *ReadOnlyEnvironment) Unset(key string) error {
	return nil // Silently ignore
}

// List returns all environment variables from the underlying environment.
func (e *ReadOnlyEnvironment) List() []string {
	return e.base.List()
}

// All returns all environment variables from the underlying environment.
func (e *ReadOnlyEnvironment) All() map[string]string {
	return e.base.All()
}
