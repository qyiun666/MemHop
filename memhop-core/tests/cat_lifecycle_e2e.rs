//! 猫生命周期端到端测试 — 完整的 meowagent cat 管理 E2E 测试 v0.24.0。
//!
//! 测试覆盖：启动 daemon → ListCats → RequestCreateCat → BindCat
//! → GetCatConfig → UpdateCatConfig → SwitchCat → Chat。
//!
//! 所有测试默认 `#[ignore]`，因为需要运行中的 meowagent daemon 环境。
//!
//! ## 运行方式
//!
//! ```bash
//! # 1. 启动 meowagent daemon（测试模式）
//! meowagent daemon start --socket /tmp/meowagent-test.sock
//!
//! # 2. 运行测试
//! cargo test --test cat_lifecycle_e2e -- --ignored
//! ```

use serde::{Deserialize, Serialize};
use std::io::{Read, Write};
use std::os::unix::net::UnixStream;
use std::path::PathBuf;
use std::process::{Child, Command};
use std::time::{Duration, Instant};

// ============================================================
// IPC 协议定义
// ============================================================

/// 帧头大小（4 字节小端 u32，表示 payload 长度）
const FRAME_HEADER_SIZE: usize = 4;

/// 最大 IPC payload 大小（64 MB），防止恶意 daemon 导致内存耗尽
const MAX_PAYLOAD: usize = 64 * 1024 * 1024;

/// meowagent daemon IPC 消息 — 请求/响应统一枚举。
#[derive(Debug, Clone, Serialize, Deserialize)]
enum IpcMessage {
    // ── 请求 ──
    /// 健康检查
    HealthCheck,
    /// 列出所有猫
    ListCats,
    /// 请求创建新猫
    RequestCreateCat {
        name: String,
        config: Option<CatConfig>,
    },
    /// 绑定猫到指定 agent_id
    BindCat {
        cat_id: String,
        agent_id: String,
    },
    /// 获取猫的配置
    GetCatConfig {
        cat_id: String,
    },
    /// 更新猫的配置
    UpdateCatConfig {
        cat_id: String,
        config: CatConfig,
    },
    /// 切换当前会话的猫
    SwitchCat {
        cat_id: String,
    },
    /// 向猫发送聊天消息
    Chat {
        cat_id: String,
        message: String,
    },

    // ── 响应 ──
    /// 健康检查响应
    HealthCheckResponse {
        status: String,
        uptime_ms: u64,
        version: String,
    },
    /// 猫列表响应
    ListCatsResponse {
        cats: Vec<CatInfo>,
    },
    /// 猫创建结果
    CatCreationResult {
        success: bool,
        cat: Option<CatInfo>,
        error: Option<String>,
    },
    /// 猫绑定结果
    BoundCatResult {
        success: bool,
        cat: Option<CatInfo>,
        error: Option<String>,
    },
    /// 猫配置快照
    CatConfigSnapshot {
        cat_id: String,
        config: CatConfig,
    },
    /// 配置更新确认
    ConfigUpdated {
        success: bool,
    },
    /// 切换结果
    SwitchResult {
        success: bool,
        message: String,
    },
    /// 聊天回复
    ChatResponse {
        reply: String,
    },
    /// 通用错误
    Error {
        message: String,
    },
}

// ============================================================
// 猫管理数据类型
// ============================================================

/// 猫的状态。
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
enum CatStatus {
    /// 未绑定 agent_id
    Unbound,
    /// 已激活
    Active,
    /// 未激活
    Inactive,
}

/// 猫的配置。
#[derive(Debug, Clone, Serialize, Deserialize)]
struct CatConfig {
    /// 系统提示词
    #[serde(default, skip_serializing_if = "Option::is_none")]
    system_prompt: Option<String>,
    /// 模型名称
    #[serde(default, skip_serializing_if = "Option::is_none")]
    model: Option<String>,
    /// 温度参数 (0.0-2.0)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    temperature: Option<f64>,
    /// 最大 token 数
    #[serde(default, skip_serializing_if = "Option::is_none")]
    max_tokens: Option<u32>,
    /// 是否启用记忆
    #[serde(default, skip_serializing_if = "Option::is_none")]
    memory_enabled: Option<bool>,
}

