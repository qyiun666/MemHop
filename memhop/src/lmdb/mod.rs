//! LMDB 存储层 — 4 个独立 heed::Env 环境。
//! L1/L2/L3/L4 各一个独立 LMDB 文件，独立事务。
//!
//! 环境布局:
//!   <brain_dir>/l1_hypergraph.db/  — 超图节点+超边+BM25快照
//!   <brain_dir>/l2_topics.db/      — 话题+话题图边+每话题BM25
//!   <brain_dir>/l3_domains.db/     — 领域节点+领域超图+每领域BM25
//!   <brain_dir>/l4_raw.db/         — 原文+时间索引+会话索引

use std::path::Path;
use heed::{Env, EnvOpenOptions, RoTxn, RwTxn};
use heed::types::{Str, Bytes};
pub type DB = heed::Database<Str, Bytes>;
use crate::error::{Result, MemHopError};

// ── L1: 超图环境 ──────────────────────────────────────────

pub struct L1Env {
    pub env: Env,
    pub nodes: DB,
    pub hyperedges: DB,
    pub node_to_hyperedges: DB,
    pub config: DB,
}

impl L1Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path).map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(1024 * 1024 * 1024) // 1GB
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l1 env: {}", e)))?
        };
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let nodes = env.create_database(&mut wtxn, Some("nodes")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let hyperedges = env.create_database(&mut wtxn, Some("hyperedges")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let node_to_hyperedges = env.create_database(&mut wtxn, Some("node_to_hyperedges")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env.create_database(&mut wtxn, Some("config")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L1Env { env, nodes, hyperedges, node_to_hyperedges, config })
    }
}

// ── L2: 话题环境 ──────────────────────────────────────────

pub struct L2Env {
    pub env: Env,
    pub topics: DB,
    pub topic_edges: DB,
    pub topic_bm25: DB,
    pub config: DB,
}

impl L2Env {
    pub fn open(path: &Path) -> Result<Self> {
        std::fs::create_dir_all(path).map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(512 * 1024 * 1024) // 512MB
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l2 env: {}", e)))?
        };
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let topics = env.create_database(&mut wtxn, Some("topics")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let topic_edges = env.create_database(&mut wtxn, Some("topic_edges")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let topic_bm25 = env.create_database(&mut wtxn, Some("topic_bm25")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env.create_database(&mut wtxn, Some("config")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L2Env { env, topics, topic_edges, topic_bm25, config })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env.read_txn().map_err(|e| MemHopError::Storage(e.to_string()))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env.write_txn().map_err(|e| MemHopError::Storage(e.to_string()))
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
        std::fs::create_dir_all(path).map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(512 * 1024 * 1024) // 512MB
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l3 env: {}", e)))?
        };
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let domain_meta = env.create_database(&mut wtxn, Some("domain_meta")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let domain_nodes = env.create_database(&mut wtxn, Some("domain_nodes")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let domain_hyperedges = env.create_database(&mut wtxn, Some("domain_hyperedges")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let domain_bm25 = env.create_database(&mut wtxn, Some("domain_bm25")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env.create_database(&mut wtxn, Some("config")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L3Env { env, domain_meta, domain_nodes, domain_hyperedges, domain_bm25, config })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env.read_txn().map_err(|e| MemHopError::Storage(e.to_string()))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env.write_txn().map_err(|e| MemHopError::Storage(e.to_string()))
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
        std::fs::create_dir_all(path).map_err(|e| MemHopError::Storage(format!("create dir: {}", e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(2 * 1024 * 1024 * 1024) // 2GB
                .max_readers(128)
                .max_dbs(16)
                .open(path)
                .map_err(|e| MemHopError::Storage(format!("open l4 env: {}", e)))?
        };
        let mut wtxn = env.write_txn().map_err(|e| MemHopError::Storage(format!("txn: {}", e)))?;
        let docs = env.create_database(&mut wtxn, Some("docs")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let turn_index = env.create_database(&mut wtxn, Some("turn_index")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let session_index = env.create_database(&mut wtxn, Some("session_index")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let doc_sequence = env.create_database(&mut wtxn, Some("doc_sequence")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        let config = env.create_database(&mut wtxn, Some("config")).map_err(|e| MemHopError::Storage(format!("db: {}", e)))?;
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;
        Ok(L4Env { env, docs, turn_index, session_index, doc_sequence, config })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>> {
        self.env.read_txn().map_err(|e| MemHopError::Storage(e.to_string()))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>> {
        self.env.write_txn().map_err(|e| MemHopError::Storage(e.to_string()))
    }
}

// ── BrainDirs: 4 环境管理器 ───────────────────────────────

#[allow(dead_code)]
pub struct BrainDirs {
    pub l1: L1Env,
    pub l2: L2Env,
    pub l3: L3Env,
    pub l4: L4Env,
}

#[allow(dead_code)]
impl BrainDirs {
    pub fn open(base_path: &Path) -> Result<Self> {
        let l1 = L1Env::open(&base_path.join("l1_hypergraph.db"))?;
        let l2 = L2Env::open(&base_path.join("l2_topics.db"))?;
        let l3 = L3Env::open(&base_path.join("l3_domains.db"))?;
        let l4 = L4Env::open(&base_path.join("l4_raw.db"))?;
        Ok(BrainDirs { l1, l2, l3, l4 })
    }
}
