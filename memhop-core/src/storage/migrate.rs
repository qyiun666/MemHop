//! LMDB → redb 数据迁移工具。
//!
//! 逐表读取 LMDB 数据，写入 redb 单文件。
//! 迁移过程中保留原始 LMDB 数据不变。
#![allow(dead_code)]

use std::path::Path;

use crate::error::{MemHopError, Result};
use crate::lmdb::{L0Env, L1Env, L2Env, L3Env, L4Env, L5Env};
use crate::storage::store::RedbStore;
use crate::storage::*;
use redb::ReadableTableMetadata;

/// 迁移配置。
pub struct MigrateConfig {
    pub source_dir: String,
    pub target_db: String,
    pub verify: bool,
}

/// 迁移结果统计。
#[derive(Debug, Default)]
pub struct MigrateReport {
    pub tables_migrated: usize,
    pub entries_migrated: u64,
    pub entries_verified: u64,
    pub errors: Vec<String>,
}

/// 执行一次完整迁移：打开所有 LMDB 环境，逐表复制到 redb。
pub fn migrate(config: &MigrateConfig) -> Result<MigrateReport> {
    let source = Path::new(&config.source_dir);
    let mut report = MigrateReport::default();

    // 打开 redb 目标
    let store = RedbStore::open(Path::new(&config.target_db))?;

    // ── L0: 角色画像 ───────────────────────────────────────
    let l0_path = source.join("l0_profile.db");
    if l0_path.exists() {
        match L0Env::open(&l0_path) {
            Ok(env) => {
                let txn = env.begin_read()
                    .map_err(|e| MemHopError::Storage(format!("L0 begin_read: {}", e)))?;
                let count = copy_table_bytes(&store, &txn, &env.profile, L0_PROFILE, "l0_profile")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.history, L0_HISTORY, "l0_history")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                eprintln!("[migrate] L0: {} entries migrated", report.entries_migrated);
            }
            Err(e) => {
                report.errors.push(format!("L0Env open: {}", e));
            }
        }
    }

    // ── L1: 超图 ───────────────────────────────────────────
    let l1_path = source.join("l1_hypergraph.db");
    if l1_path.exists() {
        match L1Env::open(&l1_path) {
            Ok(env) => {
                let txn = env.begin_read()
                    .map_err(|e| MemHopError::Storage(format!("L1 begin_read: {}", e)))?;
                let count = copy_table_bytes(&store, &txn, &env.nodes, L1_NODES, "l1_nodes")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.hyperedges, L1_HYPEREDGES, "l1_hyperedges")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.node_to_hyperedges, L1_NODE_TO_HYPEREDGES, "l1_node_to_hyperedges")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.sparse_forward, L1_SPARSE_FORWARD, "l1_sparse_forward")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                // L1_SPARSE_DOC_LEN uses U32 value type, skip for now
                let count = copy_table_u32(&store, &txn, &env.sparse_doc_len, L1_SPARSE_DOC_LEN, "l1_sparse_doc_len")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                eprintln!("[migrate] L1: {} entries migrated", report.entries_migrated);
            }
            Err(e) => {
                report.errors.push(format!("L1Env open: {}", e));
            }
        }
    }

    // ── L2: 话题图 ─────────────────────────────────────────
    let l2_path = source.join("l2_topics.db");
    if l2_path.exists() {
        match L2Env::open(&l2_path) {
            Ok(env) => {
                let txn = env.begin_read()
                    .map_err(|e| MemHopError::Storage(format!("L2 begin_read: {}", e)))?;
                let count = copy_table_bytes(&store, &txn, &env.topics, L2_TOPICS, "l2_topics")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.topic_edges, L2_TOPIC_EDGES, "l2_topic_edges")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.topic_vector_index, L2_TOPIC_VECTOR_INDEX, "l2_topic_vector_index")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                eprintln!("[migrate] L2: {} entries migrated", report.entries_migrated);
            }
            Err(e) => {
                report.errors.push(format!("L2Env open: {}", e));
            }
        }
    }

    // ── L3: 领域图 ─────────────────────────────────────────
    let l3_path = source.join("l3_domains.db");
    if l3_path.exists() {
        match L3Env::open(&l3_path) {
            Ok(env) => {
                let txn = env.begin_read()
                    .map_err(|e| MemHopError::Storage(format!("L3 begin_read: {}", e)))?;
                let count = copy_table_bytes(&store, &txn, &env.domain_meta, L3_DOMAIN_META, "l3_domain_meta")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.domain_nodes, L3_DOMAIN_NODES, "l3_domain_nodes")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                eprintln!("[migrate] L3: {} entries migrated", report.entries_migrated);
            }
            Err(e) => {
                report.errors.push(format!("L3Env open: {}", e));
            }
        }
    }

    // ── L4: 原文库 ─────────────────────────────────────────
    let l4_path = source.join("l4_raw.db");
    if l4_path.exists() {
        match L4Env::open(&l4_path) {
            Ok(env) => {
                let txn = env.begin_read()
                    .map_err(|e| MemHopError::Storage(format!("L4 begin_read: {}", e)))?;
                let count = copy_table_bytes(&store, &txn, &env.docs, L4_DOCS, "l4_docs")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.turn_index, L4_TURN_INDEX, "l4_turn_index")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.session_index, L4_SESSION_INDEX, "l4_session_index")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                eprintln!("[migrate] L4: {} entries migrated", report.entries_migrated);
            }
            Err(e) => {
                report.errors.push(format!("L4Env open: {}", e));
            }
        }
    }

    // ── L5: 程序性晶体 ─────────────────────────────────────
    let l5_path = source.join("l5_procedural.db");
    if l5_path.exists() {
        match L5Env::open(&l5_path) {
            Ok(env) => {
                let txn = env.begin_read()
                    .map_err(|e| MemHopError::Storage(format!("L5 begin_read: {}", e)))?;
                let count = copy_table_bytes(&store, &txn, &env.crystals, L5_CRYSTALS, "l5_crystals")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                let count = copy_table_bytes(&store, &txn, &env.chain_index, L5_CHAIN_INDEX, "l5_chain_index")?;
                report.entries_migrated += count;
                report.tables_migrated += 1;

                eprintln!("[migrate] L5: {} entries migrated", report.entries_migrated);
            }
            Err(e) => {
                report.errors.push(format!("L5Env open: {}", e));
            }
        }
    }

    // ── 验证 ───────────────────────────────────────────────
    if config.verify {
        eprintln!("[migrate] Starting verification...");
        let count = verify_store(&store)?;
        report.entries_verified = count;
        eprintln!("[migrate] Verified {} entries in target db", count);
    }

    Ok(report)
}

