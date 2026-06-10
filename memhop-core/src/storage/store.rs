//! Redb 存储引擎核心 — 单文件存储 6 层记忆数据。
//!
//! 提供通用的读写操作，支持 serde+bincode 序列化。
//! 所有层共享一个 redb::Database 实例，通过不同表定义隔离。
//!
//! rkyv 零拷贝序列化通过 F16Vec 等包装支持，
//! 高层次集成在各层的 store 实现中完成。

use redb::{Database, ReadableTable, ReadableTableMetadata, TableDefinition};
use std::path::Path;
use std::sync::Arc;

use crate::error::{MemHopError, Result};

use super::{METADATA, VersionInfo};

// ── RedbStore ───────────────────────────────────────────────

/// Redb 单文件存储引擎。
///
/// 提供泛型读写接口，使用 bincode（serde）序列化。
/// 各数据层通过独立的 TableDefinition 做逻辑隔离。
pub struct RedbStore {
    db: Arc<Database>,
}

impl RedbStore {
    /// 打开或创建数据库文件。
    pub fn open(path: &Path) -> Result<Self> {
        let db = Arc::new(
            Database::builder()
                .create(path)
                .map_err(|e| {
                    MemHopError::Storage(format!("Failed to open/create redb db at {}: {}", path.display(), e))
                })?
        );

        // 初始化版本信息
        let wtxn = db.begin_write().map_err(|e| MemHopError::Storage(format!("begin_write: {}", e)))?;
        {
            let mut table = wtxn.open_table(METADATA)
                .map_err(|e| MemHopError::Storage(format!("open metadata: {}", e)))?;
            if table.get("version")
                .map_err(|e| MemHopError::Storage(format!("get version: {}", e)))?
                .is_none()
            {
                let info = VersionInfo::current();
                let bytes = serde_json::to_vec(&info)
                    .map_err(|e| MemHopError::Internal(format!("serialize version: {}", e)))?;
                table.insert("version", bytes.as_slice())
                    .map_err(|e| MemHopError::Storage(format!("insert version: {}", e)))?;
            }
        }
        wtxn.commit().map_err(|e| MemHopError::Storage(format!("commit: {}", e)))?;

        Ok(RedbStore { db })
    }

    /// 获取底层 redb Database 引用。
    pub fn db(&self) -> &Database {
        &self.db
    }

    /// 获取 Arc<Database> 用于共享数据库实例。
    pub fn db_arc(&self) -> Arc<Database> {
        self.db.clone()
    }

    /// 开始一个写事务。
    pub fn begin_write(&self) -> Result<redb::WriteTransaction> {
        self.db.begin_write()
            .map_err(|e| MemHopError::Storage(format!("begin_write: {}", e)))
    }

