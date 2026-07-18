// V8 Go bindings C header
#ifndef V8GO_H
#define V8GO_H

#ifdef __cplusplus
extern "C" {
#endif

// V8 initialization
void v8go_init();
void v8go_dispose();

// Isolate operations
void* v8go_isolate_new();
void v8go_isolate_dispose(void* isolate);

// Context operations
void* v8go_context_new(void* isolate);
void* v8go_context_new_with_template(void* isolate, void* global_template);
void v8go_context_dispose(void* context);
void* v8go_context_run_script(void* context, const char* source, const char* origin, char** error);
void* v8go_context_global(void* context);

// Value operations
char* v8go_value_to_string(void* context, void* value);
int v8go_value_is_undefined(void* value);
int v8go_value_is_null(void* value);
int v8go_value_is_boolean(void* value);
int v8go_value_is_number(void* value);
int v8go_value_is_string(void* value);
int v8go_value_is_object(void* value);
int v8go_value_is_array(void* value);
int v8go_value_is_function(void* value);
int v8go_value_to_boolean(void* value);
double v8go_value_to_number(void* context, void* value);
long v8go_value_to_integer(void* context, void* value);

// Value factory
void* v8go_undefined(void* context);
void* v8go_null(void* context);
void* v8go_true(void* context);
void* v8go_false(void* context);
void* v8go_new_boolean(void* context, int value);
void* v8go_new_number(void* context, double value);
void* v8go_new_integer(void* context, long value);
void* v8go_new_string(void* context, const char* value, int length);
void* v8go_new_object(void* context);
void* v8go_new_array(void* context, int length);

// Object operations
int v8go_object_set(void* context, void* object, const char* key, void* value);
int v8go_object_set_idx(void* context, void* object, int idx, void* value);
void* v8go_object_get(void* context, void* object, const char* key);
void* v8go_object_get_idx(void* context, void* object, int idx);
int v8go_object_has(void* context, void* object, const char* key);
int v8go_object_delete(void* context, void* object, const char* key);
void* v8go_object_get_property_names(void* context, void* object);

// Array operations  
int v8go_array_length(void* context, void* array);

// Function template operations
void* v8go_function_template_new_with_id(void* isolate, int callback_id);
void* v8go_function_template_get_function(void* context, void* function_template);

// Object template operations
void* v8go_object_template_new(void* isolate);
void v8go_object_template_set_function(void* object_template, const char* key, void* function_template);
void* v8go_object_template_new_instance(void* context, void* object_template);

// Callback info operations
int v8go_callback_info_length(void* info);
void* v8go_callback_info_arg(void* info, int index);
void* v8go_callback_info_this(void* info);

// Function call
void* v8go_function_call(void* context, void* function, void* recv, int argc, void** argv, char** error);

// JSON operations
void* v8go_json_parse(void* context, const char* json, char** error);
char* v8go_json_stringify(void* context, void* value, char** error);

// Promise operations
void* v8go_promise_resolver_new(void* context);
void* v8go_promise_resolver_get_promise(void* context, void* resolver);
int v8go_promise_resolver_resolve(void* context, void* resolver, void* value);
int v8go_promise_resolver_reject(void* context, void* resolver, void* value);
int v8go_promise_state(void* promise);
void* v8go_promise_result(void* context, void* promise);

// Exception handling
void* v8go_throw_exception(void* context, const char* message);
void* v8go_throw_type_error(void* context, const char* message);
void* v8go_throw_range_error(void* context, const char* message);

// ES Module operations
void* v8go_compile_module(void* context, const char* source, const char* name, char** error);
int v8go_module_instantiate(void* context, void* module, int resolver_id, char** error);
void* v8go_module_evaluate(void* context, void* module, char** error);
int v8go_module_get_status(void* module);
void* v8go_module_get_namespace(void* context, void* module);
int v8go_module_get_requests_length(void* module);
const char* v8go_module_get_request(void* module, int index);

// Dynamic import: install the host callback + the persistent resolver id used to
// resolve import() specifiers (which happen outside a module instantiation).
// The resolver id is stored per-context; pass the context wrapper pointer.
void v8go_set_dynamic_import_resolver(void* context, int resolver_id);

// Drain V8's microtask queue (promise reactions). The default policy is kAuto,
// but callers pumping the event loop can force a checkpoint explicitly.
void v8go_perform_microtask_checkpoint(void* isolate);

#ifdef __cplusplus
}
#endif

#endif // V8GO_H
