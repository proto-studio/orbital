// V8 Go bindings C++ implementation.
//
// This file is pre-compiled into a per-platform static library (libv8go_glue.a)
// using V8's own Chromium clang + libc++, so its C++ ABI (the std::__Cr::
// namespace, pointer compression and sandbox layouts) matches libv8_monolith.a.
// It deliberately lives in this csrc/ subdirectory so the go tool / cgo does NOT
// compile it against the system libstdc++ (which would produce mismatched
// std:: symbols). See the Makefile `v8-build` target and scripts/build-glue.py.
#include "v8go.h"
#include "v8go_exports.h"

#include <v8.h>
#include <v8-external-memory-accounter.h>
#include <libplatform/libplatform.h>
#include <algorithm>
#include <atomic>
#include <cstdint>
#include <cstring>
#include <memory>
#include <string>
#include <unordered_map>
#include <vector>

#include <unistd.h>

// untypedName is a module cache key without the type a typed import adds
// ("\x01<type>\x01<url>" is <url>): the module's url, for its script origin
// (stack frames, errors) and import.meta.url.
static std::string untypedName(const char* key) {
    std::string name(key ? key : "");
    if (!name.empty() && name[0] == '\x01') {
        size_t end = name.find('\x01', 1);
        name = end == std::string::npos ? std::string() : name.substr(end + 1);
    }
    return name;
}


using namespace v8;

// Global platform instance
static std::unique_ptr<Platform> g_platform;
static bool g_initialized = false;

struct ModuleWrapper;  // fwd decl (defined below)

// Wrapper to hold persistent handles. All ES-module state is scoped HERE (not in
// process globals) so distinct isolates/contexts ("sandboxes") never share a
// module cache or resolver. A pointer to this wrapper is stashed in the
// context's embedder data (slot kContextWrapperSlot) so V8 callbacks that only
// receive a Local<Context> (module resolution, dynamic import) can recover it.
struct ContextWrapper {
    Isolate* isolate;
    Global<Context> context;
    // Resolved modules for THIS context, keyed by resolved name/url.
    std::unordered_map<std::string, ModuleWrapper*> module_cache;
    // Go resolver id for the in-progress static instantiation (scoped to a
    // single InstantiateModule call) and the persistent one for import().
    int module_resolver_id = -1;
    int dynamic_resolver_id = -1;
    // JavaScript function told about promise rejections; see v8go_set_promise_reject_handler.
    Global<Function> promise_reject_handler;
    // What the last failed v8go_context_run_script threw, until it is taken.
    Global<Value> last_script_exception;
    // Where V8's message for the exception last caught by v8go_context_run_script or
    // callRecording places it, until it is taken: see v8go_context_take_exception_place.
    struct {
        bool has = false;
        std::string resource;
        int line = 0;
        std::string source_line;
        bool has_source_line = false; // false if V8 gave none or it was too long
        int start = -1;
        int end = -1;
    } exception_place;
};

// Embedder-data slot in which each Context stores its ContextWrapper*.
static const int kContextWrapperSlot = 1;
// V8 15.4+ requires a type tag on aligned embedder-data pointer APIs.
// Store and load must use the same tag.
static constexpr EmbedderDataTypeTag kContextWrapperTag = 1;

static ContextWrapper* wrapperFromContext(Local<Context> context) {
    if (context.IsEmpty()) return nullptr;
    return static_cast<ContextWrapper*>(
        context->GetAlignedPointerFromEmbedderData(
            kContextWrapperSlot, kContextWrapperTag));
}

struct ValueWrapper {
    Isolate* isolate;
    Global<Value> value;
};

// A message's name and line longer than these are left out: the header shows one line,
// and copying a huge one out of V8 would be memory V8's heap limit doesn't cover.
static const int kMaxPlaceResource = 4096;
static const int kMaxPlaceLine = 16384;

// recordPlace keeps where message places an exception, for
// v8go_context_take_exception_place. An empty message clears what was kept.
static void recordPlace(ContextWrapper* wrapper, Isolate* isolate, Local<Context> context,
                        Local<Message> message) {
    auto& p = wrapper->exception_place;
    p = {};
    if (message.IsEmpty()) return;
    Local<Value> resource_value = message->GetScriptResourceName();
    if (resource_value->IsString() && resource_value.As<String>()->Length() > kMaxPlaceResource) return;
    String::Utf8Value resource(isolate, resource_value);
    p.resource = *resource ? *resource : "";
    p.line = message->GetLineNumber(context).FromMaybe(0);
    Local<String> source_line;
    if (message->GetSourceLine(context).ToLocal(&source_line) && source_line->Length() <= kMaxPlaceLine) {
        String::Utf8Value text(isolate, source_line);
        // With its length: a NUL character doesn't end it.
        if (*text) p.source_line.assign(*text, text.length());
        p.has_source_line = true;
    }
    p.start = message->GetStartColumn(context).FromMaybe(-1);
    p.end = message->GetEndColumn(context).FromMaybe(-1);
    p.has = true;
}

// callRecording is callRecording(fn, recv, ...args): it calls fn with recv and args and
// returns what fn returns. If fn throws, it keeps where V8's message places the throw
// before throwing the exception on: once JavaScript catches an exception, V8 no longer
// has that place.
static void callRecording(const FunctionCallbackInfo<Value>& info) {
    Isolate* isolate = info.GetIsolate();
    Local<Context> context = isolate->GetCurrentContext();
    ContextWrapper* wrapper = wrapperFromContext(context);
    if (info.Length() < 2 || !info[0]->IsFunction()) {
        if (wrapper) wrapper->exception_place = {};
        isolate->ThrowException(Exception::TypeError(
            String::NewFromUtf8Literal(isolate, "callRecording(fn, recv, ...args)")));
        return;
    }
    int argc = info.Length() - 2;
    Local<Value> inline_args[4];
    std::vector<Local<Value>> more;
    Local<Value>* args = inline_args;
    if (argc > 4) {
        more.resize(argc);
        args = more.data();
    }
    for (int i = 0; i < argc; i++) args[i] = info[i + 2];
    TryCatch try_catch(isolate);
    MaybeLocal<Value> result = info[0].As<Function>()->Call(context, info[1], argc, argc > 0 ? args : nullptr);
    if (result.IsEmpty()) {
        if (try_catch.HasCaught() && !try_catch.HasTerminated() && wrapper) {
            recordPlace(wrapper, isolate, context, try_catch.Message());
        }
        if (try_catch.CanContinue()) try_catch.ReThrow();
        return;
    }
    info.GetReturnValue().Set(result.ToLocalChecked());
}

struct FunctionTemplateWrapper {
    Isolate* isolate;
    Global<FunctionTemplate> tmpl;
    int callback_id;
};

struct ObjectTemplateWrapper {
    Isolate* isolate;
    Global<ObjectTemplate> tmpl;
};

struct CallbackInfo {
    Isolate* isolate;
    const FunctionCallbackInfo<Value>* v8_info;
    std::vector<ValueWrapper*> args;
    ValueWrapper* this_value;
};

struct ModuleWrapper {
    Isolate* isolate;
    Global<Module> module;
    std::string name;
};

// NOTE: all ES-module state (cache + resolver ids) lives in ContextWrapper so
// it is scoped per sandbox; see the struct above.

// Helper to extract isolate from context wrapper
static Isolate* getIsolate(void* context_ptr) {
    if (!context_ptr) return nullptr;
    return static_cast<ContextWrapper*>(context_ptr)->isolate;
}

static Local<Context> getLocalContext(void* context_ptr) {
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    return wrapper->context.Get(wrapper->isolate);
}