    /// 开始一个读事务。
    pub fn begin_read(&self) -> Result<redb::ReadTransaction> {
        self.db.begin_read()
            .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))
    }

    // ── bincode 读写 ─────────────────────────────────────

    /// 用 bincode 写入一个值。
    pub fn write_bincode<T: serde::Serialize + ?Sized>(
        &self,
        wtxn: &mut redb::WriteTransaction,
        table_def: TableDefinition<&str, &[u8]>,
        key: &str,
        value: &T,
    ) -> Result<()> {
        let mut table = wtxn.open_table(table_def)
            .map_err(|e| MemHopError::Storage(format!("open table: {}", e)))?;
        let bytes = bincode::serialize(value)
            .map_err(|e| MemHopError::Internal(format!("bincode serialize: {}", e)))?;
        table.insert(key, bytes.as_slice())
            .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
        Ok(())
    }

    /// 写入预序列化的原始字节。
    pub fn write_raw(
        &self,
        wtxn: &mut redb::WriteTransaction,
        table_def: TableDefinition<&str, &[u8]>,
        key: &str,
        value: &[u8],
    ) -> Result<()> {
        let mut table = wtxn.open_table(table_def)
            .map_err(|e| MemHopError::Storage(format!("open table: {}", e)))?;
        table.insert(key, value)
            .map_err(|e| MemHopError::Storage(format!("insert: {}", e)))?;
        Ok(())
    }

    /// 从 bincode 读取一个值。
    /// 如果表不存在，返回 Ok(None)；如果是 IO/损坏等错误，传播错误。
    pub fn read_bincode<T: serde::de::DeserializeOwned>(
        &self,
        txn: &redb::ReadTransaction,
        table_def: TableDefinition<&str, &[u8]>,
        key: &str,
    ) -> Result<Option<T>> {
        let table = match txn.open_table(table_def) {
            Ok(t) => t,
            Err(e) => {
                if e.to_string().contains("does not exist") {
                    return Ok(None);
                }
                return Err(MemHopError::Storage(format!("read_bincode open_table: {}", e)));
            }
        };
        match table.get(key)
            .map_err(|e| MemHopError::Storage(format!("get: {}", e)))?
        {
            Some(bytes) => {
                let value = bincode::deserialize(bytes.value())
                    .map_err(|e| MemHopError::Internal(format!("bincode deserialize: {}", e)))?;
                Ok(Some(value))
            }
            None => Ok(None),
        }
    }

    /// 读取原始字节（不反序列化）。
    /// 如果表不存在，返回 Ok(None)；如果是 IO/损坏等错误，传播错误。
    pub fn read_raw(
        &self,
        txn: &redb::ReadTransaction,
        table_def: TableDefinition<&str, &[u8]>,
        key: &str,
    ) -> Result<Option<Vec<u8>>> {
        let table = match txn.open_table(table_def) {
            Ok(t) => t,
            Err(e) => {
                if e.to_string().contains("does not exist") {
                    return Ok(None);
                }
                return Err(MemHopError::Storage(format!("read_raw open_table: {}", e)));
            }
        };
        match table.get(key)
            .map_err(|e| MemHopError::Storage(format!("get: {}", e)))?
        {
            Some(bytes) => Ok(Some(bytes.value().to_vec())),
            None => Ok(None),
        }
    }

    /// 删除一个键。
    pub fn delete(
        &self,
        wtxn: &mut redb::WriteTransaction,
        table_def: TableDefinition<&str, &[u8]>,
        key: &str,
    ) -> Result<()> {
        let mut table = wtxn.open_table(table_def)
            .map_err(|e| MemHopError::Storage(format!("open table: {}", e)))?;
        table.remove(key)
            .map_err(|e| MemHopError::Storage(format!("remove: {}", e)))?;
        Ok(())
    }

    /// 遍历表所有条目。
    /// 如果表不存在，返回空 Vec；如果是 IO/损坏等错误，传播错误。
    /// 跳过无法反序列化的条目（容错）。
    pub fn iter_bincode<T: serde::de::DeserializeOwned>(
        &self,
        txn: &redb::ReadTransaction,
        table_def: TableDefinition<&str, &[u8]>,
    ) -> Result<Vec<(String, T)>> {
        let table = match txn.open_table(table_def) {
            Ok(t) => t,
            Err(e) => {
                if e.to_string().contains("does not exist") {
                    return Ok(Vec::new());
                }
                return Err(MemHopError::Storage(format!("iter_bincode open_table: {}", e)));
            }
        };
        let mut results = Vec::new();
        for result in table.iter()
            .map_err(|e| MemHopError::Storage(format!("iter: {}", e)))?
        {
            let (key, value) = match result {
                Ok(kv) => kv,
                Err(_) => continue,
            };
            match bincode::deserialize(value.value()) {
                Ok(val) => results.push((key.value().to_string(), val)),
                Err(_) => continue,
            }
        }
        Ok(results)
    }

    // ── 计数 ─────────────────────────────────────────────

    /// 返回表中条目数。
    /// 如果表不存在，返回 0；如果是 IO/损坏等错误，传播错误。
    pub fn count(
        &self,
        txn: &redb::ReadTransaction,
        table_def: TableDefinition<&str, &[u8]>,
    ) -> Result<u64> {
        let table = match txn.open_table(table_def) {
            Ok(t) => t,
            Err(e) => {
                if e.to_string().contains("does not exist") {
                    return Ok(0);
                }
                return Err(MemHopError::Storage(format!("count open_table: {}", e)));
            }
        };
        table.len()
            .map_err(|e| MemHopError::Storage(format!("len: {}", e)))
    }
}


