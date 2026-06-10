//! DreamWriteBatch — Dream 内部写操作统一批处理。
//! v1.0: 标记设计意图，后续迭代将所有 Dream 写操作集中到此。

use crate::engram::{Hyperedge, KnowledgeNode};
/// Dream 内部写操作批处理
#[derive(Debug, Default)]
#[allow(dead_code)]
pub struct DreamWriteBatch {
    /// 衰减后的超边 (he_id, new_weight)
    pub decayed_hyperedges: Vec<(String, f32)>,
    /// 新创建的超边
    pub new_hyperedges: Vec<Hyperedge>,
    /// 更新后的节点
    pub updated_nodes: Vec<KnowledgeNode>,
    /// 情感纠缠计数
    pub emotional_edges: u32,
    /// 时间绑定计数
    pub temporal_edges: u32,
}

#[allow(dead_code)]
impl DreamWriteBatch {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn is_empty(&self) -> bool {
        self.decayed_hyperedges.is_empty()
            && self.new_hyperedges.is_empty()
            && self.updated_nodes.is_empty()
    }
}
