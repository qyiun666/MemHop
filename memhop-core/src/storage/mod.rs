//! Redb 存储层 — 单文件存储所有 6 层记忆数据。
//!
//! 文件布局:
//!   <brain_dir>/agent_brain.db   — 单文件，所有表共享同一个 redb::Database
//!
//! 表布局:
//!   l0_profile            — L0 角色画像 (profile:main → L0Profile)
//!   l0_history            — L0 画像版本历史 (hist:{version} → L0Snapshot)
//!   l0_config             — L0 配置
//!   l1_nodes              — L1 超图节点 (node_id → KnowledgeNode)
//!   l1_hyperedges         — L1 超边 (he_id → Hyperedge)
//!   l1_node_to_hyperedges — L1 节点→超边反向索引 (node_id → Vec<String>)
//!   l1_config             — L1 配置
//!   l1_vector_index       — L1 HNSW 快照 (deprecated, 保留兼容)
//!   l1_sparse_forward     — L1 BM25 forward 索引 (memory_id → sparse weights)
//!   l1_sparse_doc_len     — L1 BM25 doc 长度 (memory_id → u32)
//!   l2_topics             — L2 话题 (topic:{id}:meta → Topic)
//!   l2_topic_edges        — L2 话题边 (edge:{id} → TopicEdge)
//!   l2_topic_bm25         — L2 话题 BM25 快照 (deprecated)
//!   l2_topic_vector_index — L2 话题向量索引快照
//!   l2_config             — L2 配置
//!   l3_domain_meta        — L3 领域元信息 (domain:{id}:meta → ShelfMeta)
//!   l3_domain_nodes       — L3 领域节点 (node:{domain_id}:{node_id} → KnowledgeNode)
//!   l3_domain_hyperedges  — L3 领域超边 (hyp:{domain_id}:{he_id} → Hyperedge)
//!   l3_domain_bm25        — L3 领域 BM25 (deprecated)
//!   l3_config             — L3 配置
//!   l4_docs               — L4 原文 (doc:{id} → RawDocument)
//!   l4_turn_index         — L4 Turn 索引 (turn:{turn_id} → Vec<String>)
//!   l4_session_index      — L4 Session 索引 (session:{session_id} → Vec<String>)
//!   l4_doc_sequence       — L4 文档序列 (seq:{seq_num} → doc_id)
//!   l4_config             — L4 配置
//!   l5_crystals           — L5 程序性晶体 (crystal:{id} → ProceduralCrystal)
//!   l5_chain_index        — L5 链索引 (chain:{chain_id} → Vec<String>)
//!   metadata              — 版本信息 (version → VersionInfo)

use redb::TableDefinition;

pub mod migrate;
pub mod ops;
pub mod store;

// ── 格式 Magic & Version ─────────────────────────────────

/// 文件头 Magic 标识
pub const MAGIC: &[u8; 8] = b"MEMHOPDB";

/// 主版本号 — 破坏性变更时递增
pub const VERSION_MAJOR: u8 = 1;

/// 次版本号 — 非破坏性变更时递增
pub const VERSION_MINOR: u8 = 0;

// ── L0: 角色画像表定义 ──────────────────────────────────

pub const L0_PROFILE: TableDefinition<&str, &[u8]> = TableDefinition::new("l0_profile");
pub const L0_HISTORY: TableDefinition<&str, &[u8]> = TableDefinition::new("l0_history");

// ── L1: 超图表定义 ──────────────────────────────────────

pub const L1_NODES: TableDefinition<&str, &[u8]> = TableDefinition::new("l1_nodes");
pub const L1_HYPEREDGES: TableDefinition<&str, &[u8]> = TableDefinition::new("l1_hyperedges");
pub const L1_NODE_TO_HYPEREDGES: TableDefinition<&str, &[u8]> =
    TableDefinition::new("l1_node_to_hyperedges");
pub const L1_SPARSE_FORWARD: TableDefinition<&str, &[u8]> =
    TableDefinition::new("l1_sparse_forward");
pub const L1_SPARSE_DOC_LEN: TableDefinition<&str, u32> =
    TableDefinition::new("l1_sparse_doc_len");

// ── L2: 话题图表定义 ─────────────────────────────────────

pub const L2_TOPICS: TableDefinition<&str, &[u8]> = TableDefinition::new("l2_topics");
pub const L2_TOPIC_EDGES: TableDefinition<&str, &[u8]> = TableDefinition::new("l2_topic_edges");
pub const L2_TOPIC_VECTOR_INDEX: TableDefinition<&str, &[u8]> =
    TableDefinition::new("l2_topic_vector_index");
pub const L2_TOPIC_NGRAM_FORWARD: TableDefinition<&str, &[u8]> =
    TableDefinition::new("l2_topic_ngram_forward");
pub const L2_TOPIC_NGRAM_DOC_LEN: TableDefinition<&str, u32> =
    TableDefinition::new("l2_topic_ngram_doc_len");

// ── L3: 领域图表定义 ─────────────────────────────────────

pub const L3_DOMAIN_META: TableDefinition<&str, &[u8]> = TableDefinition::new("l3_domain_meta");
pub const L3_DOMAIN_NODES: TableDefinition<&str, &[u8]> = TableDefinition::new("l3_domain_nodes");
pub const L3_DOMAIN_HYPEREDGES: TableDefinition<&str, &[u8]> =
    TableDefinition::new("l3_domain_hyperedges");
pub const L3_SPARSE_FORWARD: TableDefinition<&str, &[u8]> =
    TableDefinition::new("l3_sparse_forward");
pub const L3_SPARSE_DOC_LEN: TableDefinition<&str, u32> =
    TableDefinition::new("l3_sparse_doc_len");

// ── L4: 原文库表定义 ─────────────────────────────────────

pub const L4_DOCS: TableDefinition<&str, &[u8]> = TableDefinition::new("l4_docs");
pub const L4_TURN_INDEX: TableDefinition<&str, &[u8]> = TableDefinition::new("l4_turn_index");
pub const L4_SESSION_INDEX: TableDefinition<&str, &[u8]> = TableDefinition::new("l4_session_index");

// ── L5: 程序性晶体表定义 ─────────────────────────────────

pub const L5_CRYSTALS: TableDefinition<&str, &[u8]> = TableDefinition::new("l5_crystals");
pub const L5_CHAIN_INDEX: TableDefinition<&str, &[u8]> = TableDefinition::new("l5_chain_index");

// ── 元数据表定义 ─────────────────────────────────────────

pub const METADATA: TableDefinition<&str, &[u8]> = TableDefinition::new("metadata");

// ── 版本信息结构体 ───────────────────────────────────────

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct VersionInfo {
    pub magic: [u8; 8],
    pub version_major: u8,
    pub version_minor: u8,
    pub endianness: u8, // 0=LE, 1=BE
    pub created_at: i64,
    pub migrated_from: Option<String>,
}

impl VersionInfo {
    pub fn current() -> Self {
        Self {
            magic: *MAGIC,
            version_major: VERSION_MAJOR,
            version_minor: VERSION_MINOR,
            endianness: 0, // little-endian
            created_at: chrono::Utc::now().timestamp_millis(),
            migrated_from: None,
        }
    }

    /// 验证版本信息是否有效。
    #[allow(dead_code)]
    pub fn is_valid(&self) -> bool {
        self.magic == *MAGIC && self.version_major == VERSION_MAJOR
    }
}
