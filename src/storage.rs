/// LMDB-backed persistent storage.
///
/// Four logical databases within a single env:
///     p: memory_id → f16 embedding vector (bincode)
///     b: memory_id → zstd-compressed text + meta (JSON)
///     m: memory_id → timestamp + importance + protection (bincode)
///     i: index_name → serialized index snapshot (bincode)

use half::f16;
use std::collections::HashMap;
use std::path::Path;

use heed::{Database, Env, EnvOpenOptions};
use heed::types::{Bytes, Str};
use serde::{Deserialize, Serialize};

// ── Error type ─────────────────────────────────────────────

#[derive(Debug)]
pub enum StorageError {
    Lmdb(String),
    Serialization(String),
    NotFound(String),
}

impl std::fmt::Display for StorageError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            StorageError::Lmdb(msg) => write!(f, "LMDB error: {}", msg),
            StorageError::Serialization(msg) => write!(f, "Serialization error: {}", msg),
            StorageError::NotFound(msg) => write!(f, "Not found: {}", msg),
        }
    }
}

impl std::error::Error for StorageError {}

impl From<heed::Error> for StorageError {
    fn from(err: heed::Error) -> Self {
        StorageError::Lmdb(err.to_string())
    }
}

// ── Records ────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MetaRecord {
    pub created_at: i64,
    pub importance: f32,
    pub protection: u8,
    pub is_dormant: bool,
    pub key: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BlobRecord {
    pub text: String,
    pub meta: HashMap<String, serde_json::Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub content_type: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub blob_data: Option<Vec<u8>>,
}

// ── LmdbStorage ────────────────────────────────────────────

pub struct LmdbStorage {
    env: Env,
    patterns_db: Database<Bytes, Bytes>,
    blobs_db: Database<Bytes, Bytes>,
    meta_db: Database<Bytes, Bytes>,
    index_db: Database<Str, Bytes>,
}

// Safety: heed::Env and Database are Send + Sync
unsafe impl Send for LmdbStorage {}
unsafe impl Sync for LmdbStorage {}

impl LmdbStorage {
    const MAP_SIZE: usize = 1024 * 1024 * 1024; // 1 GB
    const MAX_DBS: u32 = 4;
    const ZSTD_LEVEL: i32 = 3;

    /// Open or create the database environment.
    pub fn open(path: &str) -> Result<Self, StorageError> {
        let dir = Path::new(path);
        if !dir.exists() {
            std::fs::create_dir_all(dir)
                .map_err(|e| StorageError::Lmdb(format!("failed to create directory: {}", e)))?;
        }

        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(Self::MAP_SIZE)
                .max_dbs(Self::MAX_DBS)
                .open(dir)?
        };

        let mut wtxn = env.write_txn()?;

        let patterns_db = env.create_database::<Bytes, Bytes>(&mut wtxn, Some("p"))?;
        let blobs_db = env.create_database::<Bytes, Bytes>(&mut wtxn, Some("b"))?;
        let meta_db = env.create_database::<Bytes, Bytes>(&mut wtxn, Some("m"))?;
        let index_db = env.create_database::<Str, Bytes>(&mut wtxn, Some("i"))?;

        wtxn.commit()?;

