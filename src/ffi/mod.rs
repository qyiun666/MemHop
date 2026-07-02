// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! FFI entry points — JSON-in/JSON-out via C ABI with panic isolation.

mod handler;
pub mod protocol;

use std::ffi::{CStr, CString};
use std::os::raw::c_char;
use std::sync::Mutex;

use crate::ffi::handler::dispatch;
use crate::ffi::protocol::{FfiCommand, FfiResponse};
use crate::MemHop;

thread_local! {
    static LAST_ERROR: std::cell::RefCell<Option<CString>> = const { std::cell::RefCell::new(None) };
}

fn set_last_error(msg: &str) {
    if let Ok(cstr) = CString::new(msg) {
        LAST_ERROR.with(|e| *e.borrow_mut() = Some(cstr));
    }
}

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
        if config_json.is_null() {
            return Err("config_json is null".to_string());
        }
        let config_str = CStr::from_ptr(config_json)
            .to_str()
            .map_err(|e| format!("invalid UTF-8 in config: {}", e))?;

        let config: crate::MemHopConfig =
            serde_json::from_str(config_str).map_err(|e| format!("invalid config JSON: {}", e))?;

        let db = MemHop::open(config).map_err(|e| format!("failed to open MemHop: {}", e))?;

        Ok(Box::into_raw(Box::new(MemHopHandle(Mutex::new(db)))))
    });

    match result {
        Ok(Ok(ptr)) => ptr,
        Ok(Err(e)) => {
            eprintln!("[memhop_open] error: {}", e);
            set_last_error(&e);
            std::ptr::null_mut()
        }
        Err(panic) => {
            let msg = extract_panic_msg(&panic);
            eprintln!("[memhop_open] panic: {}", msg);
            set_last_error(&msg);
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
        if handle.is_null() {
            return FfiResponse::err("handle is null");
        }

        if command_json.is_null() {
            return FfiResponse::err("command_json is null");
        }

        let cmd_str = match CStr::from_ptr(command_json).to_str() {
            Ok(s) => s,
            Err(e) => return FfiResponse::err(format!("invalid UTF-8 in command: {}", e)),
        };

        let command: FfiCommand = match serde_json::from_str(cmd_str) {
            Ok(c) => c,
            Err(e) => return FfiResponse::err(format!("invalid command JSON: {}", e)),
        };

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

    let json_str = serde_json::to_string(&response).unwrap_or_else(|e| {
        format!(
            r#"{{"success":false,"error":"response serialization failed: {}"}}"#,
            e
        )
    });

    // Caller must free via memhop_free_string
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

        // Explicit scope: drop borrow before Box::from_raw takes ownership
        {
            let memhop_ref: &MemHopHandle = &*handle;
            if let Ok(mut db) = memhop_ref.0.lock() {
                // checkpoint→sync order persists B-tree + sparse index
                if let Err(e) = db.checkpoint() {
                    eprintln!("[memhop_close] checkpoint error: {}", e);
                } else if let Err(e) = db.sync() {
                    eprintln!("[memhop_close] sync error: {}", e);
                }
                db.closed = true;
            }
        }

        let _ = Box::from_raw(handle);
    });

    if let Err(panic) = result {
        let msg = extract_panic_msg(&panic);
        eprintln!("[memhop_close] panic: {}", msg);
    }
}

/// Get the last error message from the most recent failed FFI operation.
///
/// Returns a pointer to a null-terminated C string containing the error
/// message, or null if no error has occurred. The returned pointer is
/// owned by the library and must not be freed by the caller.
///
/// # Safety
/// This function returns a raw pointer to an internal C string. The caller
/// must not free or modify the returned pointer. The pointer is only valid
/// until the next FFI call that may set an error.
#[no_mangle]
pub unsafe extern "C" fn memhop_last_error() -> *const c_char {
    LAST_ERROR.with(|e| match *e.borrow() {
        Some(ref cstr) => cstr.as_ptr(),
        None => std::ptr::null(),
    })
}

fn extract_panic_msg(panic: &(dyn std::any::Any + Send)) -> String {
    if let Some(s) = panic.downcast_ref::<&str>() {
        s.to_string()
    } else if let Some(s) = panic.downcast_ref::<String>() {
        s.clone()
    } else {
        "unknown panic".to_string()
    }
}
