//! SnapshotLayer — L2 上下文压缩层
//!
//! 四种快照策略，管理 L2 快照的生命周期：
//! - Full: 完整快照，原样存储结果
//! - Diff: 差异快照，只存和上一帧的变化
//! - Anchor: 索引锚点，提取关键词
//! - Testament: 遗嘱继承，经验总结
//!
//! 当 L2 token 超预算时，`evict()` 返回最老的快照供 L3 降级。

use std::time::{SystemTime, UNIX_EPOCH};

// ── SnapshotStrategy ───────────────────────────────────────

/// 四种快照策略
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum SnapshotStrategy {
    /// 完整快照：原样存储任务结果
    Full,
    /// 差异快照：只存和上一帧的差异
    Diff,
    /// 索引锚点：提取关键词，不存完整内容
    Anchor,
    /// 遗嘱继承：转述经验总结
    Testament,
}

impl SnapshotStrategy {
    pub fn all() -> &'static [SnapshotStrategy] {
        &[SnapshotStrategy::Full, SnapshotStrategy::Diff, SnapshotStrategy::Anchor, SnapshotStrategy::Testament]
    }
}

// ── Snapshot ──────────────────────────────────────────────

/// 单条快照
#[derive(Debug, Clone)]
pub struct Snapshot {
    pub id: String,
    pub task_id: String,
    pub strategy: SnapshotStrategy,
    pub content: String,
    pub token_count: usize,
    pub created_at: u64,
    /// Diff 策略：上一帧的 snapshot id
    pub parent_snapshot: Option<String>,
    /// Anchor 策略：关键词列表
    pub anchor_keys: Vec<String>,
}

// ── SnapshotLayer ──────────────────────────────────────────

/// L2 快照层管理
pub struct SnapshotLayer {
    snapshots: Vec<Snapshot>,
    max_tokens: usize,
    strategy: SnapshotStrategy,
    total_token_cache: usize,
}

impl SnapshotLayer {
    /// 创建新的快照层
    pub fn new(strategy: SnapshotStrategy, max_tokens: usize) -> Self {
        SnapshotLayer {
            snapshots: Vec::new(),
            max_tokens,
            strategy,
            total_token_cache: 0,
        }
    }

    /// 为任务结果创建快照并加入层
    pub fn take_snapshot(&mut self, task_id: &str, task_result: &str) -> &Snapshot {
        let snapshot = self.build_snapshot(task_id, task_result);
        self.total_token_cache += snapshot.token_count;
        self.snapshots.push(snapshot);
        self.snapshots.last().unwrap()
    }

    /// 构造单条快照（内部方法，不加入层）
    fn build_snapshot(&self, task_id: &str, task_result: &str) -> Snapshot {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs();
        let id = generate_id();

        match self.strategy {
            SnapshotStrategy::Full => {
                let tokens = estimate_token_count(task_result);
                Snapshot {
                    id,
                    task_id: task_id.to_string(),
                    strategy: SnapshotStrategy::Full,
                    content: task_result.to_string(),
                    token_count: tokens,
                    created_at: now,
                    parent_snapshot: None,
                    anchor_keys: Vec::new(),
                }
            }
            SnapshotStrategy::Diff => {
                // 与上一帧比较
                let parent = self.snapshots.last();
                if let Some(prev) = parent {
                    let diff = simple_diff(&prev.content, task_result);
                    let tokens = estimate_token_count(&diff);
                    Snapshot {
                        id,
                        task_id: task_id.to_string(),
                        strategy: SnapshotStrategy::Diff,
                        content: diff,
                        token_count: tokens,
                        created_at: now,
                        parent_snapshot: Some(prev.id.clone()),
                        anchor_keys: Vec::new(),
                    }
                } else {
                    // 没有上一帧，存完整内容
                    let tokens = estimate_token_count(task_result);
                    Snapshot {
                        id,
                        task_id: task_id.to_string(),
                        strategy: SnapshotStrategy::Diff,
                        content: task_result.to_string(),
                        token_count: tokens,
                        created_at: now,
                        parent_snapshot: None,
                        anchor_keys: Vec::new(),
                    }
                }
            }
            SnapshotStrategy::Anchor => {
                let keywords = extract_keywords(task_result);
                let content = keywords.join(", ");
                let tokens = estimate_token_count(&content);
                Snapshot {
                    id,
                    task_id: task_id.to_string(),
                    strategy: SnapshotStrategy::Anchor,
                    content,
                    token_count: tokens,
                    created_at: now,
                    parent_snapshot: None,
                    anchor_keys: keywords,
                }
            }
            SnapshotStrategy::Testament => {
                let tokens = estimate_token_count(task_result);
                Snapshot {
                    id,
                    task_id: task_id.to_string(),
                    strategy: SnapshotStrategy::Testament,
                    content: task_result.to_string(),
                    token_count: tokens,
                    created_at: now,
                    parent_snapshot: None,
                    anchor_keys: Vec::new(),
                }
            }
        }
    }

