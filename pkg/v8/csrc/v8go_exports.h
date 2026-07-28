// Declarations for the Go functions exported via //export in the v8 package.
//
// When cgo compiles the package it generates these prototypes in
// _cgo_export.h. This glue (v8go.cc) is pre-compiled OUTSIDE of cgo (with
// Chromium's libc++ so its ABI matches the V8 monolith), so we declare the
// exported callbacks here instead. The symbols are provided by cgo's generated
// object at the consumer's final link step.
//
// Keep these signatures in sync with the //export functions in callback.go.
#ifndef V8GO_EXPORTS_H
#define V8GO_EXPORTS_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

extern void* goCallbackHandler(void* ctx, int callbackID, void* info);
extern int goModuleResolve(int resolverID, char* specifier, char* referrer,
                           char** sourceOut, char** nameOut);
// Near-heap-limit: return the new heap limit in bytes. Typically bump slightly
// so TerminateExecution can unwind without FatalProcessOutOfMemory.
extern size_t goNearHeapLimitCallback(int callbackID, size_t currentHeapLimit,
                                      size_t initialHeapLimit);

#ifdef __cplusplus
}
#endif

#endif // V8GO_EXPORTS_H