extern "C" {

// ---- memory budget ---------------------------------------------------------

struct v8go_budget {
    std::atomic<size_t> used{0};
    size_t limit = 0;
    std::atomic<long> refs{1};
};

v8go_budget* v8go_budget_new(size_t limit) {
    v8go_budget* budget = new v8go_budget();
    budget->limit = limit;
    return budget;
}

static void budgetRef(v8go_budget* budget) {
    if (budget) budget->refs.fetch_add(1, std::memory_order_relaxed);
}

void v8go_budget_unref(v8go_budget* budget) {
    if (budget && budget->refs.fetch_sub(1, std::memory_order_acq_rel) == 1) {
        delete budget;
    }
}

// budgetChargeUpTo takes as much of want as fits, if that's at least least,
// and returns what it took: 0 when less than least fits.
static size_t budgetChargeUpTo(v8go_budget* budget, size_t want, size_t least) {
    size_t used = budget->used.load(std::memory_order_relaxed);
    for (;;) {
        size_t room = used < budget->limit ? budget->limit - used : 0;
        size_t take = std::min(want, room);
        if (take < least || (take == 0 && want > 0)) return 0;
        if (budget->used.compare_exchange_weak(used, used + take,
                                               std::memory_order_relaxed)) {
            return take;
        }
    }
}

int v8go_budget_charge(v8go_budget* budget, size_t n) {
    if (!budget || n == 0) return 1;
    return budgetChargeUpTo(budget, n, n) == n ? 1 : 0;
}

void v8go_budget_release(v8go_budget* budget, size_t n) {
    if (budget && n > 0) budget->used.fetch_sub(n, std::memory_order_relaxed);
}

size_t v8go_budget_used(v8go_budget* budget) {
    return budget ? budget->used.load(std::memory_order_relaxed) : 0;
}

size_t v8go_budget_limit(v8go_budget* budget) {
    return budget ? budget->limit : 0;
}

// BudgetAllocator charges ArrayBuffer contents to a budget. An allocation that
// doesn't fit fails, which V8 reports, after collecting garbage and trying
// again, as RangeError: Array buffer allocation failed. V8 may free from its
// own threads, so the budget is only touched atomically.
class BudgetAllocator final : public ArrayBuffer::Allocator {
 public:
    BudgetAllocator(ArrayBuffer::Allocator* inner, v8go_budget* budget)
        : inner_(inner), budget_(budget) {
        budgetRef(budget_);
    }
    ~BudgetAllocator() override {
        delete inner_;
        v8go_budget_unref(budget_);
    }

    void* Allocate(size_t length) override {
        if (!v8go_budget_charge(budget_, length)) return nullptr;
        void* data = inner_->Allocate(length);
        if (!data) v8go_budget_release(budget_, length);
        return data;
    }

    void* AllocateUninitialized(size_t length) override {
        if (!v8go_budget_charge(budget_, length)) return nullptr;
        void* data = inner_->AllocateUninitialized(length);
        if (!data) v8go_budget_release(budget_, length);
        return data;
    }

    void Free(void* data, size_t length) override {
        inner_->Free(data, length);
        v8go_budget_release(budget_, length);
    }

    size_t MaxAllocationSize() const override {
        return inner_->MaxAllocationSize();
    }

    PageAllocator* GetPageAllocator() override {
        return inner_->GetPageAllocator();
    }

 private:
    ArrayBuffer::Allocator* inner_;
    v8go_budget* budget_;
};

// Per-isolate state stashed in Isolate::SetData(kIsolateStateSlot). Owns the
// ArrayBuffer allocator (V8 does not free it on Dispose) and tracks the Go
// near-heap-limit callback id for RemoveNearHeapLimitCallback.
struct IsolateState {
    ArrayBuffer::Allocator* allocator = nullptr;
    int near_heap_limit_cb_id = -1;
    // Prefer ExternalMemoryAccounter over the deprecated
    // Isolate::AdjustAmountOfExternalAllocatedMemory (sandbox-safe).
    ExternalMemoryAccounter external_memory;
    // The budget the heap draws from, or null. heap_charged is what it has
    // taken for this isolate's heap, given back when the isolate is disposed.
    v8go_budget* budget = nullptr;
    size_t heap_charged = 0;
    // The most the old generation may grow to.
    size_t old_generation_cap = 0;
    std::atomic<bool> out_of_memory{false};
    // The limit given an isolate that ran out of memory to finish unwinding.
    size_t recovery_limit = 0;
};

static const int kIsolateStateSlot = 0;

static IsolateState* isolateState(Isolate* isolate) {
    if (!isolate) return nullptr;
    return static_cast<IsolateState*>(isolate->GetData(kIsolateStateSlot));
}

static const size_t kMB = 1024 * 1024;
// How much of the old generation an isolate with a budget starts with. It
// grows from there, drawing from the budget, up to its cap.
static const size_t kInitialOldGeneration = 16 * kMB;
// The old generation grows by at least this much at a time (or what's left),
// and by its current size when that's more, so it doubles.
static const size_t kOldGenerationStep = 16 * kMB;
// Room given to an isolate that ran out of memory to finish the collection it
// is in and unwind, as Node gives a worker that reaches its limit. Without it
// V8 aborts the process.
static const size_t kOutOfMemoryAllowance = 16 * kMB;

// oldGenerationCommitted is how much of the heap the near-heap-limit
// callback's limit applies to is taken: every space but the young
// generation's (and the read-only one, which is shared), counted in whole
// pages, as V8 counts it against the limit (Heap::OldGenerationCapacity).
static size_t oldGenerationCommitted(Isolate* isolate) {
    size_t used = 0;
    HeapSpaceStatistics stats;
    for (size_t i = 0; i < isolate->NumberOfHeapSpaces(); i++) {
        if (!isolate->GetHeapSpaceStatistics(&stats, i)) continue;
        const char* name = stats.space_name();
        if (name && (strcmp(name, "new_space") == 0 ||
                     strcmp(name, "new_large_object_space") == 0 ||
                     strcmp(name, "read_only_space") == 0)) {
            continue;
        }
        used += stats.space_size();
    }
    return used;
}

// budgetHeapLimit is V8's near-heap-limit callback for every isolate: it grows
// the old generation from the budget while the cap and the budget allow, and
// otherwise stops the isolate as out of memory instead of letting V8 abort.
//
// V8 also calls it at the start of a last-resort collection, such as those it
// runs before failing an ArrayBuffer allocation the budget refused, whatever
// the heap holds. It grows the heap or runs out only when the heap is near its
// limit: V8 aborts only when the old generation's pages still reach the limit
// after the callback (Heap::CheckHeapLimitReached).
// recoveryLimit is the limit for an isolate that has run out of memory: one
// fixed limit with room to unwind, set the first time and raised only if what's
// in use reaches it, since V8 aborts the process otherwise. Its execution is
// terminated again each time, and it can't run JavaScript any more
// (outOfMemoryGuard), so what it uses past its limit stays bounded.
static size_t recoveryLimit(Isolate* isolate, IsolateState* state, size_t in_use) {
    isolate->TerminateExecution();
    if (!state) return in_use + kOutOfMemoryAllowance;
    if (state->recovery_limit < in_use + kMB) {
        state->recovery_limit = in_use + kOutOfMemoryAllowance;
    }
    return state->recovery_limit;
}

// outOfMemoryGuard runs before every call into JavaScript, and terminates it in
// an isolate that has run out of memory.
static void outOfMemoryGuard(Isolate* isolate) {
    IsolateState* state = isolateState(isolate);
    if (state && state->out_of_memory.load()) isolate->TerminateExecution();
}

static size_t budgetHeapLimit(void* data, size_t current_heap_limit,
                              size_t initial_heap_limit) {
    Isolate* isolate = static_cast<Isolate*>(data);
    IsolateState* state = isolateState(isolate);
    size_t used = oldGenerationCommitted(isolate);
    // A large object can take the heap past its limit in one allocation, so
    // whatever the callback returns has to cover what's in use already.
    size_t in_use = std::max(current_heap_limit, used);
    if (!state || state->out_of_memory.load()) {
        return recoveryLimit(isolate, state, in_use);
    }
    // An embedder's callback (Isolate.AddNearHeapLimitCallback) is asked first.
    // The heap it grows is charged to the budget like any other, and when it
    // doesn't grow it past what's in use, this decides as it otherwise would.
    if (state->near_heap_limit_cb_id >= 0) {
        size_t want = goNearHeapLimitCallback(state->near_heap_limit_cb_id,
                                              current_heap_limit, initial_heap_limit);
        if (want > in_use) {
            size_t extra = want - current_heap_limit;
            if (!state->budget || budgetChargeUpTo(state->budget, extra, extra) == extra) {
                if (state->budget) state->heap_charged += extra;
                return want;
            }
        }
        if (state->out_of_memory.load()) {
            return recoveryLimit(isolate, state, in_use);
        }
    }
    if (used < current_heap_limit - current_heap_limit / 8) {
        return current_heap_limit;
    }
    if (state->budget && current_heap_limit < state->old_generation_cap) {
        size_t target = std::max(current_heap_limit + std::max(current_heap_limit, kOldGenerationStep),
                                 used + kOldGenerationStep);
        target = std::min(target, state->old_generation_cap);
        // It has to grow past what's in use, with some room to spare.
        size_t least = in_use - current_heap_limit + kMB;
        if (target >= current_heap_limit + least) {
            size_t got = budgetChargeUpTo(state->budget, target - current_heap_limit, least);
            if (got > 0) {
                state->heap_charged += got;
                return current_heap_limit + got;
            }
        }
    }
    state->out_of_memory.store(true);
    size_t limit = recoveryLimit(isolate, state, in_use);
    goIsolateOutOfMemory(isolate);
    return limit;
}

static size_t physicalMemory() {
    long pages = sysconf(_SC_PHYS_PAGES);
    long page_size = sysconf(_SC_PAGESIZE);
    if (pages <= 0 || page_size <= 0) return 0;
    return static_cast<size_t>(pages) * static_cast<size_t>(page_size);
}

void v8go_init() {
    if (g_initialized) return;

    // Required for Isolate::RequestGarbageCollectionForTesting. Must be set
    // before Initialize(). Production GC nudging should use
    // MemoryPressureNotification instead.
    V8::SetFlagsFromString("--expose_gc");

    // On x64 Linux with PKU hardware, NewDefaultPlatform allocates a memory
    // protection key and V8 tags its code pages with it. Only the thread that
    // created the platform, and threads it later spawns, can read those pages;
    // every other thread gets SEGV_PKUERR. Go runs isolates on whichever OS
    // thread the scheduler picks, including threads that existed before this
    // call (or are spawned by Go's template thread), so turn the keys off.
    // Must be set before V8::Initialize, which reads it.
    V8::SetFlagsFromString("--no-memory-protection-keys");

    V8::InitializeICUDefaultLocation("");
    V8::InitializeExternalStartupData("");
    g_platform = v8::platform::NewDefaultPlatform();
    V8::InitializePlatform(g_platform.get());
    V8::Initialize();

    g_initialized = true;
}

void v8go_dispose() {
    if (!g_initialized) return;

    V8::Dispose();
    V8::DisposePlatform();
    g_platform.reset();

    g_initialized = false;
}

void* v8go_isolate_new() {
    return v8go_isolate_new_with_params(nullptr);
}

void* v8go_isolate_new_with_params(const v8go_isolate_create_params* params) {
    return v8go_isolate_new_with_limits(params, nullptr, nullptr);
}

void* v8go_isolate_new_with_limits(const v8go_isolate_create_params* params,
                                   v8go_isolate_limits* limits, int* failure) {
    if (failure) *failure = V8GO_ISOLATE_FAILED;
    v8go_isolate_create_params none = {};
    if (!params) params = &none;
    v8go_budget* budget = params->budget;

    Isolate::CreateParams create_params;
    ResourceConstraints& constraints = create_params.constraints;
    // ConfigureDefaultsFromHeapSize sets both young+old defaults from a total
    // heap budget. Prefer it when a hard isolate ceiling is desired. Without
    // one, the defaults are V8's own for this machine (as Node sets them), or,
    // with a budget, those for a heap as large as the budget.
    size_t max_heap = params->max_heap_size_in_bytes;
    if (max_heap == 0 && budget) max_heap = budget->limit;
    if (max_heap > 0 || params->initial_heap_size_in_bytes > 0) {
        constraints.ConfigureDefaultsFromHeapSize(
            params->initial_heap_size_in_bytes, max_heap);
    } else {
        constraints.ConfigureDefaults(physicalMemory(), 0);
    }
    if (params->max_old_generation_size_in_bytes > 0) {
        constraints.set_max_old_generation_size_in_bytes(
            params->max_old_generation_size_in_bytes);
    }
    size_t old_cap = constraints.max_old_generation_size_in_bytes();
    size_t young = constraints.max_young_generation_size_in_bytes();
    if (params->cap_old_generation_size_in_bytes > 0) {
        old_cap = std::min(old_cap, params->cap_old_generation_size_in_bytes);
    }
    if (params->cap_young_generation_size_in_bytes > 0) {
        young = std::min(young, params->cap_young_generation_size_in_bytes);
    }

    // With a budget, the young generation is taken whole and the old one
    // starts small and grows (budgetHeapLimit), up to what the budget had room
    // for when the isolate was made.
    size_t heap_charged = 0;
    size_t old_start = old_cap;
    if (budget) {
        size_t used = budget->used.load();
        size_t room = used < budget->limit ? budget->limit - used : 0;
        if (room < young + kMB) {
            if (failure) *failure = V8GO_ISOLATE_OUT_OF_MEMORY;
            return nullptr;
        }
        old_cap = std::min(old_cap, room - young);
        old_start = std::min(old_cap, kInitialOldGeneration);
        heap_charged = young + old_start;
        if (!v8go_budget_charge(budget, heap_charged)) {
            if (failure) *failure = V8GO_ISOLATE_OUT_OF_MEMORY;
            return nullptr;
        }
    }
    constraints.set_max_old_generation_size_in_bytes(old_start);
    constraints.set_max_young_generation_size_in_bytes(young);

    ArrayBuffer::Allocator* allocator = ArrayBuffer::Allocator::NewDefaultAllocator();
    if (budget) allocator = new BudgetAllocator(allocator, budget);
    create_params.array_buffer_allocator = allocator;

    // The callback goes in before V8 sets the isolate up, so a heap too small
    // for the snapshot runs out of memory rather than aborting the process.
    Isolate* isolate = Isolate::Allocate();
    if (!isolate) {
        delete allocator;
        v8go_budget_release(budget, heap_charged);
        return nullptr;
    }
    IsolateState* state = new IsolateState();
    state->allocator = allocator;
    state->budget = budget;
    budgetRef(budget);
    state->heap_charged = heap_charged;
    state->old_generation_cap = old_cap;
    isolate->SetData(kIsolateStateSlot, state);
    isolate->AddNearHeapLimitCallback(budgetHeapLimit, isolate);
    isolate->AddBeforeCallEnteredCallback(outOfMemoryGuard);
    Isolate::Initialize(isolate, create_params);
    if (state->out_of_memory.load()) {
        // Its heap was too small for V8 to set it up.
        v8go_isolate_dispose(isolate);
        if (failure) *failure = V8GO_ISOLATE_SETUP_OUT_OF_MEMORY;
        return nullptr;
    }

    if (limits) {
        limits->max_old_generation_size_in_bytes = old_cap;
        limits->max_young_generation_size_in_bytes = young;
    }
    if (failure) *failure = 0;
    return isolate;
}

int v8go_isolate_out_of_memory(void* ptr) {
    IsolateState* state = isolateState(static_cast<Isolate*>(ptr));
    return state && state->out_of_memory.load() ? 1 : 0;
}

void v8go_isolate_dispose(void* ptr) {
    if (!ptr) return;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    IsolateState* state = isolateState(isolate);
    isolate->SetData(kIsolateStateSlot, nullptr);
    isolate->Dispose();
    if (state) {
        // The allocator gives back its ArrayBuffers' share as V8 frees them,
        // which Dispose has done.
        delete state->allocator;
        v8go_budget_release(state->budget, state->heap_charged);
        v8go_budget_unref(state->budget);
        delete state;
    }
}

void v8go_isolate_get_heap_statistics(void* ptr, v8go_heap_statistics* out) {
    if (!ptr || !out) return;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HeapStatistics stats;
    isolate->GetHeapStatistics(&stats);
    out->total_heap_size = stats.total_heap_size();
    out->total_heap_size_executable = stats.total_heap_size_executable();
    out->total_physical_size = stats.total_physical_size();
    out->total_available_size = stats.total_available_size();
    out->total_global_handles_size = stats.total_global_handles_size();
    out->used_global_handles_size = stats.used_global_handles_size();
    out->used_heap_size = stats.used_heap_size();
    out->heap_size_limit = stats.heap_size_limit();
    out->malloced_memory = stats.malloced_memory();
    out->external_memory = stats.external_memory();
    out->peak_malloced_memory = stats.peak_malloced_memory();
    out->number_of_native_contexts = stats.number_of_native_contexts();
    out->number_of_detached_contexts = stats.number_of_detached_contexts();
    out->total_allocated_bytes = stats.total_allocated_bytes();
}

size_t v8go_isolate_number_of_heap_spaces(void* ptr) {
    if (!ptr) return 0;
    return static_cast<Isolate*>(ptr)->NumberOfHeapSpaces();
}

int v8go_isolate_get_heap_space_statistics(void* ptr, size_t index,
                                           v8go_heap_space_statistics* out) {
    if (!ptr || !out) return 0;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    HeapSpaceStatistics stats;
    if (!isolate->GetHeapSpaceStatistics(&stats, index)) {
        return 0;
    }
    out->space_name = stats.space_name();
    out->space_size = stats.space_size();
    out->space_used_size = stats.space_used_size();
    out->space_available_size = stats.space_available_size();
    out->physical_space_size = stats.physical_space_size();
    return 1;
}

// The embedder's callback isn't registered with V8, which would call only it:
// budgetHeapLimit asks it first, so the budget and the out-of-memory handling
// still apply.
void v8go_isolate_add_near_heap_limit_callback(void* ptr, int callback_id) {
    IsolateState* state = isolateState(static_cast<Isolate*>(ptr));
    if (state) state->near_heap_limit_cb_id = callback_id;
}

void v8go_isolate_remove_near_heap_limit_callback(void* ptr, size_t heap_limit) {
    if (!ptr) return;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    IsolateState* state = isolateState(isolate);
    if (!state) return;
    state->near_heap_limit_cb_id = -1;
    // Restoring a lower limit is V8's, through removing the callback that
    // raised it. With a budget the heap keeps what it was charged for.
    if (heap_limit > 0 && !state->budget) {
        isolate->RemoveNearHeapLimitCallback(budgetHeapLimit, heap_limit);
        isolate->AddNearHeapLimitCallback(budgetHeapLimit, isolate);
    }
}

void v8go_isolate_terminate_execution(void* ptr) {
    if (!ptr) return;
    static_cast<Isolate*>(ptr)->TerminateExecution();
}

void v8go_isolate_cancel_terminate_execution(void* ptr) {
    if (!ptr) return;
    // An isolate that ran out of memory stays terminated.
    if (v8go_isolate_out_of_memory(ptr)) return;
    static_cast<Isolate*>(ptr)->CancelTerminateExecution();
}

int v8go_isolate_is_execution_terminating(void* ptr) {
    if (!ptr) return 0;
    return static_cast<Isolate*>(ptr)->IsExecutionTerminating() ? 1 : 0;
}

void v8go_isolate_memory_pressure_notification(void* ptr, int level) {
    if (!ptr) return;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    MemoryPressureLevel v8_level = MemoryPressureLevel::kNone;
    switch (level) {
        case V8GO_MEMORY_PRESSURE_MODERATE:
            v8_level = MemoryPressureLevel::kModerate;
            break;
        case V8GO_MEMORY_PRESSURE_CRITICAL:
            v8_level = MemoryPressureLevel::kCritical;
            break;
        default:
            v8_level = MemoryPressureLevel::kNone;
            break;
    }
    isolate->MemoryPressureNotification(v8_level);
}

long long v8go_isolate_adjust_amount_of_external_allocated_memory(
    void* ptr, long long change_in_bytes) {
    if (!ptr) return 0;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    // Prefer ExternalMemoryAccounter (sandbox-safe). Also keep a per-isolate
    // accounter so outstanding bytes are tracked for dispose/checks.
    IsolateState* state = isolateState(isolate);
    if (state) {
        state->external_memory.Update(isolate, change_in_bytes);
    }
    return ExternalMemoryAccounter::GetTotalAmountOfExternalAllocatedMemoryForTesting(
        isolate);
}

void v8go_isolate_request_garbage_collection_for_testing(void* ptr, int type) {
    if (!ptr) return;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    Isolate::GarbageCollectionType v8_type =
        (type == V8GO_GC_MINOR)
            ? Isolate::kMinorGarbageCollection
            : Isolate::kFullGarbageCollection;
    isolate->RequestGarbageCollectionForTesting(v8_type);
}

void* v8go_context_new(void* isolate_ptr) {
    return v8go_context_new_with_template(isolate_ptr, nullptr);
}

// hostInitializeImportMeta populates an ES module's `import.meta` object. V8
// invokes it lazily the first time a module touches import.meta. We recover the
// module's url by identity from the per-context module cache (the same reverse
// lookup used for referrers) and expose it as import.meta.url, matching Node's
// file:// URL convention. This is required by loader-hook registrars such as
// ts-blank-space's register.js, which call module.register(hook, import.meta.url).
static void hostInitializeImportMeta(Local<Context> context,
                                     Local<Module> module,
                                     Local<Object> meta) {
    Isolate* isolate = Isolate::GetCurrent();
    ContextWrapper* cw = wrapperFromContext(context);
    std::string url;
    if (cw) {
        for (auto& [key, wrapper] : cw->module_cache) {
            Local<Module> cached = wrapper->module.Get(isolate);
            if (!cached.IsEmpty() &&
                cached->GetIdentityHash() == module->GetIdentityHash()) {
                url = key;
                break;
            }
        }
    }
    // A module imported with a type is cached under "\x01<type>\x01<url>";
    // the type isn't part of its url.
    std::string href = untypedName(url.c_str());
    if (!href.empty() && href.rfind("node:", 0) != 0 &&
        href.rfind("file://", 0) != 0 && href[0] == '/') {
        href = "file://" + href;
    }
    Local<String> key =
        String::NewFromUtf8(isolate, "url").ToLocalChecked();
    Local<String> val =
        String::NewFromUtf8(isolate, href.c_str()).ToLocalChecked();
    meta->CreateDataProperty(context, key, val).Check();
}

void* v8go_context_new_with_template(void* isolate_ptr, void* global_template_ptr) {
    if (!isolate_ptr) return nullptr;
    
    Isolate* isolate = static_cast<Isolate*>(isolate_ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    isolate->SetHostInitializeImportMetaObjectCallback(hostInitializeImportMeta);
    
    Local<ObjectTemplate> global_template;
    if (global_template_ptr) {
        ObjectTemplateWrapper* tmpl_wrapper = static_cast<ObjectTemplateWrapper*>(global_template_ptr);
        global_template = tmpl_wrapper->tmpl.Get(isolate);
    }
    
    Local<Context> context = Context::New(isolate, nullptr, global_template);
    
    ContextWrapper* wrapper = new ContextWrapper();
    wrapper->isolate = isolate;
    wrapper->context.Reset(isolate, context);
    // Stash the wrapper in the context so module-resolution / dynamic-import
    // callbacks (which only receive a Local<Context>) can recover per-sandbox
    // state instead of reaching for process globals.
    context->SetAlignedPointerInEmbedderData(
        kContextWrapperSlot, wrapper, kContextWrapperTag);
    
    return wrapper;
}

void v8go_context_dispose(void* ptr) {
    if (!ptr) return;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(ptr);
    if (!wrapper->context.IsEmpty()) {
        Isolate* isolate = wrapper->isolate;
        Locker locker(isolate);
        Isolate::Scope isolate_scope(isolate);
        HandleScope handle_scope(isolate);
        // V8 may keep the context, and run module and rejection callbacks in it, after this:
        // clear the slot that points here, so they find no wrapper rather than a freed one.
        Local<Context> context = wrapper->context.Get(isolate);
        if (wrapperFromContext(context) == wrapper) {
            context->SetAlignedPointerInEmbedderData(kContextWrapperSlot, nullptr, kContextWrapperTag);
        }
        wrapper->promise_reject_handler.Reset();
        wrapper->last_script_exception.Reset();
        wrapper->context.Reset();
    }
    delete wrapper;
}

void* v8go_context_run_script_rethrowing(void* context_ptr, const char* source, const char* origin) {
    if (!context_ptr || !source) return nullptr;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    TryCatch try_catch(isolate);
    ScriptOrigin script_origin(String::NewFromUtf8(isolate, origin ? origin : "").ToLocalChecked());
    Local<String> source_str = String::NewFromUtf8(isolate, source).ToLocalChecked();
    Local<Script> script;
    Local<Value> result;
    if (!Script::Compile(context, source_str, &script_origin).ToLocal(&script) ||
        !script->Run(context).ToLocal(&result)) {
        // Left pending for the JavaScript that called the native function, with V8's
        // message for it as it was.
        if (try_catch.HasCaught() && try_catch.CanContinue()) try_catch.ReThrow();
        return nullptr;
    }
    ValueWrapper* value = new ValueWrapper();
    value->isolate = isolate;
    value->value.Reset(isolate, result);
    return value;
}

void* v8go_context_compile_function_rethrowing(void* context_ptr, void* source_ptr, const char* origin,
                                               const char** params, int param_count) {
    if (!context_ptr || !source_ptr) return nullptr;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    TryCatch try_catch(isolate);
    std::vector<Local<String>> names;
    for (int i = 0; i < param_count; i++) {
        Local<String> name;
        if (!String::NewFromUtf8(isolate, params[i]).ToLocal(&name)) return nullptr;
        names.push_back(name);
    }
    // The source stays a V8 string, so a NUL character in it is kept.
    Local<Value> source_value = static_cast<ValueWrapper*>(source_ptr)->value.Get(isolate);
    if (!source_value->IsString()) return nullptr;
    ScriptOrigin script_origin(String::NewFromUtf8(isolate, origin ? origin : "").ToLocalChecked());
    ScriptCompiler::Source script_source(source_value.As<String>(), script_origin);
    Local<Function> fn;
    if (!ScriptCompiler::CompileFunction(context, &script_source, param_count,
                                         names.empty() ? nullptr : names.data(), 0, nullptr)
             .ToLocal(&fn)) {
        // Left pending for the JavaScript that called the native function, with V8's
        // message for it as it was.
        if (try_catch.HasCaught() && try_catch.CanContinue()) try_catch.ReThrow();
        return nullptr;
    }
    ValueWrapper* value = new ValueWrapper();
    value->isolate = isolate;
    value->value.Reset(isolate, fn);
    return value;
}

void* v8go_context_run_script(void* context_ptr, const char* source, const char* origin, char** error) {
    if (!context_ptr || !source) return nullptr;
    
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = wrapper->isolate;
    
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    TryCatch try_catch(isolate);
    
    Local<String> origin_str = String::NewFromUtf8(isolate, origin).ToLocalChecked();
    ScriptOrigin script_origin(origin_str);
    
    Local<String> source_str = String::NewFromUtf8(isolate, source).ToLocalChecked();
    MaybeLocal<Script> maybe_script = Script::Compile(context, source_str, &script_origin);
    
    if (maybe_script.IsEmpty()) {
        if (try_catch.HasTerminated()) {
            // Execution was terminated; leave *error unset so Go returns
            // ErrExecutionTerminated rather than a bogus exception string.
            return nullptr;
        }
        if (try_catch.HasCaught()) {
            wrapper->last_script_exception.Reset(isolate, try_catch.Exception());
            recordPlace(wrapper, isolate, context, try_catch.Message());
            String::Utf8Value exception(isolate, try_catch.Exception());
            *error = strdup(*exception ? *exception : "Unknown error");
        }
        return nullptr;
    }
    
    Local<Script> script = maybe_script.ToLocalChecked();
    MaybeLocal<Value> maybe_result = script->Run(context);
    
    if (maybe_result.IsEmpty()) {
        if (try_catch.HasTerminated()) {
            return nullptr;
        }
        if (try_catch.HasCaught()) {
            wrapper->last_script_exception.Reset(isolate, try_catch.Exception());
            recordPlace(wrapper, isolate, context, try_catch.Message());
            String::Utf8Value exception(isolate, try_catch.Exception());
            String::Utf8Value stack_trace(isolate, try_catch.StackTrace(context).FromMaybe(Local<Value>()));
            
            std::string err_msg;
            if (*exception) err_msg = *exception;
            if (*stack_trace) {
                err_msg += "\n";
                err_msg += *stack_trace;
            }
            *error = strdup(err_msg.c_str());
        }
        return nullptr;
    }
    
    Local<Value> result = maybe_result.ToLocalChecked();
    
    ValueWrapper* value_wrapper = new ValueWrapper();
    value_wrapper->isolate = isolate;
    value_wrapper->value.Reset(isolate, result);
    
    return value_wrapper;
}

void* v8go_context_global(void* context_ptr) {
    if (!context_ptr) return nullptr;
    
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = wrapper->isolate;
    
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Object> global = context->Global();
    
    ValueWrapper* value_wrapper = new ValueWrapper();
    value_wrapper->isolate = isolate;
    value_wrapper->value.Reset(isolate, global);
    
    return value_wrapper;
}

// Value operations
char* v8go_value_to_string(void* context_ptr, void* value_ptr) {
    if (!context_ptr || !value_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> value = val_wrapper->value.Get(isolate);
    MaybeLocal<String> maybe_str = value->ToString(context);
    
    if (maybe_str.IsEmpty()) return nullptr;
    
    String::Utf8Value str(isolate, maybe_str.ToLocalChecked());
    return strdup(*str ? *str : "");
}

int v8go_value_is_undefined(void* value_ptr) {
    if (!value_ptr) return 1;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsUndefined() ? 1 : 0;
}

int v8go_value_is_null(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsNull() ? 1 : 0;
}

int v8go_value_is_boolean(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsBoolean() ? 1 : 0;
}

int v8go_value_is_number(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsNumber() ? 1 : 0;
}

int v8go_value_is_string(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsString() ? 1 : 0;
}

int v8go_value_is_object(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsObject() ? 1 : 0;
}

int v8go_value_is_array(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsArray() ? 1 : 0;
}

int v8go_value_is_function(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsFunction() ? 1 : 0;
}

int v8go_value_to_boolean(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->BooleanValue(isolate) ? 1 : 0;
}

double v8go_value_to_number(void* context_ptr, void* value_ptr) {
    if (!context_ptr || !value_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> value = val_wrapper->value.Get(isolate);
    MaybeLocal<Number> maybe_num = value->ToNumber(context);
    
    if (maybe_num.IsEmpty()) return 0;
    return maybe_num.ToLocalChecked()->Value();
}

long v8go_value_to_integer(void* context_ptr, void* value_ptr) {
    if (!context_ptr || !value_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> value = val_wrapper->value.Get(isolate);
    MaybeLocal<Integer> maybe_int = value->ToInteger(context);
    
    if (maybe_int.IsEmpty()) return 0;
    return maybe_int.ToLocalChecked()->Value();
}

// Value factory
void* v8go_undefined(void* context_ptr) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, Undefined(isolate));
    return wrapper;
}

void* v8go_null(void* context_ptr) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, Null(isolate));
    return wrapper;
}

void* v8go_true(void* context_ptr) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, True(isolate));
    return wrapper;
}

