//! Shared test utilities for memhop-core integration tests.
//!
//! Provides reusable fixtures and helpers to reduce code duplication across
//! test files.

#![allow(dead_code)]

// ============================================================
// IPC Protocol Constants
// ============================================================

/// 帧头大小（4 字节小端 u32，表示 payload 长度）
pub const FRAME_HEADER_SIZE: usize = 4;

/// 最大 IPC payload 大小（64 MB），防止恶意 daemon 导致内存耗尽
pub const MAX_PAYLOAD: usize = 64 * 1024 * 1024;

// ============================================================
// DaemonFixture — 管理 daemon 进程生命周期
// ============================================================

use std::io::{Read, Write};
use std::os::unix::net::UnixStream;
use std::path::PathBuf;
use std::process::{Child, Command};

use serde::de::DeserializeOwned;
use serde::Serialize;

/// meowagent daemon 夹具。
///
/// 管理 daemon 进程的启动/停止，提供 IPC 通信能力。
pub struct DaemonFixture {
    /// daemon 子进程（None = 外部 daemon 或无 daemon）
    pub daemon: Option<Child>,
    /// Unix Domain Socket 路径
    pub socket_path: PathBuf,
    /// daemon 二进制文件路径
    pub daemon_binary: PathBuf,
}

impl DaemonFixture {
    /// 创建 DaemonFixture，使用默认 socket 路径。
    pub fn new() -> Self {
        let socket_path = PathBuf::from("/tmp/meowagent-test.sock");
        let daemon_binary = Self::find_daemon_binary();
        Self {
            daemon: None,
            socket_path,
            daemon_binary,
        }
    }

    /// 创建 DaemonFixture 并指定 socket 路径。
    pub fn with_socket(socket_path: PathBuf) -> Self {
        let daemon_binary = Self::find_daemon_binary();
        Self {
            daemon: None,
            socket_path,
            daemon_binary,
        }
    }

    /// 查找 meowagent daemon 二进制文件。
    pub fn find_daemon_binary() -> PathBuf {
        let candidates = [
            "./meowagent",
            "./target/release/meowagent",
            "./target/debug/meowagent",
            "../meowagent/target/release/meowagent",
            "../meowagent/target/debug/meowagent",
        ];
        for path in &candidates {
            let p = PathBuf::from(path);
            if p.exists() {
                return p;
            }
        }
        // 默认值，实际运行时会因找不到而报错
        PathBuf::from("meowagent")
    }

    /// 启动 daemon 并等待就绪。
    ///
    /// 返回 Ok(()) 表示 daemon 已启动。
    /// 调用者应使用 `wait_for_ready` 等待 daemon 通过 HealthCheck。
    pub fn start(&mut self) -> Result<(), String> {
        if !self.daemon_binary.exists() {
            return Err(format!(
                "meowagent binary not found at {:?}. Build it first or start daemon manually.",
                self.daemon_binary
            ));
        }

        // 清理旧的 socket 文件
        let _ = std::fs::remove_file(&self.socket_path);

        let child = Command::new(&self.daemon_binary)
            .arg("daemon")
            .arg("start")
            .arg("--socket")
            .arg(self.socket_path.to_str().unwrap())
            .arg("--test")
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .spawn()
            .map_err(|e| format!("Failed to spawn meowagent daemon: {e}"))?;

        self.daemon = Some(child);
        Ok(())
    }

    /// 发送 IPC 消息并接收响应。
    ///
    /// 泛型版本，适用于任意 Serde 序列化/反序列化消息类型。
    pub fn send_recv<Req, Res>(&self, request: &Req) -> Result<Res, String>
    where
        Req: Serialize,
        Res: DeserializeOwned,
    {
        let mut stream = UnixStream::connect(&self.socket_path)
            .map_err(|e| format!("connect to {:?}: {e}", self.socket_path))?;

        stream
            .set_read_timeout(Some(std::time::Duration::from_secs(10)))
            .map_err(|e| format!("set_read_timeout: {e}"))?;

        // 序列化并发送请求
        let payload = bincode::serialize(request)
            .map_err(|e| format!("serialize request: {e}"))?;
        let mut frame = Vec::with_capacity(FRAME_HEADER_SIZE + payload.len());
        frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
        frame.extend_from_slice(&payload);
        stream
            .write_all(&frame)
            .map_err(|e| format!("write request: {e}"))?;

        // 读取响应帧头
        let mut header = [0u8; FRAME_HEADER_SIZE];
        stream
            .read_exact(&mut header)
            .map_err(|e| format!("read response header: {e}"))?;
        let payload_len = u32::from_le_bytes(header) as usize;

        if payload_len > MAX_PAYLOAD {
            return Err(format!(
                "response payload too large: {payload_len} bytes (max {MAX_PAYLOAD})"
            ));
        }

        // 读取响应 payload
        let mut resp_payload = vec![0u8; payload_len];
        stream
            .read_exact(&mut resp_payload)
            .map_err(|e| format!("read response payload ({payload_len} bytes): {e}"))?;

        bincode::deserialize(&resp_payload)
            .map_err(|e| format!("deserialize response: {e}"))
    }

    /// 停止 daemon。
    pub fn stop(&mut self) {
        if let Some(mut child) = self.daemon.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
        let _ = std::fs::remove_file(&self.socket_path);
    }

    /// 获取 socket 路径。
    pub fn socket_path(&self) -> &PathBuf {
        &self.socket_path
    }

    /// 检查 daemon 二进制是否存在。
    pub fn binary_exists(&self) -> bool {
        self.daemon_binary.exists()
    }
}

impl Drop for DaemonFixture {
    fn drop(&mut self) {
        self.stop();
    }
}
