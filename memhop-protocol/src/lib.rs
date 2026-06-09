//! 共享 IPC 协议定义 (v0.23.0)
//!
//! 定义 memhop-encoder 和 memhop-encoder-client 之间的通信协议。
//! 使用 Unix Domain Socket + bincode 二进制协议。
//!
//! 该 crate 为 `memhop-encoder` 和 `memhop-encoder-client` 提供共享的
//! 协议类型和序列化/反序列化函数，确保两边使用完全一致的定义。

use half::f16;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// 编码请求
///
/// 客户端发送给编码器服务的请求枚举。
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
///
/// 编码器服务返回给客户端的响应枚举。
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
///
/// 将 `EncodeRequest` 序列化为带帧头的字节流。
/// 帧格式：[4 bytes: payload_len (u32 LE)][payload_len bytes: bincode payload]
pub fn serialize_request(request: &EncodeRequest) -> Result<Vec<u8>, bincode::Error> {
    let payload = bincode::serialize(request)?;
    let mut frame = Vec::with_capacity(FRAME_HEADER_SIZE + payload.len());
    frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
    frame.extend_from_slice(&payload);
    Ok(frame)
}

/// 反序列化请求
///
/// 从字节切片反序列化 `EncodeRequest`（不含帧头）。
pub fn deserialize_request(data: &[u8]) -> Result<EncodeRequest, bincode::Error> {
    bincode::deserialize(data)
}

/// 序列化响应
///
/// 将 `EncodeResponse` 序列化为带帧头的字节流。
/// 帧格式：[4 bytes: payload_len (u32 LE)][payload_len bytes: bincode payload]
pub fn serialize_response(response: &EncodeResponse) -> Result<Vec<u8>, bincode::Error> {
    let payload = bincode::serialize(response)?;
    let mut frame = Vec::with_capacity(FRAME_HEADER_SIZE + payload.len());
    frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
    frame.extend_from_slice(&payload);
    Ok(frame)
}

/// 反序列化响应
///
/// 从字节切片反序列化 `EncodeResponse`（不含帧头）。
pub fn deserialize_response(data: &[u8]) -> Result<EncodeResponse, bincode::Error> {
    bincode::deserialize(data)
}

// ── 跨平台传输层 ────────────────────────────────────────────────
//
// 统一的 IoStream / IoListener 抽象，同时支持 Unix Domain Socket 和 TCP。
// 使得 encoder 在 macOS/Linux/Windows 上均可工作。

use std::io;
use std::pin::Pin;
use std::task::{Context, Poll};
use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};

/// 统一的 IO 流 — 支持 Unix Socket 和 TCP。
///
/// 消除 `UnixStream` 与 `TcpStream` 的类型差异，
/// 使 IPC 层可以透明地在两种传输上工作。
#[derive(Debug)]
pub enum IoStream {
    /// Unix Domain Socket（macOS/Linux）
    #[cfg(unix)]
    Unix(tokio::net::UnixStream),
    /// TCP localhost（跨平台，尤其是 Windows）
    Tcp(tokio::net::TcpStream),
}

impl IoStream {
    /// 连接到编码器服务。
    ///
    /// 根据地址格式自动选择传输方式：
    /// - `unix:///path` → Unix Socket
    /// - `tcp://host:port` → TCP
    /// - 裸路径 → Unix（Unix 系统）或 TCP 127.0.0.1:9876（Windows）
    pub async fn connect(addr: &str) -> io::Result<Self> {
        if let Some(path) = addr.strip_prefix("unix://") {
            // 显式指定 Unix Socket
            Self::connect_unix(path).await
        } else if let Some(addr_str) = addr.strip_prefix("tcp://") {
            // 显式指定 TCP
            Self::connect_tcp(addr_str).await
        } else if addr.contains(':') && !cfg!(unix) {
            // 非 Unix 系统上，冒号视为 TCP host:port
            Self::connect_tcp(addr).await
        } else {
            // 默认：Unix 系统上视为路径，否则尝试 TCP
            #[cfg(unix)]
            {
                Self::connect_unix(addr).await
            }
            #[cfg(not(unix))]
            {
                Self::connect_tcp("127.0.0.1:9876").await
            }
        }
    }

