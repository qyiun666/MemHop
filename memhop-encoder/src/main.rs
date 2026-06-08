//! memhop-encoder — 独立编码器服务 (v0.23.0)
//!
//! 加载编码器模型，监听 Unix Socket，处理编码请求。
//! 支持 NgramEncoder（零模型依赖）。

use memhop_core::encoder::{Encoder, NgramEncoder};
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::UnixListener;

/// IPC 协议定义（与 memhop-encoder-client 共享）
mod protocol {
    use half::f16;
    use serde::{Deserialize, Serialize};
    use std::collections::HashMap;

    #[derive(Debug, Serialize, Deserialize)]
    pub enum EncodeRequest {
        Single { text: String },
        Batch { texts: Vec<String> },
        Dim,
        Ping,
    }

    #[derive(Debug, Serialize, Deserialize)]
    pub enum EncodeResponse {
        Single {
            dense: Vec<f16>,
            sparse: HashMap<String, f32>,
        },
        Batch {
            outputs: Vec<EncoderOutputOwned>,
        },
        Dim {
            dim: usize,
        },
        Pong,
        Error {
            message: String,
        },
    }

    #[derive(Debug, Clone, Serialize, Deserialize)]
    pub struct EncoderOutputOwned {
        pub dense: Vec<f16>,
        pub sparse: HashMap<String, f32>,
    }

    pub const FRAME_HEADER_SIZE: usize = 4;

    pub fn deserialize_request(data: &[u8]) -> Result<EncodeRequest, bincode::Error> {
        bincode::deserialize(data)
    }

    pub fn serialize_response(response: &EncodeResponse) -> Result<Vec<u8>, bincode::Error> {
        let payload = bincode::serialize(response)?;
        let mut frame = Vec::with_capacity(FRAME_HEADER_SIZE + payload.len());
        frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
        frame.extend_from_slice(&payload);
        Ok(frame)
    }
}

use protocol::{EncodeRequest, EncodeResponse, EncoderOutputOwned, FRAME_HEADER_SIZE};

/// 处理单个客户端连接
async fn handle_client(mut stream: tokio::net::UnixStream, encoder: Arc<Box<dyn Encoder>>) {
    loop {
        // 读取帧头
        let mut header = [0u8; FRAME_HEADER_SIZE];
        match stream.read_exact(&mut header).await {
            Ok(_) => {}
            Err(e) => {
                if e.kind() == std::io::ErrorKind::UnexpectedEof {
                    // 客户端断开连接
                    break;
                }
                eprintln!("[memhop-encoder] read header error: {}", e);
                break;
            }
        }

        let payload_len = u32::from_le_bytes(header) as usize;

        // 读取 payload
        let mut payload = vec![0u8; payload_len];
        if let Err(e) = stream.read_exact(&mut payload).await {
            eprintln!("[memhop-encoder] read payload error: {}", e);
            break;
        }

        // 反序列化请求
        let request = match protocol::deserialize_request(&payload) {
            Ok(req) => req,
            Err(e) => {
                eprintln!("[memhop-encoder] deserialize error: {}", e);
                let response = EncodeResponse::Error {
                    message: format!("Deserialize error: {}", e),
                };
                if let Ok(frame) = protocol::serialize_response(&response) {
                    let _ = stream.write_all(&frame).await;
                }
                continue;
            }
        };

        // 处理请求
        let response = match request {
            EncodeRequest::Single { text } => {
                let output = encoder.encode(&text);
                EncodeResponse::Single {
                    dense: output.dense,
                    sparse: output.sparse,
                }
            }
            EncodeRequest::Batch { texts } => {
                let outputs: Vec<EncoderOutputOwned> = texts
                    .iter()
                    .map(|text| {
                        let output = encoder.encode(text);
                        EncoderOutputOwned {
                            dense: output.dense,
                            sparse: output.sparse,
                        }
                    })
                    .collect();
                EncodeResponse::Batch { outputs }
            }
            EncodeRequest::Dim => EncodeResponse::Dim { dim: encoder.dim() },
            EncodeRequest::Ping => EncodeResponse::Pong,
        };

        // 发送响应
        match protocol::serialize_response(&response) {
            Ok(frame) => {
                if let Err(e) = stream.write_all(&frame).await {
                    eprintln!("[memhop-encoder] write response error: {}", e);
                    break;
                }
            }
            Err(e) => {
                eprintln!("[memhop-encoder] serialize response error: {}", e);
                let error_response = EncodeResponse::Error {
                    message: format!("Serialize error: {}", e),
                };
                if let Ok(frame) = protocol::serialize_response(&error_response) {
                    let _ = stream.write_all(&frame).await;
                }
            }
        }
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // 解析命令行参数
    let args: Vec<String> = std::env::args().collect();
    let socket_path = args.get(1).map(|s| s.as_str()).unwrap_or("/tmp/memhop-encoder.sock");

    println!("[memhop-encoder] Starting encoder service...");
    println!("[memhop-encoder] Socket path: {}", socket_path);

    // 创建编码器（默认使用 NgramEncoder）
    let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(NgramEncoder::new(1024)));
    println!("[memhop-encoder] Encoder: NgramEncoder, dim={}", encoder.dim());

    // 删除旧的 socket 文件
    let _ = std::fs::remove_file(socket_path);

    // 监听 Unix Socket
    let listener = UnixListener::bind(socket_path)?;
    println!("[memhop-encoder] Listening on {}", socket_path);

    // 接受连接
    loop {
        match listener.accept().await {
            Ok((stream, _addr)) => {
                println!("[memhop-encoder] New client connected");
                let enc = Arc::clone(&encoder);
                tokio::spawn(async move {
                    handle_client(stream, enc).await;
                });
            }
            Err(e) => {
                eprintln!("[memhop-encoder] Accept error: {}", e);
            }
        }
    }
}