void* v8go_false(void* context_ptr) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, False(isolate));
    return wrapper;
}

void* v8go_new_boolean(void* context_ptr, int value) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, Boolean::New(isolate, value != 0));
    return wrapper;
}

void* v8go_new_number(void* context_ptr, double value) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, Number::New(isolate, value));
    return wrapper;
}

void* v8go_new_integer(void* context_ptr, long value) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, Integer::New(isolate, value));
    return wrapper;
}

void* v8go_new_string(void* context_ptr, const char* value, int length) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    MaybeLocal<String> maybe_str = String::NewFromUtf8(isolate, value, NewStringType::kNormal, length);
    if (maybe_str.IsEmpty()) return nullptr;
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_str.ToLocalChecked());
    return wrapper;
}

void* v8go_new_object(void* context_ptr) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Object> obj = Object::New(isolate);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, obj);
    return wrapper;
}

void* v8go_new_array(void* context_ptr, int length) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Array> arr = Array::New(isolate, length);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, arr);
    return wrapper;
}

// Object operations
int v8go_object_set(void* context_ptr, void* object_ptr, const char* key, void* value_ptr) {
    if (!context_ptr || !object_ptr || !key || !value_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return 0;
    
    Local<Object> object = obj_value.As<Object>();
    Local<String> key_str = String::NewFromUtf8(isolate, key).ToLocalChecked();
    Local<Value> value = val_wrapper->value.Get(isolate);
    
    Maybe<bool> result = object->Set(context, key_str, value);
    return result.FromMaybe(false) ? 1 : 0;
}

int v8go_object_set_idx(void* context_ptr, void* object_ptr, int idx, void* value_ptr) {
    if (!context_ptr || !object_ptr || !value_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return 0;
    
    Local<Object> object = obj_value.As<Object>();
    Local<Value> value = val_wrapper->value.Get(isolate);
    
    Maybe<bool> result = object->Set(context, idx, value);
    return result.FromMaybe(false) ? 1 : 0;
}

void* v8go_object_get(void* context_ptr, void* object_ptr, const char* key) {
    if (!context_ptr || !object_ptr || !key) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return nullptr;
    
    Local<Object> object = obj_value.As<Object>();
    Local<String> key_str = String::NewFromUtf8(isolate, key).ToLocalChecked();
    
    MaybeLocal<Value> maybe_value = object->Get(context, key_str);
    if (maybe_value.IsEmpty()) return nullptr;
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_value.ToLocalChecked());
    return wrapper;
}

void* v8go_object_get_idx(void* context_ptr, void* object_ptr, int idx) {
    if (!context_ptr || !object_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return nullptr;
    
    Local<Object> object = obj_value.As<Object>();
    
    MaybeLocal<Value> maybe_value = object->Get(context, idx);
    if (maybe_value.IsEmpty()) return nullptr;
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_value.ToLocalChecked());
    return wrapper;
}

int v8go_object_has(void* context_ptr, void* object_ptr, const char* key) {
    if (!context_ptr || !object_ptr || !key) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return 0;
    
    Local<Object> object = obj_value.As<Object>();
    Local<String> key_str = String::NewFromUtf8(isolate, key).ToLocalChecked();
    
    Maybe<bool> result = object->Has(context, key_str);
    return result.FromMaybe(false) ? 1 : 0;
}

int v8go_object_delete(void* context_ptr, void* object_ptr, const char* key) {
    if (!context_ptr || !object_ptr || !key) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return 0;
    
    Local<Object> object = obj_value.As<Object>();
    Local<String> key_str = String::NewFromUtf8(isolate, key).ToLocalChecked();
    
    Maybe<bool> result = object->Delete(context, key_str);
    return result.FromMaybe(false) ? 1 : 0;
}

void* v8go_object_get_property_names(void* context_ptr, void* object_ptr) {
    if (!context_ptr || !object_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return nullptr;
    
    Local<Object> object = obj_value.As<Object>();
    
    // Get own enumerable property names
    MaybeLocal<Array> maybe_names = object->GetOwnPropertyNames(context);
    if (maybe_names.IsEmpty()) return nullptr;
    
    Local<Array> names = maybe_names.ToLocalChecked();
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, names);
    
    return wrapper;
}

// Own enumerable keys of an object other than its array indices: strings, then symbols, as
// Node's getOwnNonIndexProperties lists them for util.inspect. Indices are skipped while
// collecting, so an array's elements aren't walked.
void* v8go_object_get_own_non_index_keys(void* context_ptr, void* object_ptr) {
    if (!context_ptr || !object_ptr) return nullptr;

    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* obj_wrapper = static_cast<ValueWrapper*>(object_ptr);

    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);

    Local<Value> obj_value = obj_wrapper->value.Get(isolate);
    if (!obj_value->IsObject()) return nullptr;

    // A proxy's trap may throw; the caller gets nothing, with no exception left pending.
    TryCatch try_catch(isolate);
    MaybeLocal<Array> maybe_keys = obj_value.As<Object>()->GetPropertyNames(
        context, KeyCollectionMode::kOwnOnly, PropertyFilter::ONLY_ENUMERABLE,
        IndexFilter::kSkipIndices, KeyConversionMode::kConvertToString);
    if (maybe_keys.IsEmpty()) return nullptr;

    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_keys.ToLocalChecked());
    return wrapper;
}

