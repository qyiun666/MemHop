//! LMDB 存储层 — 5 层独立 heed::Env 环境。
//! L0/L1/L2/L3/L4 各一个独立 LMDB 文件，独立事务。
//!
//! 环境布局:
//!   <brain_dir>/l0_profile.db/     — 角色画像+版本历史
//!   <brain_dir>/l1_hypergraph.db/  — 超图节点+超边+BM25快照
//!   <brain_dir>/l2_topics.db/      — 话题+话题图边+每话题BM25
//!   <brain_dir>/l3_domains.db/     — 领域节点+领域超图+每领域BM25
//!   <brain_dir>/l4_raw.db/         — 原文+时间索引+会话索引

use heed::types::{Bytes, Str};
use heed::{Env, EnvOpenOptions, RoTxn, RwTxn};
use std::path::Path;
pub type DB = heed::Database<Str, Bytes>;
use crate::error::{MemHopError, Result};

/// LMDB 默认最大键大小 (MDB_MAXKEYSIZE)
const LMDB_MAX_KEY_SIZE: usize = 511;

/// LMDB 空间使用统计
#[derive(Debug, Clone, Copy)]
pub struct SpaceUsage {
    /// 已使用的字节数（数据文件大小）
    pub used_bytes: u64,
    /// 配置的映射空间大小
    pub map_size: u64,
    /// 使用率百分比
    pub usage_pct: f32,
    /// 数据库数量（当前实现中恒为 0；stat.entries 返回的是键值对数量而非数据库数量）
    /// 保留字段以保持 API 兼容
    pub db_count: usize,
}

/// 截断键以适应 LMDB 限制。
/// 如果键超过 511 字节，将其截断并添加哈希后缀以保持唯一性。
pub fn truncate_key(key: &str) -> String {
    let bytes = key.as_bytes();
    if bytes.len() <= LMDB_MAX_KEY_SIZE {
        return key.to_string();
    }

    // 计算需要截断的字节数（保留空间给哈希后缀）
    let hash_suffix_len = 8; // 8 字符的十六进制哈希
    let max_content_len = LMDB_MAX_KEY_SIZE - hash_suffix_len - 1; // -1 for separator

    // 找到安全的截断点（不在 UTF-8 字符中间）
    let mut truncate_at = max_content_len;
    while truncate_at > 0 && !key.is_char_boundary(truncate_at) {
        truncate_at -= 1;
    }

    // 生成哈希后缀
    let hash = calculate_hash(key);
    let hash_hex = format!("{:08x}", hash);

    // 组合截断后的键和哈希
    let truncated = &key[..truncate_at];
    format!("{}{}{}", truncated, "~", hash_hex)
}

/// 简单的字符串哈希函数
fn calculate_hash(s: &str) -> u32 {
    let mut hash: u32 = 5381;
    for byte in s.bytes() {
        hash = hash.wrapping_mul(33).wrapping_add(byte as u32);
    }
    hash
}

// ── L0: 角色画像环境 ──────────────────────────────────────

pub struct L0Env {
    pub env: Env,
    pub profile: DB,
    pub history: DB,
    pub config: DB,
}