    /// 移除最老的快照直到 token 数 ≤ max_tokens
    /// 返回被 evict 的快照列表（调用方决定如何处理）
    pub fn evict(&mut self) -> Vec<Snapshot> {
        let mut evicted = Vec::new();
        while self.total_token_cache > self.max_tokens && !self.snapshots.is_empty() {
            let snapshot = self.snapshots.remove(0);
            self.total_token_cache = self.total_token_cache.saturating_sub(snapshot.token_count);
            evicted.push(snapshot);
        }
        evicted
    }

    /// 当前总 token 数
    pub fn total_tokens(&self) -> usize {
        self.total_token_cache
    }

    /// 快照数量
    pub fn len(&self) -> usize {
        self.snapshots.len()
    }

    pub fn is_empty(&self) -> bool {
        self.snapshots.is_empty()
    }

    /// 获取所有快照引用
    pub fn snapshots(&self) -> &[Snapshot] {
        &self.snapshots
    }

    /// 获取当前策略
    pub fn strategy(&self) -> SnapshotStrategy {
        self.strategy
    }

    /// 获取 max_tokens
    pub fn max_tokens(&self) -> usize {
        self.max_tokens
    }

    /// 清空所有快照
    pub fn clear(&mut self) {
        self.snapshots.clear();
        self.total_token_cache = 0;
    }

    /// 最近的一条快照
    pub fn latest(&self) -> Option<&Snapshot> {
        self.snapshots.last()
    }

    /// 按 task_id 查找快照
    pub fn find_by_task(&self, task_id: &str) -> Option<&Snapshot> {
        self.snapshots.iter().rev().find(|s| s.task_id == task_id)
    }
}

// ── 辅助函数 ──────────────────────────────────────────────

/// 生成唯一快照 ID
fn generate_id() -> String {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    format!("snap_{:x}", nanos)
}

/// 估计文本 token 数：中英文混合按 char/4 估算
fn estimate_token_count(text: &str) -> usize {
    if text.is_empty() {
        return 0;
    }
    // 中英文平均每个 token 约为 3-4 chars
    (text.len() / 3).max(1)
}

/// 从文本中提取关键词（长度 ≥3 的字母数字片段）
fn extract_keywords(text: &str) -> Vec<String> {
    let mut keywords: Vec<String> = text
        .split(|c: char| !c.is_alphanumeric() && c != '\'')
        .filter(|w| w.len() >= 3 && !w.is_empty())
        .map(|w| w.to_lowercase())
        .collect();
    keywords.sort();
    keywords.dedup();
    keywords
}

/// 简单文本差异（词级别）
/// 返回 "ADDED: ...\nREMOVED: ..." 格式
fn simple_diff(old: &str, new: &str) -> String {
    let old_words: Vec<&str> = old.split_whitespace().collect();
    let new_words: Vec<&str> = new.split_whitespace().collect();

    use std::collections::HashSet;
    let old_set: HashSet<&str> = old_words.into_iter().collect();
    let new_set: HashSet<&str> = new_words.into_iter().collect();

    let added: Vec<&str> = new_set.difference(&old_set).copied().collect();
    let removed: Vec<&str> = old_set.difference(&new_set).copied().collect();

    let mut parts = Vec::new();
    if !added.is_empty() {
        parts.push(format!("ADDED: {}", added.join(" ")));
    }
    if !removed.is_empty() {
        parts.push(format!("REMOVED: {}", removed.join(" ")));
    }
    if parts.is_empty() {
        String::from("(no change)")
    } else {
        parts.join("\n")
    }
}

