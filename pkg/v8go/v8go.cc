// V8 Go bindings C++ implementation
#include "v8go.h"
#include "_cgo_export.h"

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
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsUndefined() ? 1 : 0;
}

int v8go_value_is_null(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsNull() ? 1 : 0;
}

int v8go_value_is_boolean(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsBoolean() ? 1 : 0;
}

int v8go_value_is_number(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsNumber() ? 1 : 0;
}

int v8go_value_is_string(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsString() ? 1 : 0;
}

int v8go_value_is_object(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsObject() ? 1 : 0;
}

int v8go_value_is_array(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsArray() ? 1 : 0;
}

int v8go_value_is_function(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->IsFunction() ? 1 : 0;
}

int v8go_value_to_boolean(void* value_ptr) {
    if (!value_ptr) return 0;
    ValueWrapper* wrapper = static_cast<ValueWrapper*>(value_ptr);
    Isolate* isolate = wrapper->isolate;
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    return wrapper->value.Get(isolate)->BooleanValue(isolate) ? 1 : 0;
}

double v8go_value_to_number(void* context_ptr, void* value_ptr) {
    if (!context_ptr || !value_ptr) return 0;
    
    ContextWrapper* ctx_wrapper = static_cast<ContextWrapper*>(context_ptr);
    ValueWrapper* val_wrapper = static_cast<ValueWrapper*>(value_ptr);
    
    Isolate* isolate = ctx_wrapper->isolate;
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

int v8go_array_length(void* context_ptr, void* array_ptr) {
    if (!context_ptr || !array_ptr) return 0;
    
    ValueWrapper* arr_wrapper = static_cast<ValueWrapper*>(array_ptr);
    Isolate* isolate = arr_wrapper->isolate;
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
    int callback_id = static_cast<int>(reinterpret_cast<intptr_t>(data->Value()));
    
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
    Isolate::Scope isolate_scope(isolate);
    HandleScope handle_scope(isolate);
    
    Local<External> data = External::New(isolate, reinterpret_cast<void*>(static_cast<intptr_t>(callback_id)));
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

} // extern "C"