impl L0Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(4 * 1024 * 1024) // v0.22.0: 4MB (was 64MB)
                .max_readers(128)
                .max_dbs(8)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l0 env: {}", e)))?
        };
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let profile = env
            .create_database(&mut wtxn, Some("profile"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let history = env
            .create_database(&mut wtxn, Some("history"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env
            .create_database(&mut wtxn, Some("config"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L0Env {
            env,
            profile,
            history,
            config,
        })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env
            .read_txn()
            .map_err(|e| MemHopError::Storage(format!("read txn: {}", e)))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("write txn: {}", e)))
    }

    /// 获取空间使用统计信息
    pub fn space_usage(&self) -> Result<SpaceUsage> {
        space_usage_impl(&self.env)
    }
}

// ── L1: 超图环境 ──────────────────────────────────────────

pub struct L1Env {
    pub env: Env,
    pub nodes: DB,
    pub hyperedges: DB,
    pub node_to_hyperedges: DB,
    pub config: DB,
    /// v0.15.0: 序列化的 VectorIndex 快照
    pub vector_index: DB,
    /// v0.23.0: SparseIndexV2 forward 索引 (memory_id → sparse weights)
    pub sparse_forward: DB,
}

impl L1Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(256 * 1024 * 1024) // v0.22.0: 256MB (was 1GB)
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l1 env: {}", e)))?
        };
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let nodes = env
            .create_database(&mut wtxn, Some("nodes"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let hyperedges = env
            .create_database(&mut wtxn, Some("hyperedges"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let node_to_hyperedges = env
            .create_database(&mut wtxn, Some("node_to_hyperedges"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env
            .create_database(&mut wtxn, Some("config"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let vector_index = env
            .create_database(&mut wtxn, Some("vector_index"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let sparse_forward = env
            .create_database(&mut wtxn, Some("sparse_forward"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L1Env {
            env,
            nodes,
            hyperedges,
            node_to_hyperedges,
            config,
            vector_index,
            sparse_forward,
        })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env
            .read_txn()
            .map_err(|e| MemHopError::Storage(format!("read txn: {}", e)))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("write txn: {}", e)))
    }

    /// 获取空间使用统计信息
    pub fn space_usage(&self) -> Result<SpaceUsage> {
        space_usage_impl(&self.env)
    }
}

// ── L2: 话题环境 ──────────────────────────────────────────

pub struct L2Env {
    pub env: Env,
    pub topics: DB,
    pub topic_edges: DB,
    pub topic_bm25: DB,
    pub topic_vector_index: DB,
    pub config: DB,
}

impl L2Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(128 * 1024 * 1024) // v0.22.0: 128MB (was 512MB)
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l2 env: {}", e)))?
        };
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let topics = env
            .create_database(&mut wtxn, Some("topics"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let topic_edges = env
            .create_database(&mut wtxn, Some("topic_edges"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let topic_bm25 = env
            .create_database(&mut wtxn, Some("topic_bm25"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let topic_vector_index = env
            .create_database(&mut wtxn, Some("topic_vector_index"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env
            .create_database(&mut wtxn, Some("config"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L2Env {
            env,
            topics,
            topic_edges,
            topic_bm25,
            topic_vector_index,
            config,
        })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env
            .read_txn()
            .map_err(|e| MemHopError::Storage(format!("read txn: {}", e)))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("write txn: {}", e)))
    }

    /// 获取空间使用统计信息
    pub fn space_usage(&self) -> Result<SpaceUsage> {
        space_usage_impl(&self.env)
    }
}

// ── L3: 领域环境 ──────────────────────────────────────────

pub struct L3Env {
    pub env: Env,
    pub domain_meta: DB,
    pub domain_nodes: DB,
    pub domain_hyperedges: DB,
    pub domain_bm25: DB,
    pub config: DB,
}

impl L3Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(128 * 1024 * 1024) // v0.22.0: 128MB (was 512MB)
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l3 env: {}", e)))?
        };
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let domain_meta = env
            .create_database(&mut wtxn, Some("domain_meta"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let domain_nodes = env
            .create_database(&mut wtxn, Some("domain_nodes"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let domain_hyperedges = env
            .create_database(&mut wtxn, Some("domain_hyperedges"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let domain_bm25 = env
            .create_database(&mut wtxn, Some("domain_bm25"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env
            .create_database(&mut wtxn, Some("config"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L3Env {
            env,
            domain_meta,
            domain_nodes,
            domain_hyperedges,
            domain_bm25,
            config,
        })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env
            .read_txn()
            .map_err(|e| MemHopError::Storage(format!("read txn: {}", e)))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("write txn: {}", e)))
    }

    /// 获取空间使用统计信息
    pub fn space_usage(&self) -> Result<SpaceUsage> {
        space_usage_impl(&self.env)
    }
}

// ── L4: 原文环境 ──────────────────────────────────────────

pub struct L4Env {
    pub env: Env,
    pub docs: DB,
    pub turn_index: DB,
    pub session_index: DB,
    pub doc_sequence: DB,
    pub config: DB,
}

impl L4Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(512 * 1024 * 1024) // v0.22.0: 512MB (was 2GB)
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l4 env: {}", e)))?
        };
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let docs = env
            .create_database(&mut wtxn, Some("docs"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let turn_index = env
            .create_database(&mut wtxn, Some("turn_index"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let session_index = env
            .create_database(&mut wtxn, Some("session_index"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let doc_sequence = env
            .create_database(&mut wtxn, Some("doc_sequence"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env
            .create_database(&mut wtxn, Some("config"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L4Env {
            env,
            docs,
            turn_index,
            session_index,
            doc_sequence,
            config,
        })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env
            .read_txn()
            .map_err(|e| MemHopError::Storage(format!("read txn: {}", e)))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("write txn: {}", e)))
    }

    /// 获取空间使用统计信息
    pub fn space_usage(&self) -> Result<SpaceUsage> {
        space_usage_impl(&self.env)
    }
}

// ── L5: 程序性晶体环境 ────────────────────────────────────

pub struct L5Env {
    pub env: Env,
    pub crystals: DB,
    pub chain_index: DB,
}

impl L5Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(16 * 1024 * 1024) // v0.22.0: 16MB (was 64MB)
                .max_readers(128)
                .max_dbs(8)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l5 env: {}", e)))?
        };
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let crystals = env
            .create_database(&mut wtxn, Some("crystals"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let chain_index = env
            .create_database(&mut wtxn, Some("chain_index"))
            .map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L5Env {
            env,
            crystals,
            chain_index,
        })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env
            .read_txn()
            .map_err(|e| MemHopError::Storage(format!("read txn: {}", e)))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env
            .write_txn()
            .map_err(|e| MemHopError::Storage(format!("write txn: {}", e)))
    }

    /// 获取空间使用统计信息
    pub fn space_usage(&self) -> Result<SpaceUsage> {
        space_usage_impl(&self.env)
    }
}

// ── 通用辅助函数 ─────────────────────────────────────────

/// 空间使用统计的内部实现
pub(crate) fn space_usage_impl(env: &Env) -> Result<SpaceUsage> {
    let info = env.info();
    let map_size = info.map_size as u64;

    let used_bytes = env
        .real_disk_size()
        .map_err(|e| MemHopError::Storage(format!("real_disk_size: {}", e)))?;

    let usage_pct = if map_size > 0 {
        (used_bytes as f64 / map_size as f64) * 100.0
    } else {
        0.0
    };

    // P0-5: `stat.entries` 返回的是主数据库中键值对数量，而非数据库数量。
    // 因此 db_count 字段设为 0（保留字段以保持 API 兼容）。
    Ok(SpaceUsage {
        used_bytes,
        map_size,
        usage_pct: usage_pct as f32,
        db_count: 0,
    })
}

