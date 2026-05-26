//! L0 Cortex Layer — 工作记忆快取（Working Memory Cache）
//!
//! 模拟猫脑皮层缓冲（~16 秒工作记忆窗口），用纯内存的 ring buffer
//! 提供零延迟的最近记忆访问。该层位于 MemHop 三层架构的最上层（L0），
//! 不持久化、不参与召回排序，仅作为"刚刚发生过的事"的即时快取。
//!
//! 设计要点：
//! - 使用标准库 [`VecDeque`] 实现固定容量的环形缓冲区。
//! - 默认容量 7，对应工作记忆的近似容量上限。
//! - 所有方法均为同步、轻量操作；不引入额外依赖。
//! - 入站新记忆时若容量已满，自动淘汰最旧的一条（FIFO）。

use std::collections::VecDeque;
use std::sync::{Mutex, OnceLock};

use crate::engram::Engram;

/// Cortex 默认容量 —— 对应猫脑工作记忆的近似上限。
const DEFAULT_CAPACITY: usize = 7;

/// 工作记忆中的单条记录：Engram 本体 + 所属 session。
struct CortexEntry {
    engram: Engram,
    session_id: String,
}

/// L0 Cortex Layer —— 工作记忆快取。
///
/// 模拟猫脑皮层缓冲（~16 秒工作记忆），提供零延迟的最近记忆访问。
/// 内部用 [`VecDeque`] 维护一个固定容量的环形缓冲：
/// 队列**前端为最旧**、**后端为最新**；超出容量则从前端淘汰。
pub struct Cortex {
    queue: VecDeque<CortexEntry>,
    /// 缓冲区容量上限（默认 7）。
    capacity: usize,
}

impl Cortex {
    /// 以指定容量构造一个 Cortex。
    ///
    /// 当 `capacity == 0` 时，[`push`](Self::push) 将不会保留任何记忆。
    pub fn new(capacity: usize) -> Self {
        Self {
            queue: VecDeque::with_capacity(capacity),
            capacity,
        }
    }

    /// 以默认容量（7）构造一个 Cortex。
    pub fn with_default_capacity() -> Self {
        Self::new(DEFAULT_CAPACITY)
    }

    /// 将一条新 Engram 推入工作记忆。
    ///
    /// 若缓冲区已满，则自动淘汰最旧的一条（队列前端）。
    /// `session_id` 用于后续按会话过滤召回。
    pub fn push(&mut self, engram: Engram, session_id: &str) {
        if self.capacity == 0 {
            return;
        }
        while self.queue.len() >= self.capacity {
            self.queue.pop_front();
        }
        self.queue.push_back(CortexEntry {
            engram,
            session_id: session_id.to_string(),
        });
    }

    /// 获取指定 session 的最近 N 条 Engram（按时间从新到旧）。
    ///
    /// 返回的是缓冲区内记录的克隆，调用方可按需使用。若 `limit == 0`
    /// 或该 session 在缓冲区中不存在记录，则返回空向量。
    pub fn recent(&self, session_id: &str, limit: usize) -> Vec<Engram> {
        if limit == 0 {
            return Vec::new();
        }
        self.queue
            .iter()
            .rev()
            .filter(|e| e.session_id == session_id)
            .take(limit)
            .map(|e| e.engram.clone())
            .collect()
    }

    /// 获取所有 session 的最近 N 条 Engram（按时间从新到旧）。
    ///
    /// 不区分 session，仅按入队时间倒序返回最新的 `limit` 条。
    #[allow(dead_code)]
    pub fn recent_all(&self, limit: usize) -> Vec<Engram> {
        if limit == 0 {
            return Vec::new();
        }
        self.queue
            .iter()
            .rev()
            .take(limit)
            .map(|e| e.engram.clone())
            .collect()
    }

    /// 清空指定 session 的所有工作记忆。其它 session 不受影响。
    #[allow(dead_code)]
    pub fn clear_session(&mut self, session_id: &str) {
        self.queue.retain(|e| e.session_id != session_id);
    }

    /// 当前缓冲区中的记录数。
    pub fn len(&self) -> usize {
        self.queue.len()
    }

    /// 缓冲区是否为空。
    #[allow(dead_code)]
    pub fn is_empty(&self) -> bool {
        self.queue.is_empty()
    }
}

impl Default for Cortex {
    fn default() -> Self {
        Self::with_default_capacity()
    }
}

// ── Global singleton for push_to_cortex ───────────────────────

static GLOBAL_CORTEX: OnceLock<Mutex<Cortex>> = OnceLock::new();

/// Push an engram into the global cortex cache.
///
/// Called automatically by the engine after storing a new perception.
pub(crate) fn push_to_cortex(engram: &Engram) {
    let cortex = GLOBAL_CORTEX.get_or_init(|| Mutex::new(Cortex::with_default_capacity()));
    if let Ok(mut guard) = cortex.lock() {
        guard.push(engram.clone(), "cortex");
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use half::f16;

    fn mk_engram(id: &str, text: &str) -> Engram {
        Engram::new_episode(
            id.to_string(),
            text.to_string(),
            vec![f16::from_f32(0.0); 4],
            vec![],
            0.0,
            0.5,
            1700000000000,
        )
    }

    #[test]
    fn push_and_recent_basic() {
        let mut cx = Cortex::with_default_capacity();
        assert!(cx.is_empty());
        cx.push(mk_engram("1", "hello"), "s1");
        cx.push(mk_engram("2", "world"), "s1");
        assert_eq!(cx.len(), 2);

        let recent = cx.recent("s1", 10);
        assert_eq!(recent.len(), 2);
        // 最新在前
        assert_eq!(recent[0].id, "2");
        assert_eq!(recent[1].id, "1");
    }

    #[test]
    fn evicts_oldest_when_full() {
        let mut cx = Cortex::new(3);
        cx.push(mk_engram("1", "a"), "s1");
        cx.push(mk_engram("2", "b"), "s1");
        cx.push(mk_engram("3", "c"), "s1");
        cx.push(mk_engram("4", "d"), "s1");
        assert_eq!(cx.len(), 3);
        let recent = cx.recent_all(10);
        let ids: Vec<&str> = recent.iter().map(|m| m.id.as_str()).collect();
        assert_eq!(ids, vec!["4", "3", "2"]);
    }

    #[test]
    fn session_isolation_and_clear() {
        let mut cx = Cortex::new(5);
        cx.push(mk_engram("1", "a"), "s1");
        cx.push(mk_engram("2", "b"), "s2");
        cx.push(mk_engram("3", "c"), "s1");

        let s1 = cx.recent("s1", 10);
        assert_eq!(s1.len(), 2);

        cx.clear_session("s1");
        assert_eq!(cx.len(), 1);
        assert_eq!(cx.recent("s2", 10)[0].id, "2");
    }

    #[test]
    fn zero_capacity_is_noop() {
        let mut cx = Cortex::new(0);
        cx.push(mk_engram("1", "a"), "s1");
        assert!(cx.is_empty());
    }
}
