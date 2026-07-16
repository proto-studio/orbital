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
#include <libplatform/libplatform.h>
#include <cstring>
#include <memory>
#include <unordered_map>

using namespace v8;

// Global platform instance
static std::unique_ptr<Platform> g_platform;
static bool g_initialized = false;

// Wrapper to hold persistent handles
struct ContextWrapper {
    Isolate* isolate;
    Global<Context> context;
};

struct ValueWrapper {
    Isolate* isolate;
    Global<Value> value;
};

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

// Global module resolver callback ID for the current instantiation
static thread_local int g_module_resolver_id = -1;
static thread_local void* g_module_context = nullptr;

// Module cache for resolved modules (maps specifier to ModuleWrapper*)
static std::unordered_map<std::string, ModuleWrapper*> g_module_cache;

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

void v8go_init() {
    if (g_initialized) return;
    
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
    Isolate::CreateParams create_params;
    create_params.array_buffer_allocator = ArrayBuffer::Allocator::NewDefaultAllocator();
    
    Isolate* isolate = Isolate::New(create_params);
    return isolate;
}

void v8go_isolate_dispose(void* ptr) {
    if (!ptr) return;
    Isolate* isolate = static_cast<Isolate*>(ptr);
    isolate->Dispose();
}

void* v8go_context_new(void* isolate_ptr) {
    return v8go_context_new_with_template(isolate_ptr, nullptr);
}

void* v8go_context_new_with_template(void* isolate_ptr, void* global_template_ptr) {
    if (!isolate_ptr) return nullptr;
    
    Isolate* isolate = static_cast<Isolate*>(isolate_ptr);
    Locker locker(isolate);
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<ObjectTemplate> global_template;
    if (global_template_ptr) {
        ObjectTemplateWrapper* tmpl_wrapper = static_cast<ObjectTemplateWrapper*>(global_template_ptr);
        global_template = tmpl_wrapper->tmpl.Get(isolate);
    }
    
    Local<Context> context = Context::New(isolate, nullptr, global_template);
    
    ContextWrapper* wrapper = new ContextWrapper();
    wrapper->isolate = isolate;
    wrapper->context.Reset(isolate, context);
    
    return wrapper;
}

void v8go_context_dispose(void* ptr) {
    if (!ptr) return;
    ContextWrapper* wrapper = static_cast<ContextWrapper*>(ptr);
    wrapper->context.Reset();
    delete wrapper;
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
        if (try_catch.HasCaught()) {
            String::Utf8Value exception(isolate, try_catch.Exception());
            *error = strdup(*exception ? *exception : "Unknown error");
        }
        return nullptr;
    }
    
    Local<Script> script = maybe_script.ToLocalChecked();
    MaybeLocal<Value> maybe_result = script->Run(context);
    
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
    
    // Get the context
    Local<Context> context = isolate->GetCurrentContext();
    ContextWrapper* ctx_wrapper = new ContextWrapper();
    ctx_wrapper->isolate = isolate;
    ctx_wrapper->context.Reset(isolate, context);
    
    // Call Go callback handler
    void* result = goCallbackHandler(ctx_wrapper, callback_id, cb_info);
    
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
void* v8go_function_call(void* context_ptr, void* function_ptr, void* recv_ptr, int argc, void** argv, char** error) {
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
        if (try_catch.HasCaught()) {
            String::Utf8Value exception(isolate, try_catch.Exception());
            *error = strdup(*exception ? *exception : "Unknown error");
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
    
    String::Utf8Value specifier_utf8(isolate, specifier);
    const char* specifier_str = *specifier_utf8;
    
    // Get referrer name for resolution
    std::string referrer_name;
    for (auto& [key, wrapper] : g_module_cache) {
        if (wrapper->isolate == isolate) {
            Local<Module> cached = wrapper->module.Get(isolate);
            if (cached->GetIdentityHash() == referrer->GetIdentityHash()) {
                referrer_name = wrapper->name;
                break;
            }
        }
    }
    
    // Call Go to resolve and load the module
    char* resolved_source = nullptr;
    char* resolved_name = nullptr;
    int result = goModuleResolve(g_module_resolver_id, 
        const_cast<char*>(specifier_str), 
        const_cast<char*>(referrer_name.c_str()), 
        &resolved_source, &resolved_name);
    
    if (result != 0 || !resolved_source || !resolved_name) {
        isolate->ThrowException(Exception::Error(
            String::NewFromUtf8(isolate, 
                (std::string("Cannot resolve module: ") + specifier_str).c_str()
            ).ToLocalChecked()
        ));
        if (resolved_source) free(resolved_source);
        if (resolved_name) free(resolved_name);
        return MaybeLocal<Module>();
    }
    
    std::string name_key(resolved_name);
    
    // Check if module is already in cache
    auto it = g_module_cache.find(name_key);
    if (it != g_module_cache.end()) {
        free(resolved_source);
        free(resolved_name);
        return it->second->module.Get(isolate);
    }
    
    // Compile the module
    Local<String> source_str = String::NewFromUtf8(isolate, resolved_source).ToLocalChecked();
    Local<String> name_str = String::NewFromUtf8(isolate, resolved_name).ToLocalChecked();
    
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
    
    // Cache the module
    ModuleWrapper* wrapper = new ModuleWrapper();
    wrapper->isolate = isolate;
    wrapper->module.Reset(isolate, module);
    wrapper->name = name_key;
    g_module_cache[name_key] = wrapper;
    
    free(resolved_name);
    
    // Recursively instantiate dependencies
    Maybe<bool> instantiate_result = module->InstantiateModule(context, moduleResolveCallback);
    if (instantiate_result.IsNothing() || !instantiate_result.FromJust()) {
        return MaybeLocal<Module>();
    }
    
    return module;
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
    Local<String> name_str = String::NewFromUtf8(isolate, name).ToLocalChecked();
    
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
    
    // Cache it
    g_module_cache[name] = wrapper;
    
    return wrapper;
}

int v8go_module_instantiate(void* context_ptr, void* module_ptr, int resolver_id, char** error) {
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
    
    // Set up the resolver callback context
    g_module_resolver_id = resolver_id;
    g_module_context = context_ptr;
    
    Local<Module> module = mod_wrapper->module.Get(isolate);
    
    Maybe<bool> result = module->InstantiateModule(context, moduleResolveCallback);
    
    // Clear the resolver context
    g_module_resolver_id = -1;
    g_module_context = nullptr;
    
    if (result.IsNothing() || !result.FromJust()) {
        if (try_catch.HasCaught()) {
            String::Utf8Value exception(isolate, try_catch.Exception());
            *error = strdup(*exception ? *exception : "Module instantiation failed");
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
