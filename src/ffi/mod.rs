//! FFI entry points — 4 extern "C" functions for C ABI
//!
//! Functions:
//! - `memhop_open`          : Create a MemHop instance from JSON config → `*mut MemHopHandle`
//! - `memhop_execute`       : Execute a JSON command → JSON string (caller must free)
//! - `memhop_free_string`   : Free a string returned by `memhop_execute`
//! - `memhop_close`         : Close & destroy a MemHop instance
//!
//! All extern functions are wrapped in `std::panic::catch_unwind` to prevent
//! panics from crossing FFI boundaries.

mod handler;
pub mod protocol;

use std::ffi::{CStr, CString};
use std::os::raw::c_char;
use std::sync::Mutex;

use crate::ffi::handler::dispatch;
use crate::ffi::protocol::{FfiCommand, FfiResponse};
use crate::MemHop;

/// Opaque handle to a MemHop instance, safe to pass across FFI boundary
pub struct MemHopHandle(Mutex<MemHop>);

/// Open a MemHop database from a JSON config string.
///
/// # Safety
/// `config_json` must be a valid, null-terminated UTF-8 C string containing a
/// JSON representation of `MemHopConfig`.
#[no_mangle]
pub unsafe extern "C" fn memhop_open(config_json: *const c_char) -> *mut MemHopHandle {
    let result = std::panic::catch_unwind(|| -> Result<*mut MemHopHandle, String> {
        // 1. Parse config JSON
        let config_str = CStr::from_ptr(config_json)
            .to_str()
            .map_err(|e| format!("invalid UTF-8 in config: {}", e))?;

        let config: crate::MemHopConfig =
            serde_json::from_str(config_str).map_err(|e| format!("invalid config JSON: {}", e))?;

        // 2. Open database
        let db = MemHop::open(config).map_err(|e| format!("failed to open MemHop: {}", e))?;

        // 3. Return raw pointer to handle
        Ok(Box::into_raw(Box::new(MemHopHandle(Mutex::new(db)))))
    });

    match result {
        Ok(Ok(ptr)) => ptr,
        Ok(Err(e)) => {
            eprintln!("[memhop_open] error: {}", e);
            std::ptr::null_mut()
        }
        Err(panic) => {
            let msg = extract_panic_msg(&panic);
            eprintln!("[memhop_open] panic: {}", msg);
            std::ptr::null_mut()
        }
    }
}

/// Execute a JSON command against a MemHop instance.
///
/// Returns a JSON string. The caller **must** free this string via
/// `memhop_free_string`.
///
/// # Safety
/// - `handle` must be a valid pointer returned by `memhop_open` (not yet freed)
/// - `command_json` must be a valid, null-terminated UTF-8 C string
#[no_mangle]
pub unsafe extern "C" fn memhop_execute(
    handle: *mut MemHopHandle,
    command_json: *const c_char,
) -> *mut c_char {
    let response = std::panic::catch_unwind(|| {
        // 1. Validate handle
        if handle.is_null() {
            return FfiResponse::err("handle is null");
        }

        // 2. Parse command JSON
        let cmd_str = match CStr::from_ptr(command_json).to_str() {
            Ok(s) => s,
            Err(e) => return FfiResponse::err(format!("invalid UTF-8 in command: {}", e)),
        };

        let command: FfiCommand = match serde_json::from_str(cmd_str) {
            Ok(c) => c,
            Err(e) => return FfiResponse::err(format!("invalid command JSON: {}", e)),
        };

        // 3. Lock handle and dispatch
        let handle_ref = &*handle;
        match handle_ref.0.lock() {
            Ok(mut db) => match dispatch(&mut db, command) {
                Ok(data) => FfiResponse::ok(data),
                Err(e) => FfiResponse::err(e),
            },
            Err(e) => FfiResponse::err(format!("mutex poisoned: {}", e)),
        }
    });

    let response = match response {
        Ok(r) => r,
        Err(panic) => {
            let msg = extract_panic_msg(&panic);
            FfiResponse::err(format!("internal panic: {}", msg))
        }
    };

    // Serialize response to JSON string
    let json_str = serde_json::to_string(&response).unwrap_or_else(|e| {
        format!(
            r#"{{"success":false,"error":"response serialization failed: {}"}}"#,
            e
        )
    });

    // Convert to C string (leaked, caller must free via memhop_free_string)
    CString::new(json_str)
        .unwrap_or_else(|_| {
            CString::new(r#"{"success":false,"error":"null byte in response"}"#).unwrap()
        })
        .into_raw()
}

/// Free a string returned by `memhop_execute`.
///
/// # Safety
/// `ptr` must be a valid pointer returned by `memhop_execute`, or null.
/// Calling with null is a safe no-op.
#[no_mangle]
pub unsafe extern "C" fn memhop_free_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        let _ = CString::from_raw(ptr);
    }
}

/// Close a MemHop instance and free all resources.
///
/// # Safety
/// `handle` must be a valid pointer returned by `memhop_open`.
/// After calling this function, the handle pointer is **invalidated**
/// and must not be used again.
#[no_mangle]
pub unsafe extern "C" fn memhop_close(handle: *mut MemHopHandle) {
    let result = std::panic::catch_unwind(|| {
        if handle.is_null() {
            eprintln!("[memhop_close] null handle, ignoring");
            return;
        }

        // Access inner data through raw pointer to avoid Box drop-check issues
        let memhop_ref: &MemHopHandle = &*handle;
        if let Ok(mut db) = memhop_ref.0.lock() {
            // Must checkpoint before sync to persist B-tree + sparse index
            if let Err(e) = db.checkpoint() {
                eprintln!("[memhop_close] checkpoint error: {}", e);
            } else if let Err(e) = db.sync() {
                eprintln!("[memhop_close] sync error: {}", e);
            }
            db.closed = true;
        }
        // memhop_ref borrow released; now safe to take ownership and drop
        let _ = Box::from_raw(handle);
    });

    if let Err(panic) = result {
        let msg = extract_panic_msg(&panic);
        eprintln!("[memhop_close] panic: {}", msg);
    }
}

// ============================================================================
// Helper: extract panic message from Box<dyn Any>
// ============================================================================

fn extract_panic_msg(panic: &(dyn std::any::Any + Send)) -> String {
    if let Some(s) = panic.downcast_ref::<&str>() {
        s.to_string()
    } else if let Some(s) = panic.downcast_ref::<String>() {
        s.clone()
    } else {
        "unknown panic".to_string()
    }
}