        Ok(LmdbStorage {
            env,
            patterns_db,
            blobs_db,
            meta_db,
            index_db,
        })
    }

    /// Flush data to disk.
    pub fn close(&self) -> Result<(), StorageError> {
        self.env.force_sync()?;
        Ok(())
    }

    /// Write a single memory record (one write transaction).
    pub fn put(
        &self,
        id: &str,
        pattern: &[f16],
        blob: &BlobRecord,
        meta: &MetaRecord,
    ) -> Result<(), StorageError> {
        let mut wtxn = self.env.write_txn()?;

        let key = id.as_bytes();
        self.patterns_db.put(&mut wtxn, key, &serialize_pattern(pattern)?)?;
        self.blobs_db.put(&mut wtxn, key, &serialize_blob(blob)?)?;
        self.meta_db.put(&mut wtxn, key, &serialize_meta(meta)?)?;

        wtxn.commit()?;
        Ok(())
    }

    /// Batch write in a single transaction.
    pub fn put_batch(
        &self,
        items: &[(String, Vec<f16>, BlobRecord, MetaRecord)],
    ) -> Result<(), StorageError> {
        let mut wtxn = self.env.write_txn()?;

        for (id, pattern, blob, meta) in items {
            let key = id.as_bytes();
            self.patterns_db.put(&mut wtxn, key, &serialize_pattern(pattern)?)?;
            self.blobs_db.put(&mut wtxn, key, &serialize_blob(blob)?)?;
            self.meta_db.put(&mut wtxn, key, &serialize_meta(meta)?)?;
        }

        wtxn.commit()?;
        Ok(())
    }

    /// Read the pattern vector for a memory.
    pub fn get_pattern(&self, id: &str) -> Result<Option<Vec<f16>>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let raw = self.patterns_db.get(&rtxn, id.as_bytes())?;
        match raw {
            Some(bytes) => Ok(Some(deserialize_pattern(bytes)?)),
            None => Ok(None),
        }
    }

    /// Read the blob record for a memory.
    pub fn get_blob(&self, id: &str) -> Result<Option<BlobRecord>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let raw = self.blobs_db.get(&rtxn, id.as_bytes())?;
        match raw {
            Some(bytes) => Ok(Some(deserialize_blob(bytes)?)),
            None => Ok(None),
        }
    }

    /// Read the meta record for a memory.
    pub fn get_meta(&self, id: &str) -> Result<Option<MetaRecord>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let raw = self.meta_db.get(&rtxn, id.as_bytes())?;
        match raw {
            Some(bytes) => Ok(Some(deserialize_meta(bytes)?)),
            None => Ok(None),
        }
    }

    /// Delete a memory from all sub-databases. Returns true if the memory existed.
    pub fn delete(&self, id: &str) -> Result<bool, StorageError> {
        let mut wtxn = self.env.write_txn()?;
        let key = id.as_bytes();

        let existed = self.patterns_db.delete(&mut wtxn, key)?;
        self.blobs_db.delete(&mut wtxn, key)?;
        self.meta_db.delete(&mut wtxn, key)?;

        wtxn.commit()?;
        Ok(existed)
    }

    /// Get all memory IDs.
    pub fn all_ids(&self) -> Result<Vec<String>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let iter = self.patterns_db.iter(&rtxn)?;
        let mut ids = Vec::new();
        for result in iter {
            let (key, _) = result?;
            let id = String::from_utf8(key.to_vec())
                .map_err(|e| StorageError::Serialization(format!("invalid utf8 key: {}", e)))?;
            ids.push(id);
        }
        Ok(ids)
    }

    /// Get all patterns (id + f16 vector).
    pub fn all_patterns(&self) -> Result<Vec<(String, Vec<f16>)>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let iter = self.patterns_db.iter(&rtxn)?;
        let mut out = Vec::new();
        for result in iter {
            let (key, val) = result?;
            let id = String::from_utf8(key.to_vec())
                .map_err(|e| StorageError::Serialization(format!("invalid utf8 key: {}", e)))?;
            let pattern = deserialize_pattern(val)?;
            out.push((id, pattern));
        }
        Ok(out)
    }

    /// Get all meta records.
    pub fn all_metas(&self) -> Result<Vec<(String, MetaRecord)>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let iter = self.meta_db.iter(&rtxn)?;
        let mut out = Vec::new();
        for result in iter {
            let (key, val) = result?;
            let id = String::from_utf8(key.to_vec())
                .map_err(|e| StorageError::Serialization(format!("invalid utf8 key: {}", e)))?;
            let meta = deserialize_meta(val)?;
            out.push((id, meta));
        }
        Ok(out)
    }

    /// Get all blob records.
    pub fn all_blobs(&self) -> Result<Vec<(String, BlobRecord)>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let iter = self.blobs_db.iter(&rtxn)?;
        let mut out = Vec::new();
        for result in iter {
            let (key, val) = result?;
            let id = String::from_utf8(key.to_vec())
                .map_err(|e| StorageError::Serialization(format!("invalid utf8 key: {}", e)))?;
            let blob = deserialize_blob(val)?;
            out.push((id, blob));
        }
        Ok(out)
    }

    /// Count total memories.
    pub fn count(&self) -> Result<u64, StorageError> {
        let rtxn = self.env.read_txn()?;
        Ok(self.patterns_db.len(&rtxn)?)
    }

    /// Update the pattern vector for an existing memory.
    pub fn update_pattern(&self, id: &str, pattern: &[f16]) -> Result<(), StorageError> {
        let mut wtxn = self.env.write_txn()?;
        let key = id.as_bytes();
        if self.patterns_db.get(&wtxn, key)?.is_none() {
            return Err(StorageError::NotFound(id.to_string()));
        }
        self.patterns_db.put(&mut wtxn, key, &serialize_pattern(pattern)?)?;
        wtxn.commit()?;
        Ok(())
    }

    /// Update the blob record for an existing memory.
    pub fn update_blob(&self, id: &str, blob: &BlobRecord) -> Result<(), StorageError> {
        let mut wtxn = self.env.write_txn()?;
        let key = id.as_bytes();
        if self.patterns_db.get(&wtxn, key)?.is_none() {
            return Err(StorageError::NotFound(id.to_string()));
        }
        self.blobs_db.put(&mut wtxn, key, &serialize_blob(blob)?)?;
        wtxn.commit()?;
        Ok(())
    }

    /// Update the meta record for an existing memory.
    pub fn update_meta(&self, id: &str, meta: &MetaRecord) -> Result<(), StorageError> {
        let mut wtxn = self.env.write_txn()?;
        let key = id.as_bytes();
        if self.patterns_db.get(&wtxn, key)?.is_none() {
            return Err(StorageError::NotFound(id.to_string()));
        }
        self.meta_db.put(&mut wtxn, key, &serialize_meta(meta)?)?;
        wtxn.commit()?;
        Ok(())
    }

    /// Find a memory by its upsert dedup key (linear scan over meta_db).
    pub fn find_by_key(&self, key: &str) -> Result<Option<String>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let iter = self.meta_db.iter(&rtxn)?;
        for result in iter {
            let (k, val) = result?;
            let meta: MetaRecord = deserialize_meta(val)?;
            if meta.key.as_deref() == Some(key) {
                let id = String::from_utf8(k.to_vec())
                    .map_err(|e| StorageError::Serialization(format!("invalid utf8 key: {}", e)))?;
                return Ok(Some(id));
            }
        }
        Ok(None)
    }

    /// Save an index snapshot.
    pub fn save_index(&self, name: &str, data: &[u8]) -> Result<(), StorageError> {
        let mut wtxn = self.env.write_txn()?;
        self.index_db.put(&mut wtxn, name, data)?;
        wtxn.commit()?;
        Ok(())
    }

    /// Load an index snapshot.
    pub fn load_index(&self, name: &str) -> Result<Option<Vec<u8>>, StorageError> {
        let rtxn = self.env.read_txn()?;
        let raw = self.index_db.get(&rtxn, name)?;
        Ok(raw.map(|b| b.to_vec()))
    }

    /// Estimate index size in bytes from the index_db sub-database.
    pub fn index_size_bytes(&self) -> Result<usize, StorageError> {
        let rtxn = self.env.read_txn()?;
        let mut total = 0usize;
        let iter = self.index_db.iter(&rtxn)?;
        for result in iter {
            let (key, val) = result?;
            total += key.len() + val.len();
        }
        Ok(total)
    }
}