// A JavaScript function answering what v8go_object_get_own_non_index_keys does for its argument
// (undefined for anything but an object, or when listing its keys throws). It runs in C++, so calling it from JavaScript keeps
// no native wrapper for its result.
static void OwnNonIndexKeysCallback(const FunctionCallbackInfo<Value>& info) {
    if (info.Length() < 1 || !info[0]->IsObject()) return;
    Isolate* isolate = info.GetIsolate();
    Local<Context> context = isolate->GetCurrentContext();
    // A proxy's trap may throw; the function then answers undefined instead of throwing.
    TryCatch try_catch(isolate);
    Local<Array> keys;
    if (info[0].As<Object>()->GetPropertyNames(
            context, KeyCollectionMode::kOwnOnly, PropertyFilter::ONLY_ENUMERABLE,
            IndexFilter::kSkipIndices, KeyConversionMode::kConvertToString).ToLocal(&keys)) {
        info.GetReturnValue().Set(keys);
    }
}

// A JavaScript function answering V8's name for its argument's constructor (undefined for
// anything but an object), which, unlike the constructor property, knows how the object was
// made: the class of an instance whose prototype was later set to null, say.
static void ConstructorNameCallback(const FunctionCallbackInfo<Value>& info) {
    if (info.Length() < 1 || !info[0]->IsObject()) return;
    info.GetReturnValue().Set(info[0].As<Object>()->GetConstructorName());
}

// A JavaScript function answering the target of its argument when that's a proxy, following
// proxies of proxies, and null for a revoked one; undefined for anything else. No trap runs.
static void ProxyTargetCallback(const FunctionCallbackInfo<Value>& info) {
    if (info.Length() < 1 || !info[0]->IsProxy()) return;
    Local<Value> target = info[0].As<Proxy>()->GetTarget();
    while (target->IsProxy()) target = target.As<Proxy>()->GetTarget();
    info.GetReturnValue().Set(target);
}

// Makes a JavaScript function in the context that runs callback.
static void* nativeFunction(void* context_ptr, FunctionCallback callback) {
    if (!context_ptr) return nullptr;

    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);

    Local<Function> fn;
    if (!Function::New(context, callback).ToLocal(&fn)) return nullptr;
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, fn);
    return wrapper;
}

void* v8go_own_non_index_keys_function(void* context_ptr) {
    return nativeFunction(context_ptr, OwnNonIndexKeysCallback);
}

void* v8go_constructor_name_function(void* context_ptr) {
    return nativeFunction(context_ptr, ConstructorNameCallback);
}

void* v8go_proxy_target_function(void* context_ptr) {
    return nativeFunction(context_ptr, ProxyTargetCallback);
}

// The target of value when it's a proxy, following proxies of proxies, with *is_proxy set; a
// revoked proxy's target is null. Nothing, with *is_proxy clear, for anything else.
void* v8go_value_proxy_target(void* context_ptr, void* value_ptr, int* is_proxy) {
    *is_proxy = 0;
    if (!context_ptr || !value_ptr) return nullptr;
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Value> value = val_wrapper->value.Get(isolate);
    if (!value->IsProxy()) return nullptr;
    *is_proxy = 1;
    Local<Value> target = value.As<Proxy>()->GetTarget();
    while (target->IsProxy()) target = target.As<Proxy>()->GetTarget();
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, target);
    return wrapper;
}

// Releases a value handed to Go, which mustn't use it afterwards.
void v8go_value_release(void* value_ptr) {
    if (!value_ptr) return;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Locker locker(wrapper->isolate);
    wrapper->value.Reset();
    delete wrapper;
}

int v8go_array_length(void* context_ptr, void* array_ptr) {
    if (!context_ptr || !array_ptr) return 0;
    
    ValueWrapper* arr_wrapper = static_cast<ValueWrapper*>(array_ptr);
    Isolate* isolate = arr_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Value> value = arr_wrapper->value.Get(isolate);
    if (!value->IsArray()) return 0;
    
    return value.As<Array>()->Length();
}

// Callback handling
static void functionCallback(const FunctionCallbackInfo<Value>& info) {
    Isolate* isolate = info.GetIsolate();
    HandleScope handle_scope(isolate);
    
    Local<External> data = info.Data().As<External>();
#if V8_MAJOR_VERSION >= 15
    int callback_id = static_cast<int>(reinterpret_cast<intptr_t>(data->Value(kExternalPointerTypeTagDefault)));
#else
    int callback_id = static_cast<int>(reinterpret_cast<intptr_t>(data->Value()));
#endif
    
    // Create callback info
    CallbackInfo* cb_info = new CallbackInfo();
    cb_info->isolate = isolate;
    cb_info->v8_info = &info;
    
    // A wrapper of the call's own, which Go takes over: values the callback keeps may use it
    // after the call, and Go frees it once none does. The context's own wrapper, which its Go
    // Context owns, isn't shared, so neither can free it under the other.
    Local<Context> context = isolate->GetCurrentContext();
    ContextWrapper* ctx_wrapper = new ContextWrapper();
    ctx_wrapper->isolate = isolate;
    ctx_wrapper->context.Reset(isolate, context);
    
    // Call Go callback handler
    void* result = goCallbackHandler(ctx_wrapper, isolate, callback_id, cb_info);
    
    if (result) {
        ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(result);
        info.GetReturnValue().Set(val_wrapper->value.Get(isolate));
    }
    
    delete cb_info;
}