// ── Tests ──────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    // ── SnapshotStrategy ───────────────────────────────────

    #[test]
    fn test_strategy_all_contains_four() {
        assert_eq!(SnapshotStrategy::all().len(), 4);
    }

    #[test]
    fn test_strategy_debug() {
        let s = SnapshotStrategy::Full;
        assert_eq!(format!("{:?}", s), "Full");
    }

    // ── Basic SnapshotLayer ────────────────────────────────

    #[test]
    fn test_new_layer_is_empty() {
        let layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        assert!(layer.is_empty());
        assert_eq!(layer.len(), 0);
        assert_eq!(layer.total_tokens(), 0);
    }

    #[test]
    fn test_full_snapshot_stores_content() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "Hello world, this is a test result.");
        assert_eq!(layer.len(), 1);
        let snap = layer.latest().unwrap();
        assert_eq!(snap.strategy, SnapshotStrategy::Full);
        assert!(snap.content.contains("Hello world"));
        assert!(snap.token_count > 0);
        assert!(snap.id.starts_with("snap_"));
    }

    #[test]
    fn test_full_snapshot_empty_content() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "");
        let snap = layer.latest().unwrap();
        assert_eq!(snap.content, "");
        assert_eq!(snap.token_count, 0);
    }

    #[test]
    fn test_multiple_snapshots_have_distinct_ids() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "first");
        let id1 = layer.latest().unwrap().id.clone();
        layer.take_snapshot("task_2", "second");
        let id2 = layer.latest().unwrap().id.clone();
        assert_ne!(id1, id2);
    }

    #[test]
    fn test_snapshot_tracks_task_id() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("my_task_007", "result");
        assert_eq!(layer.latest().unwrap().task_id, "my_task_007");
    }

    // ── Diff strategy ──────────────────────────────────────

    #[test]
    fn test_diff_snapshot_no_previous_stores_full() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Diff, 1000);
        layer.take_snapshot("task_1", "the quick brown fox");
        let snap = layer.latest().unwrap();
        assert_eq!(snap.content, "the quick brown fox");
        assert!(snap.parent_snapshot.is_none());
    }

    #[test]
    fn test_diff_snapshot_with_previous() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Diff, 1000);
        layer.take_snapshot("task_1", "the quick brown fox");
        let prev_id = layer.latest().unwrap().id.clone();
        layer.take_snapshot("task_2", "the quick brown fox jumps over");
        let snap = layer.latest().unwrap();
        assert!(snap.content.contains("ADDED"));
        assert!(snap.content.contains("jumps"));
        assert!(snap.content.contains("over"));
        assert_eq!(snap.parent_snapshot, Some(prev_id));
    }

    #[test]
    fn test_diff_snapshot_no_change() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Diff, 1000);
        layer.take_snapshot("task_1", "same content");
        layer.take_snapshot("task_2", "same content");
        let snap = layer.latest().unwrap();
        assert_eq!(snap.content, "(no change)");
    }

    #[test]
    fn test_diff_removed_words() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Diff, 1000);
        layer.take_snapshot("task_1", "apples oranges bananas");
        layer.take_snapshot("task_2", "apples bananas");
        let snap = layer.latest().unwrap();
        assert!(snap.content.contains("REMOVED"));
        assert!(snap.content.contains("oranges"));
    }

    // ── Anchor strategy ────────────────────────────────────

    #[test]
    fn test_anchor_snapshot_extracts_keywords() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Anchor, 1000);
        layer.take_snapshot("task_1", "the quick brown fox jumps over lazy dog");
        let snap = layer.latest().unwrap();
        // keywords: words >= 3 chars, sorted, deduped
        assert!(snap.anchor_keys.contains(&"brown".to_string()));
        assert!(snap.anchor_keys.contains(&"fox".to_string()));
        assert!(snap.anchor_keys.contains(&"jumps".to_string()));
        assert!(snap.anchor_keys.contains(&"quick".to_string()));
        assert!(snap.anchor_keys.contains(&"lazy".to_string()));
        // short 2-char words are excluded
        assert!(!snap.anchor_keys.contains(&"to".to_string()));
    }

    #[test]
    fn test_anchor_empty_text() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Anchor, 1000);
        layer.take_snapshot("task_1", "");
        let snap = layer.latest().unwrap();
        assert!(snap.anchor_keys.is_empty());
        assert_eq!(snap.content, "");
    }

    #[test]
    fn test_anchor_short_words_excluded() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Anchor, 1000);
        layer.take_snapshot("task_1", "a an at by to");
        let snap = layer.latest().unwrap();
        assert!(snap.anchor_keys.is_empty());
    }

    #[test]
    fn test_anchor_deduplicates() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Anchor, 1000);
        layer.take_snapshot("task_1", "alpha beta alpha gamma beta");
        assert_eq!(layer.latest().unwrap().anchor_keys.len(), 3);
    }

    // ── Testament strategy ─────────────────────────────────

    #[test]
    fn test_testament_snapshot_stores_content() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Testament, 1000);
        layer.take_snapshot("task_1", "important experience summary");
        let snap = layer.latest().unwrap();
        assert_eq!(snap.strategy, SnapshotStrategy::Testament);
        assert_eq!(snap.content, "important experience summary");
    }

    // ── Evict ──────────────────────────────────────────────

    #[test]
    fn test_evict_empty_layer() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 100);
        let evicted = layer.evict();
        assert!(evicted.is_empty());
    }

    #[test]
    fn test_evict_does_not_remove_within_budget() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "short");
        assert_eq!(layer.len(), 1);
        let evicted = layer.evict();
        assert!(evicted.is_empty());
        assert_eq!(layer.len(), 1);
    }

    #[test]
    fn test_evict_oldest_when_over_budget() {
        // max_tokens=3, each short snapshot is at least 1 token
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 2);
        layer.take_snapshot("task_1", "hello world"); // ~4 tokens
        assert!(layer.total_tokens() > 2);
        let evicted = layer.evict();
        assert_eq!(evicted.len(), 1);
        assert_eq!(evicted[0].task_id, "task_1");
        assert!(layer.is_empty());
    }

    #[test]
    fn test_evict_preserves_remaining() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 10);
        layer.take_snapshot("task_1", "aaaa bbbb cccc dddd eeee"); // ~6 tokens
        layer.take_snapshot("task_2", "short");                    // ~2 tokens
        layer.take_snapshot("task_3", "tiny");                     // ~1 token
        // total ~9 tokens, within budget → no evict
        let evicted = layer.evict();
        assert!(evicted.is_empty());
        assert_eq!(layer.len(), 3);

        // add one more to go over
        layer.take_snapshot("task_4", "aaaa bbbb cccc dddd eeee ffff gggg"); // ~9 tokens
        // total ~18 tokens → evict oldest until ≤ 10
        let evicted = layer.evict();
        assert!(!evicted.is_empty());
        assert_eq!(evicted[0].task_id, "task_1");
        assert_eq!(layer.total_tokens() <= 10, true);
    }

    #[test]
    fn test_evict_clears_all_when_single_over_budget() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1);
        layer.take_snapshot("task_1", "a longer text that exceeds the token budget"); // ~14 tokens
        let evicted = layer.evict();
        assert_eq!(evicted.len(), 1);
        assert!(layer.is_empty());
    }

    // ── Total tokens ────────────────────────────────────────

    #[test]
    fn test_total_tokens_after_multiple_snapshots() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 10000);
        layer.take_snapshot("task_1", "hello");
        let t1 = layer.total_tokens();
        layer.take_snapshot("task_2", "world");
        let t2 = layer.total_tokens();
        assert!(t2 > t1);
    }

    #[test]
    fn test_total_tokens_after_evict_decreases() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1);
        layer.take_snapshot("task_1", "big content here");
        let before = layer.total_tokens();
        let _evicted = layer.evict();
        assert!(layer.total_tokens() < before);
        assert_eq!(layer.total_tokens(), 0);
    }

    // ── Clear ───────────────────────────────────────────────

    #[test]
    fn test_clear_removes_all() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "hello");
        layer.take_snapshot("task_2", "world");
        assert_eq!(layer.len(), 2);
        layer.clear();
        assert!(layer.is_empty());
        assert_eq!(layer.total_tokens(), 0);
    }

    // ── Find by task ────────────────────────────────────────

    #[test]
    fn test_find_by_task_exists() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_abc", "result");
        let found = layer.find_by_task("task_abc");
        assert!(found.is_some());
        assert_eq!(found.unwrap().content, "result");
    }

    #[test]
    fn test_find_by_task_not_found() {
        let layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        assert!(layer.find_by_task("nonexistent").is_none());
    }

    #[test]
    fn test_find_by_task_returns_latest() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "first version");
        layer.take_snapshot("task_1", "second version"); // same task id
        let found = layer.find_by_task("task_1").unwrap();
        assert_eq!(found.content, "second version");
    }

    // ── Latest ──────────────────────────────────────────────

    #[test]
    fn test_latest_empty_layer() {
        let layer: SnapshotLayer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        assert!(layer.latest().is_none());
    }

    #[test]
    fn test_latest_returns_last_added() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "first");
        layer.take_snapshot("task_2", "second");
        assert_eq!(layer.latest().unwrap().content, "second");
    }

    // ── estimate_token_count ───────────────────────────────

    #[test]
    fn test_estimate_token_count_empty() {
        assert_eq!(estimate_token_count(""), 0);
    }

    #[test]
    fn test_estimate_token_count_non_empty() {
        assert!(estimate_token_count("hello") > 0);
    }

    // ── extract_keywords ───────────────────────────────────

    #[test]
    fn test_extract_keywords_empty() {
        let kw = extract_keywords("");
        assert!(kw.is_empty());
    }

    #[test]
    fn test_extract_keywords_filters_short() {
        let kw = extract_keywords("a an by at to");
        assert!(kw.is_empty());
    }

    #[test]
    fn test_extract_keywords_case_normalized() {
        let kw = extract_keywords("Hello HELLO hello");
        assert_eq!(kw.len(), 1);
        assert_eq!(kw[0], "hello");
    }

    #[test]
    fn test_extract_keywords_alphanumeric() {
        let kw = extract_keywords("test123 alpha-beta");
        assert!(kw.contains(&"test123".to_string()));
        // "alpha-beta" contains '-' which is a separator
        assert!(kw.contains(&"alpha".to_string()));
        assert!(kw.contains(&"beta".to_string()));
    }

    // ── simple_diff ────────────────────────────────────────

    #[test]
    fn test_simple_diff_identical() {
        assert_eq!(simple_diff("same text", "same text"), "(no change)");
    }

    #[test]
    fn test_simple_diff_added() {
        let diff = simple_diff("foo bar", "foo bar baz");
        assert!(diff.contains("ADDED"));
        assert!(diff.contains("baz"));
        assert!(!diff.contains("REMOVED"));
    }

    #[test]
    fn test_simple_diff_removed() {
        let diff = simple_diff("foo bar baz", "foo bar");
        assert!(diff.contains("REMOVED"));
        assert!(diff.contains("baz"));
        assert!(!diff.contains("ADDED"));
    }

    #[test]
    fn test_simple_diff_both() {
        let diff = simple_diff("foo bar", "foo baz");
        assert!(diff.contains("ADDED"));
        assert!(diff.contains("baz"));
        assert!(diff.contains("REMOVED"));
        assert!(diff.contains("bar"));
    }

    #[test]
    fn test_simple_diff_empty_new() {
        let diff = simple_diff("foo bar", "");
        assert!(diff.contains("REMOVED") || diff == "(no change)");
    }

    #[test]
    fn test_simple_diff_empty_old() {
        let diff = simple_diff("", "foo bar");
        assert!(diff.contains("ADDED"));
    }

    #[test]
    fn test_simple_diff_both_empty() {
        assert_eq!(simple_diff("", ""), "(no change)");
    }

    // ── Display ─────────────────────────────────────────────

    #[test]
    fn test_snapshot_display() {
        let mut layer = SnapshotLayer::new(SnapshotStrategy::Full, 1000);
        layer.take_snapshot("task_1", "test result");
        let snap = layer.latest().unwrap();
        let s = format!("{:?}", snap);
        assert!(s.contains("Full"));
        assert!(s.contains("task_1"));
    }
}
