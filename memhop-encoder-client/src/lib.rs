//! memhop-encoder-client — IPC 客户端库 (v0.23.0)
//!
//! 实现 memhop_core::Encoder trait，通过 Unix Socket 与 memhop-encoder 服务通信。
//! 内部使用独立的 tokio runtime 桥接异步 IPC 调用为同步接口。

pub mod protocol;

use memhop_core::encoder::{Encoder, EncoderOutput};
use protocol::{
    EncodeRequest, EncodeResponse, FRAME_HEADER_SIZE, deserialize_response,
    serialize_request,
};
use std::collections::HashMap;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::UnixStream;
use tokio::sync::Mutex;

/// IPC 编码器客户端
///
/// 通过 Unix Socket 与 memhop-encoder 服务通信。
/// 内部持有独立的 tokio runtime，将异步 IPC 调用桥接为同步接口。
pub struct EncoderClient {
    /// 独立的 tokio runtime（避免与外部 runtime 冲突）
    runtime: tokio::runtime::Runtime,
    /// Unix Socket 连接
    stream: Mutex<UnixStream>,
    /// 编码器输出维度
    dim: usize,
}

impl EncoderClient {
    /// 连接到 memhop-encoder 服务
    ///
    /// # Arguments
    /// * `socket_path` - Unix Socket 路径
    ///
    /// # Returns
    /// * `Ok(EncoderClient)` - 连接成功
    /// * `Err(...)` - 连接失败
    pub fn connect(socket_path: &str) -> Result<Self, Box<dyn std::error::Error>> {
        // 创建独立的 tokio runtime
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?;

        // 连接到 encoder 服务
        let mut stream = runtime.block_on(UnixStream::connect(socket_path))?;

        // 握手：获取 dim
        let dim = runtime.block_on(Self::request_dim(&mut stream))?;

        Ok(Self {
            runtime,
            stream: Mutex::new(stream),
            dim,
        })
    }

    /// 请求编码器维度
    async fn request_dim(stream: &mut UnixStream) -> Result<usize, Box<dyn std::error::Error>> {
        // 发送 Dim 请求
        let request = EncodeRequest::Dim;
        let frame = serialize_request(&request)?;
        stream.write_all(&frame).await?;

        // 读取响应
        let response = Self::read_response(stream).await?;
        match response {
            EncodeResponse::Dim { dim } => Ok(dim),
            EncodeResponse::Error { message } => {
                Err(format!("Encoder error: {}", message).into())
            }
            _ => Err("Unexpected response type for Dim request".into()),
        }
    }

    /// 读取完整响应帧
    async fn read_response(
        stream: &mut UnixStream,
    ) -> Result<EncodeResponse, Box<dyn std::error::Error>> {
        // 读取帧头（4 字节长度）
        let mut header = [0u8; FRAME_HEADER_SIZE];
        stream.read_exact(&mut header).await?;
        let payload_len = u32::from_le_bytes(header) as usize;

        // 读取 payload
        let mut payload = vec![0u8; payload_len];
        stream.read_exact(&mut payload).await?;

        // 反序列化响应
        let response = deserialize_response(&payload)?;
        Ok(response)
    }

    /// 发送请求并接收响应（同步接口）
    fn send_request(
        &self,
        request: EncodeRequest,
    ) -> Result<EncodeResponse, Box<dyn std::error::Error>> {
        self.runtime.block_on(async {
            let mut stream = self.stream.lock().await;

            // 发送请求
            let frame = serialize_request(&request)?;
            stream.write_all(&frame).await?;

            // 读取响应
            let response = Self::read_response(&mut stream).await?;
            Ok(response)
        })
    }
}

impl Encoder for EncoderClient {
    fn encode(&self, text: &str) -> EncoderOutput {
        let request = EncodeRequest::Single {
            text: text.to_string(),
        };

        match self.send_request(request) {
            Ok(EncodeResponse::Single { dense, sparse }) => EncoderOutput { dense, sparse },
            Ok(EncodeResponse::Error { message }) => {
                eprintln!("[EncoderClient] encode error: {}", message);
                // 返回空向量作为 fallback
                EncoderOutput {
                    dense: Vec::new(),
                    sparse: HashMap::new(),
                }
            }
            Ok(_) => {
                eprintln!("[EncoderClient] unexpected response type");
                EncoderOutput {
                    dense: Vec::new(),
                    sparse: HashMap::new(),
                }
            }
            Err(e) => {
                eprintln!("[EncoderClient] IPC error: {}", e);
                EncoderOutput {
                    dense: Vec::new(),
                    sparse: HashMap::new(),
                }
            }
        }
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn mode(&self) -> &str {
        "ipc"
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_protocol_serialize_deserialize() {
        let request = EncodeRequest::Single {
            text: "Hello, world!".to_string(),
        };
        let frame = serialize_request(&request).unwrap();
        assert!(frame.len() > FRAME_HEADER_SIZE);

        // 验证帧头
        let payload_len = u32::from_le_bytes([frame[0], frame[1], frame[2], frame[3]]) as usize;
        assert_eq!(payload_len, frame.len() - FRAME_HEADER_SIZE);
    }

    #[test]
    fn test_protocol_batch_request() {
        let request = EncodeRequest::Batch {
            texts: vec!["Hello".to_string(), "World".to_string()],
        };
        let frame = serialize_request(&request).unwrap();
        assert!(frame.len() > FRAME_HEADER_SIZE);
    }

    #[test]
    fn test_protocol_dim_request() {
        let request = EncodeRequest::Dim;
        let frame = serialize_request(&request).unwrap();
        // bincode 序列化枚举变体使用 4 字节
        assert_eq!(frame.len(), FRAME_HEADER_SIZE + 4);
    }

    #[test]
    fn test_protocol_ping_request() {
        let request = EncodeRequest::Ping;
        let frame = serialize_request(&request).unwrap();
        // bincode 序列化枚举变体使用 4 字节
        assert_eq!(frame.len(), FRAME_HEADER_SIZE + 4);
    }
}