impl Default for CatConfig {
    fn default() -> Self {
        Self {
            system_prompt: Some("You are a helpful assistant.".to_string()),
            model: Some("gpt-4o-mini".to_string()),
            temperature: Some(0.7),
            max_tokens: Some(4096),
            memory_enabled: Some(true),
        }
    }
}

/// 猫的概要信息。
#[derive(Debug, Clone, Serialize, Deserialize)]
struct CatInfo {
    /// 猫的唯一标识
    id: String,
    /// 猫的名称
    name: String,
    /// 绑定的 agent_id
    #[serde(default, skip_serializing_if = "Option::is_none")]
    agent_id: Option<String>,
    /// 当前状态
    status: CatStatus,
    /// 创建时间 (Unix 毫秒)
    created_at: i64,
    /// 更新时间 (Unix 毫秒)
    updated_at: i64,
    /// 记忆统计
    memory_stats: CatMemoryStats,
}

/// 猫记忆统计。
#[derive(Debug, Clone, Serialize, Deserialize)]
struct CatMemoryStats {
    /// L2 话题数
    topics: u32,
    /// L5 晶体数
    crystals: u32,
    /// L1 节点数
    l1_nodes: u32,
    /// 会话数
    sessions: u32,
}

// ============================================================
// DaemonFixture — 管理 daemon 进程生命周期
// ============================================================

/// meowagent daemon 夹具。
///
/// 管理 daemon 进程的启动/停止，提供 IPC 通信能力。
/// 使用 `#[ignore]` 标注的测试依赖此夹具。
struct DaemonFixture {
    /// daemon 子进程（None = 外部 daemon 或无 daemon）
    daemon: Option<Child>,
    /// Unix Domain Socket 路径
    socket_path: PathBuf,
    /// daemon 二进制文件路径
    daemon_binary: PathBuf,
}

impl DaemonFixture {
    /// 创建 DaemonFixture，使用默认 socket 路径。
    fn new() -> Self {
        let socket_path = PathBuf::from("/tmp/meowagent-test.sock");
        let daemon_binary = Self::find_daemon_binary();
        Self {
            daemon: None,
            socket_path,
            daemon_binary,
        }
    }

    /// 创建 DaemonFixture 并指定 socket 路径。
    #[allow(dead_code)]
    fn with_socket(socket_path: PathBuf) -> Self {
        let daemon_binary = Self::find_daemon_binary();
        Self {
            daemon: None,
            socket_path,
            daemon_binary,
        }
    }

