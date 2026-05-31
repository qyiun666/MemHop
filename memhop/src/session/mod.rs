//! Session — agent 侧维护的会话上下文和状态
//!
//! 设计原则：memhop 进程无状态，状态由 agent 进程维护。
//! Session 是 agent 进程内的上下文聚合，每轮对话时传给 memhop。

use crate::cortex::Cortex;
use crate::context::ActiveContextSet;

/// 会话上下文 — agent 侧维护，memhop 不存储
pub struct Session {
    /// 工作记忆（最近 N 轮对话，按 session_id 隔离）
    pub cortex: Cortex,
    /// 活跃上下文集合
    pub active_contexts: ActiveContextSet,
    /// 会话标识
    pub session_id: String,
}

impl Session {
    /// 创建新会话
    pub fn new(session_id: &str, active_contexts: ActiveContextSet) -> Self {
        Session {
            cortex: Cortex::new(7),
            active_contexts,
            session_id: session_id.to_string(),
        }
    }

    /// 从存储重建会话（用于进程重启后恢复）
    pub fn load_or_create(session_id: &str, active_contexts: ActiveContextSet) -> Self {
        Self::new(session_id, active_contexts)
    }
}
