package v8

import (
	"sync"
	"unsafe"
)

// Each Value Go holds keeps a native handle alive until it's released, and nothing releases
// one on its own. Code that reads many short-lived values, like a formatter walking an object,
// can run inside ReleaseAfter, which releases every Value made in the isolate while it runs.
//
// Scopes are kept per isolate, as a Go callback gets a Context of its own for the context it
// runs in, and belong to the goroutine running the isolate's JavaScript, the only one that may
// make Values in it: like the rest of an isolate's state, they're not for concurrent use.

// releaseScope collects the Values made while it's the innermost scope of its isolate. A kept
// scope collects nothing: it stands for a Go callback that JavaScript called while an outer
// scope was open, whose Values may be kept past the outer scope's end (a function given to a
// timer, say), so they mustn't be released with it.
type releaseScope struct {
	keep bool
	made []*Value
}

// releaseScopes holds, for each isolate with scopes open, keyed by its native pointer, its
// scopes, the innermost last.
var releaseScopes = struct {
	sync.Mutex
	open map[unsafe.Pointer][]*releaseScope
}{open: map[unsafe.Pointer][]*releaseScope{}}

// newValue wraps ptr, a native handle in ctx, and records it in the innermost scope of ctx's
// isolate, unless that's a kept one.
func newValue(ptr unsafe.Pointer, ctx *Context) *Value {
	v := &Value{ptr: ptr, ctx: ctx}
	if ctx == nil || ctx.iso == nil || ctx.iso.ptr == nil {
		return v
	}
	releaseScopes.Lock()
	if scopes := releaseScopes.open[ctx.iso.ptr]; len(scopes) > 0 {
		if top := scopes[len(scopes)-1]; !top.keep {
			top.made = append(top.made, v)
		}
	}
	releaseScopes.Unlock()
	return v
}

// openScope opens a scope on the isolate ptr and answers a function that closes it, returning
// the Values it collected.
func openScope(ptr unsafe.Pointer, keep bool) func() []*Value {
	scope := &releaseScope{keep: keep}
	releaseScopes.Lock()
	releaseScopes.open[ptr] = append(releaseScopes.open[ptr], scope)
	releaseScopes.Unlock()
	return func() []*Value {
		releaseScopes.Lock()
		defer releaseScopes.Unlock()
		scopes := releaseScopes.open[ptr]
		// Scopes close innermost first, so this is the last one.
		scopes = scopes[:len(scopes)-1]
		if len(scopes) == 0 {
			delete(releaseScopes.open, ptr)
		} else {
			releaseScopes.open[ptr] = scopes
		}
		return scope.made
	}
}

// keepValues runs f, a Go callback JavaScript called, with Values made in the isolate ptr kept
// out of any scope already open there.
func keepValues(ptr unsafe.Pointer, f func()) {
	if ptr == nil {
		f()
		return
	}
	releaseScopes.Lock()
	open := len(releaseScopes.open[ptr]) > 0
	releaseScopes.Unlock()
	if !open {
		f()
		return
	}
	closeScope := openScope(ptr, true)
	defer closeScope()
	f()
}

// ReleaseAfter runs f and then releases every Value made in c's isolate while it ran, so none
// of them may be kept or returned past it. Values made before it, in other isolates, or in Go
// callbacks that JavaScript calls meanwhile are left alone. Calls nest: an inner one releases
// only its own.
func (c *Context) ReleaseAfter(f func()) {
	if c == nil || c.ptr == nil || c.iso == nil || c.iso.ptr == nil {
		f()
		return
	}
	closeScope := openScope(c.iso.ptr, false)
	defer func() {
		for _, v := range closeScope() {
			v.Release()
		}
	}()
	f()
}