    /// 查找 meowagent daemon 二进制文件。
    fn find_daemon_binary() -> PathBuf {
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
    /// 返回 Ok(()) 表示 daemon 已启动并通过 HealthCheck。
    fn start(&mut self) -> Result<(), String> {
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

        // 等待 daemon 就绪
        self.wait_for_ready(Duration::from_secs(15))
    }

    /// 等待 daemon 通过 HealthCheck。
    fn wait_for_ready(&self, timeout: Duration) -> Result<(), String> {
        let deadline = Instant::now() + timeout;
        let mut last_err = String::from("no attempt");

        while Instant::now() < deadline {
            match self.send_recv(&IpcMessage::HealthCheck) {
                Ok(IpcMessage::HealthCheckResponse { status, .. }) => {
                    if status == "ok" {
                        return Ok(());
                    }
                    last_err = format!("status != ok: {status}");
                }
                Ok(other) => {
                    last_err = format!("unexpected response: {other:?}");
                }
                Err(e) => {
                    last_err = e;
                }
            }
            std::thread::sleep(Duration::from_millis(300));
        }

        Err(format!(
            "Daemon not ready within {timeout:?}: {last_err}"
        ))
    }

    /// 发送 IPC 消息并接收响应。
    fn send_recv(&self, request: &IpcMessage) -> Result<IpcMessage, String> {
        let mut stream = UnixStream::connect(&self.socket_path)
            .map_err(|e| format!("connect to {:?}: {e}", self.socket_path))?;

        stream
            .set_read_timeout(Some(Duration::from_secs(10)))
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
    fn stop(&mut self) {
        if let Some(mut child) = self.daemon.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
        let _ = std::fs::remove_file(&self.socket_path);
    }

    /// 获取 socket 路径。
    #[allow(dead_code)]
    fn socket_path(&self) -> &PathBuf {
        &self.socket_path
    }

    /// 检查 daemon 二进制是否存在。
    fn binary_exists(&self) -> bool {
        self.daemon_binary.exists()
    }
}

impl Drop for DaemonFixture {
    fn drop(&mut self) {
        self.stop();
    }
}

// ============================================================
// IPC 通信辅助函数 — 连接外部 daemon
// ============================================================

/// 默认 daemon socket 路径。
const DEFAULT_SOCKET_PATH: &str = "/tmp/meowagent.sock";

/// 连接到 daemon 的 Unix Socket。
fn connect() -> Result<UnixStream, String> {
    let stream = UnixStream::connect(DEFAULT_SOCKET_PATH)
        .map_err(|e| format!("connect to {DEFAULT_SOCKET_PATH}: {e}"))?;
    Ok(stream)
}

/// 发送 IPC 请求并接收响应（连接外部 daemon）。
fn send_request(request: &IpcMessage) -> Result<IpcMessage, String> {
    let mut stream = connect()?;
    stream
        .set_read_timeout(Some(Duration::from_secs(30)))
        .map_err(|e| format!("set_read_timeout: {e}"))?;

    // 序列化请求
    let payload = bincode::serialize(request)
        .map_err(|e| format!("serialize request: {e}"))?;
    let mut frame = Vec::with_capacity(FRAME_HEADER_SIZE + payload.len());
    frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
    frame.extend_from_slice(&payload);
    stream
        .write_all(&frame)
        .map_err(|e| format!("write request: {e}"))?;

    // 读取响应
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

    let mut resp_payload = vec![0u8; payload_len];
    stream
        .read_exact(&mut resp_payload)
        .map_err(|e| format!("read response payload ({payload_len} bytes): {e}"))?;

    bincode::deserialize(&resp_payload)
        .map_err(|e| format!("deserialize response: {e}"))
}

/// 等待 daemon 通过 HealthCheck（最多等待指定时长）。
fn wait_for_daemon_ready(timeout: Duration) -> Result<(), String> {
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        match send_request(&IpcMessage::HealthCheck) {
            Ok(IpcMessage::HealthCheckResponse { status, .. }) if status == "ok" => {
                return Ok(());
            }
            _ => {}
        }
        std::thread::sleep(Duration::from_millis(500));
    }
    Err(format!(
        "Daemon not ready within {timeout:?}. Is meowagent daemon running at {DEFAULT_SOCKET_PATH}?"
    ))
}

// ============================================================
// 测试辅助：创建测试用猫配置
// ============================================================

/// 创建测试用 CatConfig。
fn test_cat_config(name: &str) -> CatConfig {
    CatConfig {
        system_prompt: Some(format!(
            "You are {name}, a helpful AI assistant."
        )),
        model: Some("gpt-4o-mini".to_string()),
        temperature: Some(0.7),
        max_tokens: Some(4096),
        memory_enabled: Some(true),
    }
}

// ============================================================
// 测试用例
// ============================================================

/// 测试 1：Daemon 启动与 HealthCheck。
///
/// 启动 meowagent daemon，验证 HealthCheck 返回正常状态。
#[test]
#[ignore]
fn test_daemon_startup() {
    let mut fixture = DaemonFixture::new();

    // 检查二进制是否存在
    if !fixture.binary_exists() {
        eprintln!("WARN: meowagent binary not found, skipping test_daemon_startup");
        eprintln!("  Searched in: ./meowagent, ./target/release/, ./target/debug/");
        return;
    }

    // 启动 daemon
    fixture
        .start()
        .expect("Daemon should start and become ready");

    // 验证 HealthCheck 响应
    let response = fixture
        .send_recv(&IpcMessage::HealthCheck)
        .expect("HealthCheck request should succeed");

    match response {
        IpcMessage::HealthCheckResponse {
            status,
            uptime_ms,
            version,
        } => {
            assert_eq!(status, "ok", "Daemon status should be 'ok'");
            assert!(uptime_ms > 0, "Uptime should be positive, got {uptime_ms}");
            assert!(!version.is_empty(), "Version should not be empty");
            assert!(version.contains('.'), "Version should be semver-like");
        }
        other => panic!("Expected HealthCheckResponse, got {other:?}"),
    }

    // fixture drop 时会自动停止 daemon
}

/// 测试 2：新 daemon 无猫。
///
/// 验证 ListCats 在空 daemon 中返回空列表。
#[test]
#[ignore]
fn test_list_cats_empty() {
    // 等待外部 daemon 就绪
    wait_for_daemon_ready(Duration::from_secs(5))
        .expect("Daemon should be running");

    let response =
        send_request(&IpcMessage::ListCats).expect("ListCats should succeed");

    match response {
        IpcMessage::ListCatsResponse { cats } => {
            assert!(
                cats.is_empty(),
                "New daemon should have no cats, got {}",
                cats.len()
            );
        }
        other => panic!("Expected ListCatsResponse, got {other:?}"),
    }
}

/// 测试 3：完整猫生命周期。
///
/// 执行完整的 cat 生命周期：
/// 1. RequestCreateCat → 验证 success
/// 2. BindCat → 验证 success
/// 3. GetCatConfig → 验证字段完整
/// 4. UpdateCatConfig → 验证持久化
/// 5. SwitchCat → 验证 success
/// 6. ListCats → 验证猫列表中包含已创建的猫
#[test]
#[ignore]
fn test_cat_full_lifecycle() {
    let mut fixture = DaemonFixture::new();

    if !fixture.binary_exists() {
        eprintln!("WARN: meowagent binary not found, skipping lifecycle test");
        return;
    }

    fixture.start().expect("Daemon should start");

    // ── Step 1: RequestCreateCat ──
    let cat_name = "test_cat_e2e";
    let config = test_cat_config(cat_name);

    let create_response = fixture
        .send_recv(&IpcMessage::RequestCreateCat {
            name: cat_name.to_string(),
            config: Some(config),
        })
        .expect("RequestCreateCat should succeed");

    let created_cat = match create_response {
        IpcMessage::CatCreationResult {
            success: true,
            cat: Some(cat_info),
            error: None,
        } => {
            assert_eq!(cat_info.name, cat_name);
            assert_eq!(cat_info.status, CatStatus::Unbound);
            assert!(cat_info.created_at > 0);
            assert!(cat_info.updated_at > 0);
            cat_info
        }
        other => panic!("Expected successful CatCreationResult, got {other:?}"),
    };
    eprintln!(
        "  ✓ Cat created: id={}, name={}",
        created_cat.id, created_cat.name
    );

    // ── Step 2: BindCat ──
    let agent_id = format!("agent_{}", created_cat.id);
    let bind_response = fixture
        .send_recv(&IpcMessage::BindCat {
            cat_id: created_cat.id.clone(),
            agent_id: agent_id.clone(),
        })
        .expect("BindCat should succeed");

    match bind_response {
        IpcMessage::BoundCatResult {
            success: true,
            cat: Some(bound_cat),
            error: None,
        } => {
            assert_eq!(bound_cat.id, created_cat.id);
            assert_eq!(
                bound_cat.agent_id,
                Some(agent_id.clone()),
                "Cat should be bound to agent_id"
            );
        }
        other => panic!("Expected successful BoundCatResult, got {other:?}"),
    }
    eprintln!("  ✓ Cat bound to agent: {agent_id}");

    // ── Step 3: GetCatConfig ──
    let config_response = fixture
        .send_recv(&IpcMessage::GetCatConfig {
            cat_id: created_cat.id.clone(),
        })
        .expect("GetCatConfig should succeed");

    match config_response {
        IpcMessage::CatConfigSnapshot {
            cat_id,
            config: snapshot,
        } => {
            assert_eq!(cat_id, created_cat.id);
            assert!(
                snapshot.system_prompt.is_some(),
                "system_prompt should be present"
            );
            assert!(
                snapshot.temperature.is_some(),
                "temperature should be present"
            );
            assert!(
                snapshot.max_tokens.is_some(),
                "max_tokens should be present"
            );
            assert!(
                snapshot.memory_enabled.is_some(),
                "memory_enabled should be present"
            );
            // 验证温度值一致
            assert_eq!(
                snapshot.temperature,
                Some(0.7),
                "temperature should match"
            );
        }
        other => panic!("Expected CatConfigSnapshot, got {other:?}"),
    }
    eprintln!("  ✓ Cat config retrieved and validated");

    // ── Step 4: UpdateCatConfig ──
    let updated_config = CatConfig {
        system_prompt: Some("Updated system prompt for E2E test.".to_string()),
        model: Some("gpt-4o".to_string()),
        temperature: Some(0.5),
        max_tokens: Some(8192),
        memory_enabled: Some(true),
    };

    let update_response = fixture
        .send_recv(&IpcMessage::UpdateCatConfig {
            cat_id: created_cat.id.clone(),
            config: updated_config.clone(),
        })
        .expect("UpdateCatConfig should succeed");

    match update_response {
        IpcMessage::ConfigUpdated { success: true } => {}
        other => panic!("Expected successful ConfigUpdated, got {other:?}"),
    }
    eprintln!("  ✓ Cat config updated");

    // ── Step 4.5: 验证配置持久化 ──
    let verify_response = fixture
        .send_recv(&IpcMessage::GetCatConfig {
            cat_id: created_cat.id.clone(),
        })
        .expect("GetCatConfig after update should succeed");

    match verify_response {
        IpcMessage::CatConfigSnapshot {
            cat_id: _,
            config: persisted,
        } => {
            assert_eq!(
                persisted.system_prompt,
                updated_config.system_prompt,
                "system_prompt should persist"
            );
            assert_eq!(
                persisted.model, updated_config.model,
                "model should persist"
            );
            assert_eq!(
                persisted.temperature, updated_config.temperature,
                "temperature should persist"
            );
            assert_eq!(
                persisted.max_tokens, updated_config.max_tokens,
                "max_tokens should persist"
            );
        }
        other => panic!("Expected CatConfigSnapshot, got {other:?}"),
    }
    eprintln!("  ✓ Config persistence verified");

    // ── Step 5: SwitchCat ──
    let switch_response = fixture
        .send_recv(&IpcMessage::SwitchCat {
            cat_id: created_cat.id.clone(),
        })
        .expect("SwitchCat should succeed");

    match switch_response {
        IpcMessage::SwitchResult {
            success: true,
            message,
        } => {
            assert!(
                !message.is_empty(),
                "Switch message should not be empty"
            );
            assert!(
                message.contains(&created_cat.id) || message.contains(&created_cat.name),
                "Switch message should reference the cat"
            );
        }
        other => panic!("Expected successful SwitchResult, got {other:?}"),
    }
    eprintln!("  ✓ Cat switched");

    // ── Step 6: ListCats → 验证包含已创建 cat ──
    let list_response = fixture
        .send_recv(&IpcMessage::ListCats)
        .expect("ListCats should succeed");

    match list_response {
        IpcMessage::ListCatsResponse { cats } => {
            assert!(!cats.is_empty(), "Should have at least one cat");
            let found = cats.iter().any(|c| c.id == created_cat.id);
            assert!(found, "Created cat should be in the list");
        }
        other => panic!("Expected ListCatsResponse, got {other:?}"),
    }
    eprintln!("  ✓ Cat found in list");
}

/// 测试 4：创建猫并发送 Chat 请求。
///
/// 验证猫创建后能正常接收和回复 Chat 消息。
#[test]
#[ignore]
fn test_cat_chat() {
    let mut fixture = DaemonFixture::new();

    if !fixture.binary_exists() {
        eprintln!("WARN: meowagent binary not found, skipping chat test");
        return;
    }

    fixture.start().expect("Daemon should start");

    // 创建猫
    let cat_name = "chat_test_cat";
    let config = test_cat_config(cat_name);

    let create_resp = fixture
        .send_recv(&IpcMessage::RequestCreateCat {
            name: cat_name.to_string(),
            config: Some(config),
        })
        .expect("RequestCreateCat should succeed");

    let cat_id = match create_resp {
        IpcMessage::CatCreationResult {
            success: true,
            cat: Some(info),
            ..
        } => info.id,
        other => panic!("Expected CatCreationResult, got {other:?}"),
    };

    // 发送 Chat 消息
    let chat_response = fixture
        .send_recv(&IpcMessage::Chat {
            cat_id: cat_id.clone(),
            message: "Hello! What can you help me with?".to_string(),
        })
        .expect("Chat request should succeed");

    match chat_response {
        IpcMessage::ChatResponse { reply } => {
            assert!(!reply.is_empty(), "Chat reply should not be empty");
            eprintln!("  ✓ Chat reply received ({} chars)", reply.len());
        }
        IpcMessage::Error { message } => {
            // Chat 在没有 LLM 后端时可能报错，但至少 daemon 正确路由了消息
            eprintln!("  ℹ Chat returned error (expected if no LLM backend): {message}");
        }
        other => panic!("Expected ChatResponse or Error, got {other:?}"),
    }
}

/// 测试 5：GetCatConfig — 字段完整性验证。
///
/// 使用外部 daemon 连接方式，验证 CatConfigSnapshot 的字段完整。
#[test]
#[ignore]
fn test_get_cat_config() {
    wait_for_daemon_ready(Duration::from_secs(5))
        .expect("Daemon should be running");

    // 先创建一个猫
    let create_resp = send_request(&IpcMessage::RequestCreateCat {
        name: "config_test_cat".to_string(),
        config: Some(test_cat_config("config_test_cat")),
    })
    .expect("RequestCreateCat should succeed");

    let cat_id = match create_resp {
        IpcMessage::CatCreationResult {
            success: true,
            cat: Some(info),
            ..
        } => info.id,
        other => panic!("Expected CatCreationResult, got {other:?}"),
    };

    // 获取配置
    let config_resp = send_request(&IpcMessage::GetCatConfig {
        cat_id: cat_id.clone(),
    })
    .expect("GetCatConfig should succeed");

    match config_resp {
        IpcMessage::CatConfigSnapshot {
            cat_id: resp_cat_id,
            config,
        } => {
            assert_eq!(resp_cat_id, cat_id, "cat_id should match");
            assert!(
                config.system_prompt.is_some(),
                "CatConfigSnapshot should contain system_prompt"
            );
            assert!(
                config.model.is_some(),
                "CatConfigSnapshot should contain model"
            );
            assert!(
                config.temperature.is_some(),
                "CatConfigSnapshot should contain temperature"
            );
            assert!(
                config.max_tokens.is_some(),
                "CatConfigSnapshot should contain max_tokens"
            );
            assert!(
                config.memory_enabled.is_some(),
                "CatConfigSnapshot should contain memory_enabled"
            );
        }
        IpcMessage::Error { message } => {
            panic!("GetCatConfig returned error: {message}");
        }
        other => panic!("Expected CatConfigSnapshot, got {other:?}"),
    }
}

/// 测试 6：UpdateCatConfig — 验证配置持久化。
///
/// 使用外部 daemon 连接方式，更新配置后读取验证。
#[test]
#[ignore]
fn test_update_cat_config() {
    wait_for_daemon_ready(Duration::from_secs(5))
        .expect("Daemon should be running");

    // 创建一个猫
    let create_resp = send_request(&IpcMessage::RequestCreateCat {
        name: "update_config_test".to_string(),
        config: Some(test_cat_config("update_config_test")),
    })
    .expect("RequestCreateCat should succeed");

    let cat_id = match create_resp {
        IpcMessage::CatCreationResult {
            success: true,
            cat: Some(info),
            ..
        } => info.id,
        other => panic!("Expected CatCreationResult, got {other:?}"),
    };

    // 更新配置
    let new_config = CatConfig {
        system_prompt: Some("Persistent config test.".to_string()),
        model: Some("gpt-4o".to_string()),
        temperature: Some(0.3),
        max_tokens: Some(2048),
        memory_enabled: Some(false),
    };

    let update_resp = send_request(&IpcMessage::UpdateCatConfig {
        cat_id: cat_id.clone(),
        config: new_config.clone(),
    })
    .expect("UpdateCatConfig should succeed");

    match update_resp {
        IpcMessage::ConfigUpdated { success: true } => {}
        other => panic!("Expected ConfigUpdated(success=true), got {other:?}"),
    }

    // 读取验证
    let verify_resp = send_request(&IpcMessage::GetCatConfig {
        cat_id: cat_id.clone(),
    })
    .expect("GetCatConfig should succeed");

    match verify_resp {
        IpcMessage::CatConfigSnapshot {
            cat_id: _,
            config: persisted,
        } => {
            assert_eq!(
                persisted.system_prompt, new_config.system_prompt,
                "system_prompt should persist"
            );
            assert_eq!(
                persisted.model, new_config.model,
                "model should persist"
            );
            assert_eq!(
                persisted.temperature, new_config.temperature,
                "temperature should persist"
            );
            assert_eq!(
                persisted.max_tokens, new_config.max_tokens,
                "max_tokens should persist"
            );
            assert_eq!(
                persisted.memory_enabled, new_config.memory_enabled,
                "memory_enabled should persist"
            );
        }
        other => panic!("Expected CatConfigSnapshot, got {other:?}"),
    }
}

/// 测试 7：SwitchCat — 验证猫切换。
#[test]
#[ignore]
fn test_switch_cat() {
    wait_for_daemon_ready(Duration::from_secs(5))
        .expect("Daemon should be running");

    // 创建第一个猫
    let create_resp = send_request(&IpcMessage::RequestCreateCat {
        name: "switch_test_cat_1".to_string(),
        config: Some(test_cat_config("switch_test_cat_1")),
    })
    .expect("RequestCreateCat should succeed");

    let cat1_id = match create_resp {
        IpcMessage::CatCreationResult {
            success: true,
            cat: Some(info),
            ..
        } => info.id,
        other => panic!("Expected CatCreationResult, got {other:?}"),
    };

    // 创建第二个猫
    let create_resp2 = send_request(&IpcMessage::RequestCreateCat {
        name: "switch_test_cat_2".to_string(),
        config: Some(test_cat_config("switch_test_cat_2")),
    })
    .expect("RequestCreateCat should succeed");

    let cat2_id = match create_resp2 {
        IpcMessage::CatCreationResult {
            success: true,
            cat: Some(info),
            ..
        } => info.id,
        other => panic!("Expected CatCreationResult, got {other:?}"),
    };

    // 切换到 cat1
    let switch1 = send_request(&IpcMessage::SwitchCat {
        cat_id: cat1_id.clone(),
    })
    .expect("SwitchCat should succeed");

    match switch1 {
        IpcMessage::SwitchResult {
            success: true,
            message,
        } => {
            eprintln!("  Switch to cat1: {message}");
        }
        other => panic!("Expected successful SwitchResult, got {other:?}"),
    }

    // 切换到 cat2
    let switch2 = send_request(&IpcMessage::SwitchCat {
        cat_id: cat2_id.clone(),
    })
    .expect("SwitchCat should succeed");

    match switch2 {
        IpcMessage::SwitchResult {
            success: true,
            message,
        } => {
            eprintln!("  Switch to cat2: {message}");
        }
        other => panic!("Expected successful SwitchResult, got {other:?}"),
    }

    // 切换到不存在的猫 — 应该失败但不崩溃
    let switch_bad = send_request(&IpcMessage::SwitchCat {
        cat_id: "nonexistent_cat".to_string(),
    });

    if let Ok(IpcMessage::SwitchResult { success: false, .. }) = switch_bad {
        eprintln!("  ✓ Non-existent cat correctly rejected");
    } else {
        panic!("Switch to non-existent cat should return SwitchResult(success=false), got {switch_bad:?}");
    }
}

/// 测试 8：创建猫并 BindCat — 验证绑定流程。
#[test]
#[ignore]
fn test_create_cat_and_bind() {
    let mut fixture = DaemonFixture::new();

    if !fixture.binary_exists() {
        eprintln!("WARN: meowagent binary not found, skipping bind test");
        return;
    }

    fixture.start().expect("Daemon should start");

    // 创建猫
    let cat_name = "bind_test_cat";
    let create_resp = fixture
        .send_recv(&IpcMessage::RequestCreateCat {
            name: cat_name.to_string(),
            config: None,
        })
        .expect("RequestCreateCat should succeed");

    let (cat_id, initial_status) = match create_resp {
        IpcMessage::CatCreationResult {
            success: true,
            cat: Some(info),
            ..
        } => (info.id, info.status),
        other => panic!("Expected CatCreationResult, got {other:?}"),
    };

    assert_eq!(
        initial_status,
        CatStatus::Unbound,
        "Newly created cat should be Unbound"
    );

    // 绑定猫
    let agent_id = format!("agent_{cat_id}");
    let bind_resp = fixture
        .send_recv(&IpcMessage::BindCat {
            cat_id: cat_id.clone(),
            agent_id: agent_id.clone(),
        })
        .expect("BindCat should succeed");

    match bind_resp {
        IpcMessage::BoundCatResult {
            success: true,
            cat: Some(bound_cat),
            error: None,
        } => {
            assert_eq!(bound_cat.agent_id, Some(agent_id));
            assert_eq!(bound_cat.status, CatStatus::Active);
        }
        other => panic!("Expected BoundCatResult(success), got {other:?}"),
    }

    // 尝试再次绑定 — 应该失败（猫已绑定）
    let rebind_resp = fixture
        .send_recv(&IpcMessage::BindCat {
            cat_id: cat_id.clone(),
            agent_id: "another_agent".to_string(),
        })
        .expect("Rebind should return a response");

    match rebind_resp {
        IpcMessage::BoundCatResult {
            success: false,
            error: Some(_),
            ..
        } => {
            eprintln!("  ✓ Rebind correctly rejected");
        }
        other => {
            panic!("Rebind to bound cat should fail, got {other:?}");
        }
    }

    // 绑定不存在的猫 — 应该失败
    let bad_bind = fixture
        .send_recv(&IpcMessage::BindCat {
            cat_id: "nonexistent".to_string(),
            agent_id: "some_agent".to_string(),
        })
        .expect("BindCat to non-existent cat should return response");

    match bad_bind {
        IpcMessage::BoundCatResult {
            success: false,
            error: Some(_),
            ..
        } => {
            eprintln!("  ✓ Non-existent cat correctly rejected");
        }
        other => {
            panic!("Bind to non-existent cat should fail, got {other:?}");
        }
    }
}