    #[cfg(unix)]
    async fn connect_unix(path: &str) -> io::Result<Self> {
        let stream = tokio::net::UnixStream::connect(path).await?;
        Ok(IoStream::Unix(stream))
    }

    async fn connect_tcp(addr: &str) -> io::Result<Self> {
        let stream = tokio::net::TcpStream::connect(addr).await?;
        Ok(IoStream::Tcp(stream))
    }
}

impl AsyncRead for IoStream {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        match self.get_mut() {
            #[cfg(unix)]
            IoStream::Unix(s) => Pin::new(s).poll_read(cx, buf),
            IoStream::Tcp(s) => Pin::new(s).poll_read(cx, buf),
        }
    }
}

impl AsyncWrite for IoStream {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        match self.get_mut() {
            #[cfg(unix)]
            IoStream::Unix(s) => Pin::new(s).poll_write(cx, buf),
            IoStream::Tcp(s) => Pin::new(s).poll_write(cx, buf),
        }
    }

    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        match self.get_mut() {
            #[cfg(unix)]
            IoStream::Unix(s) => Pin::new(s).poll_flush(cx),
            IoStream::Tcp(s) => Pin::new(s).poll_flush(cx),
        }
    }

    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        match self.get_mut() {
            #[cfg(unix)]
            IoStream::Unix(s) => Pin::new(s).poll_shutdown(cx),
            IoStream::Tcp(s) => Pin::new(s).poll_shutdown(cx),
        }
    }
}

/// 统一的 IO 监听器 — 支持 Unix Socket 和 TCP。
///
/// 与服务端的 `IoStream` 对应，统一 accept 接口。
#[derive(Debug)]
pub enum IoListener {
    /// Unix Domain Socket 监听器（macOS/Linux）
    #[cfg(unix)]
    Unix(tokio::net::UnixListener),
    /// TCP 监听器（跨平台）
    Tcp(tokio::net::TcpListener),
}

impl IoListener {
    /// 绑定到指定地址。
    ///
    /// 地址格式同 `IoStream::connect()`。
    /// 对于 Unix Socket，自动删除已存在的 socket 文件。
    pub async fn bind(addr: &str) -> io::Result<Self> {
        if let Some(path) = addr.strip_prefix("unix://") {
            Self::bind_unix(path).await
        } else if let Some(addr_str) = addr.strip_prefix("tcp://") {
            Self::bind_tcp(addr_str).await
        } else if addr.contains(':') && !cfg!(unix) {
            Self::bind_tcp(addr).await
        } else {
            #[cfg(unix)]
            {
                Self::bind_unix(addr).await
            }
            #[cfg(not(unix))]
            {
                Self::bind_tcp("127.0.0.1:9876").await
            }
        }
    }

    #[cfg(unix)]
    async fn bind_unix(path: &str) -> io::Result<Self> {
        let _ = tokio::fs::remove_file(path).await;
        let listener = tokio::net::UnixListener::bind(path)?;
        Ok(IoListener::Unix(listener))
    }

    async fn bind_tcp(addr: &str) -> io::Result<Self> {
        let listener = tokio::net::TcpListener::bind(addr).await?;
        Ok(IoListener::Tcp(listener))
    }

    /// 接受一个新连接。
    pub async fn accept(&self) -> io::Result<IoStream> {
        match self {
            #[cfg(unix)]
            IoListener::Unix(listener) => {
                let (stream, _) = listener.accept().await?;
                Ok(IoStream::Unix(stream))
            }
            IoListener::Tcp(listener) => {
                let (stream, _) = listener.accept().await?;
                Ok(IoStream::Tcp(stream))
            }
        }
    }

    /// 返回绑定的本地地址。
    pub fn local_addr(&self) -> io::Result<String> {
        match self {
            #[cfg(unix)]
            IoListener::Unix(listener) => {
                listener.local_addr().map(|a| format!("{:?}", a.as_pathname()))
            }
            IoListener::Tcp(listener) => {
                listener.local_addr().map(|a| a.to_string())
            }
        }
    }
}