// ── Serialization helpers ──────────────────────────────────

fn serialize_pattern(pattern: &[f16]) -> Result<Vec<u8>, StorageError> {
    bincode::serialize(pattern).map_err(|e| StorageError::Serialization(e.to_string()))
}

fn deserialize_pattern(bytes: &[u8]) -> Result<Vec<f16>, StorageError> {
    bincode::deserialize(bytes).map_err(|e| StorageError::Serialization(e.to_string()))
}

fn serialize_blob(blob: &BlobRecord) -> Result<Vec<u8>, StorageError> {
    let json = serde_json::to_vec(blob).map_err(|e| StorageError::Serialization(e.to_string()))?;
    let compressed = std::io::Cursor::new(Vec::new());
    let mut encoder = zstd::Encoder::new(compressed, LmdbStorage::ZSTD_LEVEL)
        .map_err(|e| StorageError::Serialization(e.to_string()))?;
    use std::io::Write;
    encoder
        .write_all(&json)
        .map_err(|e| StorageError::Serialization(e.to_string()))?;
    let compressed = encoder
        .finish()
        .map_err(|e| StorageError::Serialization(e.to_string()))?;
    Ok(compressed.into_inner())
}

fn deserialize_blob(bytes: &[u8]) -> Result<BlobRecord, StorageError> {
    let decompressed = zstd::decode_all(bytes)
        .map_err(|e| StorageError::Serialization(e.to_string()))?;
    serde_json::from_slice(&decompressed).map_err(|e| StorageError::Serialization(e.to_string()))
}

fn serialize_meta(meta: &MetaRecord) -> Result<Vec<u8>, StorageError> {
    bincode::serialize(meta).map_err(|e| StorageError::Serialization(e.to_string()))
}

fn deserialize_meta(bytes: &[u8]) -> Result<MetaRecord, StorageError> {
    bincode::deserialize(bytes).map_err(|e| StorageError::Serialization(e.to_string()))
}

