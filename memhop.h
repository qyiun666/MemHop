// memhop.h — C header for libmemhop
// 4 extern "C" functions for JSON-in JSON-out FFI

#ifndef MEMHOP_H
#define MEMHOP_H

#ifdef __cplusplus
extern "C" {
#endif

#include <stddef.h>

/// Open a MemHop database from a JSON config string.
/// Returns an opaque handle pointer, or NULL on failure.
/// The handle must be freed via memhop_close().
///
/// Config JSON example:
///   {"db_path":"/tmp/test.meh","vector_dim":768}
void* memhop_open(const char* config_json);

/// Execute a JSON command against a MemHop instance.
/// Returns a JSON string (caller must free via memhop_free_string).
///
/// JSON response format:
///   {"success":true,"data":{...}}
///   {"success":false,"error":"..."}
///
/// Command JSON examples:
///   {"command":"search","dialogue":"hello","context_limit":10}
///   {"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":20}}
///   {"command":"close"}
char* memhop_execute(void* handle, const char* command_json);

/// Free a string returned by memhop_execute.
/// Calling with NULL is a safe no-op.
void memhop_free_string(char* str);

/// Close a MemHop instance and free all resources.
/// After calling this, the handle pointer is invalidated.
void memhop_close(void* handle);

#ifdef __cplusplus
}
#endif

#endif // MEMHOP_H