void* v8go_function_template_new_with_id(void* isolate_ptr, int callback_id) {
    if (!isolate_ptr) return nullptr;
    
    Isolate* isolate = static_cast<Isolate*>(isolate_ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
#if V8_MAJOR_VERSION >= 15
    Local<External> data = External::New(isolate, reinterpret_cast<void*>(static_cast<intptr_t>(callback_id)), kExternalPointerTypeTagDefault);
#else
    Local<External> data = External::New(isolate, reinterpret_cast<void*>(static_cast<intptr_t>(callback_id)));
#endif
    Local<FunctionTemplate> tmpl = FunctionTemplate::New(isolate, functionCallback, data);
    
    FunctionTemplateWrapper* wrapper = new FunctionTemplateWrapper();
    wrapper->isolate = isolate;
    wrapper->tmpl.Reset(isolate, tmpl);
    wrapper->callback_id = callback_id;
    
    return wrapper;
}

void* v8go_function_template_get_function(void* context_ptr, void* function_template_ptr) {
    if (!context_ptr || !function_template_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    FunctionTemplateWrapper* tmpl_wrapper = static_cast<FunctionTemplateWrapper*>(function_template_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<FunctionTemplate> tmpl = tmpl_wrapper->tmpl.Get(isolate);
    MaybeLocal<Function> maybe_func = tmpl->GetFunction(context);
    
    if (maybe_func.IsEmpty()) return nullptr;
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_func.ToLocalChecked());
    return wrapper;
}

void* v8go_object_template_new(void* isolate_ptr) {
    if (!isolate_ptr) return nullptr;
    
    Isolate* isolate = static_cast<Isolate*>(isolate_ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<ObjectTemplate> tmpl = ObjectTemplate::New(isolate);
    
    ObjectTemplateWrapper* wrapper = new ObjectTemplateWrapper();
    wrapper->isolate = isolate;
    wrapper->tmpl.Reset(isolate, tmpl);
    
    return wrapper;
}

void v8go_object_template_set_function(void* object_template_ptr, const char* key, void* function_template_ptr) {
    if (!object_template_ptr || !key || !function_template_ptr) return;
    
    ObjectTemplateWrapper* obj_wrapper = static_cast<ObjectTemplateWrapper*>(object_template_ptr);
    FunctionTemplateWrapper* func_wrapper = static_cast<FunctionTemplateWrapper*>(function_template_ptr);
    
    Isolate* isolate = obj_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<ObjectTemplate> obj_tmpl = obj_wrapper->tmpl.Get(isolate);
    Local<FunctionTemplate> func_tmpl = func_wrapper->tmpl.Get(isolate);
    Local<String> key_str = String::NewFromUtf8(isolate, key).ToLocalChecked();
    
    obj_tmpl->Set(key_str, func_tmpl);
}

void* v8go_object_template_new_instance(void* context_ptr, void* object_template_ptr) {
    if (!context_ptr || !object_template_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ObjectTemplateWrapper* tmpl_wrapper = static_cast<ObjectTemplateWrapper*>(object_template_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<ObjectTemplate> tmpl = tmpl_wrapper->tmpl.Get(isolate);
    MaybeLocal<Object> maybe_obj = tmpl->NewInstance(context);
    
    if (maybe_obj.IsEmpty()) return nullptr;
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_obj.ToLocalChecked());
    return wrapper;
}

// Callback info operations
int v8go_callback_info_length(void* info_ptr) {
    if (!info_ptr) return 0;
    CallbackInfo* info = static_cast<CallbackInfo*>(info_ptr);
    return info->v8_info->Length();
}

void* v8go_callback_info_arg(void* info_ptr, int index) {
    if (!info_ptr) return nullptr;
    CallbackInfo* info = static_cast<CallbackInfo*>(info_ptr);
    
    if (index < 0 || index >= info->v8_info->Length()) return nullptr;
    
    Isolate* isolate = info->isolate;
    HandleScope handle_scope(isolate);
    
    Local<Value> arg = (*info->v8_info)[index];
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, arg);
    return wrapper;
}

void* v8go_callback_info_this(void* info_ptr) {
    if (!info_ptr) return nullptr;
    CallbackInfo* info = static_cast<CallbackInfo*>(info_ptr);
    
    Isolate* isolate = info->isolate;
    HandleScope handle_scope(isolate);
    
    Local<Object> this_obj = info->v8_info->This();
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, this_obj);
    return wrapper;
}

// Function call
void* v8go_function_call(void* context_ptr, void* function_ptr, void* recv_ptr, int argc, void** argv, char** error, void** exception) {
    if (!context_ptr || !function_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* func_wrapper = static_cast<ValueWrapper*>(function_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    TryCatch try_catch(isolate);
    
    Local<Value> func_value = func_wrapper->value.Get(isolate);
    if (!func_value->IsFunction()) {
        *error = strdup("Value is not a function");
        return nullptr;
    }
    
    Local<Function> func = func_value.As<Function>();
    
    Local<Value> recv;
    if (recv_ptr) {
        ValueWrapper* recv_wrapper = static_cast<ValueWrapper*>(recv_ptr);
        recv = recv_wrapper->value.Get(isolate);
    } else {
        recv = context->Global();
    }
    
    // Convert arguments
    std::vector<Local<Value>> args(argc);
    for (int i = 0; i < argc; i++) {
        if (argv[i]) {
            ValueWrapper* arg_wrapper = static_cast<ValueWrapper*>(argv[i]);
            args[i] = arg_wrapper->value.Get(isolate);
        } else {
            args[i] = Undefined(isolate);
        }
    }
    
    MaybeLocal<Value> maybe_result = func->Call(context, recv, argc, argc > 0 ? args.data() : nullptr);
    
    if (maybe_result.IsEmpty()) {
        if (try_catch.HasTerminated()) {
            return nullptr;
        }
        if (try_catch.HasCaught()) {
            if (exception) {
                // The value thrown itself, so Go can hand it on or throw it again unchanged:
                // kept first, and described without converting it, which would run its own
                // toString or Symbol.toPrimitive.
                ValueWrapper* thrown = new ValueWrapper();
                thrown->isolate = isolate;
                thrown->value.Reset(isolate, try_catch.Exception());
                *exception = thrown;
                Local<Message> message = try_catch.Message();
                if (!message.IsEmpty()) {
                    String::Utf8Value text(isolate, message->Get());
                    *error = strdup(*text ? *text : "Uncaught exception");
                } else {
                    *error = strdup("Uncaught exception");
                }
            } else {
                String::Utf8Value message(isolate, try_catch.Exception());
                *error = strdup(*message ? *message : "Unknown error");
            }
        }
        return nullptr;
    }

    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_result.ToLocalChecked());
    return wrapper;
}

// JSON operations
void* v8go_json_parse(void* context_ptr, const char* json, char** error) {
    if (!context_ptr || !json) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    TryCatch try_catch(isolate);
    
    Local<String> json_str = String::NewFromUtf8(isolate, json).ToLocalChecked();
    MaybeLocal<Value> maybe_result = JSON::Parse(context, json_str);
    
    if (maybe_result.IsEmpty()) {
        if (try_catch.HasCaught()) {
            String::Utf8Value exception(isolate, try_catch.Exception());
            *error = strdup(*exception ? *exception : "JSON parse error");
        }
        return nullptr;
    }
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_result.ToLocalChecked());
    return wrapper;
}

char* v8go_json_stringify(void* context_ptr, void* value_ptr, char** error) {
    if (!context_ptr || !value_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    TryCatch try_catch(isolate);
    
    Local<Value> value = val_wrapper->value.Get(isolate);
    MaybeLocal<String> maybe_result = JSON::Stringify(context, value);
    
    if (maybe_result.IsEmpty()) {
        if (try_catch.HasCaught()) {
            String::Utf8Value exception(isolate, try_catch.Exception());
            *error = strdup(*exception ? *exception : "JSON stringify error");
        }
        return nullptr;
    }
    
    String::Utf8Value str(isolate, maybe_result.ToLocalChecked());
    return strdup(*str ? *str : "");
}

// Exception handling
// Throws value, which a Go callback got from a call that threw, in the context; the callback
// then returns and the exception goes on to its caller.
void v8go_throw_value(void* context_ptr, void* value_ptr) {
    if (!context_ptr || !value_ptr) return;
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    isolate->ThrowException(val_wrapper->value.Get(isolate));
}

void* v8go_throw_exception(void* context_ptr, const char* message) {
    if (!context_ptr || !message) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<String> msg = String::NewFromUtf8(isolate, message).ToLocalChecked();
    Local<Value> exception = Exception::Error(msg);
    isolate->ThrowException(exception);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, exception);
    return wrapper;
}

void* v8go_throw_type_error(void* context_ptr, const char* message) {
    if (!context_ptr || !message) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<String> msg = String::NewFromUtf8(isolate, message).ToLocalChecked();
    Local<Value> exception = Exception::TypeError(msg);
    isolate->ThrowException(exception);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, exception);
    return wrapper;
}

void* v8go_throw_range_error(void* context_ptr, const char* message) {
    if (!context_ptr || !message) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<String> msg = String::NewFromUtf8(isolate, message).ToLocalChecked();
    Local<Value> exception = Exception::RangeError(msg);
    isolate->ThrowException(exception);
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, exception);
    return wrapper;
}

// Promise operations (stubs for now)
void* v8go_promise_resolver_new(void* context_ptr) {
    if (!context_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    MaybeLocal<Promise::Resolver> maybe_resolver = Promise::Resolver::New(context);
    if (maybe_resolver.IsEmpty()) return nullptr;
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, maybe_resolver.ToLocalChecked());
    return wrapper;
}

