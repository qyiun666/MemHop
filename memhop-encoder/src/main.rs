//! memhop-encoder — 独立编码器服务 (v0.25.0)
//!
//! 加载编码器模型，监听 Unix Socket 或 TCP，处理编码请求。
//! 支持 NgramEncoder（零模型依赖）和 CandleEncoder（语义向量模型）。

use clap::Parser;
use memhop_core::encoder::{Encoder, NgramEncoder};
#[cfg(feature = "candle")]
use memhop_core::encoder::{CandleEncoder, EncoderRouter};
use memhop_protocol::{
    deserialize_request, serialize_response, EncodeRequest, EncodeResponse,
    EncoderOutputOwned, FRAME_HEADER_SIZE, IoListener, IoStream,
};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

/// memhop-encoder CLI 参数
#[derive(Parser)]
#[command(version, about = "MemHop standalone encoder service")]
struct Args {
    /// Unix socket 路径
    #[arg(long, default_value = "/tmp/memhop-encoder.sock")]
    socket: String,

    /// NgramEncoder 维度（无 --model-path 时使用）
    #[arg(long, default_value_t = 1024)]
    dim: usize,

    /// CandleEncoder 模型路径（可选，启用 CandleEncoder + EncoderRouter 双通道）
    #[arg(long)]
    model_path: Option<String>,
}

/// 创建编码器实例
///
/// - 无 `--model-path`：使用 `NgramEncoder::new(dim)`
/// - 有 `--model-path`（candle feature 启用）：加载 `CandleEncoder` + `EncoderRouter`
/// - 有 `--model-path`（candle feature 未启用）：警告，回退到 `NgramEncoder`
fn create_encoder(args: &Args) -> Arc<Box<dyn Encoder>> {
    if let Some(model_path) = &args.model_path {
        #[cfg(feature = "candle")]
        {
            match CandleEncoder::new(model_path) {
                Ok(dense_encoder) => {
                    let dim = dense_encoder.dim();
                    println!(
                        "[memhop-encoder] Loaded CandleEncoder from: {} (dim={})",
                        model_path, dim
                    );
                    let sparse_encoder = Box::new(NgramEncoder::new(dim));
                    let router = EncoderRouter::new(sparse_encoder, Box::new(dense_encoder));
                    return Arc::new(Box::new(router));
                }
                Err(e) => {
                    eprintln!(
                        "[memhop-encoder] Warning: Failed to load CandleEncoder from '{}': {}",
                        model_path, e
                    );
                }
            }
        }

        #[cfg(not(feature = "candle"))]
        {
            eprintln!(
                "[memhop-encoder] Warning: --model-path '{}' provided but candle feature not enabled",
                model_path
            );
        }

        eprintln!(
            "[memhop-encoder] Falling back to NgramEncoder (dim={})",
            args.dim
        );
    }

    Arc::new(Box::new(NgramEncoder::new(args.dim)))
}

/// 处理单个客户端连接
async fn handle_client(mut stream: IoStream, encoder: Arc<Box<dyn Encoder>>) {
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
        let request = match deserialize_request(&payload) {
            Ok(req) => req,
            Err(e) => {
                eprintln!("[memhop-encoder] deserialize error: {}", e);
                let response = EncodeResponse::Error {
                    message: format!("Deserialize error: {}", e),
                };
                if let Ok(frame) = serialize_response(&response) {
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
        match serialize_response(&response) {
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
                if let Ok(frame) = serialize_response(&error_response) {
                    let _ = stream.write_all(&frame).await;
                }
            }
        }
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = Args::parse();

    println!("[memhop-encoder] Starting encoder service...");
    println!("[memhop-encoder] Socket path: {}", args.socket);

    // 创建编码器
    let encoder = create_encoder(&args);
    println!(
        "[memhop-encoder] Encoder: {}, dim={}",
        encoder.mode(),
        encoder.dim()
    );

    // 监听 Unix Socket（IoListener 自动处理已存在的 socket 文件）
    let listener = IoListener::bind(&args.socket).await?;
    println!("[memhop-encoder] Listening on {}", args.socket);

    // 初始化信号处理器（SIGTERM/SIGINT on Unix, Ctrl+C on Windows）
    #[cfg(unix)]
    let mut sigterm = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
    #[cfg(unix)]
    let mut sigint = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::interrupt())?;
    #[cfg(not(unix))]
    let mut ctrl_c = tokio::signal::ctrl_c();

    // 构建 shutdown future（平台无关）
    let shutdown = async move {
        #[cfg(unix)]
        {
            tokio::select! {
                _ = sigterm.recv() => {}
                _ = sigint.recv() => {}
            }
        }
        #[cfg(not(unix))]
        {
            ctrl_c.await.ok();
        }
    };
    tokio::pin!(shutdown);

    // 跟踪活跃连接数，用于安全关闭
    let active_connections = Arc::new(AtomicUsize::new(0));

    println!("[memhop-encoder] Ready to accept connections");

    // 接受连接循环（带优雅关闭）
    loop {
        tokio::select! {
            accept_result = listener.accept() => {
                match accept_result {
                    Ok(stream) => {
                        println!("[memhop-encoder] New client connected");
                        active_connections.fetch_add(1, Ordering::SeqCst);
                        let enc = Arc::clone(&encoder);
                        let active = Arc::clone(&active_connections);
                        tokio::spawn(async move {
                            handle_client(stream, enc).await;
                            active.fetch_sub(1, Ordering::SeqCst);
                        });
                    }
                    Err(e) => {
                        eprintln!("[memhop-encoder] Accept error: {}", e);
                    }
                }
            }
            _ = &mut shutdown => {
                println!("[memhop-encoder] Received shutdown signal, shutting down gracefully...");
                break;
            }
        }
    }

    // 等待所有活跃连接完成
    let mut remaining = active_connections.load(Ordering::SeqCst);
    while remaining > 0 {
        println!(
            "[memhop-encoder] Waiting for {} active connection(s) to complete...",
            remaining
        );
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        remaining = active_connections.load(Ordering::SeqCst);
    }

    // IoListener 在 drop 时不做 socket 文件清理，但 Unix 系统上
    // socket 文件可以保留（下一轮启动时 IoListener::bind 会自动删除）
    println!("[memhop-encoder] Shutdown complete.");

    Ok(())
}
