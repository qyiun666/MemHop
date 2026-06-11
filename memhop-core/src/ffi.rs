//! FFI (Foreign Function Interface) — C ABI 接口层
//!
//! 提供 C 兼容的动态库接口，用于闭源 SDK 分发。
//! 编译产物：libmemhop_core.dylib (macOS) / memhop_core.dll (Windows) / libmemhop_core.so (Linux)

use std::ffi::CStr;
use std::os::raw::{c_char, c_int};
use std::ptr;
use std::sync::Mutex;

use crate::{Brain, MemHopConfig, MemHopSDK, RecallRequest, StoreBatch, StoreItem};

// 全局 Brain 实例（简化版，单 agent）
static BRAIN: Mutex<Option<Brain>> = Mutex::new(None);

/// 错误码
#[repr(C)]
pub enum MemHopResult {
    Success = 0,
    NotInitialized = -1,
    InvalidArgument = -2,
    StorageError = -3,
    InternalError = -4,
}

/// 初始化 SDK
///
/// # Safety
/// model_path 必须是有效的 UTF-8 字符串，或为 NULL
#[unsafe(no_mangle)]
pub extern "C" fn memhop_init(
    model_path: *const c_char,
    vector_dim: c_int,
) -> MemHopResult {
    let model_path_str = if model_path.is_null() {
        None
    } else {
        match unsafe { CStr::from_ptr(model_path) }.to_str() {
            Ok(s) => Some(s.to_string()),
            Err(_) => return MemHopResult::InvalidArgument,
        }
    };

    let config = MemHopConfig {
        model_path: model_path_str,
        vector_dim: vector_dim as usize,
        ..Default::default()
    };

    match MemHopSDK::init(config) {
        Ok(_) => MemHopResult::Success,
        Err(_) => MemHopResult::InternalError,
    }
}

/// 创建 Brain 实例
///
/// # Safety
/// brains_dir 和 agent_id 必须是有效的 UTF-8 字符串
#[unsafe(no_mangle)]
pub extern "C" fn memhop_create_brain(
    brains_dir: *const c_char,
    agent_id: *const c_char,
) -> MemHopResult {
    let brains_dir = match unsafe { CStr::from_ptr(brains_dir) }.to_str() {
        Ok(s) => s,
        Err(_) => return MemHopResult::InvalidArgument,
    };

    let agent_id = match unsafe { CStr::from_ptr(agent_id) }.to_str() {
        Ok(s) => s,
        Err(_) => return MemHopResult::InvalidArgument,
    };

    match MemHopSDK::create_brain(brains_dir, agent_id) {
        Ok(brain) => {
            if let Ok(mut global_brain) = BRAIN.lock() {
                *global_brain = Some(brain);
                MemHopResult::Success
            } else {
                MemHopResult::InternalError
            }
        }
        Err(_) => MemHopResult::StorageError,
    }
}

/// 存储记忆
///
/// # Safety
/// text 必须是有效的 UTF-8 字符串
#[unsafe(no_mangle)]
pub extern "C" fn memhop_store(
    text: *const c_char,
    topic_label: *const c_char,
) -> MemHopResult {
    let text = match unsafe { CStr::from_ptr(text) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return MemHopResult::InvalidArgument,
    };

    let topic_label = if topic_label.is_null() {
        None
    } else {
        match unsafe { CStr::from_ptr(topic_label) }.to_str() {
            Ok(s) => Some(s.to_string()),
            Err(_) => return MemHopResult::InvalidArgument,
        }
    };

    let mut brain_guard = match BRAIN.lock() {
        Ok(g) => g,
        Err(_) => return MemHopResult::InternalError,
    };

    let brain = match brain_guard.as_mut() {
        Some(b) => b,
        None => return MemHopResult::NotInitialized,
    };

    let batch = StoreBatch {
        items: vec![StoreItem {
            text,
            topic_label,
            ..Default::default()
        }],
    };

    match brain.batch_store(batch) {
        Ok(_) => MemHopResult::Success,
        Err(_) => MemHopResult::StorageError,
    }
}

/// 检索记忆
///
/// # Safety
/// - query 必须是有效的 UTF-8 字符串
/// - result_buffer 必须有足够空间 (至少 4096 字节)
/// - result_len 会被设置为实际写入的字节数
#[unsafe(no_mangle)]
pub extern "C" fn memhop_recall(
    query: *const c_char,
    max_results: c_int,
    result_buffer: *mut c_char,
    result_buffer_size: c_int,
    result_len: *mut c_int,
) -> MemHopResult {
    let query = match unsafe { CStr::from_ptr(query) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return MemHopResult::InvalidArgument,
    };

    let mut brain_guard = match BRAIN.lock() {
        Ok(g) => g,
        Err(_) => return MemHopResult::InternalError,
    };

    let brain = match brain_guard.as_mut() {
        Some(b) => b,
        None => return MemHopResult::NotInitialized,
    };

    let req = RecallRequest {
        query,
        max_results: max_results as usize,
        ..Default::default()
    };

    match brain.recall(&req) {
        Ok(response) => {
            // 简单序列化：每行一个结果 "score|text"
            let mut output = String::new();
            for result in &response.results {
                output.push_str(&format!("{:.4}|{}\n", result.score, result.text));
            }

            let output_bytes = output.as_bytes();
            let copy_len = (output_bytes.len()).min(result_buffer_size as usize - 1);

            unsafe {
                ptr::copy_nonoverlapping(output_bytes.as_ptr(), result_buffer as *mut u8, copy_len);
                *result_buffer.add(copy_len) = 0; // null terminator
                *result_len = copy_len as c_int;
            }

            MemHopResult::Success
        }
        Err(_) => MemHopResult::StorageError,
    }
}

/// 释放资源
#[unsafe(no_mangle)]
pub extern "C" fn memhop_cleanup() -> MemHopResult {
    if let Ok(mut brain) = BRAIN.lock() {
        *brain = None;
        MemHopResult::Success
    } else {
        MemHopResult::InternalError
    }
}
