//! Session — agent 侧维护的会话上下文和状态。
//!
//! memhop v0.14 不存储会话状态，由 agent 侧维护。

/// 会话上下文 — agent 侧维护，memhop 不存储
pub struct Session {
    pub session_id: String,
}

impl Session {
    pub fn new(session_id: &str) -> Self {
        Session { session_id: session_id.to_string() }
    }
}
