//! IPC 协议定义 (v0.23.0)
//!
//! 定义 memhop-encoder 和 memhop-encoder-client 之间的通信协议。
//! 使用 Unix Domain Socket + bincode 二进制协议。

use half::f16;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// 编码请求
#[derive(Debug, Serialize, Deserialize)]
pub enum EncodeRequest {
    /// 单条编码
    Single { text: String },
    /// 批量编码
    Batch { texts: Vec<String> },
    /// 查询编码器维度
    Dim,
    /// 健康检查
    Ping,
}

/// 编码响应
#[derive(Debug, Serialize, Deserialize)]
pub enum EncodeResponse {
    /// 单条编码结果
    Single {
        dense: Vec<f16>,
        sparse: HashMap<String, f32>,
    },
    /// 批量编码结果
    Batch { outputs: Vec<EncoderOutputOwned> },
    /// 编码器维度
    Dim { dim: usize },
    /// 健康检查响应
    Pong,
    /// 错误
    Error { message: String },
}

/// 拥有所有权的编码器输出（用于 IPC 传输）
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EncoderOutputOwned {
    pub dense: Vec<f16>,
    pub sparse: HashMap<String, f32>,
}

/// 帧格式：[4 bytes: payload_len (u32 LE)][payload_len bytes: bincode payload]
pub const FRAME_HEADER_SIZE: usize = 4;

/// 序列化请求
pub fn serialize_request(request: &EncodeRequest) -> Result<Vec<u8>, bincode::Error> {
    let payload = bincode::serialize(request)?;
    let mut frame = Vec::with_capacity(FRAME_HEADER_SIZE + payload.len());
    frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
    frame.extend_from_slice(&payload);
    Ok(frame)
}

/// 反序列化请求
pub fn deserialize_request(data: &[u8]) -> Result<EncodeRequest, bincode::Error> {
    bincode::deserialize(data)
}

/// 序列化响应
pub fn serialize_response(response: &EncodeResponse) -> Result<Vec<u8>, bincode::Error> {
    let payload = bincode::serialize(response)?;
    let mut frame = Vec::with_capacity(FRAME_HEADER_SIZE + payload.len());
    frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
    frame.extend_from_slice(&payload);
    Ok(frame)
}

/// 反序列化响应
pub fn deserialize_response(data: &[u8]) -> Result<EncodeResponse, bincode::Error> {
    bincode::deserialize(data)
}