// ── Tests ──────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    fn make_blob(text: &str) -> BlobRecord {
        BlobRecord {
            text: text.to_string(),
            meta: HashMap::new(),
            content_type: None,
            blob_data: None,
        }
    }

    fn make_meta() -> MetaRecord {
        MetaRecord {
            created_at: 1700000000000,
            importance: 0.8,
            protection: 0,
            is_dormant: false,
            key: None,
        }
    }

    fn make_f16_pattern(values: &[f32]) -> Vec<f16> {
        values.iter().map(|&x| f16::from_f32(x)).collect()
    }

    fn temp_storage(_prefix: &str) -> LmdbStorage {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.keep().to_string_lossy().to_string();
        let path = Box::leak(path.into_boxed_str());
        LmdbStorage::open(path).unwrap()
    }

    #[test]
    fn test_put_and_get() {
        let storage = temp_storage("put_get");

        let id = "m_000000000001";
        let pattern = make_f16_pattern(&[0.1, 0.2, 0.3, 0.4]);
        let blob = make_blob("hello world");
        let meta = make_meta();

        storage.put(id, &pattern, &blob, &meta).unwrap();

        let got_pattern = storage.get_pattern(id).unwrap().unwrap();
        assert_eq!(got_pattern.len(), 4);
        assert!((got_pattern[0].to_f32() - 0.1).abs() < 0.01);
        assert!((got_pattern[3].to_f32() - 0.4).abs() < 0.01);

        let got_blob = storage.get_blob(id).unwrap().unwrap();
        assert_eq!(got_blob.text, "hello world");

        let got_meta = storage.get_meta(id).unwrap().unwrap();
        assert_eq!(got_meta.created_at, 1700000000000);
        assert!((got_meta.importance - 0.8).abs() < 1e-6);
        assert_eq!(got_meta.protection, 0);
        assert!(!got_meta.is_dormant);
    }

    #[test]
    fn test_persistence() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().to_string_lossy().to_string();

        {
            let storage = LmdbStorage::open(&path).unwrap();
            let id = "m_000000000002";
            let pattern = make_f16_pattern(&[1.0, 2.0, 3.0]);
            let blob = make_blob("persist test");
            let meta = make_meta();
            storage.put(id, &pattern, &blob, &meta).unwrap();
            storage.close().unwrap();
        }

        {
            let storage = LmdbStorage::open(&path).unwrap();
            let got_pattern = storage.get_pattern("m_000000000002").unwrap().unwrap();
            assert_eq!(got_pattern.len(), 3);
            assert!((got_pattern[0].to_f32() - 1.0).abs() < 0.01);

            let got_blob = storage.get_blob("m_000000000002").unwrap().unwrap();
            assert_eq!(got_blob.text, "persist test");
        }
    }

    #[test]
    fn test_put_batch_and_count() {
        let storage = temp_storage("batch");

        let items: Vec<(String, Vec<f16>, BlobRecord, MetaRecord)> = (0..10)
            .map(|i| {
                let id = format!("m_{:012}", i);
                let pattern = make_f16_pattern(&[i as f32]);
                let blob = make_blob(&format!("item {}", i));
                let mut meta = make_meta();
                meta.key = if i == 5 { Some("unique_key".to_string()) } else { None };
                (id, pattern, blob, meta)
            })
            .collect();

        storage.put_batch(&items).unwrap();
        assert_eq!(storage.count().unwrap(), 10);
    }

    #[test]
    fn test_delete() {
        let storage = temp_storage("delete");

        let id = "m_000000000099";
        let pattern = make_f16_pattern(&[0.5]);
        storage.put(id, &pattern, &make_blob("to delete"), &make_meta()).unwrap();
        assert!(storage.get_pattern(id).unwrap().is_some());

        let existed = storage.delete(id).unwrap();
        assert!(existed);
        assert!(storage.get_pattern(id).unwrap().is_none());
        assert!(storage.get_blob(id).unwrap().is_none());
        assert!(storage.get_meta(id).unwrap().is_none());
    }

    #[test]
    fn test_find_by_key() {
        let storage = temp_storage("find_key");

        let mut meta = make_meta();
        meta.key = Some("dedup_abc".to_string());
        let pattern = make_f16_pattern(&[0.1]);
        storage.put("m_000000000010", &pattern, &make_blob("with key"), &meta).unwrap();
        let pattern2 = make_f16_pattern(&[0.2]);
        storage.put("m_000000000011", &pattern2, &make_blob("no key"), &make_meta()).unwrap();

        let found = storage.find_by_key("dedup_abc").unwrap();
        assert_eq!(found, Some("m_000000000010".to_string()));

        let not_found = storage.find_by_key("nonexistent").unwrap();
        assert!(not_found.is_none());
    }
}