/// 将 LMDB 表中的所有键值对逐条复制到 redb 表（Bytes 类型）。
fn copy_table_bytes(
    store: &RedbStore,
    txn: &heed::RoTxn<'_>,
    src: &heed::Database<heed::types::Str, heed::types::Bytes>,
    dest: TableDefinition<&str, &[u8]>,
    label: &str,
) -> Result<u64> {
    let mut count = 0u64;
    let wtxn = store.begin_write()?;
    {
        let mut table = wtxn.open_table(dest)
            .map_err(|e| MemHopError::Storage(format!("open {}: {}", label, e)))?;
        if let Ok(iter) = src.iter(txn) {
            for item in iter {
                match item {
                    Ok((key, data)) => {
                        let k: &str = key;
                        table.insert(k, data)
                            .map_err(|e| MemHopError::Storage(format!("insert {}: {}", label, e)))?;
                        count += 1;
                    }
                    Err(e) => {
                        eprintln!("[migrate] Warning: iter error in {}: {}", label, e);
                    }
                }
            }
        }
    }
    wtxn.commit()
        .map_err(|e| MemHopError::Storage(format!("commit {}: {}", label, e)))?;
    eprintln!("[migrate] {}: {} entries", label, count);
    Ok(count)
}

/// 将 LMDB 表中 U32 值类型的键值对复制到 redb。
fn copy_table_u32(
    store: &RedbStore,
    txn: &heed::RoTxn<'_>,
    src: &heed::Database<heed::types::Str, heed::types::U32<heed::byteorder::NativeEndian>>,
    dest: TableDefinition<&str, u32>,
    label: &str,
) -> Result<u64> {
    let mut count = 0u64;
    let wtxn = store.begin_write()?;
    {
        let mut table = wtxn.open_table(dest)
            .map_err(|e| MemHopError::Storage(format!("open {}: {}", label, e)))?;
        if let Ok(iter) = src.iter(txn) {
            for item in iter {
                match item {
                    Ok((key, data)) => {
                        let k: &str = key;
                        table.insert(k, data)
                            .map_err(|e| MemHopError::Storage(format!("insert {}: {}", label, e)))?;
                        count += 1;
                    }
                    Err(e) => {
                        eprintln!("[migrate] Warning: iter error in {}: {}", label, e);
                    }
                }
            }
        }
    }
    wtxn.commit()
        .map_err(|e| MemHopError::Storage(format!("commit {}: {}", label, e)))?;
    eprintln!("[migrate] {}: {} entries", label, count);
    Ok(count)
}

/// 遍历 redb 所有表并统计条目数（验证用）。
fn verify_store(store: &RedbStore) -> Result<u64> {
    let txn = store.begin_read()?;
    let mut total = 0u64;

    let tables: Vec<TableDefinition<&str, &[u8]>> = vec![
        L0_PROFILE, L0_HISTORY,
        L1_NODES, L1_HYPEREDGES, L1_NODE_TO_HYPEREDGES, L1_SPARSE_FORWARD,
        L2_TOPICS, L2_TOPIC_EDGES, L2_TOPIC_VECTOR_INDEX,
        L3_DOMAIN_META, L3_DOMAIN_NODES,
        L4_DOCS, L4_TURN_INDEX, L4_SESSION_INDEX,
        L5_CRYSTALS, L5_CHAIN_INDEX,
    ];

    for table_def in tables {
        if let Ok(table) = txn.open_table(table_def)
            && let Ok(count) = table.len()
        {
            total += count;
        }
    }

    Ok(total)
}