void* v8go_promise_resolver_get_promise(void* context_ptr, void* resolver_ptr) {
    if (!context_ptr || !resolver_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* res_wrapper = static_cast<ValueWrapper*>(resolver_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Value> value = res_wrapper->value.Get(isolate);
    Local<Promise::Resolver> resolver = value.As<Promise::Resolver>();
    Local<Promise> promise = resolver->GetPromise();
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, promise);
    return wrapper;
}

int v8go_promise_resolver_resolve(void* context_ptr, void* resolver_ptr, void* value_ptr) {
    if (!context_ptr || !resolver_ptr || !value_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* res_wrapper = static_cast<ValueWrapper*>(resolver_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> res_value = res_wrapper->value.Get(isolate);
    Local<Promise::Resolver> resolver = res_value.As<Promise::Resolver>();
    Local<Value> value = val_wrapper->value.Get(isolate);
    
    Maybe<bool> result = resolver->Resolve(context, value);
    return result.FromMaybe(false) ? 1 : 0;
}

int v8go_promise_resolver_reject(void* context_ptr, void* resolver_ptr, void* value_ptr) {
    if (!context_ptr || !resolver_ptr || !value_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* res_wrapper = static_cast<ValueWrapper*>(resolver_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    Local<Value> res_value = res_wrapper->value.Get(isolate);
    Local<Promise::Resolver> resolver = res_value.As<Promise::Resolver>();
    Local<Value> value = val_wrapper->value.Get(isolate);
    
    Maybe<bool> result = resolver->Reject(context, value);
    return result.FromMaybe(false) ? 1 : 0;
}

int v8go_promise_state(void* promise_ptr) {
    if (!promise_ptr) return -1;
    
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(promise_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Value> value = wrapper->value.Get(isolate);
    if (!value->IsPromise()) return -1;
    
    Local<Promise> promise = value.As<Promise>();
    return static_cast<int>(promise->State());
}

void* v8go_promise_result(void* context_ptr, void* promise_ptr) {
    if (!context_ptr || !promise_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* prom_wrapper = static_cast<ValueWrapper*>(promise_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Value> value = prom_wrapper->value.Get(isolate);
    if (!value->IsPromise()) return nullptr;
    
    Local<Promise> promise = value.As<Promise>();
    Local<Value> result = promise->Result();
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, result);
    return wrapper;
}

// The exception for a failed module resolution: the error the Go resolver
// supplied (such as Node's ERR_MODULE_NOT_FOUND, with its code and url), or a
// plain Error with the fallback message when it supplied none.
static Local<Value> resolveFailure(Isolate* isolate, void* error_value,
                                   const std::string& fallback) {
    if (error_value) {
        ValueWrapper* w = static_cast<ValueWrapper*>(error_value);
        // Only a value of this isolate can be thrown here.
        if (w->isolate == isolate && !w->value.IsEmpty()) return w->value.Get(isolate);
    }
    return Exception::Error(
        String::NewFromUtf8(isolate, fallback.c_str()).ToLocalChecked());
}

// importAttributes flattens an import's attributes into key, value, key, ...
// strings. V8 lists them as (key, value) pairs for import(), and as (key, value,
// source offset) triples for a static import: stride says which.
// losslessUtf8 is a string's UTF-8, with a lone surrogate written as its own
// three bytes (WTF-8) rather than replaced by U+FFFD, so distinct JavaScript
// strings stay distinct.
static std::string losslessUtf8(Isolate* isolate, Local<Context> context,
                                Local<Value> value) {
    Local<String> str;
    if (!value->ToString(context).ToLocal(&str)) return std::string();
#if V8_MAJOR_VERSION >= 15
    std::string out(str->Utf8Length(isolate), '\0');
    size_t written = str->WriteUtf8(isolate, out.data(), out.size());
#else
    std::string out(str->Utf8LengthV2(isolate), '\0');
    size_t written = str->WriteUtf8V2(isolate, out.data(), out.size());
#endif
    out.resize(written);
    return out;
}

static std::vector<std::string> importAttributes(Isolate* isolate,
                                                 Local<Context> context,
                                                 Local<FixedArray> attrs,
                                                 int stride) {
    std::vector<std::string> out;
    if (attrs.IsEmpty()) return out;
    for (int i = 0; i + 1 < attrs->Length(); i += stride) {
#if V8_MAJOR_VERSION >= 15
        Local<Data> key = attrs->Get(i);
        Local<Data> value = attrs->Get(i + 1);
#else
        Local<Data> key = attrs->Get(context, i);
        Local<Data> value = attrs->Get(context, i + 1);
#endif
        if (!key->IsValue() || !value->IsValue()) continue;
        // Lengths kept (a key or value may contain a NUL), surrogates too.
        out.emplace_back(losslessUtf8(isolate, context, key.As<Value>()));
        out.emplace_back(losslessUtf8(isolate, context, value.As<Value>()));
    }
    return out;
}

// cStrings exposes strings to Go as a char* array and their lengths (valid while
// strs lives).
static std::vector<char*> cStrings(std::vector<std::string>& strs,
                                   std::vector<int>* lens) {
    std::vector<char*> out;
    for (auto& s : strs) {
        out.push_back(const_cast<char*>(s.data()));
        lens->push_back(static_cast<int>(s.size()));
    }
    return out;
}

// ES Module resolver callback - called by V8 when it encounters an import
static MaybeLocal<Module> moduleResolveCallback(
    Local<Context> context,
    Local<String> specifier,
    Local<FixedArray> import_attributes,
    Local<Module> referrer) {
    
#if V8_MAJOR_VERSION >= 15
    Isolate* isolate = Isolate::GetCurrent();
#else
    Isolate* isolate = context->GetIsolate();
#endif

    ContextWrapper* cw = wrapperFromContext(context);
    if (!cw) {
        isolate->ThrowException(Exception::Error(
            String::NewFromUtf8(isolate,
                "module resolution: no context wrapper").ToLocalChecked()));
        return MaybeLocal<Module>();
    }
    
    String::Utf8Value specifier_utf8(isolate, specifier);
    const char* specifier_str = *specifier_utf8;
    
    // Get referrer name for resolution (search THIS context's cache only).
    std::string referrer_name;
    for (auto& [key, wrapper] : cw->module_cache) {
        Local<Module> cached = wrapper->module.Get(isolate);
        if (cached->GetIdentityHash() == referrer->GetIdentityHash()) {
            referrer_name = wrapper->name;
            break;
        }
    }
    
    // Call Go to resolve and load the module
    char* resolved_source = nullptr;
    char* resolved_name = nullptr;
    void* resolve_error = nullptr;
    std::vector<std::string> attrs =
        importAttributes(isolate, context, import_attributes, 3);
    std::vector<int> attr_lens;
    std::vector<char*> attr_ptrs = cStrings(attrs, &attr_lens);
    int result = goModuleResolve(cw->module_resolver_id, 
        const_cast<char*>(specifier_str), 
        const_cast<char*>(referrer_name.c_str()), 
        attr_ptrs.data(), attr_lens.data(), static_cast<int>(attr_ptrs.size()),
        &resolved_source, &resolved_name, &resolve_error);
    
    if (result != 0 || !resolved_source || !resolved_name) {
        if (resolved_source) free(resolved_source);
        if (resolved_name) free(resolved_name);
        if (isolate->IsExecutionTerminating()) return MaybeLocal<Module>();
        isolate->ThrowException(resolveFailure(isolate, resolve_error,
            std::string("Cannot resolve module: ") + specifier_str));
        return MaybeLocal<Module>();
    }
    
    std::string name_key(resolved_name);
    
    // Check if module is already in this context's cache
    auto it = cw->module_cache.find(name_key);
    if (it != cw->module_cache.end()) {
        free(resolved_source);
        free(resolved_name);
        return it->second->module.Get(isolate);
    }
    
    // Compile the module
    Local<String> source_str = String::NewFromUtf8(isolate, resolved_source).ToLocalChecked();
    Local<String> name_str =
        String::NewFromUtf8(isolate, untypedName(resolved_name).c_str()).ToLocalChecked();
    
    ScriptOrigin origin(name_str,
                        0,                      // line offset
                        0,                      // column offset
                        false,                  // is_shared_cross_origin
                        -1,                     // script_id
                        Local<Value>(),         // source_map_url
                        false,                  // is_opaque
                        false,                  // is_wasm
                        true);                  // is_module
    
    ScriptCompiler::Source source(source_str, origin);
    
    TryCatch try_catch(isolate);
    MaybeLocal<Module> maybe_module = ScriptCompiler::CompileModule(isolate, &source);
    
    free(resolved_source);
    
    if (maybe_module.IsEmpty()) {
        if (try_catch.HasCaught()) {
            try_catch.ReThrow();
        }
        free(resolved_name);
        return MaybeLocal<Module>();
    }
    
    Local<Module> module = maybe_module.ToLocalChecked();
    
    // Cache the module in this context
    ModuleWrapper* wrapper = new ModuleWrapper();
    wrapper->isolate = isolate;
    wrapper->module.Reset(isolate, module);
    wrapper->name = name_key;
    cw->module_cache[name_key] = wrapper;
    
    free(resolved_name);
    
    // Recursively instantiate dependencies
    Maybe<bool> instantiate_result = module->InstantiateModule(context, moduleResolveCallback);
    if (instantiate_result.IsNothing() || !instantiate_result.FromJust()) {
        return MaybeLocal<Module>();
    }
    
    return module;
}

// Compile (or fetch from cache), instantiate and evaluate a module by its
// already-resolved (source, name). Returns the module on success; on failure
// sets *out_exception (if a JS exception is pending) and returns empty.
static MaybeLocal<Module> compileInstantiateEvaluate(
    ContextWrapper* cw, Isolate* isolate, Local<Context> context,
    const char* resolved_source, const char* resolved_name,
    Local<Value>* out_eval_result) {

    std::string name_key(resolved_name);
    Local<Module> module;

    auto it = cw->module_cache.find(name_key);
    if (it != cw->module_cache.end()) {
        module = it->second->module.Get(isolate);
    } else {
        Local<String> source_str =
            String::NewFromUtf8(isolate, resolved_source).ToLocalChecked();
        Local<String> name_str =
            String::NewFromUtf8(isolate, untypedName(resolved_name).c_str()).ToLocalChecked();
        ScriptOrigin origin(name_str, 0, 0, false, -1, Local<Value>(),
                            false, false, true);
        ScriptCompiler::Source source(source_str, origin);
        MaybeLocal<Module> maybe_module =
            ScriptCompiler::CompileModule(isolate, &source);
        if (maybe_module.IsEmpty()) {
            return MaybeLocal<Module>();
        }
        module = maybe_module.ToLocalChecked();
        ModuleWrapper* wrapper = new ModuleWrapper();
        wrapper->isolate = isolate;
        wrapper->module.Reset(isolate, module);
        wrapper->name = name_key;
        cw->module_cache[name_key] = wrapper;
    }

    // Nested static imports inside a dynamically imported module must resolve
    // through the same callback; scope the dynamic resolver id for the duration.
    int saved = cw->module_resolver_id;
    cw->module_resolver_id = cw->dynamic_resolver_id;

    if (module->GetStatus() < Module::kInstantiated) {
        Maybe<bool> ok = module->InstantiateModule(context, moduleResolveCallback);
        if (ok.IsNothing() || !ok.FromJust()) {
            cw->module_resolver_id = saved;
            return MaybeLocal<Module>();
        }
    }

    // Evaluate() also answers for a module that has already been evaluated, or whose
    // evaluation is still waiting on a top-level await: it gives the promise of that
    // evaluation, so import() of a module still evaluating waits for it, and of one
    // that threw rejects, as in Node. Only a module evaluating synchronously right now
    // has no such promise; it is treated as done, as before.
    MaybeLocal<Value> maybe_eval;
    if (module->GetStatus() != Module::kEvaluating) {
        maybe_eval = module->Evaluate(context);
    } else {
        maybe_eval = Undefined(isolate).As<Value>();
    }
    cw->module_resolver_id = saved;

    if (maybe_eval.IsEmpty()) {
        return MaybeLocal<Module>();
    }
    if (out_eval_result) *out_eval_result = maybe_eval.ToLocalChecked();
    return module;
}

// Load, link and evaluate the module an import() names, then settle the import()
// promise with its namespace or the failure. Returns false when execution is
// terminating: the module, or a CommonJS dependency loaded for it, called
// process.exit(). That is not an exception to reject import() with (rejecting would
// swallow the termination and the importer would carry on with a null reason), so
// the termination is left pending to keep unwinding and the promise is not settled.
static bool runDynamicImport(ContextWrapper* cw, Isolate* isolate,
                             Local<Context> context,
                             Local<Promise::Resolver> resolver,
                             const std::string& spec,
                             const std::string& referrer,
                             std::vector<std::string>& attrs) {
    // Resolve + load the specifier's source via the Go loader.
    char* resolved_source = nullptr;
    char* resolved_name = nullptr;
    void* resolve_error = nullptr;
    std::vector<int> attr_lens;
    std::vector<char*> attr_ptrs = cStrings(attrs, &attr_lens);
    int r = goModuleResolve(cw->dynamic_resolver_id,
                            const_cast<char*>(spec.c_str()),
                            const_cast<char*>(referrer.c_str()),
                            attr_ptrs.data(), attr_lens.data(), static_cast<int>(attr_ptrs.size()),
                            &resolved_source, &resolved_name, &resolve_error);
    if (r != 0 || !resolved_source || !resolved_name) {
        if (resolved_source) free(resolved_source);
        if (resolved_name) free(resolved_name);
        if (isolate->IsExecutionTerminating()) return false;
        resolver->Reject(context, resolveFailure(isolate, resolve_error,
            std::string("Cannot find module '") + spec + "'")).Check();
        return true;
    }

    TryCatch try_catch(isolate);
    Local<Value> eval_result;
    MaybeLocal<Module> maybe_module = compileInstantiateEvaluate(
        cw, isolate, context, resolved_source, resolved_name, &eval_result);
    free(resolved_source);
    free(resolved_name);

    if (maybe_module.IsEmpty()) {
        if (try_catch.HasTerminated() || isolate->IsExecutionTerminating()) {
            try_catch.ReThrow();
            return false;
        }
        Local<Value> exc = try_catch.HasCaught()
            ? try_catch.Exception()
            : Exception::Error(String::NewFromUtf8(isolate,
                  "dynamic import failed").ToLocalChecked());
        resolver->Reject(context, exc).Check();
        return true;
    }

    Local<Module> module = maybe_module.ToLocalChecked();
    Local<Value> ns = module->GetModuleNamespace();

    // module->Evaluate() returns a promise (fulfilled once the body, including
    // any top-level await, settles). Chain: resolve the import() promise with
    // the namespace only after that evaluation promise fulfills, propagating
    // rejections. A tiny JS helper does the .then mapping to the namespace.
    Local<Object> global = context->Global();
    Local<Value> finisher;
    bool have_finisher =
        global->Get(context, String::NewFromUtf8(isolate,
                    "__esmFinishDynamicImport").ToLocalChecked())
            .ToLocal(&finisher) && finisher->IsFunction();

    if (have_finisher && !eval_result.IsEmpty() && eval_result->IsPromise()) {
        Local<Value> argv[2] = { eval_result, ns };
        Local<Value> chained;
        if (finisher.As<Function>()->Call(context, Undefined(isolate), 2, argv)
                .ToLocal(&chained)) {
            resolver->Resolve(context, chained).Check();
            return true;
        }
        if (try_catch.HasTerminated() || isolate->IsExecutionTerminating()) {
            try_catch.ReThrow();
            return false;
        }
    }

    // No evaluation promise (or no finisher): the body already ran to
    // completion, so resolve directly with the namespace.
    resolver->Resolve(context, ns).Check();
    return true;
}

// The deferred half of import(): a native function holding [resolver, specifier,
// referrer] as its data, which the JS scheduler calls once the importer's current
// code has finished.
static void dynamicImportJob(const FunctionCallbackInfo<Value>& info) {
    Isolate* isolate = info.GetIsolate();
    HandleScope handle_scope(isolate);
    Local<Context> context = isolate->GetCurrentContext();
    ContextWrapper* cw = wrapperFromContext(context);
    if (!cw || !info.Data()->IsArray()) return;

    Local<Array> data = info.Data().As<Array>();
    Local<Value> resolver_v, spec_v, referrer_v, attrs_v;
    if (!data->Get(context, 0).ToLocal(&resolver_v) ||
        !data->Get(context, 1).ToLocal(&spec_v) ||
        !data->Get(context, 2).ToLocal(&referrer_v) ||
        !data->Get(context, 3).ToLocal(&attrs_v) ||
        !resolver_v->IsPromise()) {
        return;
    }
    std::vector<std::string> attrs;
    if (attrs_v->IsArray()) {
        Local<Array> list = attrs_v.As<Array>();
        for (uint32_t i = 0; i < list->Length(); i++) {
            Local<Value> item;
            if (!list->Get(context, i).ToLocal(&item)) return;
            attrs.emplace_back(losslessUtf8(isolate, context, item));
        }
    }
    // Run it at most once, however often the scheduler calls it.
    if (!data->Set(context, 0, Undefined(isolate)).FromMaybe(false)) return;

    String::Utf8Value spec_utf8(isolate, spec_v);
    String::Utf8Value referrer_utf8(isolate, referrer_v);
    std::string spec = *spec_utf8 ? *spec_utf8 : "";
    std::string referrer = *referrer_utf8 ? *referrer_utf8 : "";
    runDynamicImport(cw, isolate, context, resolver_v.As<Promise::Resolver>(),
                     spec, referrer, attrs);
}

// Dynamic import() host callback. V8 calls this for every import() expression;
// we must return a Promise that resolves to the module namespace (or rejects).
// Node loads the module asynchronously, so the code after import() runs before the
// module does: the load and evaluation are handed to the JS helper
// __esmScheduleDynamicImport, which runs them from the event loop. Without the
// helper they run here, synchronously.
static MaybeLocal<Promise> hostImportModuleDynamically(
    Local<Context> context,
    Local<Data> /*host_defined_options*/,
    Local<Value> resource_name,
    Local<String> specifier,
    Local<FixedArray> import_attributes) {

#if V8_MAJOR_VERSION >= 15
    Isolate* isolate = Isolate::GetCurrent();
#else
    Isolate* isolate = context->GetIsolate();
#endif

    ContextWrapper* cw = wrapperFromContext(context);
    if (!cw) return MaybeLocal<Promise>();

    Local<Promise::Resolver> resolver;
    if (!Promise::Resolver::New(context).ToLocal(&resolver)) {
        return MaybeLocal<Promise>();
    }
    Local<Promise> promise = resolver->GetPromise();

    Local<Value> referrer_value = String::Empty(isolate);
    if (!resource_name.IsEmpty() && resource_name->IsString()) {
        referrer_value = resource_name;
    }

    std::vector<std::string> attrs =
        importAttributes(isolate, context, import_attributes, 2);

    Local<Value> scheduler;
    if (context->Global()->Get(context, String::NewFromUtf8(isolate,
                "__esmScheduleDynamicImport").ToLocalChecked())
            .ToLocal(&scheduler) && scheduler->IsFunction()) {
        // The attributes' own strings, so nothing is lost before the job runs.
        std::vector<Local<Value>> attr_values;
        for (int i = 0; !import_attributes.IsEmpty() && i + 1 < import_attributes->Length(); i += 2) {
#if V8_MAJOR_VERSION >= 15
            Local<Data> key = import_attributes->Get(i);
            Local<Data> value = import_attributes->Get(i + 1);
#else
            Local<Data> key = import_attributes->Get(context, i);
            Local<Data> value = import_attributes->Get(context, i + 1);
#endif
            if (!key->IsValue() || !value->IsValue()) continue;
            attr_values.push_back(key.As<Value>());
            attr_values.push_back(value.As<Value>());
        }
        Local<Value> items[4] = {
            resolver, specifier, referrer_value,
            Array::New(isolate, attr_values.data(), attr_values.size())};
        Local<Array> data = Array::New(isolate, items, 4);
        Local<Function> job;
        if (!Function::New(context, dynamicImportJob, data, 0,
                           ConstructorBehavior::kThrow).ToLocal(&job)) {
            return MaybeLocal<Promise>();
        }
        Local<Value> argv[1] = { job };
        if (scheduler.As<Function>()->Call(context, Undefined(isolate), 1, argv)
                .IsEmpty()) {
            return MaybeLocal<Promise>();
        }
        return promise;
    }

    String::Utf8Value spec_utf8(isolate, specifier);
    String::Utf8Value referrer_utf8(isolate, referrer_value);
    std::string spec = *spec_utf8 ? *spec_utf8 : "";
    std::string referrer = *referrer_utf8 ? *referrer_utf8 : "";
    if (!runDynamicImport(cw, isolate, context, resolver, spec, referrer, attrs)) {
        return MaybeLocal<Promise>();
    }
    return promise;
}

void v8go_set_dynamic_import_resolver(void* context_ptr, int resolver_id) {
    if (!context_ptr) return;
    ContextWrapper* cw = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = cw->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    // The resolver id is scoped to this context; the host callback is an
    // isolate-wide hook that recovers per-context state via embedder data.
    cw->dynamic_resolver_id = resolver_id;
    isolate->SetHostImportModuleDynamicallyCallback(hostImportModuleDynamically);
}

// V8 only calls this for rejections with no handler yet and for handlers added afterwards. The
// JavaScript handler decides what that means, once the microtask queue has drained.
static void promiseRejectCallback(PromiseRejectMessage message) {
    Local<Promise> promise = message.GetPromise();
    Isolate* isolate = Isolate::GetCurrent();
    HandleScope handle_scope(isolate);
    Local<Context> context = promise->GetCreationContextChecked(isolate);
    ContextWrapper* wrapper = wrapperFromContext(context);
    if (!wrapper || wrapper->promise_reject_handler.IsEmpty()) return;

    Context::Scope context_scope(context);
    TryCatch try_catch(isolate);
    Local<Value> value = message.GetValue();
    if (value.IsEmpty()) value = Undefined(isolate);
    Local<Value> args[] = {
        Integer::New(isolate, static_cast<int>(message.GetEvent())),
        promise,
        value,
    };
    Local<Function> handler = wrapper->promise_reject_handler.Get(isolate);
    (void)handler->Call(context, Undefined(isolate), 3, args);
}

int v8go_set_promise_reject_handler(void* context_ptr, void* function_ptr) {
    if (!context_ptr || !function_ptr) return 0;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* func_wrapper = static_cast<ValueWrapper*>(function_ptr);
    Isolate* isolate = wrapper->isolate;

    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Value> func = func_wrapper->value.Get(isolate);
    if (!func->IsFunction()) return 0;
    wrapper->promise_reject_handler.Reset(isolate, func.As<Function>());
    isolate->SetPromiseRejectCallback(promiseRejectCallback);
    return 1;
}

void* v8go_context_take_last_script_exception(void* context_ptr) {
    if (!context_ptr) return nullptr;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = wrapper->isolate;

    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    if (wrapper->last_script_exception.IsEmpty()) return nullptr;
    ValueWrapper* result = new ValueWrapper();
    result->isolate = isolate;
    result->value.Reset(isolate, wrapper->last_script_exception.Get(isolate));
    wrapper->last_script_exception.Reset();
    return result;
}

// Runs at most one task V8 has posted to the isolate's foreground task
// runner, such as one FinalizationRegistry's cleanup, without waiting, and
// returns 1 if it ran one. The caller runs it on the thread that drives the
// isolate, between callbacks, never while JavaScript is on the stack.
// Microtasks don't run during the task: the caller runs them afterwards, so
// it can run its own queues first, as Node does after each task.
int v8go_isolate_pump_message_loop(void* isolate_ptr) {
    if (!isolate_ptr || !g_platform) return 0;
    Isolate* isolate = static_cast<Isolate*>(isolate_ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    MicrotasksPolicy policy = isolate->GetMicrotasksPolicy();
    isolate->SetMicrotasksPolicy(MicrotasksPolicy::kExplicit);
    bool ran = v8::platform::PumpMessageLoop(g_platform.get(), isolate,
                                             v8::platform::MessageLoopBehavior::kDoNotWait);
    isolate->SetMicrotasksPolicy(policy);
    return ran ? 1 : 0;
}

int v8go_context_take_exception_place(void* context_ptr, char** resource, int* line,
                                      char** source_line, int* source_line_length, int* start, int* end) {
    if (!context_ptr) return 0;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    // The same lock as the calls that record it.
    Locker locker(wrapper->isolate);
    auto& p = wrapper->exception_place;
    if (!p.has) return 0;
    *resource = strdup(p.resource.c_str());
    *line = p.line;
    *source_line = nullptr;
    *source_line_length = 0;
    if (p.has_source_line) {
        *source_line = static_cast<char*>(malloc(p.source_line.size() + 1));
        memcpy(*source_line, p.source_line.data(), p.source_line.size());
        (*source_line)[p.source_line.size()] = '\0';
        *source_line_length = static_cast<int>(p.source_line.size());
    }
    *start = p.start;
    *end = p.end;
    p = {};
    return 1;
}

int v8go_context_place_of(void* context_ptr, void* value_ptr, char** resource, int* line,
                          char** source_line, int* source_line_length, int* start, int* end) {
    if (!context_ptr || !value_ptr) return 0;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* value = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    // Kept apart from a recorded place, which this must not replace.
    auto saved = wrapper->exception_place;
    recordPlace(wrapper, isolate, context, Exception::CreateMessage(isolate, value->value.Get(isolate)));
    int has = v8go_context_take_exception_place(context_ptr, resource, line, source_line,
                                                source_line_length, start, end);
    wrapper->exception_place = saved;
    return has;
}

void* v8go_context_call_recording_function(void* context_ptr) {
    if (!context_ptr) return nullptr;
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    Local<Function> func;
    if (!FunctionTemplate::New(isolate, callRecording)->GetFunction(context).ToLocal(&func)) return nullptr;
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, func);
    return wrapper;
}

void v8go_perform_microtask_checkpoint(void* isolate_ptr) {
    if (!isolate_ptr) return;
    Isolate* isolate = static_cast<Isolate*>(isolate_ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    isolate->PerformMicrotaskCheckpoint();
}

void* v8go_compile_module(void* context_ptr, const char* source, const char* name, char** error) {
    if (!context_ptr || !source || !name) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    Isolate* isolate = ctx_wrapper->isolate;
    
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    TryCatch try_catch(isolate);
    
    Local<String> source_str = String::NewFromUtf8(isolate, source).ToLocalChecked();
    Local<String> name_str =
        String::NewFromUtf8(isolate, untypedName(name).c_str()).ToLocalChecked();
    
    ScriptOrigin origin(name_str,
                        0,                      // line offset
                        0,                      // column offset
                        false,                  // is_shared_cross_origin
                        -1,                     // script_id
                        Local<Value>(),         // source_map_url
                        false,                  // is_opaque
                        false,                  // is_wasm
                        true);                  // is_module
    
    ScriptCompiler::Source script_source(source_str, origin);
    
    MaybeLocal<Module> maybe_module = ScriptCompiler::CompileModule(isolate, &script_source);
    
    if (maybe_module.IsEmpty()) {
        if (try_catch.HasCaught()) {
            String::Utf8Value exception(isolate, try_catch.Exception());
            String::Utf8Value stack_trace(isolate, try_catch.StackTrace(context).FromMaybe(Local<Value>()));
            std::string err_msg;
            if (*exception) err_msg = *exception;
            if (*stack_trace) {
                err_msg += "\n";
                err_msg += *stack_trace;
            }
            *error = strdup(err_msg.c_str());
        }
        return nullptr;
    }
    
    Local<Module> module = maybe_module.ToLocalChecked();
    
    ModuleWrapper* wrapper = new ModuleWrapper();
    wrapper->isolate = isolate;
    wrapper->module.Reset(isolate, module);
    wrapper->name = name;
    
    // Cache it in this context
    ctx_wrapper->module_cache[name] = wrapper;
    
    return wrapper;
}

int v8go_module_instantiate(void* context_ptr, void* module_ptr, int resolver_id, char** error, void** exception) {
    if (!context_ptr || !module_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ModuleWrapper* mod_wrapper = static_cast<ModuleWrapper*>(module_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    TryCatch try_catch(isolate);
    
    // Set up the resolver id for this context's in-progress instantiation.
    int saved_resolver_id = ctx_wrapper->module_resolver_id;
    ctx_wrapper->module_resolver_id = resolver_id;
    
    Local<Module> module = mod_wrapper->module.Get(isolate);
    
    Maybe<bool> result = module->InstantiateModule(context, moduleResolveCallback);
    
    // Restore the previous resolver id (supports nested/re-entrant loads).
    ctx_wrapper->module_resolver_id = saved_resolver_id;
    
    if (result.IsNothing() || !result.FromJust()) {
        if (try_catch.HasCaught()) {
            String::Utf8Value text(isolate, try_catch.Exception());
            *error = strdup(*text ? *text : "Module instantiation failed");
            if (exception) {
                ValueWrapper* thrown = new ValueWrapper();
                thrown->isolate = isolate;
                thrown->value.Reset(isolate, try_catch.Exception());
                *exception = thrown;
            }
        }
        return 0;
    }
    
    return 1;
}

void* v8go_module_evaluate(void* context_ptr, void* module_ptr, char** error) {
    if (!context_ptr || !module_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ModuleWrapper* mod_wrapper = static_cast<ModuleWrapper*>(module_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    Local<Context> context = ctx_wrapper->context.Get(isolate);
    Context::Scope context_scope(context);
    
    TryCatch try_catch(isolate);
    
    Local<Module> module = mod_wrapper->module.Get(isolate);
    
    MaybeLocal<Value> maybe_result = module->Evaluate(context);
    
    if (maybe_result.IsEmpty()) {
        if (try_catch.HasCaught()) {
            String::Utf8Value exception(isolate, try_catch.Exception());
            String::Utf8Value stack_trace(isolate, try_catch.StackTrace(context).FromMaybe(Local<Value>()));
            std::string err_msg;
            if (*exception) err_msg = *exception;
            if (*stack_trace) {
                err_msg += "\n";
                err_msg += *stack_trace;
            }
            *error = strdup(err_msg.c_str());
        }
        return nullptr;
    }
    
    Local<Value> result = maybe_result.ToLocalChecked();
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, result);
    return wrapper;
}

int v8go_module_get_status(void* module_ptr) {
    if (!module_ptr) return -1;
    
    ModuleWrapper* wrapper = static_cast<ModuleWrapper*>(module_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Module> module = wrapper->module.Get(isolate);
    return static_cast<int>(module->GetStatus());
}

void* v8go_module_get_namespace(void* context_ptr, void* module_ptr) {
    if (!context_ptr || !module_ptr) return nullptr;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ModuleWrapper* mod_wrapper = static_cast<ModuleWrapper*>(module_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Module> module = mod_wrapper->module.Get(isolate);
    Local<Value> ns = module->GetModuleNamespace();
    
    ValueWrapper* wrapper = new ValueWrapper();
    wrapper->isolate = isolate;
    wrapper->value.Reset(isolate, ns);
    return wrapper;
}

int v8go_module_get_requests_length(void* module_ptr) {
    if (!module_ptr) return 0;
    
    ModuleWrapper* wrapper = static_cast<ModuleWrapper*>(module_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Module> module = wrapper->module.Get(isolate);
    Local<FixedArray> requests = module->GetModuleRequests();
    return requests->Length();
}

const char* v8go_module_get_request(void* module_ptr, int index) {
    if (!module_ptr) return nullptr;
    
    ModuleWrapper* wrapper = static_cast<ModuleWrapper*>(module_ptr);
    Isolate* isolate = wrapper->isolate;
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<Module> module = wrapper->module.Get(isolate);
    Local<FixedArray> requests = module->GetModuleRequests();
    
    if (index < 0 || index >= requests->Length()) return nullptr;
    
#if V8_MAJOR_VERSION >= 15
    Local<Data> data = requests->Get(index);
#else
    Local<Data> data = requests->Get(isolate->GetCurrentContext(), index);
#endif
    Local<ModuleRequest> request = data.As<ModuleRequest>();
    Local<String> specifier = request->GetSpecifier();
    
    String::Utf8Value utf8(isolate, specifier);
    return strdup(*utf8 ? *utf8 : "");
}

} // extern "C"

// ---- ValueSerializer --------------------------------------------------------

namespace {

// Array buffer views are host objects written the way Node's serializer writes them: a type,
// the byte length and only the view's own bytes, so a view keeps its type and a Buffer
// does not carry the rest of its pool. V8 runs no JavaScript while it deserializes, so
// the types are worked out here: a Buffer is a Uint8Array whose prototype is
// Buffer.prototype, which the caller passes in.
enum ViewType : uint32_t {
  kInt8 = 0, kUint8, kUint8Clamped, kInt16, kUint16, kInt32, kUint32, kFloat32, kFloat64,
  kDataView, kBuffer, kBigInt64, kBigUint64, kFloat16, kViewTypeCount
};

// inheritsFrom reports whether proto is on object's prototype chain. A typed array is not a
// proxy, but a prototype above it can be, and GetPrototype on a proxy runs its trap, so the
// walk stops at the first proxy, and after a bounded number of steps.
bool inheritsFrom(Local<Object> object, Local<Value> proto) {
  Local<Value> current = object->GetPrototype();
  for (int depth = 0; depth < 64 && current->IsObject(); depth++) {
    if (current->StrictEquals(proto)) return true;
    if (current->IsProxy()) return false;
    current = current.As<Object>()->GetPrototype();
  }
  return false;
}

bool viewTypeOf(Local<Object> object, Local<Value> buffer_proto, uint32_t* type) {
  if (object->IsUint8Array()) {
    // Node checks view.constructor === Buffer, which holds for anything inheriting from
    // Buffer.prototype.
    *type = (!buffer_proto.IsEmpty() && buffer_proto->IsObject() &&
             inheritsFrom(object, buffer_proto)) ? kBuffer : kUint8;
  } else if (object->IsInt8Array()) { *type = kInt8; }
  else if (object->IsUint8ClampedArray()) { *type = kUint8Clamped; }
  else if (object->IsInt16Array()) { *type = kInt16; }
  else if (object->IsUint16Array()) { *type = kUint16; }
  else if (object->IsInt32Array()) { *type = kInt32; }
  else if (object->IsUint32Array()) { *type = kUint32; }
  else if (object->IsFloat32Array()) { *type = kFloat32; }
  else if (object->IsFloat64Array()) { *type = kFloat64; }
  else if (object->IsDataView()) { *type = kDataView; }
  else if (object->IsBigInt64Array()) { *type = kBigInt64; }
  else if (object->IsBigUint64Array()) { *type = kBigUint64; }
  else if (object->IsFloat16Array()) { *type = kFloat16; }
  else { return false; }
  return true;
}

size_t elementSize(uint32_t type) {
  switch (type) {
    case kInt16: case kUint16: case kFloat16: return 2;
    case kInt32: case kUint32: case kFloat32: return 4;
    case kFloat64: case kBigInt64: case kBigUint64: return 8;
    default: return 1;
  }
}

Local<Object> makeView(Isolate* isolate, Local<Context> context, uint32_t type,
                       Local<ArrayBuffer> buffer, size_t length, Local<Value> buffer_proto) {
  size_t n = length / elementSize(type);
  switch (type) {
    case kInt8: return Int8Array::New(buffer, 0, n);
    case kUint8: return Uint8Array::New(buffer, 0, n);
    case kUint8Clamped: return Uint8ClampedArray::New(buffer, 0, n);
    case kInt16: return Int16Array::New(buffer, 0, n);
    case kUint16: return Uint16Array::New(buffer, 0, n);
    case kInt32: return Int32Array::New(buffer, 0, n);
    case kUint32: return Uint32Array::New(buffer, 0, n);
    case kFloat32: return Float32Array::New(buffer, 0, n);
    case kFloat64: return Float64Array::New(buffer, 0, n);
    case kDataView: return DataView::New(buffer, 0, length);
    case kBigInt64: return BigInt64Array::New(buffer, 0, n);
    case kBigUint64: return BigUint64Array::New(buffer, 0, n);
    case kFloat16: return Float16Array::New(buffer, 0, n);
    case kBuffer: {
      Local<Uint8Array> view = Uint8Array::New(buffer, 0, n);
      if (!buffer_proto.IsEmpty() && buffer_proto->IsObject()) {
        // An ordinary typed array's prototype: no JavaScript runs.
        if (view->SetPrototype(context, buffer_proto).IsNothing()) return Local<Object>();
      }
      return view;
    }
  }
  return Local<Object>();
}

class ViewSerializerDelegate : public ValueSerializer::Delegate {
 public:
  ViewSerializerDelegate(Isolate* isolate, Local<Value> buffer_proto)
      : isolate_(isolate), buffer_proto_(buffer_proto) {}

  void ThrowDataCloneError(Local<String> message) override {
    isolate_->ThrowException(Exception::Error(message));
  }

  Maybe<bool> WriteHostObject(Isolate* isolate, Local<Object> object) override {
    uint32_t type = 0;
    if (!object->IsArrayBufferView() || !viewTypeOf(object, buffer_proto_, &type)) {
      return ValueSerializer::Delegate::WriteHostObject(isolate, object);
    }
    Local<ArrayBufferView> view = object.As<ArrayBufferView>();
    // Node copies a view through a new Uint8Array over its buffer, which fails once the
    // buffer is detached.
    if (view->Buffer()->WasDetached()) {
      ThrowDataCloneError(String::NewFromUtf8Literal(isolate, "Cannot clone a view of a detached ArrayBuffer"));
      return Nothing<bool>();
    }
    size_t length = view->ByteLength();
    if (length > 0xFFFFFFFFu) {
      ThrowDataCloneError(String::NewFromUtf8Literal(isolate, "Array buffer view is too large"));
      return Nothing<bool>();
    }
    std::vector<uint8_t> bytes(length);
    if (length > 0) view->CopyContents(bytes.data(), length);
    serializer->WriteUint32(type);
    serializer->WriteUint32(static_cast<uint32_t>(length));
    if (length > 0) serializer->WriteRawBytes(bytes.data(), length);
    return Just(true);
  }

  ValueSerializer* serializer = nullptr;

 private:
  Isolate* isolate_;
  Local<Value> buffer_proto_;
};

class ViewDeserializerDelegate : public ValueDeserializer::Delegate {
 public:
  ViewDeserializerDelegate(Local<Context> context, Local<Value> buffer_proto)
      : context_(context), buffer_proto_(buffer_proto) {}

  MaybeLocal<Object> ReadHostObject(Isolate* isolate) override {
    uint32_t type = 0;
    uint32_t length = 0;
    const void* data = nullptr;
    if (!deserializer->ReadUint32(&type) || type >= kViewTypeCount ||
        !deserializer->ReadUint32(&length) ||
        (length > 0 && !deserializer->ReadRawBytes(length, &data)) ||
        length % elementSize(type) != 0) {
      isolate->ThrowException(Exception::Error(
          String::NewFromUtf8Literal(isolate, "Unable to deserialize cloned data.")));
      return MaybeLocal<Object>();
    }
    // ArrayBuffer::New aborts the process when the allocator refuses, as a
    // MemoryBudget does when it's full, so this asks for null instead and
    // fails as JavaScript's own allocation does.
    std::unique_ptr<BackingStore> store = ArrayBuffer::NewBackingStore(
        isolate, length, BackingStoreInitializationMode::kUninitialized,
        BackingStoreOnFailureMode::kReturnNull);
    if (!store) {
      isolate->ThrowException(Exception::RangeError(
          String::NewFromUtf8Literal(isolate, "Array buffer allocation failed")));
      return MaybeLocal<Object>();
    }
    if (length > 0) memcpy(store->Data(), data, length);
    Local<ArrayBuffer> buffer = ArrayBuffer::New(isolate, std::move(store));
    Local<Object> view = makeView(isolate, context_, type, buffer, length, buffer_proto_);
    if (view.IsEmpty()) return MaybeLocal<Object>();
    return view;
  }

  ValueDeserializer* deserializer = nullptr;

 private:
  Local<Context> context_;
  Local<Value> buffer_proto_;
};

void setCaughtError(Isolate* isolate, TryCatch& try_catch, char** error) {
  if (try_catch.HasTerminated() || !try_catch.HasCaught()) {
    *error = strdup("");
    return;
  }
  String::Utf8Value text(isolate, try_catch.Exception());
  *error = strdup(*text ? *text : "Unknown error");
}

}  // namespace

unsigned char* v8go_value_serialize(void* context_ptr, void* value_ptr, void* buffer_proto_ptr, size_t* size, char** error) {
  if (!context_ptr || !value_ptr || !size || !error) return nullptr;
  ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
  Isolate* isolate = ctx_wrapper->isolate;
  Locker locker(isolate);
  Isolate::Scope isolate_scope(isolate);
  HandleScope handle_scope(isolate);
  Local<Context> context = ctx_wrapper->context.Get(isolate);
  Context::Scope context_scope(context);
  TryCatch try_catch(isolate);

  Local<Value> value = static_cast<ValueWrapper*>(value_ptr)->value.Get(isolate);
  Local<Value> buffer_proto;
  if (buffer_proto_ptr) buffer_proto = static_cast<ValueWrapper*>(buffer_proto_ptr)->value.Get(isolate);
  ViewSerializerDelegate delegate(isolate, buffer_proto);
  ValueSerializer serializer(isolate, &delegate);
  delegate.serializer = &serializer;
  serializer.SetTreatArrayBufferViewsAsHostObjects(true);
  serializer.WriteHeader();
  if (!serializer.WriteValue(context, value).FromMaybe(false)) {
    setCaughtError(isolate, try_catch, error);
    return nullptr;
  }
  std::pair<uint8_t*, size_t> result = serializer.Release();
  *size = result.second;
  return result.first;
}

void* v8go_value_deserialize(void* context_ptr, const unsigned char* data, size_t size, void* buffer_proto_ptr, char** error) {
  if (!context_ptr || !error) return nullptr;
  ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
  Isolate* isolate = ctx_wrapper->isolate;
  Locker locker(isolate);
  Isolate::Scope isolate_scope(isolate);
  HandleScope handle_scope(isolate);
  Local<Context> context = ctx_wrapper->context.Get(isolate);
  Context::Scope context_scope(context);
  TryCatch try_catch(isolate);

  Local<Value> buffer_proto;
  if (buffer_proto_ptr) buffer_proto = static_cast<ValueWrapper*>(buffer_proto_ptr)->value.Get(isolate);
  ViewDeserializerDelegate delegate(context, buffer_proto);
  ValueDeserializer deserializer(isolate, data, size, &delegate);
  delegate.deserializer = &deserializer;
  Local<Value> value;
  if (!deserializer.ReadHeader(context).FromMaybe(false) ||
      !deserializer.ReadValue(context).ToLocal(&value)) {
    setCaughtError(isolate, try_catch, error);
    return nullptr;
  }
  ValueWrapper* wrapper = new ValueWrapper();
  wrapper->isolate = isolate;
  wrapper->value.Reset(isolate, value);
  return wrapper;
}
